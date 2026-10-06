package server

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: DM-01, DM-02, FMT-01, FMT-02, FMT-03, CBOR-09, CBOR-10, GEN-03
// Read works whatever data format the client prefers: the server sends no
// Accept unless asked (the client then picks, C §7.5) and decodes every
// format the spec defines. All requests are Confirmable. An explicit
// Accept is sent as given, and a client's 4.06 is surfaced.
func TestReadEveryFormat(t *testing.T) {
	h := newHarness(t)
	for _, cf := range []lwm2m.ContentFormat{
		lwm2m.FormatTLV, lwm2m.FormatOMAJSON, lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR,
	} {
		ep := "fmt-" + cf.String()
		c := h.device(testclient.Config{Endpoint: ep, Format: cf})
		mustCode(mustRegister(h, c))
		r := expect(t, "2.05")(h.srv.Read(h.ctx, ep, p("/3/0"), ReadOptions{}))
		if r.ContentFormat != cf || r.DecodeErr != nil || !lwm2m.NodesEqual(r.Nodes, c.Nodes(p("/3/0"))) {
			t.Fatalf("%v: decoded %v (err %v), want\n%s", cf, lwm2m.FormatNodes(r.Nodes), r.DecodeErr, lwm2m.FormatNodes(c.Nodes(p("/3/0"))))
		}
		req, _ := c.LastRequest()
		if req.Accept != nil || req.Type != message.Confirmable || req.Code != codes.GET {
			t.Fatalf("%v: request %+v", cf, req)
		}
	}
	c := h.registered("single")
	for path, want := range map[string]lwm2m.ContentFormat{"/3/0/0": lwm2m.FormatText, "/3/0/11/0": lwm2m.FormatText} {
		r := expect(t, "2.05")(h.srv.Read(h.ctx, "single", p(path), ReadOptions{}))
		if r.ContentFormat != want || len(r.Nodes) != 1 || !r.Nodes[0].Value.Equal(must(c.Get(p(path)))) {
			t.Fatalf("%s: %v %v", path, r.ContentFormat, r.Nodes)
		}
	}
	for _, acc := range []lwm2m.ContentFormat{lwm2m.FormatCBOR, lwm2m.FormatOpaque} {
		c.Set(p("/5/0/0"), lwm2m.Opaque([]byte{1, 2, 3}))
		r := expect(t, "2.05")(h.srv.Read(h.ctx, "single", p("/5/0/0"), ReadOptions{Accept: &acc}))
		req, _ := c.LastRequest()
		if req.Accept == nil || *req.Accept != acc || r.ContentFormat != acc || len(r.Nodes) != 1 {
			t.Fatalf("accept %v: %+v %v", acc, req, r.Nodes)
		}
	}
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.NotAcceptable, nil, nil, r.Accept != nil
	})
	acc := lwm2m.FormatTLV
	expect(t, "4.06")(h.srv.Read(h.ctx, "single", p("/3/0"), ReadOptions{Accept: &acc}))
	c.SetOverride(nil)
	// An object without instances is an empty 2.05 (DM-02).
	c.AddObject(6)
	r := expect(t, "2.05")(h.srv.Read(h.ctx, "single", p("/6"), ReadOptions{}))
	if len(r.Nodes) != 0 {
		t.Fatalf("nodes %v", r.Nodes)
	}
}

// expect returns a checker for a downlink result with the given code.
func expect(t *testing.T, want string) func(*Response, error) *Response {
	return func(r *Response, err error) *Response {
		t.Helper()
		return mustResp(t, r, err, want)
	}
}

func must[T any](v T, ok bool) T {
	if !ok {
		panic("missing")
	}
	return v
}

// Proves: DM-03, GEN-02
// A Read reply carrying resources the server's model doesn't know is not
// an error: they are decoded and returned (C §6.3.1).
func TestReadUnknownResources(t *testing.T) {
	h := newHarness(t)
	c := h.registered("extra")
	c.Set(p("/3/0/9999"), lwm2m.String("vendor"))
	c.Set(p("/30000/0/1"), lwm2m.Integer(7))
	r := expect(t, "2.05")(h.srv.Read(h.ctx, "extra", p("/3/0"), ReadOptions{}))
	if r.DecodeErr != nil || !slices.ContainsFunc(r.Nodes, func(n lwm2m.Node) bool { return n.Path == p("/3/0/9999") }) {
		t.Fatalf("unknown resource lost: %v %v", r.DecodeErr, r.Nodes)
	}
}

// Proves: GEN-09, DM-11, SEC-21
// The server refuses to send paths the client must reject: operations on
// /0, /21 and /23 outside bootstrap, Execute on anything but a resource,
// Delete on anything but an instance, Write on an object. Nothing is sent.
func TestServerValidatesTargets(t *testing.T) {
	h := newHarness(t)
	c := h.registered("val")
	ctx := h.ctx
	checks := []error{
		second(h.srv.Read(ctx, "val", p("/0/0"), ReadOptions{})),
		second(h.srv.Read(ctx, "val", p("/21"), ReadOptions{})),
		second(h.srv.Read(ctx, "val", p("/23/0"), ReadOptions{})),
		second(h.srv.Read(ctx, "val", lwm2m.Root, ReadOptions{})),
		second(h.srv.Execute(ctx, "val", p("/3/0"), "")),
		second(h.srv.Execute(ctx, "val", p("/3/0/4/0"), "")),
		second(h.srv.Delete(ctx, "val", p("/3/0/1"))),
		second(h.srv.Delete(ctx, "val", p("/3/0"))),
		second(h.srv.Write(ctx, "val", p("/3"), nil, WriteOptions{})),
		second(h.srv.Write(ctx, "val", p("/3/0/13/0"), nil, WriteOptions{Mode: PartialUpdate})),
		second(h.srv.WriteAttributes(ctx, "val", lwm2m.Root, []string{"pmin=1"})),
		second(h.srv.ReadComposite(ctx, "val", []lwm2m.Path{p("/0/0")}, CompositeOptions{})),
	}
	for i, err := range checks {
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("check %d: err %v, want ErrBadRequest", i, err)
		}
	}
	if n := len(c.Requests()); n != 0 {
		t.Fatalf("%d invalid requests reached the client", n)
	}
	// A client 4.01 is surfaced (DM-14).
	c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.Unauthorized, nil, nil, true
	})
	expect(t, "4.01")(h.srv.Read(ctx, "val", p("/3/0"), ReadOptions{}))
}

func second[A, B any](_ A, b B) B { return b }

// Proves: DM-05, DM-20, DM-06
// Replace is PUT and Partial Update is POST, both with a Content-Format.
// Single values go as plain text; multi-value payloads use a format the
// client takes, retrying on 4.15 and remembering the one that worked.
// Values that don't fit the client's model are refused before sending.
func TestWrite(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "w", Formats: []lwm2m.ContentFormat{lwm2m.FormatTLV}})
	mustCode(mustRegister(h, c))
	expect(t, "2.04")(h.srv.Write(h.ctx, "w", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(300))}, WriteOptions{}))
	req, _ := c.LastRequest()
	if req.Code != codes.PUT || req.Format == nil || *req.Format != lwm2m.FormatText || string(req.Body) != "300" {
		t.Fatalf("single write %+v", req)
	}
	nodes := []lwm2m.Node{
		lwm2m.ValueNode(p("/1/0/2"), lwm2m.Integer(5)),
		lwm2m.ValueNode(p("/1/0/3"), lwm2m.Integer(60)),
	}
	expect(t, "2.04")(h.srv.Write(h.ctx, "w", p("/1/0"), nodes, WriteOptions{Mode: PartialUpdate}))
	reqs := c.Requests()
	var formats []lwm2m.ContentFormat
	for _, r := range reqs[1:] {
		formats = append(formats, *r.Format)
		if r.Code != codes.POST {
			t.Fatalf("partial update sent %v", r.Code)
		}
	}
	if formats[len(formats)-1] != lwm2m.FormatTLV || len(formats) < 2 {
		t.Fatalf("negotiation %v", formats)
	}
	if v, _ := c.Get(p("/1/0/1")); !v.Equal(lwm2m.Integer(300)) {
		t.Fatal("partial update removed other resources")
	}
	before := len(c.Requests())
	expect(t, "2.04")(h.srv.Write(h.ctx, "w", p("/1/0"), nodes, WriteOptions{Mode: PartialUpdate}))
	if got := c.Requests()[before:]; len(got) != 1 || *got[0].Format != lwm2m.FormatTLV {
		t.Fatalf("learned format not reused: %+v", got)
	}
	// Replace of a multi-instance resource replaces the whole array (DM-20).
	c.Set(p("/16/0/0/0"), lwm2m.String("a"))
	c.Set(p("/16/0/0/1"), lwm2m.String("b"))
	if r, err := c.Update(h.ctx, nil, []byte(c.ObjectLinks())); err != nil || r.Code != codes.Changed {
		t.Fatal(r, err)
	}
	expect(t, "2.04")(h.srv.Write(h.ctx, "w", p("/16/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/16/0/0/0"), lwm2m.String("z"))}, WriteOptions{}))
	if req, _ := c.LastRequest(); req.Code != codes.PUT {
		t.Fatalf("replace sent %v", req.Code)
	}
	if _, ok := c.Get(p("/16/0/0/1")); ok {
		t.Fatal("replace kept an old resource instance")
	}
	// Model validation (DM-06): wrong type, read-only resource.
	if _, err := h.srv.Write(h.ctx, "w", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.String("x"))}, WriteOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("wrong type sent: %v", err)
	}
	if _, err := h.srv.Write(h.ctx, "w", p("/3/0/9"), []lwm2m.Node{lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(1))}, WriteOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("read-only write sent: %v", err)
	}
}

// Proves: DM-08
// Execute is POST on /o/i/r with optional text/plain arguments.
func TestExecute(t *testing.T) {
	h := newHarness(t)
	c := h.registered("x")
	expect(t, "2.04")(h.srv.Execute(h.ctx, "x", p("/3/0/4"), ""))
	expect(t, "2.04")(h.srv.Execute(h.ctx, "x", p("/3/0/4"), "0='a',1"))
	ex := c.Executed()
	if len(ex) != 2 || ex[0].Path != "/3/0/4" || ex[0].Body != nil || string(ex[1].Body) != "0='a',1" || *ex[1].Format != lwm2m.FormatText {
		t.Fatalf("executed %+v", ex)
	}
}

// Proves: DM-09, DM-10
// Create is POST on a registered object with the instance in the payload;
// Delete is DELETE on an instance, never /3/0. Missing mandatory resources
// are refused before sending.
func TestCreateDelete(t *testing.T) {
	h := newHarness(t)
	c := h.registered("cd")
	c.AddObject(16)
	expect(t, "2.05")(h.srv.Read(h.ctx, "cd", p("/3/0/0"), ReadOptions{})) // keep registration fresh
	if _, err := c.Update(h.ctx, nil, []byte(c.ObjectLinks())); err != nil {
		t.Fatal(err)
	}
	r := expect(t, "2.01")(h.srv.Create(h.ctx, "cd", p("/16"), []lwm2m.Node{
		lwm2m.ValueNode(p("/16/1/0/0"), lwm2m.String("Host Device ID #2")),
	}, nil))
	if !slices.Equal(r.Location, []string{"16", "1"}) {
		t.Fatalf("location %v", r.Location)
	}
	if v, ok := c.Get(p("/16/1/0/0")); !ok || !v.Equal(lwm2m.String("Host Device ID #2")) {
		t.Fatal("instance not created")
	}
	if _, err := h.srv.Create(h.ctx, "cd", p("/9"), nil, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("create on unregistered object: %v", err)
	}
	if _, err := h.srv.Create(h.ctx, "cd", p("/16"), []lwm2m.Node{lwm2m.ValueNode(p("/16/2/9"), lwm2m.String("x"))}, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("create without mandatory resources: %v", err)
	}
	expect(t, "2.02")(h.srv.Delete(h.ctx, "cd", p("/16/1")))
	if _, ok := c.Get(p("/16/1/0/0")); ok {
		t.Fatal("not deleted")
	}
	if _, err := h.srv.Delete(h.ctx, "cd", p("/3/0")); !errors.Is(err, ErrBadRequest) {
		t.Fatal("/3/0 delete sent")
	}
}

// Proves: DM-12, ETCH-02, ETCH-03, ETCH-07
// Read-Composite is FETCH on / with a path list and no Uri-Path or
// Uri-Query, in SenML or SenML-ETCH; missing paths are simply absent.
// Write-Composite is iPATCH on /. A malformed pack is 4.00 at the client
// and surfaced as such.
func TestComposite(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "comp", Version: "1.2"})
	mustCode(mustRegister(h, c))
	for _, cf := range []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLETCHCBOR, lwm2m.FormatSenMLETCHJSON} {
		f := cf
		r := expect(t, "2.05")(h.srv.ReadComposite(h.ctx, "comp", []lwm2m.Path{p("/3/0/0"), p("/1/0/1"), p("/3/0/5")}, CompositeOptions{Format: &f}))
		req, _ := c.LastRequest()
		if req.Code != codeFETCH || req.Path != "/" || len(req.Queries) != 0 || *req.Format != cf {
			t.Fatalf("%v: request %+v", cf, req)
		}
		if len(r.Nodes) != 2 {
			t.Fatalf("%v: nodes %v", cf, r.Nodes)
		}
	}
	expect(t, "2.04")(h.srv.WriteComposite(h.ctx, "comp", []lwm2m.Node{
		lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(120)),
		lwm2m.ValueNode(p("/1/0/6"), lwm2m.Boolean(true)),
	}, nil))
	req, _ := c.LastRequest()
	if req.Code != codeIPATCH || req.Path != "/" || len(req.Queries) != 0 {
		t.Fatalf("iPATCH %+v", req)
	}
	if v, _ := c.Get(p("/1/0/6")); !v.Equal(lwm2m.Boolean(true)) {
		t.Fatal("write-composite not applied")
	}
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.BadRequest, nil, nil, r.Code == codeIPATCH
	})
	expect(t, "4.00")(h.srv.WriteComposite(h.ctx, "comp", []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(1))}, nil))
}

// Proves: GEN-08
// With an alternate path (rt="oma.lwm2m") every DM request is prefixed.
func TestAlternatePath(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "alt"})
	r, err := c.RegisterRaw(h.ctx, c.RegisterQuery(), []byte(`</lwm2m>;rt="oma.lwm2m",</lwm2m/1/0>,</lwm2m/3/0>`), true)
	mustCode(t, r, err, "2.01")
	reg, _ := h.srv.Store().ByEndpoint("alt")
	if reg.RootPath != "/lwm2m" || !reg.HasInstance(3, 0) {
		t.Fatalf("registration %+v", reg)
	}
	_, _ = h.srv.Read(h.ctx, "alt", p("/3/0/0"), ReadOptions{})
	_, _ = h.srv.ReadComposite(h.ctx, "alt", []lwm2m.Path{p("/3/0/0")}, CompositeOptions{})
	reqs := c.Requests()
	if len(reqs) != 2 || reqs[0].Path != "/lwm2m/3/0/0" || reqs[1].Path != "/lwm2m" {
		t.Fatalf("paths %+v", reqs)
	}
}

// Proves: DM-04
// Discover sends GET with Accept link-format and, for 1.2, a depth query.
func TestDiscoverRequest(t *testing.T) {
	h := newHarness(t)
	c := h.registered("disc")
	r := expect(t, "2.05")(h.srv.Discover(h.ctx, "disc", p("/3/0"), nil))
	if !strings.Contains(string(r.Payload), "</3/0/11>;dim=1") {
		t.Fatalf("payload %q", r.Payload)
	}
	d := 1
	_, _ = h.srv.Discover(h.ctx, "disc", p("/3"), &d)
	req, _ := c.LastRequest()
	if *req.Accept != lwm2m.FormatLinkFormat || !slices.Equal(req.Queries, []string{"depth=1"}) {
		t.Fatalf("request %+v", req)
	}
	bad := 4
	if _, err := h.srv.Discover(h.ctx, "disc", p("/3"), &bad); !errors.Is(err, ErrBadRequest) {
		t.Fatal("depth 4 sent")
	}
}

// Proves: DM-07
// Write-Attributes is PUT with the attributes as Uri-Query and no payload.
func TestWriteAttributesRequest(t *testing.T) {
	h := newHarness(t)
	c := h.registered("wa")
	expect(t, "2.04")(h.srv.WriteAttributes(h.ctx, "wa", p("/3/0/9"), []string{"pmin=10", "pmax=60", "gt"}))
	req, _ := c.LastRequest()
	if req.Code != codes.PUT || req.Body != nil || req.Format != nil || !slices.Equal(req.Queries, []string{"pmin=10", "pmax=60", "gt"}) {
		t.Fatalf("request %+v", req)
	}
}
