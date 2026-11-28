package server

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"time"

	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestRegisterLostACKReplay(t *testing.T) {
	store := NewMemoryStore()
	s := New(Config{Store: store})
	defer s.Close()
	mid := uint16(42)
	m := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid, Token: []byte{1}}
	peer := exchangePeer{addr: "192.0.2.1"}
	first, after := s.HandleUplink(peer, m)
	after() // response was committed, but its ACK was lost
	second, after := s.HandleUplink(peer, m)
	after()
	if first.Code != codes.Created || second.Code != codes.Created || len(first.Location) != 2 || len(second.Location) != 2 || first.Location[1] != second.Location[1] {
		t.Fatalf("retransmit changed response: first=%+v second=%+v", first, second)
	}
	if len(store.All()) != 1 {
		t.Fatalf("registrations: %d", len(store.All()))
	}
}

func TestRegisterReplayDifferentMIDAndServerRestart(t *testing.T) {
	store := NewMemoryStore()
	peer := exchangePeer{addr: "192.0.2.1"}
	mid := uint16(42)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid, Token: []byte{1}}
	firstServer := New(Config{Store: store})
	first, after := firstServer.HandleUplink(peer, msg)
	after()
	firstServer.Close()
	secondServer := New(Config{Store: store})
	defer secondServer.Close()
	replay, after := secondServer.HandleUplink(peer, msg)
	after()
	if first.Location[1] != replay.Location[1] {
		t.Fatal("same store lost committed exchange across server restart")
	}
	mid++
	distinct, after := secondServer.HandleUplink(peer, msg)
	after()
	if distinct.Location[1] == first.Location[1] {
		t.Fatal("different MID reused location")
	}
	mid--
	again, after := secondServer.HandleUplink(peer, msg)
	after()
	if again.Code != codes.Created || again.Location[1] == first.Location[1] {
		t.Fatal("replayed a replaced registration")
	}
	if len(store.All()) != 1 || store.All()[0].ID != again.Location[1] {
		t.Fatal("new registration was not committed")
	}
}

func TestMemoryReplayRetention(t *testing.T) {
	store := NewMemoryStore()
	var key [32]byte
	ctx := RegisterContext{Exchange: &key}
	original := &Registration{ID: "original", Endpoint: "node"}
	store.AddCheckedReplay(ctx, original)
	entry := store.replays[key]
	entry.at = time.Now().Add(-replayLifetime)
	store.replays[key] = entry
	replacement := &Registration{ID: "expired", Endpoint: "node"}
	committed, _, replayed, err := store.AddCheckedReplay(ctx, replacement)
	if err != nil || replayed || committed != replacement {
		t.Fatal("expired exchange did not evict")
	}
	for i := 1; i <= replayLimit; i++ {
		binary.BigEndian.PutUint64(key[:8], uint64(i))
		store.AddCheckedReplay(ctx, &Registration{ID: "item", Endpoint: "node"})
	}
	if len(store.replays) != replayLimit {
		t.Fatalf("replay history size = %d", len(store.replays))
	}
	key = [32]byte{}
	_, _, replayed, err = store.AddCheckedReplay(ctx, original)
	if err != nil || replayed {
		t.Fatal("oldest exchange did not evict at capacity")
	}
}

func TestMemoryReplayExpiresWhileIdle(t *testing.T) {
	store := NewMemoryStore()
	store.replayTTL = 30 * time.Millisecond
	var key [32]byte
	store.AddCheckedReplay(RegisterContext{Exchange: &key}, &Registration{ID: "a", Endpoint: "node"})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.RLock()
		remaining := len(store.replays)
		store.mu.RUnlock()
		if remaining == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expired replay retained while store was idle")
}

func TestRegisterWithoutMessageIDStillReplaces(t *testing.T) {
	store := NewMemoryStore()
	s := New(Config{Store: store})
	defer s.Close()
	m := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>")}
	peer := exchangePeer{addr: "192.0.2.1"}
	first, after := s.HandleUplink(peer, m)
	after()
	second, after := s.HandleUplink(peer, m)
	after()
	if first.Location[1] == second.Location[1] {
		t.Fatal("message without MID was replayed")
	}
}

// wrappedClampStore mirrors the demo's interface-embedding lifetime clamp.
type wrappedClampStore struct {
	Store
	adds int
}

func (s *wrappedClampStore) Add(r *Registration) *Registration {
	s.adds++
	if r.Lifetime > time.Minute {
		r.Lifetime = time.Minute
	}
	return s.Store.Add(r)
}

func TestRegisterReplayThroughWrappedStore(t *testing.T) {
	store := &wrappedClampStore{Store: NewMemoryStore()}
	srv := New(Config{Store: store})
	defer srv.Close()
	mid := uint16(42)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid, Token: []byte{1}}
	peer := exchangePeer{addr: "192.0.2.1"}
	first, done := srv.HandleUplink(peer, msg)
	done()
	second, done := srv.HandleUplink(peer, msg)
	done()
	if first.Code != codes.Created || second.Code != codes.Created || first.Location[1] != second.Location[1] {
		t.Fatalf("replay changed response: %+v %+v", first, second)
	}
	reg, ok := store.ByID(first.Location[1])
	if !ok || reg.Lifetime != time.Minute || store.adds != 1 {
		t.Fatalf("clamp not honored: %+v adds=%d", reg, store.adds)
	}
}

func TestRegisterReplayDoesNotConsumeAuthorization(t *testing.T) {
	calls := 0
	srv := New(Config{Authorize: func(string, Identity) bool { calls++; return calls == 1 }})
	defer srv.Close()
	mid := uint16(43)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid, Token: []byte{1}}
	peer := exchangePeer{addr: "192.0.2.1"}
	first, done := srv.HandleUplink(peer, msg)
	done()
	second, done := srv.HandleUplink(peer, msg)
	done()
	if first.Code != codes.Created || second.Code != codes.Created || first.Location[1] != second.Location[1] || calls != 1 {
		t.Fatalf("replay: first=%+v second=%+v calls=%d", first, second, calls)
	}
	if !srv.RemoveRegistration("node") {
		t.Fatal("remove failed")
	}
	third, done := srv.HandleUplink(peer, msg)
	done()
	if third.Code != codes.Forbidden || calls != 2 {
		t.Fatalf("removed registration replayed: %+v calls=%d", third, calls)
	}
}

type secureReplayPeer struct{ exchangePeer }

func (p secureReplayPeer) Identity() Identity {
	return Identity{Mode: ModePSK, PSKIdentity: "identity"}
}
func TestRegisterReplayRejectsRevokedSecurity(t *testing.T) {
	calls := 0
	s := New(Config{Authorize: func(string, Identity) bool { calls++; return true }})
	defer s.Close()
	if err := s.Security().Put(SecurityInfo{Endpoint: "node", PSKIdentity: "identity", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	mid := uint16(15)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid}
	peer := secureReplayPeer{exchangePeer{addr: "192.0.2.1"}}
	first, done := s.HandleUplink(peer, msg)
	done()
	if first.Code != codes.Created {
		t.Fatalf("first: %+v", first)
	}
	if _, ok, err := s.RevokeSecurity("node"); err != nil || !ok {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	retry, done := s.HandleUplink(peer, msg)
	done()
	if retry.Code == codes.Created {
		t.Fatalf("revoked retry accepted: %+v", retry)
	}
}
func TestRegisterReplayExpiredRegistrationReauthorizes(t *testing.T) {
	now := time.Now()
	calls := 0
	s := New(Config{Now: func() time.Time { return now }, Authorize: func(string, Identity) bool { calls++; return calls == 1 }})
	defer s.Close()
	mid := uint16(12)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=1", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid}
	peer := exchangePeer{addr: "192.0.2.1"}
	first, done := s.HandleUplink(peer, msg)
	done()
	if first.Code != codes.Created {
		t.Fatalf("first: %+v", first)
	}
	now = now.Add(2 * time.Second)
	retry, done := s.HandleUplink(peer, msg)
	done()
	if retry.Code != codes.Forbidden || calls != 2 {
		t.Fatalf("expired retry: %+v calls=%d", retry, calls)
	}
}

func TestConcurrentRegisterReplayConsumesOneAuthorization(t *testing.T) {
	calls := 0
	s := New(Config{Store: &wrappedClampStore{Store: NewMemoryStore()}, Authorize: func(string, Identity) bool { calls++; return calls == 1 }})
	defer s.Close()
	mid := uint16(55)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid}
	peer := exchangePeer{addr: "192.0.2.1"}
	start := make(chan struct{})
	results := make(chan *Message, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, done := s.HandleUplink(peer, msg)
			done()
			results <- response
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var id string
	for response := range results {
		if response.Code != codes.Created || len(response.Location) != 2 {
			t.Fatalf("response: %+v", response)
		}
		if id != "" && id != response.Location[1] {
			t.Fatal("different locations")
		}
		id = response.Location[1]
	}
	if calls != 1 {
		t.Fatalf("authorization called %d times", calls)
	}
}
func TestRegisterReplayDoesNotHideChangedContentFormat(t *testing.T) {
	s := New(Config{})
	defer s.Close()
	mid := uint16(56)
	msg := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), MessageID: &mid}
	peer := exchangePeer{addr: "192.0.2.1"}
	first, done := s.HandleUplink(peer, msg)
	done()
	if first.Code != codes.Created {
		t.Fatalf("first: %+v", first)
	}
	invalid := lwm2m.ContentFormat(0)
	msg.Format = &invalid
	retry, done := s.HandleUplink(peer, msg)
	done()
	if retry.Code != codes.BadRequest {
		t.Fatalf("invalid format replayed: %+v", retry)
	}
}
