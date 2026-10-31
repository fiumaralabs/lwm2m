package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
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
	Replay(tenant, key string) (TrustAnchorResult, bool)
	CreateResult(tenant, key string, result TrustAnchorResult) TrustAnchorResult
	DeleteMatch(tenant, id string, match store.WebhookMatch) (found, matched bool)
}
type TrustAnchorResult struct {
	Anchor  *TrustAnchor
	Problem *Problem
}
type trustAnchorReplay struct {
	result  TrustAnchorResult
	expires time.Time
}
type memoryTrustAnchorStore struct {
	mu      sync.RWMutex
	limit   int
	count   int
	items   map[string]map[string]TrustAnchor
	replays map[string]map[string]trustAnchorReplay
	now     func() time.Time
}

// NewMemoryTrustAnchorStore bounds the total number of anchors across tenants.
// Nonpositive limits use the default of 1000.
func NewMemoryTrustAnchorStore(limit int) TrustAnchorStore {
	if limit <= 0 {
		limit = 1000
	}
	return &memoryTrustAnchorStore{limit: limit, items: make(map[string]map[string]TrustAnchor), replays: make(map[string]map[string]trustAnchorReplay), now: time.Now}
}
func trustAnchorETag(a TrustAnchor) string {
	b, _ := json.Marshal(a)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("\"anchor-v1-%x\"", sum)
}
func cloneAnchorResult(r TrustAnchorResult) TrustAnchorResult {
	if r.Anchor != nil {
		a := cloneTrustAnchor(*r.Anchor)
		r.Anchor = &a
	}
	if r.Problem != nil {
		p := *r.Problem
		r.Problem = &p
	}
	return r
}
func (s *memoryTrustAnchorStore) Replay(tenant, key string) (TrustAnchorResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.replays[tenant][key]
	if key == "" || !ok || !s.now().Before(r.expires) {
		return TrustAnchorResult{}, false
	}
	return cloneAnchorResult(r.result), true
}
func (s *memoryTrustAnchorStore) CreateResult(tenant, key string, result TrustAnchorResult) TrustAnchorResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if key != "" {
		for tenantID, entries := range s.replays {
			for k, r := range entries {
				if !now.Before(r.expires) {
					delete(entries, k)
				}
			}
			if len(entries) == 0 {
				delete(s.replays, tenantID)
			}
		}
		if r, ok := s.replays[tenant][key]; ok {
			return cloneAnchorResult(r.result)
		}
		count := 0
		for _, entries := range s.replays {
			count += len(entries)
		}
		if count >= 10000 {
			return TrustAnchorResult{Problem: &Problem{Status: 503, Code: "capacity_exceeded", Detail: "Replay capacity reached"}}
		}
	}
	if result.Problem == nil && result.Anchor != nil {
		if err := s.create(tenant, *result.Anchor); err != nil {
			result.Anchor = nil
			if errors.Is(err, ErrTrustAnchorDuplicate) {
				result.Problem = &Problem{Status: 409, Code: "duplicate_trust_anchor", Detail: "Certificate already exists"}
			} else {
				result.Problem = &Problem{Status: 503, Code: "capacity_exceeded", Detail: "Trust anchor capacity reached"}
			}
		}
	}
	if key != "" {
		if s.replays[tenant] == nil {
			s.replays[tenant] = make(map[string]trustAnchorReplay)
		}
		snapshot := cloneAnchorResult(result)
		if snapshot.Anchor != nil {
			snapshot.Anchor.PEM = ""
		}
		s.replays[tenant][key] = trustAnchorReplay{snapshot, now.Add(24 * time.Hour)}
	}
	return cloneAnchorResult(result)
}
func cloneTrustAnchor(a TrustAnchor) TrustAnchor {
	a.Certificates = append([]TrustAnchorCertificate(nil), a.Certificates...)
	return a
}
func (s *memoryTrustAnchorStore) Create(tenant string, a TrustAnchor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.create(tenant, a)
}
func (s *memoryTrustAnchorStore) create(tenant string, a TrustAnchor) error {
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
	found, _ := s.DeleteMatch(tenant, id, store.WebhookMatch{})
	return found
}
func (s *memoryTrustAnchorStore) DeleteMatch(tenant, id string, match store.WebhookMatch) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[tenant][id]
	if !ok {
		return false, false
	}
	matched := !match.Required || match.Any
	for _, tag := range match.Tags {
		if tag == trustAnchorETag(a) {
			matched = true
		}
	}
	if !matched {
		return true, false
	}
	delete(s.items[tenant], id)
	s.count--
	if len(s.items[tenant]) == 0 {
		delete(s.items, tenant)
	}
	return true, true
}
