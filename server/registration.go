package server

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
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
	Addr           net.Addr // address at the last Register/Update; see RemoteAddr
	RegisteredAt   time.Time
	LastUpdate     time.Time

	peer Peer // current transport session
}

// Peer returns the transport session the client registered or last
// updated on; downlinks go over it. It is nil for a Registration that was
// not created by the server (e.g. one loaded into a Store).
func (r *Registration) Peer() Peer { return r.peer }

// RemoteAddr returns the client's current address: the live transport
// session's address when there is one, else Addr. With a DTLS Connection ID
// the session can migrate to a new path after the Update that carried it
// was handled (pion moves the address after delivering the record), so Addr
// may lag; RemoteAddr does not.
func (r *Registration) RemoteAddr() net.Addr {
	if r.peer != nil {
		if a := r.peer.RemoteAddr(); a != nil {
			return a
		}
	}
	return r.Addr
}

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

// ErrRegistrationUnavailable indicates a temporary registration store failure.
var ErrRegistrationUnavailable = errors.New("server: registration unavailable")

// RegisterContext describes the validated request and its transport identity.
// Fingerprint is a SHA-256 digest of deterministic request content and identity,
// independent of message IDs, tokens, registration IDs, and server time.
// Identity is authenticated only for secured transports; NoSec uses the source address.
type RegisterContext struct {
	Endpoint    string
	Identity    Identity
	Fingerprint [32]byte
	// Exchange identifies a datagram exchange within the CoAP exchange lifetime.
	// Nil means the binding does not expose a datagram message ID. It is not a
	// globally unique identifier: message IDs and tokens can be reused.
	Exchange *[32]byte
}

// ContextCheckedAdder optionally commits a Register using its exchange context.
// It takes precedence over CheckedAdder. On error, it must leave the store and
// any existing registration unchanged. Implementations must be concurrency-safe.
type ContextCheckedAdder interface {
	AddCheckedContext(RegisterContext, *Registration) (replaced *Registration, err error)
}

// CheckedAdder optionally lets a Store reject a Register before it is acknowledged.
// On error, AddChecked must leave the store and any existing registration unchanged.
// Implementations must be safe for concurrent use.
type CheckedAdder interface {
	AddChecked(*Registration) (replaced *Registration, err error)
}

// CommittedRegistrationPublisher attaches a live peer after a checked commit.
// It must ignore registrations that have since been replaced or removed.
type CommittedRegistrationPublisher interface {
	PublishCommitted(*Registration)
}

const replayLifetime = 247 * time.Second
const replayLimit = 1024

type replayEntry struct {
	registration *Registration
	at           time.Time
}

// MemoryStore is an in-memory Store.
type MemoryStore struct {
	mu          sync.RWMutex
	byID        map[string]*Registration
	byEP        map[string]string
	replays     map[[32]byte]replayEntry
	replayTTL   time.Duration
	replayTimer *time.Timer
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: map[string]*Registration{}, byEP: map[string]string{}}
}

// Add stores r and returns the previous registration of the same endpoint,
// which it removes (REG-12).
func (s *MemoryStore) Add(r *Registration) *Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLocked(r)
}

func (s *MemoryStore) addLocked(r *Registration) *Registration {
	var old *Registration
	if id, ok := s.byEP[r.Endpoint]; ok {
		old = s.byID[id]
		delete(s.byID, id)
	}
	s.byID[r.ID] = r
	s.byEP[r.Endpoint] = r.ID
	return old
}

func (s *MemoryStore) forgetReplay(key [32]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.replays, key)
}

// lookupReplay discards expired or no-longer-live history before admission.
func (s *MemoryStore) lookupReplay(key [32]byte) (replayEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.replays[key]
	ttl := s.replayTTL
	if ttl == 0 {
		ttl = replayLifetime
	}
	if ok {
		_, live := s.byID[entry.registration.ID]
		if !time.Now().Before(entry.at.Add(ttl)) || !live {
			delete(s.replays, key)
			return replayEntry{}, false
		}
	}
	return entry, ok
}

// AddCheckedReplay retains at most replayLimit exchanges for replayLifetime.
// This in-memory history does not survive creation of a new store.
func (s *MemoryStore) AddCheckedReplay(ctx RegisterContext, r *Registration) (*Registration, *Registration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Exchange == nil {
		return r, s.addLocked(r), false, nil
	}
	ttl := s.replayTTL
	if ttl == 0 {
		ttl = replayLifetime
	}
	now := time.Now()
	var oldestKey [32]byte
	var oldest time.Time
	for key, entry := range s.replays {
		if !now.Before(entry.at.Add(ttl)) {
			delete(s.replays, key)
			continue
		}
		if oldest.IsZero() || entry.at.Before(oldest) {
			oldest, oldestKey = entry.at, key
		}
	}
	if entry, ok := s.replays[*ctx.Exchange]; ok {
		return entry.registration, nil, true, nil
	}
	if s.replays == nil {
		s.replays = make(map[[32]byte]replayEntry)
	} else if len(s.replays) >= replayLimit {
		delete(s.replays, oldestKey)
	}
	old := s.addLocked(r)
	s.replays[*ctx.Exchange] = replayEntry{registration: r, at: now}
	if s.replayTimer == nil {
		s.replayTimer = time.AfterFunc(ttl, func() { s.expireReplays(ttl) })
	}
	return r, old, false, nil
}

// expireReplays schedules only one timer per store and stops once idle history is empty.
func (s *MemoryStore) expireReplays(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var next time.Time
	for key, entry := range s.replays {
		expires := entry.at.Add(ttl)
		if !now.Before(expires) {
			delete(s.replays, key)
		} else if next.IsZero() || expires.Before(next) {
			next = expires
		}
	}
	s.replayTimer = nil
	if !next.IsZero() {
		s.replayTimer = time.AfterFunc(time.Until(next), func() { s.expireReplays(ttl) })
	}
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
