package server

import (
	"errors"
	"slices"
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

// Proves: ATT-05, ATT-04, OBS-07, OBS-04, ATT-01, ATT-06, DM-07
// gt, lt and st each notify on their own condition (crossing either way,
// step since the last notification) and other changes stay silent. pmax
// sends a full instance notification without a change. Attributes attached
// below the observed level are ignored. The server writes every
// <NOTIFICATION> attribute as given and refuses an inconsistent set
// before sending it.
func TestThresholdAttributes(t *testing.T) {
	h := newHarness(t)
	c := h.registered("thr")
	silent := func(ob *Observation, v int64) {
		t.Helper()
		c.Set(p("/3/0/9"), lwm2m.Integer(v))
		if _, ok := waitNotification(h, ob, 400*time.Millisecond); ok {
			t.Fatalf("%d notified", v)
		}
	}
	notified := func(ob *Observation, v int64) {
		t.Helper()
		c.Set(p("/3/0/9"), lwm2m.Integer(v))
		n, ok := waitNotification(h, ob, 2*time.Second)
		if !ok || !slices.ContainsFunc(n.Response.Nodes, func(x lwm2m.Node) bool { return x.Path == p("/3/0/9") && x.Value.Int == v }) {
			t.Fatalf("%d not notified", v)
		}
	}
	cancel := func(ob *Observation) {
		t.Helper()
		if _, err := h.srv.CancelObservation(h.ctx, ob, true); err != nil {
			t.Fatal(err)
		}
	}

	ob := observeWith(t, h, "thr", "/3/0/9", "gt=50") // from 100
	silent(ob, 95)
	notified(ob, 45) // crosses gt downwards
	silent(ob, 40)
	notified(ob, 60) // and upwards
	cancel(ob)

	ob = observeWith(t, h, "thr", "/3/0/9", "gt", "lt=20") // from 60
	silent(ob, 30)
	notified(ob, 15)
	silent(ob, 10)
	notified(ob, 25)
	cancel(ob)

	ob = observeWith(t, h, "thr", "/3/0/9", "lt", "st=10") // from 25
	silent(ob, 30)
	notified(ob, 36) // 11 since the last notification
	silent(ob, 40)   // 4 since 36
	cancel(ob)

	// pmax on /3/0 sends every readable resource of the instance unchanged.
	ob2 := observeWith(t, h, "thr", "/3/0", "pmax=1")
	if n, ok := waitNotification(h, ob2, 3*time.Second); !ok || len(n.Response.Nodes) != len(c.Nodes(p("/3/0"))) {
		t.Fatal("pmax notification missing or incomplete")
	}
	cancel(ob2)
	// st=10 stays attached to /3/0/9; an observation of /3/0 ignores it.
	ob3 := observeWith(t, h, "thr", "/3/0", "pmax")
	notified(ob3, 41)

	// Every <NOTIFICATION> attribute reaches the client exactly as written.
	all := []string{"pmin=1", "pmax=60", "gt=50", "lt=10", "st=5", "epmin=1", "epmax=30", "con=1", "hqmax=3"}
	expect(t, "2.04")(h.srv.WriteAttributes(h.ctx, "thr", p("/3/0/9"), all))
	if req, _ := c.LastRequest(); !slices.Equal(req.Queries, all) {
		t.Fatalf("queries %v", req.Queries)
	}
	expect(t, "2.04")(h.srv.WriteAttributes(h.ctx, "thr", p("/1/0/6"), []string{"edge=1"}))
	// An inconsistent or misplaced set is refused before it is sent.
	n := len(c.Requests())
	for _, q := range [][]string{{"lt=10", "gt=30", "st=10"}, {"lt=30", "gt=30"}, {"pmin=10", "pmax=5"}, {"epmin=5", "epmax=5"}, {"ver=1.1"}, {"foo=1"}} {
		if _, err := h.srv.WriteAttributes(h.ctx, "thr", p("/3/0/9"), q); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%v: %v, want ErrBadRequest", q, err)
		}
	}
	if _, err := h.srv.WriteAttributes(h.ctx, "thr", p("/3/0"), []string{"gt=1"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("gt on an instance: %v", err)
	}
	if len(c.Requests()) != n {
		t.Fatal("refused attributes were sent")
	}
}

// Proves: ATT-08, OBS-09
// edge notifies only the chosen Boolean transition; con=0 makes the client
// send NON notifications, which the server accepts like CON ones; with no
// con, /1/x/26 sets the default mode and an inherited con=1 overrides it.
// hqmax keeps the newest stored notifications while offline (only with
// /1/x/6 = true) and they arrive as one timed batch.
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
	non := c.NONNotifications()
	c.Set(p("/3/0/9"), lwm2m.Integer(1))
	if n, ok := waitNotification(h, ob2, 2*time.Second); !ok || n.Response.Nodes[0].Value.Int != 1 {
		t.Fatal("NON notification not delivered")
	}
	if c.NONNotifications() != non+1 {
		t.Fatal("con=0 notification was not NON")
	}

	c2 := h.registered("mode")
	c2.Set(p("/1/0/26"), lwm2m.Integer(0))
	ob3 := observeWith(t, h, "mode", "/3/0/9")
	c2.Set(p("/3/0/9"), lwm2m.Integer(2))
	if _, ok := waitNotification(h, ob3, 2*time.Second); !ok {
		t.Fatal("default NON mode notification not delivered")
	}
	if c2.NONNotifications() != 1 {
		t.Fatal("/1/x/26 = 0 notification was not NON")
	}
	expect(t, "2.04")(h.srv.WriteAttributes(h.ctx, "mode", p("/3/0"), []string{"con=1"}))
	c2.Set(p("/3/0/9"), lwm2m.Integer(3))
	if _, ok := waitNotification(h, ob3, 2*time.Second); !ok || c2.NONNotifications() != 1 {
		t.Fatal("inherited con=1 did not make the notification CON")
	}

	c3 := h.registered("hq")
	c3.Set(p("/1/0/6"), lwm2m.Boolean(true))
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

	c4 := h.registered("nostore") // /1/0/6 absent: nothing is stored
	ob5 := observeWith(t, h, "nostore", "/3/0/9", "hqmax=2")
	c4.SetOffline(true)
	c4.Set(p("/3/0/9"), lwm2m.Integer(10))
	c4.SetOffline(false)
	if _, ok := waitNotification(h, ob5, 500*time.Millisecond); ok {
		t.Fatal("stored notifications without /1/x/6")
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

// Proves: REG-19, GEN-15, REG-24, GEN-14, REG-25, REG-26, PROF-10
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

	if _, err := h.srv.SetPreferredTransport(h.ctx, "trig", "UQ"); !errors.Is(err, ErrBadRequest) {
		t.Fatal("/1/x/22 holds a single binding")
	}
	expect(t, "2.04")(h.srv.SetPreferredTransport(h.ctx, "trig", "U"))
	expect(t, "2.04")(h.srv.SetBinding(h.ctx, "trig", "UQ"))
	expect(t, "2.04")(h.srv.AdvertiseVersions(h.ctx, "trig"))
	expect(t, "2.04")(h.srv.SetProfileHashAlgorithm(h.ctx, "trig", 6))
	if v, _ := c.Get(p("/1/0/22")); v.Str != "U" {
		t.Fatal("/1/0/22")
	}
	if v, _ := c.Get(p("/1/0/7")); v.Str != "UQ" {
		t.Fatal("/1/0/7")
	}
	for i, want := range []string{"1.0", "1.1", "1.2"} {
		if v, _ := c.Get(p("/1/0/25").Append(uint16(i))); v.Type != lwm2m.TypeString || v.Str != want {
			t.Fatalf("/1/0/25/%d = %+v", i, v)
		}
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
