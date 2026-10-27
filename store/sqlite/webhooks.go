package sqlite

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// Webhooks shares its owning Devices connection and lifetime.
// Configure Now before concurrent use. It defaults to UTC wall time.
type Webhooks struct {
	db  *sql.DB
	Now func() time.Time
}

var _ store.Webhooks = (*Webhooks)(nil)

func (s *Devices) Webhooks() *Webhooks { return &Webhooks{db: s.db} }
func (s *Webhooks) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// The private envelope preserves fields omitted by the public JSON view.
type webhookEnvelope struct {
	Value           store.Webhook
	Version         uint64
	Secret          string
	PreviousSecrets []store.WebhookSecret
}

func webhookBody(v store.Webhook) ([]byte, error) {
	return json.Marshal(webhookEnvelope{v, v.Version, v.Secret, v.PreviousSecrets})
}
func decodeWebhook(b []byte, v *store.Webhook) error {
	var e webhookEnvelope
	if err := json.Unmarshal(b, &e); err != nil {
		return err
	}
	*v = e.Value
	v.Version = e.Version
	v.Secret = e.Secret
	v.PreviousSecrets = e.PreviousSecrets
	return nil
}
func readWebhook(row *sql.Row) (v store.Webhook, err error) {
	var b []byte
	if err = row.Scan(&b); err == sql.ErrNoRows {
		return v, store.ErrWebhookNotFound
	}
	if err == nil {
		err = decodeWebhook(b, &v)
	}
	return
}
func (s *Webhooks) Get(tenant, id string) (store.Webhook, error) {
	return readWebhook(s.db.QueryRow("SELECT body FROM webhooks WHERE tenant=? AND id=?", tenant, id))
}
func (s *Webhooks) replay(tx *sql.Tx, tenant, target, key string) (store.Webhook, bool, error) {
	if key == "" {
		return store.Webhook{}, false, nil
	}
	v, err := readWebhook(tx.QueryRow("SELECT body FROM webhook_replays WHERE tenant=? AND target=? AND key=? AND expires>?", tenant, target, key, s.now().UnixNano()))
	if err == store.ErrWebhookNotFound {
		return store.Webhook{}, false, nil
	}
	return v, err == nil, err
}
func (s *Webhooks) Replay(tenant, target, key string) (store.Webhook, bool) {
	tx, err := s.db.Begin()
	if err != nil {
		return store.Webhook{}, false
	}
	defer tx.Rollback()
	v, ok, err := s.replay(tx, tenant, target, key)
	return v, ok && err == nil
}

// mutate keeps replay lookup, version conditions, and durable effects atomic.
func (s *Webhooks) mutate(tenant, id, target, key string, create bool, change func(store.Webhook) (store.Webhook, error)) (store.Webhook, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return store.Webhook{}, err
	}
	defer tx.Rollback()
	if v, ok, err := s.replay(tx, tenant, target, key); err != nil || ok {
		return v, err
	}
	v, err := readWebhook(tx.QueryRow("SELECT body FROM webhooks WHERE tenant=? AND id=?", tenant, id))
	if create {
		if err == nil {
			return store.Webhook{}, store.ErrWebhookExists
		}
		if err != store.ErrWebhookNotFound {
			return store.Webhook{}, err
		}
	} else if err != nil {
		return store.Webhook{}, err
	}
	v, err = change(v)
	if err != nil {
		return store.Webhook{}, err
	}
	body, err := webhookBody(v)
	if err != nil {
		return store.Webhook{}, err
	}
	if _, err = tx.Exec("INSERT INTO webhooks(tenant,id,body) VALUES(?,?,?) ON CONFLICT(tenant,id) DO UPDATE SET body=excluded.body", tenant, id, body); err != nil {
		return store.Webhook{}, err
	}
	if _, err = tx.Exec("DELETE FROM webhook_replays WHERE expires<=?", s.now().UnixNano()); err != nil {
		return store.Webhook{}, err
	}
	if key != "" {
		if _, err = tx.Exec("INSERT INTO webhook_replays(tenant,target,key,body,expires) VALUES(?,?,?,?,?) ON CONFLICT(tenant,target,key) DO UPDATE SET body=excluded.body,expires=excluded.expires", tenant, target, key, body, s.now().Add(24*time.Hour).UnixNano()); err != nil {
			return store.Webhook{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return store.Webhook{}, err
	}
	return v, nil
}
func (s *Webhooks) Create(tenant string, v store.Webhook, key string) (store.Webhook, error) {
	// Round-trip before returning to own caller-provided slices as well as stored data.
	body, err := webhookBody(v)
	if err != nil {
		return store.Webhook{}, err
	}
	if err = decodeWebhook(body, &v); err != nil {
		return store.Webhook{}, err
	}
	return s.mutate(tenant, v.ID, "", key, true, func(store.Webhook) (store.Webhook, error) {
		v.Version = 1
		v.CreatedAt = s.now()
		v.UpdatedAt = v.CreatedAt
		return v, nil
	})
}
func (s *Webhooks) Patch(tenant, id string, p store.WebhookPatch, m store.WebhookMatch) (store.Webhook, error) {
	return s.mutate(tenant, id, "", "", false, func(v store.Webhook) (store.Webhook, error) {
		if !m.Matches(v) {
			return store.Webhook{}, store.ErrWebhookPrecondition
		}
		if p.URL != nil {
			v.URL = *p.URL
		}
		if p.Types != nil {
			v.Types = slices.Clone(*p.Types)
		}
		if p.Selector != nil {
			v.Selector = *p.Selector
		}
		if p.Status != nil {
			v.Status = *p.Status
		}
		v.Version++
		v.UpdatedAt = s.now()
		return v, nil
	})
}
func (s *Webhooks) Rotate(tenant, id, secret, key string) (store.Webhook, error) {
	return s.mutate(tenant, id, id, key, false, func(v store.Webhook) (store.Webhook, error) {
		now := s.now()
		previous := make([]store.WebhookSecret, 0, len(v.PreviousSecrets)+1)
		for _, p := range v.PreviousSecrets {
			if p.ExpiresAt.After(now) {
				previous = append(previous, p)
			}
		}
		v.PreviousSecrets = append(previous, store.WebhookSecret{Secret: v.Secret, ExpiresAt: now.Add(24 * time.Hour)})
		v.Secret = secret
		v.Version++
		v.UpdatedAt = now
		return v, nil
	})
}
func (s *Webhooks) Delete(tenant, id string, m store.WebhookMatch) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := readWebhook(tx.QueryRow("SELECT body FROM webhooks WHERE tenant=? AND id=?", tenant, id))
	if err != nil {
		return err
	}
	if !m.Matches(v) {
		return store.ErrWebhookPrecondition
	}
	if _, err = tx.Exec("DELETE FROM webhooks WHERE tenant=? AND id=?", tenant, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Webhooks) List(tenant, cursor string, limit int) ([]store.Webhook, string, error) {
	var c struct{ Tenant, After string }
	if limit < 1 || limit > 500 {
		return nil, "", store.ErrWebhookCursor
	}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.After == "" {
			return nil, "", store.ErrWebhookCursor
		}
	}
	rows, err := s.db.Query("SELECT body FROM webhooks WHERE tenant=? AND id>? ORDER BY id LIMIT ?", tenant, c.After, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]store.Webhook, 0)
	for rows.Next() {
		var b []byte
		var v store.Webhook
		if err = rows.Scan(&b); err != nil {
			return nil, "", err
		}
		if err = decodeWebhook(b, &v); err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		c.Tenant = tenant
		c.After = out[len(out)-1].ID
		b, _ := json.Marshal(c)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, next, nil
}
