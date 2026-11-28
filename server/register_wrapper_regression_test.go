package server

import (
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"testing"
)

type customAddStore struct {
	*MemoryStore
	called bool
}

func (s *customAddStore) Add(r *Registration) *Registration {
	s.called = true
	return s.MemoryStore.Add(r)
}

func TestRegisterNoMIDUsesCustomAdd(t *testing.T) {
	store := &customAddStore{MemoryStore: NewMemoryStore()}
	s := New(Config{Store: store})
	defer s.Close()
	m := &Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>")}
	response, after := s.HandleUplink(exchangePeer{addr: "192.0.2.1"}, m)
	after()
	if response.Code != codes.Created || !store.called {
		t.Fatalf("custom Add not called: response=%v called=%v", response.Code, store.called)
	}
}
