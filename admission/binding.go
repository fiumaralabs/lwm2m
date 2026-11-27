package admission

import (
	"context"
	"errors"
	"sync"
	"time"
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

// MaxExchangeIDLifetime is the intended maximum retry lifetime. Enforcement
// is not implemented here; bindings must not be pruned until it is enforced.
const MaxExchangeIDLifetime = 24 * time.Hour
const DefaultBindingRetention = 7 * 24 * time.Hour

// Receipt is the authenticated decision persisted with its binding.
type Receipt struct {
	Kind, ReservationID        string
	DecisionID, DecisionDigest string
	IssuedAt, ExpiresAt        time.Time
}

// MemoryBindingStore is process-local and non-durable. Its callback receives a
// nil transaction and must not mutate external state before it succeeds.
type MemoryBindingStore struct {
	mu       sync.Mutex
	bindings map[string]Binding
	receipts map[string]Receipt
	admitted map[admittedIdentity]struct{}
	existing map[string]admittedIdentity
}

func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{bindings: make(map[string]Binding), receipts: make(map[string]Receipt), admitted: make(map[admittedIdentity]struct{}), existing: make(map[string]admittedIdentity)}
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
