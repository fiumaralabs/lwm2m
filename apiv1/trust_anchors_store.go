package apiv1

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// TrustAnchor is a named PEM bundle and its parsed certificate metadata.
type TrustAnchor struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	PEM          string                   `json:"-"`
	Certificates []TrustAnchorCertificate `json:"certificates"`
}
type TrustAnchorCertificate struct {
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"`
}

var ErrTrustAnchorDuplicate = errors.New("duplicate trust anchor")
var ErrTrustAnchorLimit = errors.New("trust anchor limit reached")

// TrustAnchorStore must atomically enforce fingerprint uniqueness per tenant.
// List returns anchors ordered by ascending ID. Implementations return owned copies.
type TrustAnchorStore interface {
	Create(tenant string, anchor TrustAnchor) error
	List(tenant string) []TrustAnchor
	Get(tenant, id string) (TrustAnchor, bool)
	Delete(tenant, id string) bool
}
type memoryTrustAnchorStore struct {
	mu    sync.RWMutex
	limit int
	count int
	items map[string]map[string]TrustAnchor
}

// NewMemoryTrustAnchorStore bounds the total number of anchors across tenants.
// Nonpositive limits use the default of 1000.
func NewMemoryTrustAnchorStore(limit int) TrustAnchorStore {
	if limit <= 0 {
		limit = 1000
	}
	return &memoryTrustAnchorStore{limit: limit, items: make(map[string]map[string]TrustAnchor)}
}
func cloneTrustAnchor(a TrustAnchor) TrustAnchor {
	a.Certificates = append([]TrustAnchorCertificate(nil), a.Certificates...)
	return a
}
func (s *memoryTrustAnchorStore) Create(tenant string, a TrustAnchor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.items[tenant] {
		if existing.ID == a.ID {
			return ErrTrustAnchorDuplicate
		}
		for _, x := range existing.Certificates {
			for _, y := range a.Certificates {
				if x.Fingerprint == y.Fingerprint {
					return ErrTrustAnchorDuplicate
				}
			}
		}
	}
	if s.count >= s.limit {
		return ErrTrustAnchorLimit
	}
	if s.items[tenant] == nil {
		s.items[tenant] = make(map[string]TrustAnchor)
	}
	s.items[tenant][a.ID] = cloneTrustAnchor(a)
	s.count++
	return nil
}
func (s *memoryTrustAnchorStore) List(tenant string) []TrustAnchor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TrustAnchor, 0, len(s.items[tenant]))
	for _, a := range s.items[tenant] {
		out = append(out, cloneTrustAnchor(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *memoryTrustAnchorStore) Get(tenant, id string) (TrustAnchor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.items[tenant][id]
	return cloneTrustAnchor(a), ok
}
func (s *memoryTrustAnchorStore) Delete(tenant, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[tenant][id]; !ok {
		return false
	}
	delete(s.items[tenant], id)
	s.count--
	if len(s.items[tenant]) == 0 {
		delete(s.items, tenant)
	}
	return true
}
