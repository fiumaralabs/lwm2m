package bootstrap

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// clientOSCORE is a device's /21 view of its BS account.
func clientOSCORE(sid string) oscore.Params {
	return oscore.Params{MasterSecret: randBytes(16), MasterSalt: randBytes(8), SenderID: []byte(sid), RecipientID: []byte("bs")}
}

// oscoreClient dials a C.13 bootstrap client whose traffic with the BS is
// OSCORE-protected (Security Mode 3 + OSCORE: OSCORE only, over UDP).
func (h *harness) oscoreClient(ep string, p oscore.Params) (*testclient.BootstrapClient, *testclient.OSCOREClient) {
	h.t.Helper()
	b := testclient.NewBootstrap(testclient.Config{Endpoint: ep})
	c13(b, ModeNoSec, "", "")
	ctx, err := oscore.New(p)
	if err != nil {
		h.t.Fatal(err)
	}
	o := testclient.NewOSCOREOn(b.Client, ctx)
	if err := o.Dial(h.udp); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = o.Close() })
	return b, o
}

// Bootstrap over OSCORE (T §5.4.3, §6.4.2): the first Bootstrap-Request
// on a new context is answered with a protected 4.01 carrying Echo and no
// session starts; the request repeated with that Echo gets 2.04, and the
// session's Writes and Finish are OSCORE-protected (the client refuses
// unprotected requests). The same holds for Bootstrap-Pack-Request on a
// new context. An unprotected Bootstrap-Request for an OSCORE endpoint is
// refused with 4.01; one whose ep is not the context's endpoint gets 4.00.
// Proves: BS-13, OSC-04, OSC-05
func TestOSCOREBootstrapEcho(t *testing.T) {
	h := newHarness(t)
	o := h.bs.EnableOSCORE()
	cp := clientOSCORE("dev1")
	if err := o.Put("osc-ep", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	h.put("osc-ep", c1("coap://dm.example.com", "osc-ep", "dm-key-0123456789"))

	b, oc := h.oscoreClient("osc-ep", cp)
	r, err := oc.Raw(h.ctx, codes.POST, "/bs", b.BootstrapQuery(nil), nil, nil)
	if err != nil || r.Code != codes.Unauthorized || !r.Protected || len(r.Echo) == 0 {
		t.Fatalf("first use: %+v %v", r, err)
	}
	select {
	case res := <-h.results:
		t.Fatalf("session ran without freshness: %+v", res)
	default:
	}
	echo := r.Echo
	br, err := b.BootstrapRequestWith(func() (*testclient.Response, error) {
		r, err := oc.Raw(h.ctx, codes.POST, "/bs", b.BootstrapQuery(nil), nil, nil, message.Option{ID: oscore.OptionEcho, Value: echo})
		if err != nil {
			return nil, err
		}
		return &r.Response, nil
	})
	mustCode(t, br, err, codes.Changed)
	if fin, _ := b.WaitFinish(h.ctx); fin != codes.Changed {
		t.Fatalf("finish %s", server.CodeString(fin))
	}
	res := h.result()
	if res.Err != nil || res.Endpoint != "osc-ep" || len(res.Steps) < 3 {
		t.Fatalf("session %+v", res)
	}
	if uri, _ := b.Get(lwm2m.MustParsePath("/0/0/0")); uri.Str != "coap://dm.example.com" {
		t.Fatalf("server account not written: %v", uri)
	}

	// Unprotected Bootstrap-Request for the OSCORE endpoint: 4.01.
	plain := h.client("osc-ep", testclient.Config{})
	pr, err := plain.BootstrapRequest(h.ctx, plain.BootstrapQuery(nil))
	mustCode(t, pr, err, codes.Unauthorized)
	// ep not bound to the context: 4.00 (protected), even for a known
	// endpoint.
	h.put("someone-else", c1("coap://dm.example.com", "someone-else", "dm-key-0123456789"))
	rr, err := oc.Do(h.ctx, codes.POST, "/bs", []string{"ep=someone-else"}, nil, nil)
	if err != nil || rr.Code != codes.BadRequest || !rr.Protected {
		t.Fatalf("foreign ep: %+v %v", rr, err)
	}

	// Bootstrap-Pack-Request on a new context: Echo first.
	pp := clientOSCORE("dev2")
	if err := o.Put("pack-ep", pp.Reverse()); err != nil {
		t.Fatal(err)
	}
	h.put("pack-ep", c1("coap://dm.example.com", "pack-ep", "dm-key-0123456789"))
	_, pc := h.oscoreClient("pack-ep", pp)
	acc := lwm2m.FormatSenMLCBOR
	pr2, err := pc.Raw(h.ctx, codes.GET, "/bspack", []string{"ep=pack-ep"}, nil, nil, message.Option{ID: message.Accept, Value: []byte{byte(acc)}})
	if err != nil || pr2.Code != codes.Unauthorized || len(pr2.Echo) == 0 {
		t.Fatalf("pack first use: %+v %v", pr2, err)
	}
	pr2, err = pc.Raw(h.ctx, codes.GET, "/bspack", []string{"ep=pack-ep"}, nil, nil,
		message.Option{ID: message.Accept, Value: []byte{byte(acc)}}, message.Option{ID: oscore.OptionEcho, Value: pr2.Echo})
	if err != nil || pr2.Code != codes.Content || !pr2.Protected || len(pr2.Body) == 0 {
		t.Fatalf("pack: %+v %v", pr2, err)
	}
	if res := h.result(); !res.Pack || res.Endpoint != "pack-ep" {
		t.Fatalf("pack result %+v", res)
	}
	// A Bootstrap-Pack-Request whose ep is not the context's: 4.00.
	pr2, err = pc.Do(h.ctx, codes.GET, "/bspack", []string{"ep=someone-else"}, nil, nil)
	if err != nil || pr2.Code != codes.BadRequest || !pr2.Protected {
		t.Fatalf("pack foreign ep: %+v %v", pr2, err)
	}
}

// OSCORE bootstrapping with a PSK (T §5.4.3): the BS refuses a Master
// Secret that is short, shared with another device or equal to the
// device's DTLS PSK, and a Recipient ID/ID Context pair already in use
// (RFC 8613 §3.3). On first use the client derives a fresh context with
// RFC 8613 Appendix B.2: request #1 carries kid context R1; the BS answers
// a 4.01 protected with ID Context R2||R1 carrying kid context R2 (and
// Echo); request #2 under ID Context R2||R3 is served and the session runs
// on that context. A replayed request #2 is refused.
// Proves: OSC-03, BS-10
func TestOSCOREBootstrapPSKAppendixB2(t *testing.T) {
	h := newHarness(t)
	o := h.bs.EnableOSCORE()
	if err := h.sec.Put(server.SecurityInfo{Endpoint: "dtls-ep", PSKIdentity: "dtls-ep", PSKKey: []byte(bsKey)}); err != nil {
		t.Fatal(err)
	}
	weak := clientOSCORE("w")
	weak.MasterSecret = []byte("password")
	if err := o.Put("w", weak.Reverse()); !errors.Is(err, ErrWeakOSCORESecret) {
		t.Fatalf("weak secret: %v", err)
	}
	reused := clientOSCORE("r")
	reused.MasterSecret = []byte(bsKey)
	if err := o.Put("dtls-ep", reused.Reverse()); !errors.Is(err, ErrSharedOSCORESecret) {
		t.Fatalf("secret of another protocol: %v", err)
	}
	cp := clientOSCORE("b2")
	if err := o.Put("b2-ep", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	twin := clientOSCORE("twin")
	twin.MasterSecret = cp.MasterSecret
	if err := o.Put("twin-ep", twin.Reverse()); !errors.Is(err, ErrSharedOSCORESecret) {
		t.Fatalf("secret of another device: %v", err)
	}
	dup := clientOSCORE("b2")
	if err := o.Put("dup-ep", dup.Reverse()); !errors.Is(err, oscore.ErrDuplicateRecipient) {
		t.Fatalf("duplicate recipient: %v", err)
	}

	h.put("b2-ep", c1("coap://dm.example.com", "b2-ep", "dm-key-0123456789"))
	b, oc := h.oscoreClient("b2-ep", cp)
	oc.Base = &cp
	if err := oc.Rederive(); err != nil {
		t.Fatal(err)
	}
	r1 := oc.Ctx.Params().IDContext
	r, err := oc.Raw(h.ctx, codes.POST, "/bs", b.BootstrapQuery(nil), nil, nil)
	if err != nil || r.Code != codes.Unauthorized || !r.Protected || len(r.KIDContext) != 8 || len(r.Echo) == 0 {
		t.Fatalf("response #1: %+v %v", r, err)
	}
	id3 := oc.Ctx.Params().IDContext
	if !bytes.HasPrefix(id3, r.KIDContext) || bytes.Equal(id3[8:], r1) {
		t.Fatalf("ID Context %x after R2 %x", id3, r.KIDContext)
	}
	echo := r.Echo
	got, err := b.BootstrapRequestWith(func() (*testclient.Response, error) {
		r, err := oc.Raw(h.ctx, codes.POST, "/bs", b.BootstrapQuery(nil), nil, nil, message.Option{ID: oscore.OptionEcho, Value: echo})
		if err != nil {
			return nil, err
		}
		return &r.Response, nil
	})
	mustCode(t, got, err, codes.Changed)
	if fin, _ := b.WaitFinish(h.ctx); fin != codes.Changed {
		t.Fatalf("finish %s", server.CodeString(fin))
	}
	if res := h.result(); res.Err != nil {
		t.Fatal(res.Err)
	}
	e, _ := o.Get("b2-ep")
	if !bytes.Equal(e.Context().Params().IDContext, id3) {
		t.Fatal("BS did not switch to the R2||R3 context")
	}
	rp, err := oc.Replay(h.ctx)
	if err != nil || rp.Code != codes.Unauthorized || rp.Protected {
		t.Fatalf("replayed request #2: %+v %v", rp, err)
	}
}

// The BS only provisions valid OSCORE links (T §5.4.7.1): /0/x/17 must
// name a /21 instance the config writes, with resources 0-2, and no /21
// instance may be linked from two /0 instances.
// Proves: OSC-07
func TestOSCORELinkValidation(t *testing.T) {
	cfg := func(links ...uint16) *BootstrapConfig {
		c := &BootstrapConfig{
			Security: map[uint16]SecurityConfig{},
			Servers:  map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 60}},
			OSCORE:   map[uint16]OSCOREConfig{0: {MasterSecret: randBytes(16), SenderID: Bytes("dev"), RecipientID: Bytes("srv")}},
		}
		for i, l := range links {
			c.Security[uint16(i)] = SecurityConfig{URI: "coap://s.example.com", SecurityMode: ModeNoSec, ServerID: u16(uint16(i + 1)), OSCORE: u16(l)}
		}
		return c
	}
	if err := cfg(0).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*BootstrapConfig{"missing /21": cfg(3), "shared /21": cfg(0, 0)} {
		if err := c.Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := cfg(0)
	bad.OSCORE[0] = OSCOREConfig{SenderID: Bytes("dev"), RecipientID: Bytes("srv")} // no Master Secret
	if err := bad.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("no master secret: %v", err)
	}
}
