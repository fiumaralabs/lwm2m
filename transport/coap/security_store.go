package coap

import (
	"sync"

	"github.com/fiumaralabs/lwm2m/server"
)

// RevokingSecurityStore decorates a store so successful credential mutations
// close established sessions using the previous PSK identity. All mutations
// must use this wrapper; retaining and mutating the underlying store bypasses it.
// In-flight handshakes and already-dispatched requests are not covered (#97).
type RevokingSecurityStore struct {
	server.SecurityStore
	mu     sync.Mutex
	revoke func(string)
}

// NewRevokingSecurityStore wraps store and invokes revoke after each successful
// replacement or removal of an existing PSK credential.
func NewRevokingSecurityStore(store server.SecurityStore, revoke func(string)) *RevokingSecurityStore {
	return &RevokingSecurityStore{SecurityStore: store, revoke: revoke}
}

func (s *RevokingSecurityStore) Put(info server.SecurityInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.SecurityStore.ByEndpoint(info.Endpoint)
	if err := s.SecurityStore.Put(info); err != nil {
		return err
	}
	if ok && old.PSKIdentity != "" {
		s.revoke(old.PSKIdentity)
	}
	return nil
}

func (s *RevokingSecurityStore) Remove(endpoint string) (server.SecurityInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.SecurityStore.Remove(endpoint)
	if ok && old.PSKIdentity != "" {
		s.revoke(old.PSKIdentity)
	}
	return old, ok
}
