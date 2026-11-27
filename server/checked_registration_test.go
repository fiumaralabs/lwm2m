package server_test

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
)

type checkedRegistrationStore struct {
	*server.MemoryStore
	err    error
	reject atomic.Bool
	calls  atomic.Int32
}

func (s *checkedRegistrationStore) AddChecked(r *server.Registration) (*server.Registration, error) {
	s.calls.Add(1)
	if s.reject.Load() {
		return nil, errors.New("rejected")
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.MemoryStore.Add(r), nil
}

func TestCheckedRegistrationRejectsBeforeProjection(t *testing.T) {
	store := &checkedRegistrationStore{MemoryStore: server.NewMemoryStore(), err: errors.New("rejected")}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	client := h.device(testclient.Config{Endpoint: "checked"})
	r, err := client.Register(h.ctx)
	mustCode(t, r, err, "4.03")
	if _, ok := store.ByEndpoint("checked"); ok || len(store.All()) != 0 {
		t.Fatal("rejected registration was projected")
	}
	select {
	case event := <-h.ev.ch:
		t.Fatalf("unexpected event: %T", event)
	default:
	}
}

func TestCheckedRegistrationAccepts(t *testing.T) {
	store := &checkedRegistrationStore{MemoryStore: server.NewMemoryStore()}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	client := h.device(testclient.Config{Endpoint: "accepted"})
	r, err := client.Register(h.ctx)
	mustCode(t, r, err, "2.01")
	reg, ok := store.ByEndpoint("accepted")
	if !ok || reg.ID != client.Location() || len(store.All()) != 1 || store.calls.Load() != 1 {
		t.Fatal("accepted registration not stored")
	}
	h.ev.wait(t, func(e server.Event) bool { _, ok := e.(server.Registered); return ok })
}

func TestCheckedRegistrationUnavailable(t *testing.T) {
	store := &checkedRegistrationStore{MemoryStore: server.NewMemoryStore(), err: fmt.Errorf("backend: %w", server.ErrRegistrationUnavailable)}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	client := h.device(testclient.Config{Endpoint: "unavailable"})
	r, err := client.Register(h.ctx)
	mustCode(t, r, err, "5.03")
	if _, ok := store.ByEndpoint("unavailable"); ok || len(store.All()) != 0 {
		t.Fatal("unavailable registration was projected")
	}
	select {
	case event := <-h.ev.ch:
		t.Fatalf("unexpected event: %T", event)
	default:
	}
}

func TestCheckedRegistrationRejectedReplacementKeepsOld(t *testing.T) {
	store := &checkedRegistrationStore{MemoryStore: server.NewMemoryStore()}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	client := h.registered("existing")
	h.ev.wait(t, func(e server.Event) bool { _, ok := e.(server.Registered); return ok })
	old, ok := store.ByEndpoint("existing")
	if !ok {
		t.Fatal("initial registration missing")
	}
	ob, _, err := h.srv.Observe(h.ctx, "existing", p("/3/0/9"), server.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store.reject.Store(true)
	r, err := client.Register(h.ctx)
	mustCode(t, r, err, "4.03")
	got, ok := store.ByEndpoint("existing")
	if !ok || got != old || len(store.All()) != 1 {
		t.Fatal("old registration changed")
	}
	if got, ok := store.ByID(old.ID); !ok || got != old {
		t.Fatal("old location removed")
	}
	if !h.srv.KnownObservation(server.ObservationToken(ob)) {
		t.Fatal("old observation removed")
	}
	select {
	case event := <-h.ev.ch:
		t.Fatalf("unexpected event: %T", event)
	default:
	}
}
