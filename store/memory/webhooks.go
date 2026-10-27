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
}

func cloneWebhook(v store.Webhook) store.Webhook {
	v.Types = slices.Clone(v.Types)
	v.PreviousSecrets = slices.Clone(v.PreviousSecrets)
	return v
}
func (s *Webhooks) Create(tenant string, v store.Webhook) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[string]map[string]store.Webhook{}
	}
	if s.records[tenant] == nil {
		s.records[tenant] = map[string]store.Webhook{}
	}
	if _, ok := s.records[tenant][v.ID]; ok {
		return store.Webhook{}, store.ErrWebhookExists
	}
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	s.records[tenant][v.ID] = cloneWebhook(v)
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
func (s *Webhooks) Patch(tenant, id string, p store.WebhookPatch) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrWebhookNotFound
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
	v.UpdatedAt = time.Now().UTC()
	s.records[tenant][id] = v
	return cloneWebhook(v), nil
}
func (s *Webhooks) Rotate(tenant, id, secret string) (store.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrWebhookNotFound
	}
	now := time.Now().UTC()
	previous := make([]store.WebhookSecret, 0, len(v.PreviousSecrets)+1)
	for _, p := range v.PreviousSecrets {
		if p.ExpiresAt.After(now) {
			previous = append(previous, p)
		}
	}
	v.PreviousSecrets = append(previous, store.WebhookSecret{Secret: v.Secret, ExpiresAt: now.Add(24 * time.Hour)})
	v.Secret = secret
	v.UpdatedAt = now
	s.records[tenant][id] = v
	return cloneWebhook(v), nil
}
func (s *Webhooks) Delete(tenant, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[tenant][id]; !ok {
		return store.ErrWebhookNotFound
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
