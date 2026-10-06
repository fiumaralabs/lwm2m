package server

import (
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// TestOperatorAPI covers the operator and extension API: raw queued
// requests, Wake, the OnQueued hook, removing a registration (closing its
// session), revoking credentials, listing credentials, and typed
// validation errors.
func TestOperatorAPI(t *testing.T) {
	queued := make(chan string, 4)
	h := newHarness(t, func(c *Config) { c.OnQueued = func(r *Registration) { queued <- r.Endpoint } })

	c := h.registered("op")
	r, err := h.srv.Do(h.ctx, "op", &Message{Code: codes.GET, Path: "/3/0/0"})
	if err != nil || r.Code != codes.Content || string(r.Payload) != "Open Mobile Alliance" {
		t.Fatalf("Do: %v %+v", err, r)
	}
	reg, _ := h.srv.Store().ByEndpoint("op")
	if reg.RawLinks != c.ObjectLinks() {
		t.Fatalf("raw links %q", reg.RawLinks)
	}

	q := h.device(testclient.Config{Endpoint: "q", Queue: true})
	mustCode(mustRegister(h, q))
	h.clock.Add(94 * time.Second)
	done := make(chan error, 1)
	go func() { _, err := h.srv.Read(h.ctx, "q", p("/3/0/0"), ReadOptions{}); done <- err }()
	select {
	case ep := <-queued:
		if ep != "q" {
			t.Fatal(ep)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnQueued not called")
	}
	h.srv.Wake("q")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatal("OnQueued called more than once")
	}

	_, err = h.srv.Write(h.ctx, "op", p("/3/0/9"), []lwm2m.Node{lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(1))}, WriteOptions{})
	if !errors.Is(err, ErrBadRequest) || !errors.Is(err, model.ErrNotWritable) {
		t.Fatalf("validation error lost its type: %v", err)
	}

	if !h.srv.RemoveRegistration("op") {
		t.Fatal("remove failed")
	}
	if _, err := h.srv.Read(h.ctx, "op", p("/3/0/0"), ReadOptions{}); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("read after removal: %v", err)
	}
	h.ev.wait(t, func(e Event) bool { d, ok := e.(Deregistered); return ok && d.Reason == ReasonRemoved })

	key := []byte("0123456789abcdef")
	for _, ep := range []string{"b-dev", "a-dev"} {
		if err := h.srv.Security().Put(SecurityInfo{Endpoint: ep, PSKIdentity: ep, PSKKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	if all := h.srv.Security().All(); len(all) != 2 || all[0].Endpoint != "a-dev" {
		t.Fatalf("All %+v", all)
	}
	s := h.device(testclient.Config{Endpoint: "a-dev", PSKIdentity: "a-dev", PSKKey: key})
	mustCode(mustRegister(h, s))
	if _, ok := h.srv.RevokeSecurity("a-dev"); !ok {
		t.Fatal("revoke")
	}
	if _, ok := h.srv.Store().ByEndpoint("a-dev"); ok {
		t.Fatal("registration survived credential revocation")
	}
	if r, err := s.Update(h.ctx, nil, nil); err == nil && r.Code == codes.Changed {
		t.Fatal("revoked session still usable")
	}
}
