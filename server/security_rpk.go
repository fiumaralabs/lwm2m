package server

import "bytes"

// PublicKeyLookup is implemented by security stores that resolve an RFC
// 7250 raw public key to its endpoint, so a DTLS handshake with an unknown
// key fails before any data exchange (SEC-01, SEC-09). Without it raw keys
// are only checked at Register (SEC-06).
type PublicKeyLookup interface {
	ByPublicKey(spki []byte) (SecurityInfo, bool)
}

// ByPublicKey returns the endpoint whose RPK is spki. A key stored for two
// endpoints matches neither: client keys must be unique (SEC-11).
// ponytail: linear scan, add a key index to Put if fleets get large.
func (s *MemorySecurityStore) ByPublicKey(spki []byte) (SecurityInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found SecurityInfo
	n := 0
	for _, si := range s.byEP {
		if len(si.PublicKey) > 0 && bytes.Equal(si.PublicKey, spki) {
			found = si
			n++
		}
	}
	return found, n == 1
}
