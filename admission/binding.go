package admission

import (
	"context"
	"errors"
	"sync"
)

// Binding identifies a validated decision and its original request.
type Binding struct {
	ExchangeID, TenantID, IdentityID, Fingerprint, RequestDigest, DecisionDigest string
}

// ErrBindingConflict indicates an exchange ID was reused with different content.
var ErrBindingConflict = errors.New("admission exchange binding conflict")

// BindingStore records first use atomically. Marking is not atomic with external
// admission effects; callers must not wire this into a daemon until that is fixed.
type BindingStore interface {
	BindAndMarkApplied(context.Context, Binding) (alreadyApplied bool, err error)
}

// TODO(#257): define retention no shorter than the maximum exchange ID lifetime.
// Until then bindings are durable tombstones and must never be purged.
type MemoryBindingStore struct {
	mu       sync.Mutex
	bindings map[string]Binding
}

func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{bindings: make(map[string]Binding)}
}
func (s *MemoryBindingStore) BindAndMarkApplied(ctx context.Context, b Binding) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.bindings[b.ExchangeID]; ok {
		if old != b {
			return false, ErrBindingConflict
		}
		return true, nil
	}
	s.bindings[b.ExchangeID] = b
	return false, nil
}
