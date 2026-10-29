package dtlscoap

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/pion/dtls/v4"
)

// An old handshake may finish and publish its session after rotation.
func TestSessionStoredAfterRotationIsRejected(t *testing.T) {
	var mu sync.RWMutex
	key := []byte("old")
	lookup := func([]byte) ([]byte, error) { mu.RLock(); defer mu.RUnlock(); return append([]byte(nil), key...), nil }
	st := NewSessionStore(time.Hour, 10, time.Now)
	st.lookup = lookup
	old, _ := lookup(nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); mu.Lock(); key = []byte("new"); mu.Unlock() }()
	wg.Wait()
	id := []byte("session")
	st.Set(id, dtls.Session{ID: id})
	st.setIdentity(id, []byte("device"), sha256.Sum256(old), true)
	got, err := st.Get(id)
	if err != nil || len(got.ID) != 0 {
		t.Fatalf("stale session accepted: %v, %v", got.ID, err)
	}
}
