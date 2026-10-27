package apiv1

import (
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type keyMetadata = store.KeyMetadata
type keyStoreContext struct{}
type keyStore struct {
	store.APIKeys
	bootstrap *memory.APIKeys
	entries   map[string]credential
	mu        sync.Mutex
}

func newKeyStore(entries map[string]credential) *keyStore {
	s := &keyStore{APIKeys: &memory.APIKeys{}, bootstrap: &memory.APIKeys{}, entries: entries}
	for id, c := range entries {
		_ = s.bootstrap.Create(store.APIKey{KeyMetadata: keyMetadata{ID: id, Name: id, Scopes: strings.Split(strings.Trim(c.scopes, ","), ","), CreatedAt: time.Now().UTC()}, Hash: c.hash, Tenant: c.tenant})
	}
	return s
}

// WithStore uses durable storage for managed keys. File keys remain ephemeral
// bootstrap credentials, so removing a file entry cannot leave a durable copy.
func (k Keys) WithStore(s store.APIKeys) Keys {
	k.state = newKeyStore(k.entries)
	k.state.APIKeys = s
	return k
}
func (s *keyStore) Get(id string) (store.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.entries[id]; ok {
		k, err := s.bootstrap.Get(id)
		k.Hash, k.Tenant, k.Scopes = c.hash, c.tenant, strings.Split(strings.Trim(c.scopes, ","), ",")
		return k, err
	}
	return s.APIKeys.Get(id)
}
func (s *keyStore) Delete(tenant, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.entries[id]; ok {
		if c.tenant != tenant {
			return store.ErrAPIKeyNotFound
		}
		delete(s.entries, id)
		return s.bootstrap.Delete(c.tenant, id)
	}
	return s.APIKeys.Delete(tenant, id)
}
func (s *keyStore) Touch(id string, at time.Time) error {
	if _, err := s.bootstrap.Get(id); err == nil {
		return s.bootstrap.Touch(id, at)
	}
	return s.APIKeys.Touch(id, at)
}
func (s *keyStore) List(tenant string) ([]keyMetadata, error) {
	items, err := s.APIKeys.List(tenant)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.bootstrap.List(tenant)
	return append(items, bootstrap...), err
}
