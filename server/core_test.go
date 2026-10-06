package server_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: VER-01
// Object versions come from "ver" in the Register list, else from the
// client's LwM2M version: /1/0/10 exists in Server object 1.1 but not 1.0,
// so a 1.1 client that registers </1/0>;ver=1.0 cannot be written there.
func TestObjectVersionResolution(t *testing.T) {
	h := newHarness(t)
	link := lwm2m.Value{Type: lwm2m.TypeObjlnk, Link: lwm2m.NullObjLink}
	write := []lwm2m.Node{lwm2m.ValueNode(p("/1/0/10"), link)}

	def := h.device(testclient.Config{Endpoint: "v11"})
	mustCode(mustRegister(h, def))
	expect(t, "2.04")(h.srv.Write(h.ctx, "v11", p("/1/0/10"), write, server.WriteOptions{}))

	old := h.device(testclient.Config{Endpoint: "v10"})
	r, err := old.RegisterRaw(h.ctx, old.RegisterQuery(), []byte("</1/0>;ver=1.0,</3/0>"), true)
	mustCode(t, r, err, "2.01")
	if _, err := h.srv.Write(h.ctx, "v10", p("/1/0/10"), write, server.WriteOptions{}); !errors.Is(err, server.ErrBadRequest) {
		t.Fatalf("write to a resource of another object version: %v", err)
	}
	// ver on the object link (0 or >=2 instances form, C §7.2.3).
	obj := h.device(testclient.Config{Endpoint: "v10obj"})
	r, err = obj.RegisterRaw(h.ctx, obj.RegisterQuery(), []byte("</1>;ver=1.0,</1/0>,</3/0>"), true)
	mustCode(t, r, err, "2.01")
	if _, err := h.srv.Write(h.ctx, "v10obj", p("/1/0/10"), write, server.WriteOptions{}); !errors.Is(err, server.ErrBadRequest) {
		t.Fatalf("object-link ver ignored: %v", err)
	}
}

// Proves: DT-03
// Objlnk values must name the null link, a registered object (oid:65535)
// or a registered instance; anything else is refused before sending.
// The Corelnk clause of Tbl C-2 is the receiving client's Write check
// (C §6.3.3); the server does not parse Corelnk targets.
func TestObjlnkTargets(t *testing.T) {
	h := newHarness(t)
	c := h.registered("lnk")
	ok := []lwm2m.ObjLink{lwm2m.NullObjLink, {Object: 3, Instance: lwm2m.MaxID}, {Object: 3, Instance: 0}}
	for _, l := range ok {
		v := lwm2m.Value{Type: lwm2m.TypeObjlnk, Link: l}
		expect(t, "2.04")(h.srv.Write(h.ctx, "lnk", p("/1/0/10"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/10"), v)}, server.WriteOptions{}))
	}
	for _, l := range []lwm2m.ObjLink{{Object: 9, Instance: 0}, {Object: 3, Instance: 7}} {
		v := lwm2m.Value{Type: lwm2m.TypeObjlnk, Link: l}
		if _, err := h.srv.Write(h.ctx, "lnk", p("/1/0/10"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/10"), v)}, server.WriteOptions{}); !errors.Is(err, server.ErrBadRequest) {
			t.Errorf("objlnk %v sent: %v", l, err)
		}
	}
	if v, _ := c.Get(p("/1/0/10")); v.Link != (lwm2m.ObjLink{Object: 3, Instance: 0}) {
		t.Fatalf("stored %v", v)
	}
}

// Proves: DT-01, DT-02
// Every data type is checked against the model before sending (a String
// to an Integer resource, a Boolean to a Time) and on values read back, and
// accepted values are encoded per Tbl C-2: an Integer of 300 goes out as
// a 2-byte TLV value, a Time as an Integer.
func TestDataTypes(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "dt", Formats: []lwm2m.ContentFormat{lwm2m.FormatTLV}})
	mustCode(mustRegister(h, c))
	bad := map[string]lwm2m.Value{
		"/1/0/1":  lwm2m.String("60"),
		"/1/0/6":  lwm2m.Integer(1),
		"/3/0/13": lwm2m.Boolean(true),
		"/3/0/14": lwm2m.Integer(1),
	}
	for path, v := range bad {
		if _, err := h.srv.Write(h.ctx, "dt", p(path), []lwm2m.Node{lwm2m.ValueNode(p(path), v)}, server.WriteOptions{}); !errors.Is(err, server.ErrBadRequest) {
			t.Errorf("%s=%v sent: %v", path, v, err)
		}
	}
	tlv := lwm2m.FormatTLV
	expect(t, "2.04")(h.srv.Write(h.ctx, "dt", p("/1/0"), []lwm2m.Node{
		lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(300)),
	}, server.WriteOptions{Mode: server.PartialUpdate, Format: &tlv}))
	req, _ := c.LastRequest()
	if string(req.Body) != "\xc2\x01\x01\x2c" {
		t.Fatalf("TLV %x, want c201012c", req.Body)
	}
	expect(t, "2.04")(h.srv.Write(h.ctx, "dt", p("/3/0/13"), []lwm2m.Node{lwm2m.ValueNode(p("/3/0/13"), lwm2m.Time(1700000000))}, server.WriteOptions{}))
	if v, _ := c.Get(p("/3/0/13")); !v.Equal(lwm2m.Time(1700000000)) {
		t.Fatalf("time stored as %v", v)
	}
	// Retrieved values are type-checked too: text for the Integer /3/0/9,
	// a 2-byte TLV Boolean.
	for _, bad := range []struct {
		cf   lwm2m.ContentFormat
		path string
		body string
	}{{lwm2m.FormatText, "/3/0/9", "abc"}, {lwm2m.FormatTLV, "/1/0/6", "\xc2\x06\x00\x01"}} {
		cf := bad.cf
		body := []byte(bad.body)
		c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
			return codes.Content, &cf, body, true
		})
		if r, err := h.srv.Read(h.ctx, "dt", p(bad.path), server.ReadOptions{}); err != nil || r.DecodeErr == nil {
			t.Errorf("%s %q decoded: %v %+v", bad.path, bad.body, err, r)
		}
	}
	c.SetOverride(nil)
}

// Proves: FMT-06
// Responses labelled with the pre-IANA numbers 1541/1542/1543 are decoded
// as plain text, TLV and OMA JSON.
func TestLegacyContentFormats(t *testing.T) {
	h := newHarness(t)
	c := h.registered("legacy")
	for legacy, real := range map[lwm2m.ContentFormat]lwm2m.ContentFormat{
		lwm2m.FormatLegacyText: lwm2m.FormatText, lwm2m.FormatLegacyTLV: lwm2m.FormatTLV, lwm2m.FormatLegacyOMAJSON: lwm2m.FormatOMAJSON,
	} {
		path := p("/3/0")
		if real == lwm2m.FormatText {
			path = p("/3/0/9")
		}
		cd, _ := codec.For(real)
		body, err := cd.Encode(path, c.Nodes(path))
		if err != nil {
			t.Fatal(err)
		}
		cf := legacy
		c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
			return codes.Content, &cf, body, true
		})
		r := expect(t, "2.05")(h.srv.Read(h.ctx, "legacy", path, server.ReadOptions{}))
		if r.DecodeErr != nil || !lwm2m.NodesEqual(r.Nodes, c.Nodes(path)) {
			t.Fatalf("%d: %v %v", legacy, r.DecodeErr, r.Nodes)
		}
	}
}

// Proves: GEN-06, DM-14, QM-04
// Every response code of T §6.7 reaches the caller unchanged, including
// 4.01 from client access control and 5.xx; a transport failure (no
// answer) is reported as an error.
func TestResponseCodes(t *testing.T) {
	h := newHarness(t)
	c := h.registered("codes")
	for _, code := range []codes.Code{
		codes.BadRequest, codes.Unauthorized, codes.NotFound, codes.MethodNotAllowed, codes.NotAcceptable,
		codes.RequestEntityIncomplete, server.CodeConflict, codes.PreconditionFailed, codes.RequestEntityTooLarge,
		codes.UnsupportedMediaType, codes.InternalServerError, codes.NotImplemented, codes.ServiceUnavailable,
	} {
		cc := code
		c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) { return cc, nil, nil, true })
		r, err := h.srv.Read(h.ctx, "codes", p("/3/0/0"), server.ReadOptions{})
		if err != nil || r.Code != code || r.Success() {
			t.Fatalf("%s: %v %+v", server.CodeString(code), err, r)
		}
	}
	c.SetOverride(nil)
	_ = c.Close()
	h2 := newHarness(t, func(cfg *server.Config) { cfg.RequestTimeout = 300e6 })
	gone := h2.registered("gone")
	_ = gone.Close()
	if _, err := h2.srv.Read(h2.ctx, "gone", p("/3/0/0"), server.ReadOptions{}); err == nil {
		t.Fatal("unanswered request reported success")
	}
}

// Proves: GEN-05, GEN-12, GEN-13
// UDP is served; requests go out over the session the client registered
// on (a DTLS client gets its DM over DTLS), and writing the client's
// binding resource /1/x/7 changes neither the session nor the
// registration.
func TestBindingSession(t *testing.T) {
	h := newHarness(t)
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "sec", PSKIdentity: "sec", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	c := h.device(testclient.Config{Endpoint: "sec", PSKIdentity: "sec", PSKKey: []byte("0123456789abcdef")})
	mustCode(mustRegister(h, c))
	reg, _ := h.srv.Store().ByEndpoint("sec")
	if reg.Peer().Binding() != "U" || !reg.Identity.Secure() {
		t.Fatalf("peer %v %+v", reg.Peer().Binding(), reg.Identity)
	}
	expect(t, "2.04")(h.srv.Write(h.ctx, "sec", p("/1/0/7"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/7"), lwm2m.String("UQ"))}, server.WriteOptions{}))
	expect(t, "2.05")(h.srv.Read(h.ctx, "sec", p("/3/0/0"), server.ReadOptions{}))
	after, _ := h.srv.Store().ByEndpoint("sec")
	if after != reg || after.QueueMode || after.Binding != "U" {
		t.Fatal("binding resource write changed the registration")
	}
	if n := len(c.Requests()); n != 2 {
		t.Fatalf("DTLS client saw %d requests, want 2", n)
	}
}

// Proves: REG-20, ID-02
// Retried Register bursts from one endpoint leave exactly one registration
// (ep is unique on the server), and URN endpoint names are accepted.
func TestRegisterBurstAndURNs(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		c := h.device(testclient.Config{Endpoint: "urn:uuid:3b2c9e4f-1d0a-4f7e-9c11-0a6b5e7d8c90"})
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Register(h.ctx)
		}()
	}
	wg.Wait()
	n := 0
	for _, r := range h.srv.Store().All() {
		if r.Endpoint == "urn:uuid:3b2c9e4f-1d0a-4f7e-9c11-0a6b5e7d8c90" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d registrations for one endpoint", n)
	}
	for _, ep := range []string{"urn:dev:ops:32473-Refrigerator-5002", "urn:dev:os:32473-123456", "urn:gsma:imei:90420156-025763-0", "urn:imei-msisdn:490154203237518-4915123456789", "urn:nai:user@example.com"} {
		c := h.device(testclient.Config{Endpoint: ep})
		mustCode(mustRegister(h, c))
	}
}

// Proves: QM-05
// After sleeping, a client may come back on a new DTLS session from a new
// port with the same PSK identity: its Update is accepted and later
// requests use the new session.
func TestNewSessionUpdate(t *testing.T) {
	h := newHarness(t)
	key := []byte("0123456789abcdef")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "sleepy", PSKIdentity: "sleepy", PSKKey: key}); err != nil {
		t.Fatal(err)
	}
	c := h.device(testclient.Config{Endpoint: "sleepy", PSKIdentity: "sleepy", PSKKey: key, Queue: true})
	mustCode(mustRegister(h, c))
	loc := c.Location()
	_ = c.Close()
	c2 := h.device(testclient.Config{Endpoint: "sleepy", PSKIdentity: "sleepy", PSKKey: key, Queue: true})
	r, err := c2.Raw(h.ctx, codes.POST, "/rd/"+loc, nil, nil, nil)
	mustCode(t, r, err, "2.04")
	expect(t, "2.05")(h.srv.Read(h.ctx, "sleepy", p("/3/0/0"), server.ReadOptions{}))
	if len(c2.Requests()) != 1 {
		t.Fatal("request not sent over the new session")
	}
}

// Proves: OBS-08
// A notification carrying several time-stamped SenML records (stored while
// offline) is delivered with all records and their times.
func TestBatchedNotification(t *testing.T) {
	h := newHarness(t)
	c := h.registered("batch")
	ob, _, err := h.srv.Observe(h.ctx, "batch", p("/3/0/9"), server.ObserveOptions{Accept: fmtPtr(lwm2m.FormatSenMLCBOR)})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := c.LastRequest()
	hist := []lwm2m.Node{
		{Path: p("/3/0/9"), Value: lwm2m.Integer(90), Time: 1700000000, HasTime: true},
		{Path: p("/3/0/9"), Value: lwm2m.Integer(80), Time: 1700000060, HasTime: true},
		{Path: p("/3/0/9"), Value: lwm2m.Integer(70), Time: 1700000120, HasTime: true},
	}
	cd, _ := codec.For(lwm2m.FormatSenMLCBOR)
	body, err := cd.Encode(p("/3/0/9"), hist)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.NotifyRaw(h.ctx, req.Token, lwm2m.FormatSenMLCBOR, body); err != nil {
		t.Fatal(err)
	}
	n := notification(t, h, ob)
	if !lwm2m.NodesEqual(n.Response.Nodes, hist) {
		t.Fatalf("batched notification %s", lwm2m.FormatNodes(n.Response.Nodes))
	}
}

// Proves: CBOR-09
// LwM2M CBOR is used for Write, Create and Write-Composite, accepted for
// Read-Composite, Observe and Notify, but never as a Read-Composite request
// body. Read responses: TestReadEveryFormat; Send: TestSend;
// Bootstrap: bootstrap package tests.
func TestLwM2MCBORUses(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "lwcbor", Version: "1.2"})
	c.AddObject(16)
	mustCode(mustRegister(h, c))
	cb := lwm2m.FormatLwM2MCBOR
	if _, err := h.srv.ReadComposite(h.ctx, "lwcbor", []lwm2m.Path{p("/3/0/0")}, server.CompositeOptions{Format: &cb}); !errors.Is(err, server.ErrBadRequest) {
		t.Fatalf("LwM2M CBOR path list sent: %v", err)
	}
	r := expect(t, "2.05")(h.srv.ReadComposite(h.ctx, "lwcbor", []lwm2m.Path{p("/3/0/0")}, server.CompositeOptions{Accept: &cb}))
	if r.ContentFormat != cb || len(r.Nodes) != 1 {
		t.Fatalf("response %v %v", r.ContentFormat, r.Nodes)
	}
	expect(t, "2.04")(h.srv.WriteComposite(h.ctx, "lwcbor", []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(99))}, &cb))
	expect(t, "2.04")(h.srv.Write(h.ctx, "lwcbor", p("/1/0"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(98))}, server.WriteOptions{Mode: server.PartialUpdate, Format: &cb}))
	if req, _ := c.LastRequest(); *req.Format != cb {
		t.Fatalf("format %v", *req.Format)
	}
	expect(t, "2.01")(h.srv.Create(h.ctx, "lwcbor", p("/16"), []lwm2m.Node{lwm2m.ValueNode(p("/16/0/0/0"), lwm2m.String("x"))}, &cb))
	if req, _ := c.LastRequest(); req.Format == nil || *req.Format != cb || req.Code != codes.POST {
		t.Fatalf("create %+v", req)
	}
	// Observe response and Notify in LwM2M CBOR.
	ob, r, err := h.srv.Observe(h.ctx, "lwcbor", p("/3/0"), server.ObserveOptions{Accept: &cb})
	if err != nil || r.ContentFormat != cb || !lwm2m.NodesEqual(r.Nodes, c.Nodes(p("/3/0"))) {
		t.Fatalf("observe %v %+v", err, r)
	}
	req, _ := c.LastRequest()
	c.Set(p("/3/0/9"), lwm2m.Integer(42))
	if _, err := c.Notify(h.ctx, req.Token); err != nil {
		t.Fatal(err)
	}
	if n := notification(t, h, ob); n.Response.ContentFormat != cb || !lwm2m.NodesEqual(n.Response.Nodes, c.Nodes(p("/3/0"))) {
		t.Fatalf("notify %v %s", n.Response.ContentFormat, lwm2m.FormatNodes(n.Response.Nodes))
	}
}

// Proves: FMT-10
// OMA JSON from a 1.1 client is decoded, but the server never encodes it
// towards a 1.1+ client: a write that every other format fails ends in
// 4.15 without an OMA JSON attempt.
func TestOMAJSONOnlyAccepted(t *testing.T) {
	h := newHarness(t)
	oj := lwm2m.FormatOMAJSON
	c := h.device(testclient.Config{Endpoint: "oj", Version: "1.1", Format: oj, Formats: []lwm2m.ContentFormat{oj}})
	mustCode(mustRegister(h, c))
	r := expect(t, "2.05")(h.srv.Read(h.ctx, "oj", p("/3/0"), server.ReadOptions{}))
	if r.ContentFormat != oj || !lwm2m.NodesEqual(r.Nodes, c.Nodes(p("/3/0"))) {
		t.Fatalf("read %v %v", r.ContentFormat, r.DecodeErr)
	}
	expect(t, "4.15")(h.srv.Write(h.ctx, "oj", p("/1/0"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(5))}, server.WriteOptions{Mode: server.PartialUpdate}))
	for _, req := range c.Requests() {
		if req.Format != nil && *req.Format == oj {
			t.Fatalf("server sent OMA JSON to a 1.1 client: %+v", req)
		}
	}
}

// Proves: DT-03
// A Corelnk value naming LwM2M objects or instances gets the same target
// check as an Objlnk: only registered ones may be written; non-LwM2M
// links are left alone.
func TestCorelnkTargets(t *testing.T) {
	h := newHarness(t)
	c := h.registered("clnk")
	c.Set(p("/22/0/0/0"), lwm2m.Corelnk("</3/0>"))
	if r, err := c.Update(h.ctx, nil, []byte(c.ObjectLinks())); err != nil || r.Code != codes.Changed {
		t.Fatal(r, err)
	}
	w := func(v string) error {
		_, err := h.srv.Write(h.ctx, "clnk", p("/22/0/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/22/0/0/0"), lwm2m.Corelnk(v))}, server.WriteOptions{})
		return err
	}
	for _, ok := range []string{"</3/0>", "</3>,</1/0>", "<coap://example.com/x>"} {
		if err := w(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"</9/0>", "</3/7>", "</3/0>,</42>"} {
		if err := w(bad); !errors.Is(err, server.ErrBadObjlnk) {
			t.Errorf("%q sent: %v", bad, err)
		}
	}
}
