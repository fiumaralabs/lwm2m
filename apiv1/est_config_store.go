package apiv1

import "sync"

// ESTConfig contains the tenant's issuer material. It must not be serialized in responses.
type ESTConfig struct {
	Enabled      bool
	Certificate  string
	PrivateKey   string
	ValidityDays int
	Selector     string
}

// ESTConfigStore stores tenant-scoped EST settings.
type ESTConfigStore interface {
	Get(tenant string) (ESTConfig, bool)
	Put(tenant string, config ESTConfig) bool
}

type memoryESTConfigStore struct {
	mu    sync.RWMutex
	items map[string]ESTConfig
}

// NewMemoryESTConfigStore creates a volatile EST configuration store.
func NewMemoryESTConfigStore() ESTConfigStore {
	return &memoryESTConfigStore{items: make(map[string]ESTConfig)}
}
func (s *memoryESTConfigStore) Get(tenant string) (ESTConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.items[tenant]
	return c, ok
}
func (s *memoryESTConfigStore) Put(tenant string, c ESTConfig) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.items[tenant]
	s.items[tenant] = c
	return exists
}
