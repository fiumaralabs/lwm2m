package memory

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// APIKeys is a concurrent store whose zero value is ready for use.
type APIKeys struct {
	mu   sync.RWMutex
	keys map[string]store.APIKey
}

func cloneKey(k store.APIKey) store.APIKey {
	k.Scopes = slices.Clone(k.Scopes)
	if k.ExpiresAt != nil {
		at := *k.ExpiresAt
		k.ExpiresAt = &at
	}
	if k.LastUsedAt != nil {
		at := *k.LastUsedAt
		k.LastUsedAt = &at
	}
	return k
}
func (s *APIKeys) Get(id string) (store.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[id]
	if !ok {
		return store.APIKey{}, store.ErrAPIKeyNotFound
	}
	return cloneKey(k), nil
}
func (s *APIKeys) Create(k store.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[k.ID]; ok {
		return store.ErrAPIKeyExists
	}
	if s.keys == nil {
		s.keys = make(map[string]store.APIKey)
	}
	s.keys[k.ID] = cloneKey(k)
	return nil
}
func (s *APIKeys) List(tenant string) ([]store.KeyMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]store.KeyMetadata, 0)
	for _, k := range s.keys {
		if k.Tenant == tenant {
			out = append(out, cloneKey(k).KeyMetadata)
		}
	}
	slices.SortFunc(out, func(a, b store.KeyMetadata) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
func (s *APIKeys) Delete(tenant, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[id]; !ok || k.Tenant != tenant {
		return store.ErrAPIKeyNotFound
	}
	delete(s.keys, id)
	return nil
}
func (s *APIKeys) Touch(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return store.ErrAPIKeyNotFound
	}
	k.LastUsedAt = &at
	s.keys[id] = k
	return nil
}

var _ store.APIKeys = (*APIKeys)(nil)
