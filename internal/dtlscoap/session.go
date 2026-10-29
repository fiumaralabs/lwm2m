package dtlscoap

import (
	"bytes"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/pion/dtls/v4"
)

// SessionStore keeps DTLS 1.2 sessions so a client that wakes up on a new
// address can resume with an abbreviated handshake (SEC-11, QM-05). It
// also keeps the PSK identity each session was authenticated with: pion
// stores only the session ID and master secret, so a resumed connection
// has no identity (no ClientKeyExchange) and Conn restores it from here.
// pion does not cache sessions authenticated with a client certificate.
type SessionStore struct {
	mu     sync.Mutex
	m      map[string]sessionEntry
	ttl    time.Duration
	max    int
	now    func() time.Time
	lookup dtls.PSKCallback
}

type sessionEntry struct {
	s           dtls.Session
	identity    []byte
	fingerprint [sha256.Size]byte
	bound       bool
	expires     time.Time
}

// NewSessionStore keeps up to max sessions for ttl each.
func NewSessionStore(ttl time.Duration, max int, now func() time.Time) *SessionStore {
	return &SessionStore{m: map[string]sessionEntry{}, ttl: ttl, max: max, now: now}
}

func (st *SessionStore) Set(key []byte, s dtls.Session) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	if len(st.m) >= st.max {
		for k, e := range st.m {
			if now.After(e.expires) {
				delete(st.m, k)
			}
		}
		if len(st.m) >= st.max {
			return nil // ponytail: full, the client just gets a full handshake
		}
	}
	st.m[string(key)] = sessionEntry{s: s, expires: now.Add(st.ttl)}
	return nil
}

func (st *SessionStore) Get(key []byte) (dtls.Session, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.m[string(key)]
	if !ok || st.now().After(e.expires) {
		delete(st.m, string(key))
		return dtls.Session{}, nil // pion: zero Session = no resumption
	}
	if st.lookup != nil {
		psk, err := st.lookup(e.identity)
		if !e.bound || err != nil || sha256.Sum256(psk) != e.fingerprint {
			delete(st.m, string(key))
			return dtls.Session{}, nil
		}
	}
	return e.s, nil
}

func (st *SessionStore) Del(key []byte) error {
	st.mu.Lock()
	delete(st.m, string(key))
	st.mu.Unlock()
	return nil
}

// setIdentity records the PSK identity of a full handshake's session.
func (st *SessionStore) setIdentity(id, identity []byte, fingerprint [sha256.Size]byte, bound bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if e, ok := st.m[string(id)]; ok {
		e.identity = bytes.Clone(identity)
		e.fingerprint = fingerprint
		e.bound = bound
		st.m[string(id)] = e
	}
}

// identity is the PSK identity of a resumed session.
func (st *SessionStore) identity(id []byte) []byte {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.m[string(id)].identity
}
