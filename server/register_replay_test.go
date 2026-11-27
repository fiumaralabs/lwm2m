package server

import (
	"encoding/binary"
	"testing"
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
	if again.Location[1] != first.Location[1] {
		t.Fatal("original exchange lost after later register")
	}
	if len(store.All()) != 1 || store.All()[0].ID != distinct.Location[1] {
		t.Fatal("replay mutated current registration")
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
