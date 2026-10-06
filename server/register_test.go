package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: REG-01, REG-05, REG-11, REG-22, REG-27
// Register with every parameter of C Tbl 6.2.1-1 is accepted, answered
// 2.01 with a Location under /rd, and every parameter is recorded. The
// recorded connection is used for later requests.
func TestRegisterAllParameters(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "urn:dev:all"})
	r, err := c.RegisterRaw(h.ctx, []string{"ep=urn:dev:all", "lt=600", "lwm2m=1.1", "b=U", "Q", "sms=4412345678"}, []byte(c.ObjectLinks()), true)
	mustCode(t, r, err, "2.01")
	if len(r.Location) != 2 || r.Location[0] != "rd" || r.Location[1] == "" || len(r.Location[1]) > 32 {
		t.Fatalf("location %v", r.Location)
	}
	reg, ok := h.srv.Store().ByEndpoint("urn:dev:all")
	if !ok {
		t.Fatal("not stored")
	}
	if reg.ID != r.Location[1] || reg.Lifetime != 600*time.Second || reg.Version != "1.1" || !reg.QueueMode || reg.SMS != "4412345678" || reg.Binding != "U" {
		t.Fatalf("registration %+v", reg)
	}
	if reg.Addr.String() != c.LocalAddr().String() {
		t.Fatalf("addr %v, want %v", reg.Addr, c.LocalAddr())
	}
	resp, err := h.srv.Read(h.ctx, "urn:dev:all", p("/3/0/0"), ReadOptions{})
	mustResp(t, resp, err, "2.05")
}

// Proves: REG-03, REG-06
// lt and lwm2m are mandatory; unknown, duplicated or malformed parameters
// are 4.00 (T Tbl 6.7-2).
func TestRegisterRejectsBadParameters(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "bad"})
	links := []byte(c.ObjectLinks())
	for name, q := range map[string][]string{
		"missing lt":     {"ep=bad", "lwm2m=1.1"},
		"missing lwm2m":  {"ep=bad", "lt=60"},
		"unknown param":  {"ep=bad", "lt=60", "lwm2m=1.1", "foo=1"},
		"duplicate":      {"ep=bad", "lt=60", "lt=70", "lwm2m=1.1"},
		"bad lifetime":   {"ep=bad", "lt=-1", "lwm2m=1.1"},
		"bad binding":    {"ep=bad", "lt=60", "lwm2m=1.1", "b=X"},
		"Q with value":   {"ep=bad", "lt=60", "lwm2m=1.1", "Q=1"},
		"bad profile id": {"ep=bad", "lt=60", "lwm2m=1.2", "pid=6:ABCD"},
		"1.0 without ep": {"lt=60", "lwm2m=1.0"},
		"empty ep":       {"ep=", "lt=60", "lwm2m=1.1"},
	} {
		r, err := c.RegisterRaw(h.ctx, q, links, true)
		if err != nil {
			t.Fatal(name, err)
		}
		if CodeString(r.Code) != "4.00" {
			t.Errorf("%s: code %s, want 4.00", name, CodeString(r.Code))
		}
	}
	// A payload that is not link-format (REG-07).
	cf := lwm2m.FormatText
	r, err := c.Raw(h.ctx, 2, "/rd", []string{"ep=bad", "lt=60", "lwm2m=1.1"}, &cf, links)
	mustCode(t, r, err, "4.00")
	if len(h.srv.Store().All()) != 0 {
		t.Fatal("a rejected Register was stored")
	}
}

// Proves: REG-04, REG-06, GEN-01
// Versions 1.0, 1.1 and 1.2 (and their patch releases) are served; any
// other declared version is refused with 4.12, even if other parameters
// are malformed, so clients can fall back.
func TestRegisterVersions(t *testing.T) {
	h := newHarness(t)
	for _, v := range []string{"1.0", "1.1", "1.2", "1.2.1", "1.2.2"} {
		c := h.device(testclient.Config{Endpoint: "v" + v, Version: v})
		r, err := c.Register(h.ctx)
		mustCode(t, r, err, "2.01")
		reg, _ := h.srv.Store().ByEndpoint("v" + v)
		if reg.Version != v[:3] {
			t.Fatalf("version %q stored as %q", v, reg.Version)
		}
	}
	c := h.device(testclient.Config{Endpoint: "vx"})
	for _, v := range []string{"1.3", "2.0", "0.9", "x"} {
		r, err := c.RegisterRaw(h.ctx, []string{"ep=vx", "lt=60", "lwm2m=" + v}, []byte(c.ObjectLinks()), true)
		mustCode(t, r, err, "4.12")
	}
	r, err := c.RegisterRaw(h.ctx, []string{"ep=vx", "lwm2m=1.3"}, nil, false)
	mustCode(t, r, err, "4.12")
}

// Proves: REG-12, OBS-03, OBS-10
// A Register from an endpoint that is already registered replaces the old
// registration (new location, Replaced event) and voids its observations.
func TestReRegisterReplaces(t *testing.T) {
	h := newHarness(t)
	c := h.registered("rereg")
	first := c.Location()
	ob, _, err := h.srv.Observe(h.ctx, "rereg", p("/3/0/9"), ObserveOptions{})
	if err != nil || ob == nil {
		t.Fatal(err)
	}
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, "2.01")
	if c.Location() == first {
		t.Fatal("location reused")
	}
	if _, ok := h.srv.Store().ByID(first); ok {
		t.Fatal("old registration kept")
	}
	if n := len(h.srv.Observations("rereg")); n != 0 || h.srv.KnownObservation(ob.token) {
		t.Fatalf("%d observations survived re-register", n)
	}
	ev := h.ev.wait(t, func(e Event) bool { x, ok := e.(Registered); return ok && x.Replaced != nil })
	if ev.(Registered).Replaced.ID != first {
		t.Fatal("wrong replaced registration")
	}
	// The old location is gone (REG-13 wording: later Update gets 4.04).
	old, err := c.Raw(h.ctx, 2, "/rd/"+first, nil, nil, nil)
	mustCode(t, old, err, "4.04")
	// OBS-03: the server re-initiates the observation on the new
	// registration; the client sees a fresh Observe request.
	ob2, resp, err := h.srv.Observe(h.ctx, "rereg", p("/3/0/9"), ObserveOptions{})
	if err != nil || ob2 == nil || !resp.Success() {
		t.Fatalf("re-observe: %v %+v", err, resp)
	}
	if ob2.RegistrationID != c.Location() || string(ob2.token) == string(ob.token) || c.Observers() != 1 {
		t.Fatalf("re-initiated observation %+v, client observers %d", ob2, c.Observers())
	}
}

// Proves: REG-13, REG-23
// A registration whose lifetime elapses without Update is removed with its
// observations, and its location then answers 4.04. lt=0 never expires.
func TestLifetimeExpiry(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "exp", Lifetime: 60})
	mustCode(mustRegister(h, c))
	if _, _, err := h.srv.Observe(h.ctx, "exp", p("/3/0/9"), ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	inf := h.device(testclient.Config{Endpoint: "inf"})
	r, err := inf.RegisterRaw(h.ctx, []string{"ep=inf", "lt=0", "lwm2m=1.1"}, []byte(inf.ObjectLinks()), true)
	mustCode(t, r, err, "2.01")

	h.clock.Add(59 * time.Second)
	h.srv.expireNow()
	if _, ok := h.srv.Store().ByEndpoint("exp"); !ok {
		t.Fatal("expired early")
	}
	h.clock.Add(2 * time.Second)
	h.srv.expireNow()
	if _, ok := h.srv.Store().ByEndpoint("exp"); ok {
		t.Fatal("not expired")
	}
	if len(h.srv.Observations("exp")) != 0 || len(h.srv.obs.forRegistration(c.Location())) != 0 {
		t.Fatal("observations survived expiry")
	}
	h.ev.wait(t, func(e Event) bool { x, ok := e.(Deregistered); return ok && x.Reason == ReasonExpired })
	u, err := c.Update(h.ctx, nil, nil)
	mustCode(t, u, err, "4.04")

	h.clock.Add(1000 * time.Hour)
	h.srv.expireNow()
	if _, ok := h.srv.Store().ByEndpoint("inf"); !ok {
		t.Fatal("lt=0 registration expired")
	}
}

func mustRegister(h *harness, c *testclient.Client) (*testing.T, *testclient.Response, error, string) {
	r, err := c.Register(h.ctx)
	return h.t, r, err, "2.01"
}

// Proves: REG-14, REG-15, REG-08
// Update carries only changed parameters, refreshes the lifetime, and an
// object list in an Update replaces the stored one. Register-only
// parameters and unknown ones are 4.00; unknown locations 4.04.
func TestUpdate(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "upd", Lifetime: 100})
	mustCode(mustRegister(h, c))
	reg0, _ := h.srv.Store().ByEndpoint("upd")

	h.clock.Add(90 * time.Second)
	r, err := c.Update(h.ctx, nil, nil) // empty Update: lifetime refresh
	mustCode(t, r, err, "2.04")
	h.clock.Add(90 * time.Second)
	h.srv.expireNow()
	reg, ok := h.srv.Store().ByEndpoint("upd")
	if !ok {
		t.Fatal("empty Update did not refresh the lifetime")
	}
	if reg.Lifetime != 100*time.Second || len(reg.Objects) != len(reg0.Objects) {
		t.Fatal("empty Update changed parameters")
	}

	r, err = c.Update(h.ctx, []string{"lt=300", "b=U", "sms=123"}, []byte("</1/0>,</3/0>,</4/0>"))
	mustCode(t, r, err, "2.04")
	reg, _ = h.srv.Store().ByEndpoint("upd")
	if reg.Lifetime != 300*time.Second || reg.SMS != "123" || len(reg.Objects) != 3 || !reg.HasObject(4) {
		t.Fatalf("update not applied: %+v", reg)
	}
	r, err = c.Update(h.ctx, nil, []byte("</3/0>"))
	mustCode(t, r, err, "2.04")
	reg, _ = h.srv.Store().ByEndpoint("upd")
	if len(reg.Objects) != 1 || reg.HasObject(1) {
		t.Fatal("object list was merged, not replaced")
	}
	ev := h.ev.wait(t, func(e Event) bool { _, ok := e.(Updated); return ok })
	if ev.(Updated).Registration.Endpoint != "upd" {
		t.Fatal("event")
	}
	// REG-08: unknown objects and versions do not block an Update.
	r, err = c.Update(h.ctx, nil, []byte("</3/0>,</32769/0>;ver=9.9,</40000>"))
	mustCode(t, r, err, "2.04")
	if reg, _ = h.srv.Store().ByEndpoint("upd"); !reg.HasInstance(32769, 0) || !reg.HasObject(40000) {
		t.Fatalf("unknown objects dropped: %+v", reg.Objects)
	}
	// Same media type as Register: anything but link-format is 4.00.
	txt := lwm2m.FormatText
	r, err = c.Raw(h.ctx, 2, "/rd/"+c.Location(), nil, &txt, []byte("</3/0>"))
	mustCode(t, r, err, "4.00")
	for _, q := range [][]string{{"ep=upd"}, {"lwm2m=1.1"}, {"x=1"}, {"lt=abc"}} {
		r, err = c.Update(h.ctx, q, nil)
		mustCode(t, r, err, "4.00")
	}
	r, err = c.Raw(h.ctx, 2, "/rd/unknown", nil, nil, nil)
	mustCode(t, r, err, "4.04")
}

// Proves: REG-16, OBS-10
// De-register removes the registration and voids its observations (2.02);
// an unknown location is 4.04.
func TestDeregister(t *testing.T) {
	h := newHarness(t)
	c := h.registered("dereg")
	ob, _, err := h.srv.Observe(h.ctx, "dereg", p("/3/0/9"), ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loc := c.Location()
	r, err := c.Deregister(h.ctx)
	mustCode(t, r, err, "2.02")
	if _, ok := h.srv.Store().ByID(loc); ok || len(h.srv.obs.forRegistration(loc)) != 0 || h.srv.KnownObservation(ob.token) {
		t.Fatal("registration or observations kept")
	}
	h.ev.wait(t, func(e Event) bool { x, ok := e.(Deregistered); return ok && x.Reason == ReasonDeregistered })
	r, err = c.Deregister(h.ctx)
	mustCode(t, r, err, "4.04")
}

// Proves: REG-17
// In NoSec mode an Update from a different address is refused with 4.04,
// so the client registers again (T §6.4.3). A De-register from another
// address is accepted only when the endpoint has no credentials (int-105).
func TestNoSecAddressChange(t *testing.T) {
	h := newHarness(t)
	c := h.registered("moved")
	other := h.device(testclient.Config{Endpoint: "moved"})
	r, err := other.Raw(h.ctx, 2, "/rd/"+c.Location(), nil, nil, nil)
	mustCode(t, r, err, "4.04")
	r, err = other.Raw(h.ctx, 4, "/rd/"+c.Location(), nil, nil, nil)
	mustCode(t, r, err, "2.02")
	u, err := c.Update(h.ctx, nil, nil)
	mustCode(t, u, err, "4.04")
}

// Proves: REG-02
// Without ep a 1.1+ client is named by its authenticated identity; a NoSec
// client has none, so it is refused.
func TestEndpointFromIdentity(t *testing.T) {
	h := newHarness(t)
	if err := h.srv.Security().Put(SecurityInfo{Endpoint: "urn:imei:1", PSKIdentity: "id-1", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	c := h.device(testclient.Config{PSKIdentity: "id-1", PSKKey: []byte("0123456789abcdef")})
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, "2.01")
	if _, ok := h.srv.Store().ByEndpoint("urn:imei:1"); !ok {
		t.Fatal("endpoint not derived from the PSK identity")
	}
	n := h.device(testclient.Config{})
	r, err = n.Register(h.ctx)
	mustCode(t, r, err, "4.00")
}

// Proves: SEC-06, SEC-07, SEC-05
// The endpoint name must match the authenticated identity (4.00
// otherwise); NoSec is refused for endpoints that have credentials. PSK
// identities up to 128 bytes and keys up to 64 bytes work.
func TestEndpointIdentityBinding(t *testing.T) {
	h := newHarness(t)
	longID := string(make128())
	longKey := make([]byte, 64)
	for i := range longKey {
		longKey[i] = byte(i)
	}
	for _, si := range []SecurityInfo{
		{Endpoint: "a", PSKIdentity: "id-a", PSKKey: []byte("aaaaaaaaaaaaaaaa")},
		{Endpoint: "b", PSKIdentity: "id-b", PSKKey: []byte("bbbbbbbbbbbbbbbb")},
		{Endpoint: "long", PSKIdentity: longID, PSKKey: longKey},
	} {
		if err := h.srv.Security().Put(si); err != nil {
			t.Fatal(err)
		}
	}
	// Client authenticated as id-a claims endpoint b.
	c := h.device(testclient.Config{Endpoint: "b", PSKIdentity: "id-a", PSKKey: []byte("aaaaaaaaaaaaaaaa")})
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, "4.00")
	ok := h.device(testclient.Config{Endpoint: "a", PSKIdentity: "id-a", PSKKey: []byte("aaaaaaaaaaaaaaaa")})
	r, err = ok.Register(h.ctx)
	mustCode(t, r, err, "2.01")
	reg, _ := h.srv.Store().ByEndpoint("a")
	if reg.Identity.Mode != ModePSK || reg.Identity.PSKIdentity != "id-a" {
		t.Fatalf("identity %+v", reg.Identity)
	}
	// NoSec registration for an endpoint with credentials.
	ns := h.device(testclient.Config{Endpoint: "b"})
	r, err = ns.Register(h.ctx)
	mustCode(t, r, err, "4.00")
	// Unknown PSK identity: the handshake fails.
	bad := testclient.New(testclient.Config{Endpoint: "z", PSKIdentity: "nobody", PSKKey: []byte("zzzzzzzzzzzzzzzz")})
	if err := bad.Dial(h.dtls); err == nil {
		if _, err := bad.Register(h.ctx); err == nil {
			t.Fatal("unknown PSK identity registered")
		}
		_ = bad.Close()
	}
	lc := h.device(testclient.Config{Endpoint: "long", PSKIdentity: longID, PSKKey: longKey})
	r, err = lc.Register(h.ctx)
	mustCode(t, r, err, "2.01")
}

func make128() []byte {
	b := make([]byte, 128)
	for i := range b {
		b[i] = 'a' + byte(i%26)
	}
	return b
}

// Proves: REG-06
// An Authorize policy refusing an authenticated endpoint answers 4.03.
func TestRegisterForbidden(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Authorize = func(ep string, _ Identity) bool { return ep != "banned" } })
	c := h.device(testclient.Config{Endpoint: "banned"})
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, "4.03")
}

// Proves: PROF-01, PROF-03, PROF-04, PROF-05, PROF-06, PROF-07, PROF-08, PROF-09, PROF-11, REG-21, REG-06
// Profile IDs: an unknown pid without a list is 4.09; a pid sent with its
// list is learned and later resolves alone; pre-configured oma:/v: IDs
// resolve from configuration; several pids and a payload combine; a 1.2
// Register with neither list nor pid is 4.09; /0 /21 /23 never enter the
// resolved list.
func TestProfileIDs(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Profiles = map[string][]link.Object{
			"v:tracker":   {{ID: 6, Instances: []uint16{0}}},
			"oma:sensors": {{ID: 3303, Instances: []uint16{0, 1}}},
		}
	})
	c := h.device(testclient.Config{Endpoint: "prof", Version: "1.2"})
	base := []string{"ep=prof", "lt=600", "lwm2m=1.2"}
	r, err := c.RegisterRaw(h.ctx, append(base, "pid=6:00aabbcc"), nil, false)
	mustCode(t, r, err, "4.09")
	r, err = c.RegisterRaw(h.ctx, base, nil, false)
	mustCode(t, r, err, "4.09")

	r, err = c.RegisterRaw(h.ctx, append(base, "pid=6:00aabbcc"), []byte("</0/0>,</1/0>,</3/0>,</21/0>,</23/0>"), true)
	mustCode(t, r, err, "2.01")
	r, err = c.RegisterRaw(h.ctx, append(base, "pid=6:00aabbcc"), nil, false)
	mustCode(t, r, err, "2.01")
	reg, _ := h.srv.Store().ByEndpoint("prof")
	if !reg.HasInstance(3, 0) || !reg.HasInstance(1, 0) || reg.HasObject(0) || reg.HasObject(21) || reg.HasObject(23) {
		t.Fatalf("learned profile resolved to %+v", reg.Objects)
	}
	r, err = c.RegisterRaw(h.ctx, append(base, `pid="6:00aabbcc,v:tracker"`), []byte("</4/0>"), true)
	mustCode(t, r, err, "2.01")
	reg, _ = h.srv.Store().ByEndpoint("prof")
	for _, o := range []uint16{1, 3, 4, 6} {
		if !reg.HasObject(o) {
			t.Fatalf("object %d missing from combined list %+v", o, reg.Objects)
		}
	}
	// PROF-07: instances of one object from a pid and the payload combine.
	r, err = c.RegisterRaw(h.ctx, append(base, `pid="6:00aabbcc,oma:sensors"`), []byte("</3/1>,</3303/2>"), true)
	mustCode(t, r, err, "2.01")
	reg, _ = h.srv.Store().ByEndpoint("prof")
	if !reg.HasInstance(3, 0) || !reg.HasInstance(3, 1) || !reg.HasInstance(3303, 0) || !reg.HasInstance(3303, 1) || !reg.HasInstance(3303, 2) {
		t.Fatalf("instances not combined: %+v", reg.Objects)
	}
	// The learned list is not altered by a later merge.
	if l, _ := h.srv.resolveProfiles([]string{"6:00aabbcc"}); len(l) != 2 {
		t.Fatalf("cached profile changed: %+v", l)
	}
	// PROF-04: an oma: pre-configured ID alone.
	r, err = c.RegisterRaw(h.ctx, append(base, "pid=oma:sensors"), nil, false)
	mustCode(t, r, err, "2.01")
	reg, _ = h.srv.Store().ByEndpoint("prof")
	if len(reg.Objects) != 1 || !reg.HasInstance(3303, 1) {
		t.Fatalf("oma: profile resolved to %+v", reg.Objects)
	}
	// PROF-09: pid on Update replaces the list; an unknown one is 4.09.
	u, err := c.Update(h.ctx, []string{"pid=v:tracker"}, nil)
	mustCode(t, u, err, "2.04")
	reg, _ = h.srv.Store().ByEndpoint("prof")
	if len(reg.Objects) != 1 || !reg.HasInstance(6, 0) || reg.ProfileIDs[0] != "v:tracker" {
		t.Fatalf("Update pid resolved to %+v", reg.Objects)
	}
	u, err = c.Update(h.ctx, []string{"pid=6:ffff0000"}, nil)
	mustCode(t, u, err, "4.09")
	u, err = c.Update(h.ctx, []string{"pid=6:FFFF"}, nil)
	mustCode(t, u, err, "4.00")
}

// Proves: GEN-10
// The Registered event fires after the 2.01 is sent, so a DM request
// issued from the event handler reaches a client that knows it is
// registered.
func TestDMAfterRegisterReply(t *testing.T) {
	var srv *Server
	got := make(chan *Response, 1)
	h := newHarness(t, func(c *Config) {
		prev := c.OnEvent
		c.OnEvent = func(e Event) {
			prev(e)
			if r, ok := e.(Registered); ok {
				go func() {
					resp, _ := srv.Read(context.Background(), r.Registration.Endpoint, p("/3/0/0"), ReadOptions{})
					got <- resp
				}()
			}
		}
	})
	srv = h.srv
	c := h.registered("order")
	select {
	case resp := <-got:
		if resp == nil || !resp.Success() {
			t.Fatalf("read after register: %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	reqs := c.Requests()
	if len(reqs) == 0 || reqs[0].Path != "/3/0/0" {
		t.Fatalf("requests %+v", reqs)
	}

	// The binding contract that makes this hold: HandleUplink returns the
	// 2.01 with Registered deferred to after(), which bindings run once the
	// response is written.
	h2 := newHarness(t)
	cf := lwm2m.FormatLinkFormat
	resp, after := h2.srv.HandleUplink(fakePeer{}, &Message{Code: codes.POST, Path: "/rd",
		Query: []string{"ep=deferred", "lt=60", "lwm2m=1.1"}, Format: &cf, Payload: []byte("</3/0>")})
	if resp == nil || resp.Code != codes.Created {
		t.Fatalf("response %+v", resp)
	}
	registered := func() bool {
		h2.ev.mu.Lock()
		defer h2.ev.mu.Unlock()
		for _, e := range h2.ev.l {
			if _, ok := e.(Registered); ok {
				return true
			}
		}
		return false
	}
	if registered() {
		t.Fatal("Registered emitted before the response was sent")
	}
	after()
	if !registered() {
		t.Fatal("Registered not emitted after the response")
	}
}

// fakePeer is a NoSec transport session that cannot carry requests.
type fakePeer struct{}

func (fakePeer) Exchange(context.Context, *Message) (*Message, error) {
	return nil, errors.New("fakePeer: no transport")
}
func (fakePeer) Identity() Identity   { return Identity{Mode: ModeNoSec, Addr: "192.0.2.1:5683"} }
func (fakePeer) RemoteAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 5683} }
func (fakePeer) Binding() string      { return "U" }

// Proves: REG-27, REG-18
// The example flow of T Fig 6.4.3-1: Register, Update ?lt=600000,
// De-register, with the codes and Location it shows. Without b the
// binding defaults to U.
func TestSpecExampleFlow(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "example-client"})
	r, err := c.RegisterRaw(h.ctx, []string{"ep=example-client", "lwm2m=1.1", "lt=86400"}, []byte("</1/0>,</3/0>"), true)
	mustCode(t, r, err, "2.01")
	if len(r.Location) != 2 || r.Location[0] != "rd" {
		t.Fatalf("location %v", r.Location)
	}
	if reg, _ := h.srv.Store().ByEndpoint("example-client"); reg.Binding != "U" || reg.QueueMode {
		t.Fatalf("default binding %q queue %v", reg.Binding, reg.QueueMode)
	}
	r, err = c.Raw(h.ctx, codes.POST, "/rd/"+r.Location[1], []string{"lt=600000"}, nil, nil)
	mustCode(t, r, err, "2.04")
	if reg, _ := h.srv.Store().ByEndpoint("example-client"); reg.Lifetime != 600000*time.Second {
		t.Fatalf("lifetime %v", reg.Lifetime)
	}
	r, err = c.Raw(h.ctx, codes.DELETE, "/rd/"+c.Location(), nil, nil, nil)
	mustCode(t, r, err, "2.02")
	if len(h.srv.Store().All()) != 0 {
		t.Fatal("still registered")
	}
}

// Proves: REG-03
// A Server write of /1/x/1 becomes the registration's lifetime at once
// (C §6.2); 0 is infinite.
func TestSetLifetime(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "lt", Lifetime: 600})
	mustCode(mustRegister(h, c))
	expect(t, "2.04")(h.srv.SetLifetime(h.ctx, "lt", 30*time.Second))
	if v, _ := c.Get(p("/1/0/1")); v.Int != 30 {
		t.Fatalf("/1/0/1 = %+v", v)
	}
	if _, err := h.srv.SetLifetime(h.ctx, "lt", -time.Second); err == nil {
		t.Fatal("negative lifetime accepted")
	}
	h.clock.Add(31 * time.Second)
	h.srv.expireNow()
	if _, ok := h.srv.Store().ByEndpoint("lt"); ok {
		t.Fatal("written lifetime not applied")
	}
}
