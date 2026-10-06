package server

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func notification(t *testing.T, h *harness, ob *Observation) Notification {
	t.Helper()
	return h.ev.wait(t, func(e Event) bool {
		n, ok := e.(Notification)
		return ok && n.Observation.ID == ob.ID
	}).(Notification)
}

// Proves: OBS-01, OBS-04, GEN-04
// Observe is GET with Observe=0; the first response carries the value and
// every later CON notification is ACKed and delivered as an event.
func TestObserveNotify(t *testing.T) {
	h := newHarness(t)
	c := h.registered("obs")
	ob, r, err := h.srv.Observe(h.ctx, "obs", p("/3/0/9"), ObserveOptions{})
	if err != nil || ob == nil || !r.Success() || !r.Nodes[0].Value.Equal(lwm2m.Integer(100)) {
		t.Fatalf("observe: %v %+v", err, r)
	}
	req, _ := c.LastRequest()
	if req.Code != codes.GET || req.Observe == nil || *req.Observe != 0 {
		t.Fatalf("request %+v", req)
	}
	c.Set(p("/3/0/9"), lwm2m.Integer(80))
	n := notification(t, h, ob)
	if len(n.Response.Nodes) != 1 || !n.Response.Nodes[0].Value.Equal(lwm2m.Integer(80)) {
		t.Fatalf("notification %+v", n.Response)
	}
	res, err := c.Notify(h.ctx, req.Token)
	if err != nil || res != testclient.NotifyAcked {
		t.Fatalf("notification not ACKed: %v %v", res, err)
	}
	// A client refusal creates no observation.
	_, r, err = h.srv.Observe(h.ctx, "obs", p("/3/0/99"), ObserveOptions{})
	if err != nil || r.Code != codes.NotFound || len(h.srv.Observations("obs")) != 1 {
		t.Fatalf("refused observe: %v %+v %d", err, r, len(h.srv.Observations("obs")))
	}
}

// Proves: OBS-02
// Passive cancel forgets the observation and the next notification gets a
// Reset; active cancel sends GET with Observe=1 on the same token.
func TestCancelObservation(t *testing.T) {
	h := newHarness(t)
	c := h.registered("cancel")
	ob, _, err := h.srv.Observe(h.ctx, "cancel", p("/3/0/9"), ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	obsReq, _ := c.LastRequest()
	if _, err := h.srv.CancelObservation(h.ctx, ob, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.Notify(h.ctx, obsReq.Token)
	if err != nil || res != testclient.NotifyReset {
		t.Fatalf("forgotten observation: %v %v, want Reset", res, err)
	}

	ob2, _, err := h.srv.Observe(h.ctx, "cancel", p("/3/0/9"), ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	obsReq2, _ := c.LastRequest()
	r, err := h.srv.CancelObservation(h.ctx, ob2, true)
	if err != nil || !r.Success() {
		t.Fatal(err, r)
	}
	req, _ := c.LastRequest()
	if req.Observe == nil || *req.Observe != 1 || string(req.Token) != string(obsReq2.Token) || req.Path != "/3/0/9" {
		t.Fatalf("cancel request %+v", req)
	}
	if c.Observers() != 0 || len(h.srv.Observations("cancel")) != 0 {
		t.Fatal("observation not removed")
	}
}

// Proves: OBS-05
// Observe-Composite is FETCH on / with Observe=0 and a path list; every
// notification carries the listed paths; Cancel-Composite resends exactly
// the same list with Observe=1.
func TestObserveComposite(t *testing.T) {
	h := newHarness(t)
	c := h.registered("oc")
	paths := []lwm2m.Path{p("/3/0/9"), p("/1/0/1")}
	ob, r, err := h.srv.ObserveComposite(h.ctx, "oc", paths, CompositeOptions{})
	if err != nil || !r.Success() || len(r.Nodes) != 2 {
		t.Fatal(err, r)
	}
	first, _ := c.LastRequest()
	if first.Code != codeFETCH || first.Path != "/" || *first.Observe != 0 {
		t.Fatalf("request %+v", first)
	}
	c.Set(p("/3/0/9"), lwm2m.Integer(7))
	n := notification(t, h, ob)
	if len(n.Response.Nodes) != 2 {
		t.Fatalf("composite notification %v", n.Response.Nodes)
	}
	if _, err := h.srv.CancelObservation(h.ctx, ob, true); err != nil {
		t.Fatal(err)
	}
	cancel, _ := c.LastRequest()
	if cancel.Code != codeFETCH || *cancel.Observe != 1 || string(cancel.Body) != string(first.Body) || string(cancel.Token) != string(first.Token) {
		t.Fatalf("cancel %+v vs %+v", cancel, first)
	}
}

// Proves: OBS-06
// 1.2 clients can take notification attributes in the Observe request; for
// older clients the server refuses to send them.
func TestObserveWithAttributes(t *testing.T) {
	h := newHarness(t)
	c12 := h.device(testclient.Config{Endpoint: "o12", Version: "1.2"})
	mustCode(mustRegister(h, c12))
	if _, _, err := h.srv.Observe(h.ctx, "o12", p("/3/0/9"), ObserveOptions{Query: []string{"pmin=5", "pmax=60"}}); err != nil {
		t.Fatal(err)
	}
	req, _ := c12.LastRequest()
	if !slices.Equal(req.Queries, []string{"pmin=5", "pmax=60"}) || *req.Observe != 0 {
		t.Fatalf("request %+v", req)
	}
	h.registered("o11")
	if _, _, err := h.srv.Observe(h.ctx, "o11", p("/3/0/9"), ObserveOptions{Query: []string{"pmin=5"}}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("attributes sent to a 1.1 client: %v", err)
	}
}

// Proves: GEN-04
// Notifications are reordered per RFC 7641 §3.4: an older sequence number
// arriving after a newer one is dropped.
func TestNotificationReordering(t *testing.T) {
	now := time.Unix(0, 0)
	for _, tc := range []struct {
		v1, v2 uint32
		dt     time.Duration
		newer  bool
	}{
		{1, 2, 0, true},
		{2, 1, 0, false},
		{1<<24 - 1, 3, 0, true}, // wrap-around
		{5, 4, 129 * time.Second, true},
	} {
		if got := newerNotification(tc.v1, tc.v2, now, now.Add(tc.dt)); got != tc.newer {
			t.Errorf("%d→%d after %v: newer=%v", tc.v1, tc.v2, tc.dt, got)
		}
	}
}

// Proves: SEND-01, SEND-02
// Send on /dp is accepted in SenML JSON, SenML CBOR and LwM2M CBOR (2.04)
// and delivered as an event; a missing or other Content-Format is 4.00;
// data for an unregistered object or instance is 4.04; an unregistered
// client is refused.
func TestSend(t *testing.T) {
	h := newHarness(t)
	c := h.registered("send")
	nodes := []lwm2m.Node{lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(42))}
	for _, cf := range []lwm2m.ContentFormat{lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR} {
		r, err := c.Send(h.ctx, nodes, cf)
		mustCode(t, r, err, "2.04")
		ev := h.ev.wait(t, func(e Event) bool { _, ok := e.(SendReceived); return ok }).(SendReceived)
		if ev.ContentFormat != cf || !lwm2m.NodesEqual(ev.Nodes, nodes) {
			t.Fatalf("%v: event %+v", cf, ev)
		}
	}
	tlv := lwm2m.FormatTLV
	r, err := c.Raw(h.ctx, codes.POST, "/dp", nil, &tlv, []byte{0xc1, 0x09, 0x2a})
	mustCode(t, r, err, "4.00")
	r, err = c.Raw(h.ctx, codes.POST, "/dp", nil, nil, []byte("x"))
	mustCode(t, r, err, "4.00")
	r, err = c.Send(h.ctx, []lwm2m.Node{lwm2m.ValueNode(p("/4/0/0"), lwm2m.Integer(1))}, lwm2m.FormatSenMLCBOR)
	mustCode(t, r, err, "4.04")
	r, err = c.Send(h.ctx, []lwm2m.Node{lwm2m.ValueNode(p("/3/1/0"), lwm2m.String("x"))}, lwm2m.FormatSenMLCBOR)
	mustCode(t, r, err, "4.04")
	stranger := h.device(testclient.Config{Endpoint: "stranger"})
	r, err = stranger.Send(h.ctx, nodes, lwm2m.FormatSenMLCBOR)
	mustCode(t, r, err, "4.00")
}

// Proves: QM-01, QM-02, QM-03, QM-04
// A queue-mode client (1.1 Q flag, or 1.0 b=UQ) is reachable for 93 s
// after its last message. Requests to a sleeping client wait until it
// sends an Update, then go out one at a time; requests that time out
// report failure; waiters fail when the registration ends.
func TestQueueMode(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "q", Queue: true})
	mustCode(mustRegister(h, c))
	reg, _ := h.srv.Store().ByEndpoint("q")
	if !reg.QueueMode || !h.srv.Awake("q") {
		t.Fatal("queue client not awake after Register")
	}
	h.clock.Add(94 * time.Second)
	if h.srv.Awake("q") {
		t.Fatal("still awake after 93 s")
	}
	done := make(chan *Response, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r, _ := h.srv.Read(h.ctx, "q", p("/3/0/9"), ReadOptions{})
			done <- r
		}()
	}
	time.Sleep(100 * time.Millisecond)
	if len(c.Requests()) != 0 {
		t.Fatal("request sent to a sleeping client")
	}
	ar, err := c.Update(h.ctx, nil, nil)
	mustCode(t, ar, err, "2.04")
	for i := 0; i < 2; i++ {
		select {
		case r := <-done:
			if r == nil || !r.Success() {
				t.Fatalf("queued request: %+v", r)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued request not delivered after wake-up")
		}
	}
	h.ev.wait(t, func(e Event) bool { _, ok := e.(Awake); return ok })

	h.clock.Add(94 * time.Second)
	ctx, cancel := context.WithTimeout(h.ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := h.srv.Read(ctx, "q", p("/3/0/9"), ReadOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out queued request: %v", err)
	}
	waiting := make(chan error, 1)
	go func() {
		_, err := h.srv.Read(h.ctx, "q", p("/3/0/9"), ReadOptions{})
		waiting <- err
	}()
	time.Sleep(50 * time.Millisecond)
	dr, err := c.Deregister(h.ctx)
	mustCode(t, dr, err, "2.02")
	if err := <-waiting; err == nil {
		t.Fatal("waiter not released on de-register")
	}

	q10 := h.device(testclient.Config{Endpoint: "q10", Version: "1.0", Binding: "UQ"})
	mustCode(mustRegister(h, q10))
	if reg, _ := h.srv.Store().ByEndpoint("q10"); !reg.QueueMode {
		t.Fatal("1.0 b=UQ not queue mode")
	}
}
