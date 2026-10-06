package server

import (
	"crypto/rand"
	"encoding/base32"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
)

// Registration is the server's record of a registered client (C §6.2.1).
// A stored Registration is never mutated; an Update stores a new copy.
type Registration struct {
	ID             string        // location segment: Location-Path rd/<ID>
	Endpoint       string        // ep, or derived from the security identity (REG-02)
	Version        string        // lwm2m=, normalised to "1.0", "1.1" or "1.2"
	Lifetime       time.Duration // 0 = infinite (REG-23)
	Binding        string        // b=, as sent ("U" default, REG-18)
	QueueMode      bool          // 1.1+ Q flag, or 1.0 b=UQ/SQ/UQS (QM-01)
	SMS            string        // sms= (REG-22)
	ProfileIDs     []string      // pid= (PROF-02)
	Objects        []link.Object // registered object list (REG-07)
	RawLinks       string        // the object list payload as received
	RootPath       string        // alternate path from rt="oma.lwm2m" (GEN-08), "" = none
	ContentFormats []lwm2m.ContentFormat
	Identity       Identity
	Addr           net.Addr
	RegisteredAt   time.Time
	LastUpdate     time.Time

	peer Peer // current transport session
}

// Peer returns the transport session the client registered or last
// updated on; downlinks go over it. It is nil for a Registration that was
// not created by the server (e.g. one loaded into a Store).
func (r *Registration) Peer() Peer { return r.peer }

// Expired reports whether the lifetime has elapsed at now.
func (r *Registration) Expired(now time.Time) bool {
	return r.Lifetime > 0 && now.After(r.LastUpdate.Add(r.Lifetime))
}

// HasObject reports whether object oid is registered.
func (r *Registration) HasObject(oid uint16) bool {
	_, ok := r.Object(oid)
	return ok
}

// Object returns the registered entry of object oid.
func (r *Registration) Object(oid uint16) (link.Object, bool) {
	for _, o := range r.Objects {
		if o.ID == oid {
			return o, true
		}
	}
	return link.Object{}, false
}

// HasInstance reports whether /oid/iid is registered.
func (r *Registration) HasInstance(oid, iid uint16) bool {
	o, ok := r.Object(oid)
	return ok && slices.Contains(o.Instances, iid)
}

// Store holds registrations. Implementations must be safe for concurrent use.
type Store interface {
	Add(*Registration) (replaced *Registration)
	ByID(id string) (*Registration, bool)
	ByEndpoint(ep string) (*Registration, bool)
	// Update replaces the stored registration with the same ID; false if
	// it no longer exists.
	Update(*Registration) bool
	Remove(id string) (*Registration, bool)
	All() []*Registration
}

// MemoryStore is an in-memory Store.
type MemoryStore struct {
	mu   sync.RWMutex
	byID map[string]*Registration
	byEP map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: map[string]*Registration{}, byEP: map[string]string{}}
}

// Add stores r and returns the previous registration of the same endpoint,
// which it removes (REG-12).
func (s *MemoryStore) Add(r *Registration) *Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var old *Registration
	if id, ok := s.byEP[r.Endpoint]; ok {
		old = s.byID[id]
		delete(s.byID, id)
	}
	s.byID[r.ID] = r
	s.byEP[r.Endpoint] = r.ID
	return old
}

func (s *MemoryStore) Update(r *Registration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[r.ID]; !ok {
		return false
	}
	s.byID[r.ID] = r
	return true
}

func (s *MemoryStore) ByID(id string) (*Registration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[id]
	return r, ok
}

func (s *MemoryStore) ByEndpoint(ep string) (*Registration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byEP[ep]
	if !ok {
		return nil, false
	}
	return s.byID[id], true
}

func (s *MemoryStore) Remove(id string) (*Registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if ok {
		delete(s.byID, id)
		if s.byEP[r.Endpoint] == id {
			delete(s.byEP, r.Endpoint)
		}
	}
	return r, ok
}

func (s *MemoryStore) All() []*Registration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Registration, 0, len(s.byID))
	for _, r := range s.byID {
		out = append(out, r)
	}
	return out
}

// newRegistrationID returns a 16-character location segment. Short enough
// for every client (Zephyr ≤ 32 B, Anjay Lite ≤ 40 B per segment; C12).
func newRegistrationID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}
