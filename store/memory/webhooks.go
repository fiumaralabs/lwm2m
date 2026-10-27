package memory

import (
	"encoding/base64"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/store"
	"slices"
	"sort"
	"sync"
	"time"
)

// Webhooks is a tenant-isolated store with a usable zero value.
type Webhooks struct {
	mu      sync.Mutex
	records map[string]map[string]store.Webhook
	replays map[webhookReplayKey]webhookReplay
	// Now optionally supplies the clock; configure before concurrent use.
	Now func() time.Time
}

func cloneWebhook(v store.Webhook) store.Webhook {
	v.Types = slices.Clone(v.Types)
	v.PreviousSecrets = slices.Clone(v.PreviousSecrets)
	return v
}

type webhookReplayKey struct{ tenant, endpoint, key string }
type webhookReplay struct {
	value   store.Webhook
	expires time.Time
}

func (s *Webhooks) Replay(tenant, targetID, key string) (store.Webhook, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	endpoint := "create"
	if targetID != "" {
		endpoint = "rotate/" + targetID
	}
	return s.replay(webhookReplayKey{tenant, endpoint, key})
}

func (s *Webhooks) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// replay and remember run under mu, atomically with the effect they describe.
func (s *Webhooks) replay(k webhookReplayKey) (store.Webhook, bool) {
	now := s.now()
	for key, entry := range s.replays {
		if !entry.expires.After(now) {
			delete(s.replays, key)
		}
	}
	entry, ok := s.replays[k]
	return cloneWebhook(entry.value), k.key != "" && ok
}
func (s *Webhooks) remember(k webhookReplayKey, v store.Webhook) {
	if k.key == "" {
		return
	}
	if s.replays == nil {
		s.replays = make(map[webhookReplayKey]webhookReplay)
	}
	s.replays[k] = webhookReplay{cloneWebhook(v), s.now().Add(24 * time.Hour)}
}
func (s *Webhooks) Create(tenant string, v store.Webhook, idempotencyKey string) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := webhookReplayKey{tenant, "create", idempotencyKey}
	if previous, ok := s.replay(k); ok {
		return previous, nil
	}
	if s.records == nil {
		s.records = map[string]map[string]store.Webhook{}
	}
	if s.records[tenant] == nil {
		s.records[tenant] = map[string]store.Webhook{}
	}
	if _, ok := s.records[tenant][v.ID]; ok {
		return store.Webhook{}, store.ErrWebhookExists
	}
	v.CreatedAt = s.now()
	v.UpdatedAt = v.CreatedAt
	v.Version = 1
	s.records[tenant][v.ID] = cloneWebhook(v)
	s.remember(k, v)
	return cloneWebhook(v), nil
}
func (s *Webhooks) Get(tenant, id string) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrWebhookNotFound
	}
	return cloneWebhook(v), nil
}
func (s *Webhooks) Patch(tenant, id string, p store.WebhookPatch, match store.WebhookMatch) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrWebhookNotFound
	}
	if !match.Matches(v) {
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
	v.UpdatedAt = s.now()
	v.Version++
	s.records[tenant][id] = v
	return cloneWebhook(v), nil
}
func (s *Webhooks) Rotate(tenant, id, secret, idempotencyKey string) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := webhookReplayKey{tenant, "rotate/" + id, idempotencyKey}
	if previous, ok := s.replay(k); ok {
		return previous, nil
	}
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrWebhookNotFound
	}
	now := s.now()
	previous := make([]store.WebhookSecret, 0, len(v.PreviousSecrets)+1)
	for _, p := range v.PreviousSecrets {
		if p.ExpiresAt.After(now) {
			previous = append(previous, p)
		}
	}
	v.PreviousSecrets = append(previous, store.WebhookSecret{Secret: v.Secret, ExpiresAt: now.Add(24 * time.Hour)})
	v.Secret = secret
	v.UpdatedAt = now
	v.Version++
	s.records[tenant][id] = v
	s.remember(k, v)
	return cloneWebhook(v), nil
}
func (s *Webhooks) Delete(tenant, id string, match store.WebhookMatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return store.ErrWebhookNotFound
	}
	if !match.Matches(v) {
		return store.ErrWebhookPrecondition
	}
	delete(s.records[tenant], id)
	return nil
}
func (s *Webhooks) List(tenant, cursor string, limit int) ([]store.Webhook, string, error) {
	var c struct{ Tenant, After string }
	if limit < 1 || limit > 500 {
		return nil, "", store.ErrWebhookCursor
	}
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.After == "" {
			return nil, "", store.ErrWebhookCursor
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Webhook, 0)
	for _, v := range s.records[tenant] {
		if v.ID > c.After {
			out = append(out, cloneWebhook(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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
