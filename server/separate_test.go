package server

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: QM-07
// Responses arrive piggybacked in the ACK (every other test), or as a
// separate CON or NON message after an empty ACK; the server takes both.
func TestSeparateResponses(t *testing.T) {
	h := newHarness(t)
	c := h.registered("sep")
	for _, con := range []bool{true, false} {
		confirmable := con
		c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
			go func() {
				time.Sleep(100 * time.Millisecond)
				cf := lwm2m.FormatText
				_ = c.RespondSeparately(context.Background(), r.Token, codes.Content, &cf, []byte("late"), confirmable)
			}()
			return codes.Empty, nil, nil, true
		})
		r := expect(t, "2.05")(h.srv.Read(h.ctx, "sep", p("/3/0/0"), ReadOptions{}))
		if len(r.Nodes) != 1 || r.Nodes[0].Value.Str != "late" {
			t.Fatalf("con=%v: %+v", con, r)
		}
	}
}

// Proves: BS-21
// A client busy bootstrapping may ignore requests and drop pending
// responses: the server's request fails cleanly when it times out, and
// the registration keeps working afterwards.
func TestInFlightDroppedDuringBootstrap(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RequestTimeout = 500 * time.Millisecond })
	c := h.registered("busy")
	c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.Empty, nil, nil, true // empty ACK, the response never comes
	})
	if _, err := h.srv.Read(h.ctx, "busy", p("/3/0/0"), ReadOptions{}); err == nil {
		t.Fatal("dropped exchange reported success")
	}
	c.SetOverride(nil)
	expect(t, "2.05")(h.srv.Read(h.ctx, "busy", p("/3/0/0"), ReadOptions{}))
}

// Proves: BS-23
// When bootstrap deletes a server account the client considers itself
// de-registered without telling the server. The server copes: the stale
// registration is replaced when the client registers again, or removed
// when its lifetime ends.
func TestImplicitDeregistration(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "implicit", Lifetime: 60})
	mustCode(mustRegister(h, c))
	old := c.Location()
	mustCode(mustRegister(h, c)) // registers again after bootstrap, no De-register sent
	if _, ok := h.srv.Store().ByID(old); ok {
		t.Fatal("stale registration kept")
	}
	gone := h.device(testclient.Config{Endpoint: "gone", Lifetime: 60})
	mustCode(mustRegister(h, gone))
	h.clock.Add(61 * time.Second)
	h.srv.expireNow()
	if _, ok := h.srv.Store().ByEndpoint("gone"); ok {
		t.Fatal("silently dropped registration never expired")
	}
}
