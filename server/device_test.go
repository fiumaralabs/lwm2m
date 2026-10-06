package server

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

// Proves: OBJ-03
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
	r := expect(t, "2.05")(h.srv.Read(h.ctx, "dev", p("/3/0"), ReadOptions{}))
	if err := CheckPowerSources(r.Nodes); err != nil {
		t.Fatal(err)
	}
	if errs := DeviceErrors(r.Nodes); len(errs) != 0 {
		t.Fatalf("no-error code read as %v", errs)
	}
	bad := append(r.Nodes, lwm2m.ValueNode(p("/3/0/8/1"), lwm2m.Integer(10)))
	if CheckPowerSources(bad) == nil {
		t.Fatal("mismatched power source instances accepted")
	}
	ob, _, err := h.srv.Observe(h.ctx, "dev", p("/3/0/11"), ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Set(p("/3/0/11/0"), lwm2m.Integer(2))
	n := notification(t, h, ob)
	if errs := DeviceErrors(n.Response.Nodes); len(errs) != 1 || errs[0] != 2 {
		t.Fatalf("errors %v", errs)
	}
}
