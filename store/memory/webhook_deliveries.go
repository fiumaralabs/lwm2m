package memory

import (
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/fiumaralabs/lwm2m/store"
)

// WebhookDeliveries retains the last 1000 attempts per webhook in memory.
type WebhookDeliveries struct {
	mu      sync.Mutex
	entries map[[2]string][]store.WebhookDelivery
}

func copyDelivery(d store.WebhookDelivery) store.WebhookDelivery {
	if d.Status != nil {
		n := *d.Status
		d.Status = &n
	}
	return d
}

func (s *WebhookDeliveries) Append(tenant, webhook string, d store.WebhookDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[[2]string][]store.WebhookDelivery)
	}
	k := [2]string{tenant, webhook}
	a := append(s.entries[k], copyDelivery(d))
	if len(a) > 1000 {
		a = append([]store.WebhookDelivery(nil), a[len(a)-1000:]...)
	}
	s.entries[k] = a
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
