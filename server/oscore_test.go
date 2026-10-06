package server

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// clientParams is the client's /21 view: Sender ID = sid, Recipient ID =
// "srv".
func clientParams(sid string) oscore.Params {
	return oscore.Params{MasterSecret: randBytes(16), MasterSalt: randBytes(8), SenderID: []byte(sid), RecipientID: []byte("srv")}
}

// oscoreDevice dials an OSCORE client with the harness's Device objects.
func (h *harness) oscoreDevice(cfg testclient.Config, params oscore.Params) *testclient.OSCOREClient {
	h.t.Helper()
	ctx, err := oscore.New(params)
	if err != nil {
		h.t.Fatal(err)
	}
	c := testclient.NewOSCORE(cfg, ctx)
	c.Set(p("/1/0/0"), lwm2m.Integer(1))
	c.Set(p("/1/0/1"), lwm2m.Integer(86400))
	c.Set(p("/1/0/7"), lwm2m.String("U"))
	c.Set(p("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	c.Set(p("/3/0/9"), lwm2m.Integer(100))
	c.Set(p("/3/0/16"), lwm2m.String("U"))
	addr := h.addr
	if cfg.PSKIdentity != "" {
		addr = h.dtls
	}
	if err := c.Dial(addr); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = c.Close() })
	return c
}

func echoOpt(v []byte) message.Option { return message.Option{ID: oscore.OptionEcho, Value: v} }

func oscoreCode(t *testing.T, r *testclient.OSCOREResponse, err error, want string, protected bool) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := CodeString(r.Code); got != want || r.Protected != protected {
		t.Fatalf("code %s protected %v, want %s protected %v (body %q)", got, r.Protected, want, protected, r.Body)
	}
}

// Proves: OSC-01, OSC-04, OSC-06, REG-06
// OSCORE protects client↔Server traffic over plain UDP (Security Mode 3
// plus OSCORE = OSCORE only). The first Register on a new context is
// refused with a protected 4.01 carrying only Echo (Tbl 6.7-2, RFC 8613
// Appendix B.1.2: with a fresh Partial IV); the Register repeated with
// that Echo succeeds. Device Management requests are then sent with the
// Server as OSCORE client, protected with its Sender Context (the client
// refuses unprotected requests), and Update and De-register work over
// OSCORE.
func TestOSCORERegisterEchoAndDM(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	cp := clientParams("c1")
	if err := o.Put("urn:dev:osc", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:osc"}, cp)
	cf := lwm2m.FormatLinkFormat
	r, err := c.Raw(h.ctx, codes.POST, "/rd", c.RegisterQuery(), &cf, []byte(c.ObjectLinks()))
	oscoreCode(t, r, err, "4.01", true)
	if len(r.Echo) == 0 || len(r.Body) != 0 {
		t.Fatalf("Echo %x body %q", r.Echo, r.Body)
	}
	if len(h.srv.Store().All()) != 0 {
		t.Fatal("Register without Echo was processed")
	}
	r, err = c.Raw(h.ctx, codes.POST, "/rd", c.RegisterQuery(), &cf, []byte(c.ObjectLinks()), echoOpt(r.Echo))
	oscoreCode(t, r, err, "2.01", true)
	reg, ok := h.srv.Store().ByEndpoint("urn:dev:osc")
	if !ok || !strings.HasPrefix(reg.Identity.Addr, oscoreAddrPrefix) {
		t.Fatalf("registration %+v", reg)
	}
	// Later requests need no Echo.
	c.SetLocation(r.Location[1])
	r, err = c.Update(h.ctx, []string{"lt=600"})
	oscoreCode(t, r, err, "2.04", true)

	resp, err := h.srv.Read(h.ctx, "urn:dev:osc", p("/3/0/0"), ReadOptions{})
	mustResp(t, resp, err, "2.05")
	if len(resp.Nodes) != 1 || !resp.Nodes[0].Value.Equal(lwm2m.String("Open Mobile Alliance")) {
		t.Fatalf("read %+v", resp)
	}
	req, _ := c.LastRequest()
	if req.Code != codes.GET || req.Path != "/3/0/0" {
		t.Fatalf("decrypted request %+v", req)
	}
	resp, err = h.srv.Write(h.ctx, "urn:dev:osc", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(300))}, WriteOptions{})
	mustResp(t, resp, err, "2.04")
	if v, _ := c.Get(p("/1/0/1")); !v.Equal(lwm2m.Integer(300)) {
		t.Fatalf("written %v", v)
	}
	r, err = c.Deregister(h.ctx)
	oscoreCode(t, r, err, "2.02", true)
	if len(h.srv.Store().All()) != 0 {
		t.Fatal("not deregistered")
	}
}

// Proves: OSC-05, SEC-15
// With a configured endpoint name the Register ep must equal it (4.00
// otherwise, compared as bytes, A-18); an omitted ep is taken from the
// context. With no configured name the ep is authenticated by Sender ID =
// ep.
func TestOSCOREEndpointBinding(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	cp := clientParams("kid-a")
	if err := o.Put("urn:dev:a", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:b"}, cp)
	r, err := c.Register(h.ctx)
	oscoreCode(t, r, err, "4.00", true)
	r, err = c.Do(h.ctx, codes.POST, "/rd", []string{"lt=60", "lwm2m=1.1"}, nil, []byte(c.ObjectLinks()))
	oscoreCode(t, r, err, "2.01", true)
	if _, ok := h.srv.Store().ByEndpoint("urn:dev:a"); !ok {
		t.Fatal("ep not derived from the OSCORE context")
	}

	sp := clientParams("ep-sid")
	if err := o.Put("", sp.Reverse()); err != nil {
		t.Fatal(err)
	}
	c2 := h.oscoreDevice(testclient.Config{Endpoint: "ep-sid"}, sp)
	r, err = c2.Register(h.ctx)
	oscoreCode(t, r, err, "2.01", true)
	c3 := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:other"}, sp)
	c3.Ctx.SetSequence(1000)
	r, err = c3.Register(h.ctx)
	oscoreCode(t, r, err, "4.00", true)
	// Two endpoints cannot share a Recipient ID (RFC 8613 §3.3).
	if err := o.Put("urn:dev:dup", clientParams("kid-a").Reverse()); !errors.Is(err, ErrDuplicateOSCORERecipient) {
		t.Fatalf("duplicate: %v", err)
	}
}

// Proves: OSC-04
// OSCORE errors are unprotected (RFC 8613 §8.2): unknown kid 4.01, replay
// 4.01, failed decryption 4.00. An Echo older than its lifetime is not
// accepted. An unprotected request for an OSCORE endpoint is refused with
// 4.01, and another OSCORE client cannot act on the registration (4.00).
func TestOSCOREErrors(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	cp := clientParams("c1")
	if err := o.Put("urn:dev:e", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	cf := lwm2m.FormatLinkFormat
	links := "</3/0>"

	stranger := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:e"}, clientParams("nobody"))
	r, err := stranger.Raw(h.ctx, codes.POST, "/rd", []string{"lt=60", "lwm2m=1.1"}, &cf, []byte(links))
	oscoreCode(t, r, err, "4.01", false)
	if string(r.Body) != "Security context not found" {
		t.Fatalf("diagnostic %q", r.Body)
	}
	forged := cp
	forged.MasterSecret = randBytes(16)
	f := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:e"}, forged)
	r, err = f.Raw(h.ctx, codes.POST, "/rd", []string{"lt=60", "lwm2m=1.1"}, &cf, []byte(links))
	oscoreCode(t, r, err, "4.00", false)

	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:e"}, cp)
	r, err = c.Raw(h.ctx, codes.POST, "/rd", c.RegisterQuery(), &cf, []byte(links))
	oscoreCode(t, r, err, "4.01", true)
	h.clock.Add(2 * time.Minute) // past EchoLifetime
	r, err = c.Raw(h.ctx, codes.POST, "/rd", c.RegisterQuery(), &cf, []byte(links), echoOpt(r.Echo))
	oscoreCode(t, r, err, "4.01", true)
	if len(r.Echo) == 0 || len(h.srv.Store().All()) != 0 {
		t.Fatal("stale Echo accepted")
	}
	r, err = c.Raw(h.ctx, codes.POST, "/rd", c.RegisterQuery(), &cf, []byte(links), echoOpt(r.Echo))
	oscoreCode(t, r, err, "2.01", true)
	c.SetLocation(r.Location[1])
	r, err = c.Replay(h.ctx)
	oscoreCode(t, r, err, "4.01", false)
	if string(r.Body) != "Replay detected" || len(h.srv.Store().All()) != 1 {
		t.Fatalf("replay: %q", r.Body)
	}

	plain := h.device(testclient.Config{Endpoint: "urn:dev:e"})
	pr, err := plain.Register(h.ctx)
	mustCode(t, pr, err, "4.01")
	pr, err = plain.Raw(h.ctx, codes.DELETE, "/rd/"+c.Location(), nil, nil, nil)
	mustCode(t, pr, err, "4.01")

	other := clientParams("c2")
	if err := o.Put("urn:dev:other", other.Reverse()); err != nil {
		t.Fatal(err)
	}
	oc := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:other"}, other)
	r, err = oc.Do(h.ctx, codes.DELETE, "/rd/"+c.Location(), nil, nil, nil)
	oscoreCode(t, r, err, "4.00", true)
	if len(h.srv.Store().All()) != 1 {
		t.Fatal("registration removed by another client")
	}
}

// Proves: OSC-04
// After the server lost its replay window (here: the context is
// re-added, as after a restart) the next request is challenged with Echo
// and the verified Partial IV becomes the window's lower limit (RFC 8613
// Appendix B.1.2), so older captured requests stay refused.
func TestOSCOREContextReloadedNeedsEcho(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	cp := clientParams("c1")
	_ = o.Put("urn:dev:r", cp.Reverse())
	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:r"}, cp)
	r, err := c.Register(h.ctx)
	oscoreCode(t, r, err, "2.01", true)
	c.SetLocation(r.Location[1])
	r, err = c.Update(h.ctx, nil)
	oscoreCode(t, r, err, "2.04", true)
	_ = o.Put("urn:dev:r", cp.Reverse()) // restart: same parameters, mutable state lost
	r, err = c.Replay(h.ctx)             // the captured Update
	oscoreCode(t, r, err, "4.01", true)  // challenged, not executed
	r, err = c.Update(h.ctx, nil)
	oscoreCode(t, r, err, "2.04", true)
	r, err = c.Replay(h.ctx)
	oscoreCode(t, r, err, "4.01", false)
}

// Proves: OSC-04, OSC-06
// A client that requires Echo before a Write answers a protected 4.01 with
// Echo; the server repeats the request once with it.
func TestOSCOREDownlinkEchoRetry(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	cp := clientParams("c1")
	_ = o.Put("urn:dev:w", cp.Reverse())
	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:w"}, cp)
	c.RequireEcho = true
	r, err := c.Register(h.ctx)
	oscoreCode(t, r, err, "2.01", true)
	resp, err := h.srv.Write(h.ctx, "urn:dev:w", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(120))}, WriteOptions{})
	mustResp(t, resp, err, "2.04")
	if n := len(c.Requests()); n != 2 {
		t.Fatalf("%d requests, want the challenged one and the retry", n)
	}
	if v, _ := c.Get(p("/1/0/1")); !v.Equal(lwm2m.Integer(120)) {
		t.Fatalf("written %v", v)
	}
}

// Proves: OSC-06
// Observe over OSCORE: the registration is protected by the Server (outer
// FETCH), notifications carry the client's fresh Partial IVs and are
// delivered; an unprotected notification on the same token is dropped.
func TestOSCOREObserve(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	cp := clientParams("c1")
	_ = o.Put("urn:dev:o", cp.Reverse())
	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:o"}, cp)
	r, err := c.Register(h.ctx)
	oscoreCode(t, r, err, "2.01", true)
	ob, resp, err := h.srv.Observe(h.ctx, "urn:dev:o", p("/3/0/9"), ObserveOptions{})
	if err != nil || ob == nil || !resp.Success() || !resp.Nodes[0].Value.Equal(lwm2m.Integer(100)) {
		t.Fatalf("observe: %v %+v", err, resp)
	}
	req, _ := c.LastRequest()
	c.Set(p("/3/0/9"), lwm2m.Integer(42))
	if err := c.Notify(h.ctx, req.Token); err != nil { // protected
		t.Fatal(err)
	}
	n := notification(t, h, ob)
	if len(n.Response.Nodes) != 1 || !n.Response.Nodes[0].Value.Equal(lwm2m.Integer(42)) {
		t.Fatalf("notification %+v", n.Response)
	}
	// The embedded plain client sends an unprotected one on the same token.
	nctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
	_, _ = c.Client.Notify(nctx, req.Token)
	<-nctx.Done()
	cancel()
	h.ev.mu.Lock()
	count := 0
	for _, e := range h.ev.l {
		if _, ok := e.(Notification); ok {
			count++
		}
	}
	h.ev.mu.Unlock()
	if count != 1 {
		t.Fatalf("%d notifications delivered, want only the protected one", count)
	}
}

// OSC-08 (DTLS + OSCORE clause only; the SMS clause is unimplemented, see spec/coverage-pending.txt)
// OSCORE over DTLS (Security Mode 0 plus OSCORE = both): the registration
// carries the DTLS identity and still needs OSCORE.
func TestOSCOREOverDTLS(t *testing.T) {
	h := newHarness(t)
	o := h.srv.EnableOSCORE()
	if err := h.srv.Security().Put(SecurityInfo{Endpoint: "urn:dev:both", PSKIdentity: "both", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	cp := clientParams("c1")
	_ = o.Put("urn:dev:both", cp.Reverse())
	// DTLS alone is not enough: the plain Register over the same PSK session
	// is refused.
	plain := h.device(testclient.Config{Endpoint: "urn:dev:both", PSKIdentity: "both", PSKKey: []byte("0123456789abcdef")})
	pr, err := plain.Register(h.ctx)
	mustCode(t, pr, err, "4.01")
	if len(h.srv.Store().All()) != 0 {
		t.Fatal("plain DTLS Register processed")
	}
	c := h.oscoreDevice(testclient.Config{Endpoint: "urn:dev:both", PSKIdentity: "both", PSKKey: []byte("0123456789abcdef")}, cp)
	r, err := c.Register(h.ctx)
	oscoreCode(t, r, err, "2.01", true)
	reg, _ := h.srv.Store().ByEndpoint("urn:dev:both")
	if reg.Identity.Mode != ModePSK {
		t.Fatalf("identity %+v", reg.Identity)
	}
	resp, err := h.srv.Read(h.ctx, "urn:dev:both", p("/3/0/9"), ReadOptions{})
	mustResp(t, resp, err, "2.05")
}

// Proves: OSC-07
// A /21 instance gives the OSCORE input parameters (Opaque or UTF-8
// string, A-18); resources 0-2 are mandatory; every /0/x/17 must link to
// an existing /21 instance and no /21 instance may be linked twice.
func TestOSCOREObject21(t *testing.T) {
	inst := func(i uint16, extra ...lwm2m.Node) []lwm2m.Node {
		base := lwm2m.Path{}.Append(21).Append(i)
		return append([]lwm2m.Node{
			lwm2m.ValueNode(base.Append(0), lwm2m.Opaque([]byte("0123456789abcdef"))),
			lwm2m.ValueNode(base.Append(1), lwm2m.String("dev-x")),
			lwm2m.ValueNode(base.Append(2), lwm2m.Opaque([]byte("srv"))),
		}, extra...)
	}
	pr, err := OSCOREParams(inst(0,
		lwm2m.ValueNode(p("/21/0/3"), lwm2m.Integer(oscore.AESCCM16_64_128)),
		lwm2m.ValueNode(p("/21/0/4"), lwm2m.Integer(oscore.HMAC256)),
		lwm2m.ValueNode(p("/21/0/5"), lwm2m.Opaque([]byte{1, 2})),
		lwm2m.ValueNode(p("/21/0/6"), lwm2m.Opaque([]byte{3}))))
	if err != nil || string(pr.SenderID) != "dev-x" || string(pr.RecipientID) != "srv" || len(pr.MasterSalt) != 2 || len(pr.IDContext) != 1 {
		t.Fatalf("%+v %v", pr, err)
	}
	if _, err := OSCOREParams(inst(0)[:2]); err == nil {
		t.Fatal("missing Recipient ID accepted")
	}
	link := func(sec, i uint16) lwm2m.Node {
		return lwm2m.ValueNode(lwm2m.Path{}.Append(0).Append(sec).Append(17), lwm2m.Objlnk(21, i))
	}
	good := append(append(inst(0), inst(1)...), link(0, 0), link(1, 1))
	if err := CheckOSCORELinks(good); err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string][]lwm2m.Node{
		"shared /21": append(inst(0), link(0, 0), link(1, 0)),
		"missing":    append(inst(0), link(0, 3)),
		"not /21":    {lwm2m.ValueNode(p("/0/0/17"), lwm2m.Objlnk(1, 0))},
	} {
		if err := CheckOSCORELinks(cfg); !errors.Is(err, ErrOSCORELink) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
