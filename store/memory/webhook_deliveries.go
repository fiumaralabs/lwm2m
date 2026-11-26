package memory

import (
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/fiumaralabs/lwm2m/store"
)

// WebhookDeliveries stores attempts only for existing webhooks. Construct it
// with NewWebhookDeliveryStores and use the returned wrapper for all mutations.
type WebhookDeliveries struct {
	mu      sync.Mutex
	hooks   store.Webhooks
	entries map[[2]string][]store.WebhookDelivery
	counts  map[[2]string]map[string]int
}

type webhookDeliveryHooks struct {
	store.Webhooks
	log *WebhookDeliveries
}

// NewWebhookDeliveryStores coordinates deletion with late delivery attempts.
// All webhook mutations must use the returned store, not the underlying store.
func NewWebhookDeliveryStores(hooks store.Webhooks) (store.Webhooks, *WebhookDeliveries) {
	log := &WebhookDeliveries{hooks: hooks}
	return &webhookDeliveryHooks{Webhooks: hooks, log: log}, log
}

func (s *webhookDeliveryHooks) Create(tenant string, hook store.Webhook, key string) (store.Webhook, error) {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	return s.Webhooks.Create(tenant, hook, key)
}

func (s *webhookDeliveryHooks) Delete(tenant, id string, match store.WebhookMatch) error {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	if err := s.Webhooks.Delete(tenant, id, match); err != nil {
		return err
	}
	delete(s.log.entries, [2]string{tenant, id})
	delete(s.log.counts, [2]string{tenant, id})
	return nil
}

func copyDelivery(d store.WebhookDelivery) store.WebhookDelivery {
	d.Envelope = append([]byte(nil), d.Envelope...)
	if d.Status != nil {
		n := *d.Status
		d.Status = &n
	}
	return d
}

func (s *WebhookDeliveries) Append(tenant, webhook string, d store.WebhookDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hooks == nil {
		return
	}
	if _, err := s.hooks.Get(tenant, webhook); err != nil {
		return
	}
	if s.entries == nil {
		s.entries = make(map[[2]string][]store.WebhookDelivery)
	}
	k := [2]string{tenant, webhook}
	if s.counts == nil {
		s.counts = make(map[[2]string]map[string]int)
	}
	if s.counts[k] == nil {
		s.counts[k] = make(map[string]int)
	}
	s.counts[k][d.EventID]++
	d.Attempt = s.counts[k][d.EventID]
	a := append(s.entries[k], copyDelivery(d))
	if len(a) > 1000 {
		a = append([]store.WebhookDelivery(nil), a[len(a)-1000:]...)
	}
	s.entries[k] = a
}

func (s *WebhookDeliveries) Get(tenant, webhook, deliveryID string) (store.WebhookDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.entries[[2]string{tenant, webhook}] {
		if d.ID == deliveryID {
			return copyDelivery(d), nil
		}
	}
	return store.WebhookDelivery{}, store.ErrWebhookDeliveryNotFound
}

func (s *WebhookDeliveries) List(tenant, webhook, cursor string, limit int) ([]store.WebhookDelivery, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.entries[[2]string{tenant, webhook}]
	end := len(a)
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var v [3]string
		if err != nil || json.Unmarshal(raw, &v) != nil || v[0] != tenant || v[1] != webhook || v[2] == "" {
			return nil, "", store.ErrWebhookCursor
		}
		end = -1
		for i := len(a) - 1; i >= 0; i-- {
			if a[i].ID == v[2] {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, "", store.ErrWebhookCursor
		}
	}
	if limit < 1 {
		return nil, "", store.ErrWebhookCursor
	}
	items := make([]store.WebhookDelivery, 0, limit)
	for i := end - 1; i >= 0 && len(items) < limit; i-- {
		items = append(items, copyDelivery(a[i]))
	}
	next := ""
	if end > len(items) {
		raw, _ := json.Marshal([3]string{tenant, webhook, items[len(items)-1].ID})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return items, next, nil
}
