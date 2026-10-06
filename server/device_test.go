package server_test

import (
	"context"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: OBJ-03, SEC-20
// Device object duties the server relies on: it can set the client clock
// through /3/0/13; power-source resources 6-8 must share instance IDs; an
// Error Code of a single 0 means no error, and /3/0/11 can be observed.
func TestDeviceObjectDuties(t *testing.T) {
	h := newHarness(t)
	c := h.registered("dev")
	expect(t, "2.04")(h.srv.SyncClock(h.ctx, "dev"))
	if v, _ := c.Get(p("/3/0/13")); v.Type != lwm2m.TypeTime || v.Int != h.clock.Now().Unix() {
		t.Fatalf("clock %v", v)
	}
	c.Set(p("/3/0/6/0"), lwm2m.Integer(1))
	c.Set(p("/3/0/6/2"), lwm2m.Integer(5))
	c.Set(p("/3/0/7/0"), lwm2m.Integer(3800))
	c.Set(p("/3/0/7/2"), lwm2m.Integer(5000))
	r := expect(t, "2.05")(h.srv.Read(h.ctx, "dev", p("/3/0"), server.ReadOptions{}))
	if err := server.CheckPowerSources(r.Nodes); err != nil {
		t.Fatal(err)
	}
	if errs := server.DeviceErrors(r.Nodes); len(errs) != 0 {
		t.Fatalf("no-error code read as %v", errs)
	}
	bad := append(r.Nodes, lwm2m.ValueNode(p("/3/0/8/1"), lwm2m.Integer(10)))
	if server.CheckPowerSources(bad) == nil {
		t.Fatal("mismatched power source instances accepted")
	}
	ob, _, err := h.srv.Observe(h.ctx, "dev", p("/3/0/11"), server.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Set(p("/3/0/11/0"), lwm2m.Integer(2))
	n := notification(t, h, ob)
	if errs := server.DeviceErrors(n.Response.Nodes); len(errs) != 1 || errs[0] != 2 {
		t.Fatalf("errors %v", errs)
	}
}

// Proves: OBJ-03
// Factory Reset (/3/0/5) MAY De-register before answering the Execute:
// the server takes the 2.04 that arrives after the registration is gone
// and does not keep the registration.
func TestFactoryResetDeregistersFirst(t *testing.T) {
	h := newHarness(t)
	c := h.registered("reset")
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if r.Code != codes.POST || r.Path != "/3/0/5" {
			return 0, nil, nil, false
		}
		if dr, err := c.Deregister(context.Background()); err != nil || dr.Code != codes.Deleted {
			t.Errorf("de-register: %v %v", dr, err)
		}
		return codes.Changed, nil, nil, true
	})
	r, err := h.srv.Execute(h.ctx, "reset", p("/3/0/5"), "")
	if err != nil || r.Code != codes.Changed {
		t.Fatalf("execute: %v %v", r, err)
	}
	if _, ok := h.srv.Store().ByEndpoint("reset"); ok {
		t.Fatal("registration kept after De-register")
	}
}
