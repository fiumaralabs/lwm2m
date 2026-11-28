package server

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/model"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestModelsRegistrationLifecycle(t *testing.T) {
	now := time.Unix(1700000000, 0)
	models := NewModels(model.Default())
	s := New(Config{Schema: models.Schema, Now: func() time.Time { return now }, ExpiryCheck: time.Hour})
	defer s.Close()
	peer := exchangePeer{addr: "192.0.2.1"}
	send := func(m *Message) *Message {
		t.Helper()
		result, after := s.HandleUplink(peer, m)
		if after != nil {
			after()
		}
		return result
	}
	register := func(ep string, lifetime int) *Registration {
		t.Helper()
		r := send(&Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=" + ep, fmt.Sprintf("lt=%d", lifetime), "lwm2m=1.1"}, Payload: []byte("</3/0>")})
		if r.Code != codes.Created {
			t.Fatalf("register %s: %v", ep, r.Code)
		}
		reg, ok := s.store.ByID(r.Location[1])
		if !ok {
			t.Fatal("missing registration")
		}
		models.schemaOf(reg)
		return reg
	}
	count := func() int { n := 0; models.cache.Range(func(_, _ any) bool { n++; return true }); return n }
	live := register("live", 600)
	oldSchema := models.schemaOf(live)
	result := send(&Message{Code: codes.POST, Path: "/rd/" + live.ID, Payload: []byte("</1/0>")})
	if result.Code != codes.Changed {
		t.Fatalf("update: %v", result.Code)
	}
	updated, _ := s.store.ByID(live.ID)
	newSchema := models.schemaOf(updated)
	if newSchema == oldSchema {
		t.Fatal("update reused stale schema")
	}
	if _, ok := newSchema.Object(1); !ok {
		t.Fatal("updated schema lacks object 1")
	}
	if _, ok := newSchema.Object(3); ok {
		t.Fatal("updated schema retains object 3")
	}
	for i := 0; i < 10; i++ {
		r := register(fmt.Sprintf("gone-%d", i), 600)
		if got := send(&Message{Code: codes.DELETE, Path: "/rd/" + r.ID}); got.Code != codes.Deleted {
			t.Fatalf("deregister: %v", got.Code)
		}
	}
	for i := 0; i < 5; i++ {
		register(fmt.Sprintf("expired-%d", i), 1)
	}
	replaced := register("replace", 600)
	register("replace", 600)
	models.schemaOf(replaced) // an in-flight operation must not resurrect an ended entry
	now = now.Add(2 * time.Second)
	s.expireNow()
	if got, want := count(), len(s.store.All()); got != want {
		t.Fatalf("cache entries = %d, live registrations = %d", got, want)
	}
}

func TestModelsConcurrentEndCannotResurrectCache(t *testing.T) {
	reg := &Registration{ID: "race", Version: "1.1", models: &registrationModels{}}
	first, second := NewModels(model.Default()), NewModels(model.Default())
	first.schemaOf(reg)
	second.schemaOf(reg)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				first.schemaOf(reg)
				second.schemaOf(reg)
			}
		}()
	}
	reg.models.end(reg.ID)
	wg.Wait()
	for _, m := range []*Models{first, second} {
		if _, ok := m.cache.Load(reg.ID); ok {
			t.Fatal("ended registration was retained or resurrected")
		}
	}
}
