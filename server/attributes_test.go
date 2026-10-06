package server

import (
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// waitNotification waits up to d for a notification of ob and returns it,
// or ok=false.
func waitNotification(h *harness, ob *Observation, d time.Duration) (Notification, bool) {
	deadline := time.After(d)
	for {
		select {
		case e := <-h.ev.ch:
			if n, ok := e.(Notification); ok && n.Observation.ID == ob.ID {
				return n, true
			}
		case <-deadline:
			return Notification{}, false
		}
	}
}

func observeWith(t *testing.T, h *harness, ep, path string, attrs ...string) *Observation {
	t.Helper()
	if len(attrs) > 0 {
		expect(t, "2.04")(h.srv.WriteAttributes(h.ctx, ep, p(path), attrs))
	}
	ob, r, err := h.srv.Observe(h.ctx, ep, p(path), ObserveOptions{})
	if err != nil || !r.Success() {
		t.Fatal(err, r)
	}
	return ob
}

// Proves: ATT-03, ATT-09
// pmin: a change inside pmin is notified when pmin expires, not before.
func TestPmin(t *testing.T) {
	h := newHarness(t)
	c := h.registered("pmin")
	ob := observeWith(t, h, "pmin", "/3/0/9", "pmin=1")
	start := time.Now()
	c.Set(p("/3/0/9"), lwm2m.Integer(50))
	n, ok := waitNotification(h, ob, 3*time.Second)
	if !ok || !n.Response.Nodes[0].Value.Equal(lwm2m.Integer(50)) {
		t.Fatal("no notification after pmin")
	}
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("notified after %v, inside pmin", el)
	}
}

// Proves: ATT-05, OBS-07
// gt/lt notify on crossing, st on a large enough step; other changes are
// not notified. pmax sends without a change. Attributes attached below
// the observed level are ignored.
func TestThresholdAttributes(t *testing.T) {
	h := newHarness(t)
	c := h.registered("thr")
	ob := observeWith(t, h, "thr", "/3/0/9", "gt=50", "lt=20", "st=30")
	// From 100: 95 (no crossing, step 5) → silent.
	c.Set(p("/3/0/9"), lwm2m.Integer(95))
	if _, ok := waitNotification(h, ob, 400*time.Millisecond); ok {
		t.Fatal("notified without crossing or step")
	}
	// 45 crosses gt=50 → notify.
	c.Set(p("/3/0/9"), lwm2m.Integer(45))
	if n, ok := waitNotification(h, ob, 2*time.Second); !ok || n.Response.Nodes[0].Value.Int != 45 {
		t.Fatal("gt crossing not notified")
	}
	// 15 crosses lt=20 → notify.
	c.Set(p("/3/0/9"), lwm2m.Integer(15))
	if _, ok := waitNotification(h, ob, 2*time.Second); !ok {
		t.Fatal("lt crossing not notified")
	}
	// 14: no crossing, step 1 → silent.
	c.Set(p("/3/0/9"), lwm2m.Integer(14))
	if _, ok := waitNotification(h, ob, 400*time.Millisecond); ok {
		t.Fatal("small step notified")
	}
	// 45 from last notified 15: step 30 >= st, and crosses lt → notify.
	c.Set(p("/3/0/9"), lwm2m.Integer(45))
	if _, ok := waitNotification(h, ob, 2*time.Second); !ok {
		t.Fatal("st step not notified")
	}

	// Observation of /3/0 ignores attributes attached to /3/0/9 (below it),
	// and pmax on /3/0 sends a notification without any change.
	ob2 := observeWith(t, h, "thr", "/3/0", "pmax=1")
	if n, ok := waitNotification(h, ob2, 3*time.Second); !ok || len(n.Response.Nodes) < 2 {
		t.Fatal("pmax notification missing")
	}
	c.Set(p("/3/0/9"), lwm2m.Integer(44)) // small step, but gt/lt/st of /3/0/9 don't apply to /3/0
	if _, ok := waitNotification(h, ob2, 900*time.Millisecond); !ok {
		t.Fatal("instance observation used attributes from below its level")
	}
}

// Proves: ATT-08, OBS-09
// edge notifies only the chosen Boolean transition; con=0 makes the client
// send NON notifications, which the server accepts like CON ones; with no
// con, /1/x/26 sets the default mode. hqmax keeps the newest stored
// notifications while offline and they arrive as one timed batch.
func TestEdgeConHqmax(t *testing.T) {
	h := newHarness(t)
	c := h.registered("edge")
	c.Set(p("/1/0/6"), lwm2m.Boolean(false))
	ob := observeWith(t, h, "edge", "/1/0/6", "edge=1")
	c.Set(p("/1/0/6"), lwm2m.Boolean(true))
	if _, ok := waitNotification(h, ob, 2*time.Second); !ok {
		t.Fatal("rising edge not notified")
	}
	c.Set(p("/1/0/6"), lwm2m.Boolean(false))
	if _, ok := waitNotification(h, ob, 400*time.Millisecond); ok {
		t.Fatal("falling edge notified with edge=1")
	}

	ob2 := observeWith(t, h, "edge", "/3/0/9", "con=0")
	c.Set(p("/3/0/9"), lwm2m.Integer(1))
	if n, ok := waitNotification(h, ob2, 2*time.Second); !ok || n.Response.Nodes[0].Value.Int != 1 {
		t.Fatal("NON notification not delivered")
	}

	c2 := h.registered("mode")
	c2.Set(p("/1/0/26"), lwm2m.Integer(0))
	ob3 := observeWith(t, h, "mode", "/3/0/9")
	c2.Set(p("/3/0/9"), lwm2m.Integer(2))
	if _, ok := waitNotification(h, ob3, 2*time.Second); !ok {
		t.Fatal("default NON mode notification not delivered")
	}

	c3 := h.registered("hq")
	ob4 := observeWith(t, h, "hq", "/3/0/9", "hqmax=2")
	c3.SetOffline(true)
	for v := int64(10); v <= 13; v++ {
		c3.Set(p("/3/0/9"), lwm2m.Integer(v))
		time.Sleep(20 * time.Millisecond)
	}
	c3.SetOffline(false)
	n, ok := waitNotification(h, ob4, 2*time.Second)
	if !ok || len(n.Response.Nodes) != 2 || n.Response.Nodes[0].Value.Int != 12 || n.Response.Nodes[1].Value.Int != 13 || !n.Response.Nodes[0].HasTime {
		t.Fatalf("hqmax batch %s", lwm2m.FormatNodes(n.Response.Nodes))
	}
}

// Proves: ATT-12
// Changing attributes of a live observation cancels it, writes the
// attributes and observes again.
func TestChangeObservationAttributes(t *testing.T) {
	h := newHarness(t)
	c := h.registered("chg")
	ob := observeWith(t, h, "chg", "/3/0/9")
	ob2, r, err := h.srv.ChangeObservationAttributes(h.ctx, ob, []string{"pmin=5"})
	if err != nil || ob2 == nil || !r.Success() || ob2.ID == ob.ID {
		t.Fatal(err, r)
	}
	reqs := c.Requests()
	n := len(reqs)
	if n < 3 || *reqs[n-3].Observe != 1 || reqs[n-2].Queries[0] != "pmin=5" || *reqs[n-1].Observe != 0 {
		t.Fatalf("sequence %+v", reqs[n-3:])
	}
	if len(h.srv.Observations("chg")) != 1 {
		t.Fatal("old observation kept")
	}
}

// Proves: REG-19, GEN-15, REG-24, GEN-14, REG-25, REG-26, PROF-10, SEC-20
// Server object triggers: Execute /1/x/8 makes the client Update (with a
// binding override only if /1/x/7 lists it); Disable /1/x/4 makes it
// de-register and come back after /1/x/5 seconds; /1/x/22, /1/x/7,
// /1/x/25 and /1/x/27 are written with the right types.
func TestServerObjectTriggers(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "trig", Version: "1.2"})
	c.Set(p("/1/0/7"), lwm2m.String("UT"))
	mustCode(mustRegister(h, c))

	expect(t, "2.04")(h.srv.TriggerUpdate(h.ctx, "trig", ""))
	h.ev.wait(t, func(e Event) bool { u, ok := e.(Updated); return ok && u.Registration.Endpoint == "trig" })
	expect(t, "2.04")(h.srv.TriggerUpdate(h.ctx, "trig", "T"))
	h.ev.wait(t, func(e Event) bool { u, ok := e.(Updated); return ok && u.Registration.Endpoint == "trig" })
	if c.BindingOverride() != "T" {
		t.Fatalf("override %q", c.BindingOverride())
	}
	if _, err := h.srv.TriggerUpdate(h.ctx, "trig", "S"); err == nil {
		t.Fatal("offered a binding not in /1/x/7")
	}

	expect(t, "2.04")(h.srv.SetPreferredTransport(h.ctx, "trig", "U"))
	expect(t, "2.04")(h.srv.SetBinding(h.ctx, "trig", "UQ"))
	expect(t, "2.04")(h.srv.AdvertiseVersions(h.ctx, "trig"))
	expect(t, "2.04")(h.srv.SetProfileHashAlgorithm(h.ctx, "trig", 6))
	if v, _ := c.Get(p("/1/0/22")); v.Str != "U" {
		t.Fatal("/1/0/22")
	}
	if v, _ := c.Get(p("/1/0/25/2")); v.Str != "1.2" {
		t.Fatal("/1/0/25")
	}
	if v, _ := c.Get(p("/1/0/27")); v.Uint != 6 {
		t.Fatal("/1/0/27")
	}

	bs := make(chan struct{}, 1)
	c.SetBootstrapTrigger(func(testclient.Request) { bs <- struct{}{} })
	expect(t, "2.04")(h.srv.TriggerBootstrap(h.ctx, "trig"))
	select {
	case <-bs:
	case <-time.After(2 * time.Second):
		t.Fatal("Bootstrap-Request Trigger not executed")
	}

	c.Set(p("/1/0/5"), lwm2m.Integer(1))
	expect(t, "2.04")(h.srv.Disable(h.ctx, "trig"))
	h.ev.wait(t, func(e Event) bool { d, ok := e.(Deregistered); return ok && d.Registration.Endpoint == "trig" })
	h.ev.wait(t, func(e Event) bool { r, ok := e.(Registered); return ok && r.Registration.Endpoint == "trig" })
}
