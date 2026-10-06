package server

import (
	"sync"
	"time"

	piondtls "github.com/fiumaralabs/dtls/v3"
)

// sessionStore keeps DTLS 1.2 sessions so a client that wakes up on a new
// address can resume with an abbreviated handshake (SEC-11, QM-05). The
// patched pion restores the PSK identity with the session, so the resumed
// connection keeps its registration (README C2). pion does not cache
// sessions authenticated with a client certificate or raw key.
type sessionStore struct {
	mu  sync.Mutex
	m   map[string]sessionEntry
	ttl time.Duration
	max int
	now func() time.Time
}

type sessionEntry struct {
	s       piondtls.Session
	expires time.Time
}

func newSessionStore(ttl time.Duration, max int, now func() time.Time) *sessionStore {
	return &sessionStore{m: map[string]sessionEntry{}, ttl: ttl, max: max, now: now}
}

func (st *sessionStore) Set(key []byte, s piondtls.Session) error {
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

func (st *sessionStore) Get(key []byte) (piondtls.Session, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.m[string(key)]
	if !ok || st.now().After(e.expires) {
		delete(st.m, string(key))
		return piondtls.Session{}, nil // pion: zero Session = no resumption
	}
	return e.s, nil
}

func (st *sessionStore) Del(key []byte) error {
	st.mu.Lock()
	delete(st.m, string(key))
	st.mu.Unlock()
	return nil
}
