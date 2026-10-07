package server

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// SecurityInfo is the credential the server holds for one endpoint
// (T §5.2.4, SEC-05/09/10). Exactly one credential kind is set.
type SecurityInfo struct {
	Endpoint    string
	PSKIdentity string
	PSKKey      []byte
	PublicKey   []byte // RPK: client SubjectPublicKeyInfo DER; refused, see ErrRPKUnsupported
	X509        bool   // the client authenticates with a certificate whose CN is Endpoint
	// OSCORE and other modes are configured in their own packages.
}

// SecurityStore resolves credentials. Implementations must be safe for
// concurrent use.
type SecurityStore interface {
	ByEndpoint(ep string) (SecurityInfo, bool)
	ByPSKIdentity(identity string) (SecurityInfo, bool)
	Put(SecurityInfo) error
	Remove(ep string) (SecurityInfo, bool)
	All() []SecurityInfo
}

// MemorySecurityStore is an in-memory SecurityStore.
type MemorySecurityStore struct {
	mu    sync.RWMutex
	byEP  map[string]SecurityInfo
	byPSK map[string]string
}

func NewMemorySecurityStore() *MemorySecurityStore {
	return &MemorySecurityStore{byEP: map[string]SecurityInfo{}, byPSK: map[string]string{}}
}

var ErrDuplicatePSKIdentity = errors.New("server: PSK identity already used by another endpoint")

// ErrRPKUnsupported refuses RPK credentials (security mode 1): RFC 7250
// raw public keys need handshake support pion/dtls does not have yet
// (pending upstream, branch rfc7250-raw-public-keys).
var ErrRPKUnsupported = errors.New("RPK (RFC 7250 raw public key) credentials are not supported: pion/dtls lacks RFC 7250 (pending upstream)")

// Put stores si. RPK credentials are refused with ErrRPKUnsupported.
func (s *MemorySecurityStore) Put(si SecurityInfo) error {
	if len(si.PublicKey) > 0 {
		return ErrRPKUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if si.PSKIdentity != "" {
		if ep, ok := s.byPSK[si.PSKIdentity]; ok && ep != si.Endpoint {
			return ErrDuplicatePSKIdentity
		}
	}
	if old, ok := s.byEP[si.Endpoint]; ok && old.PSKIdentity != "" {
		delete(s.byPSK, old.PSKIdentity)
	}
	s.byEP[si.Endpoint] = si
	if si.PSKIdentity != "" {
		s.byPSK[si.PSKIdentity] = si.Endpoint
	}
	return nil
}

func (s *MemorySecurityStore) ByEndpoint(ep string) (SecurityInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	si, ok := s.byEP[ep]
	return si, ok
}

func (s *MemorySecurityStore) ByPSKIdentity(id string) (SecurityInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ep, ok := s.byPSK[id]
	if !ok {
		return SecurityInfo{}, false
	}
	return s.byEP[ep], true
}

func (s *MemorySecurityStore) Remove(ep string) (SecurityInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	si, ok := s.byEP[ep]
	if ok {
		delete(s.byEP, ep)
		delete(s.byPSK, si.PSKIdentity)
	}
	return si, ok
}

// Matches reports whether an authenticated identity is the one stored for
// the endpoint (SEC-06: equality or lookup, never trust ep alone).
func (si SecurityInfo) Matches(id Identity) bool {
	switch id.Mode {
	case ModePSK:
		return si.PSKIdentity != "" && si.PSKIdentity == id.PSKIdentity
	case ModeRPK:
		return len(si.PublicKey) > 0 && bytes.Equal(si.PublicKey, id.PublicKey)
	case ModeX509:
		return si.X509 && id.CertCN == si.Endpoint
	}
	return false
}

// endpointFromIdentity derives the endpoint name when the client omits ep
// (REG-02): the PSK identity's endpoint, or the certificate CN.
func endpointFromIdentity(store SecurityStore, id Identity) (string, bool) {
	switch id.Mode {
	case ModePSK:
		if si, ok := store.ByPSKIdentity(id.PSKIdentity); ok {
			return si.Endpoint, true
		}
	case ModeX509:
		if id.CertCN != "" {
			return id.CertCN, true
		}
	}
	return "", false
}

// All returns every stored credential, ordered by endpoint.
func (s *MemorySecurityStore) All() []SecurityInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SecurityInfo, 0, len(s.byEP))
	for _, si := range s.byEP {
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Endpoint < out[j].Endpoint })
	return out
}
