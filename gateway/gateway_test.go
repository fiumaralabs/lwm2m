package gateway

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/codec/senml"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func vector(t *testing.T, id string) vectors.Vector {
	t.Helper()
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no vector %s", id)
	return vectors.Vector{}
}

func pn(prefix, path string, v lwm2m.Value) Node {
	return Node{Prefix: prefix, Node: lwm2m.ValueNode(lwm2m.MustParsePath(path), v)}
}

func same(t *testing.T, got, want []Node) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	for i := range got {
		if got[i].Prefix != want[i].Prefix || !got[i].Node.Equal(want[i].Node) {
			t.Fatalf("node %d: got %v %v, want %v %v", i, got[i].Prefix, got[i].Node, want[i].Prefix, want[i].Node)
		}
	}
}

// The GW §10 Read-Composite response.
var composite = []Node{
	pn("d01", "/3/0/0", lwm2m.String("Company A")), pn("d01", "/3/0/9", lwm2m.Integer(100)),
	pn("d02", "/3/0/0", lwm2m.String("Company B")), pn("d02", "/3/0/9", lwm2m.Integer(65)),
}

// Proves: GW-08, CBOR-11, GW-07
// LwM2M CBOR with prefixes: the GW §10 composite response encodes byte
// for byte and decodes back (array keys ["d01", 3, 0]). Every form of the
// extended ID grammar decodes: a bare text PREFIX key, a definite array
// with prefix, an indefinite array with prefix; gateway and device nodes
// mix in one payload. Formats without names refuse a second device.
func TestPrefixedLwM2MCBOR(t *testing.T) {
	v := vector(t, "spec-lwcbor-gateway-prefix-composite")
	want, _ := v.Payload()
	got, err := Encode(lwm2m.FormatLwM2MCBOR, Path{}, composite)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("encode %x, want %x (%v)", got, want, err)
	}
	dec, err := Decode(lwm2m.FormatLwM2MCBOR, Path{}, want, nil)
	if err != nil {
		t.Fatal(err)
	}
	same(t, dec, composite)

	for name, h := range map[string]string{
		// {"d01": {3: {0: {0: "A"}}}}
		"prefix key": "a1 63643031 a1 03 a1 00 a1 00 6141",
		// {["d01", 3, 0, 0]: "A"}
		"definite array": "a18463643031030000" + "6141",
		// {[_ "d01", 3, 0, 0]: "A"}
		"indefinite array": "a19f63643031030000ff6141",
	} {
		b, _ := hex.DecodeString(strings.ReplaceAll(h, " ", ""))
		ns, err := Decode(lwm2m.FormatLwM2MCBOR, Path{}, b, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		same(t, ns, []Node{pn("d01", "/3/0/0", lwm2m.String("A"))})
	}

	mixed := []Node{pn("", "/3/0/9", lwm2m.Integer(80)), pn("d01", "/3303/0/5700", lwm2m.Float(22.5)), pn("d02", "/3306/0/5850", lwm2m.Boolean(true))}
	b, err := Encode(lwm2m.FormatLwM2MCBOR, Path{}, mixed)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := Decode(lwm2m.FormatLwM2MCBOR, Path{}, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	same(t, ns, mixed)
	if _, err := Encode(lwm2m.FormatTLV, Path{}, mixed); err == nil {
		t.Fatal("TLV carried prefixes")
	}
	// A nameless format under a prefixed path belongs to that device.
	ns, err = Decode(lwm2m.FormatText, Path{"d01", lwm2m.MustParsePath("/3/0/0")}, []byte("A"), nil)
	if err != nil || ns[0].Prefix != "d01" {
		t.Fatalf("text %v %v", ns, err)
	}
}

// Proves: GW-08
// SenML with prefixes: the GW §10 Read response (bn "/d01/3303/0/")
// decodes to d01 nodes; a single-device payload is written with the
// prefix in bn; a payload mixing the gateway and two devices, with
// timestamps on some records, round-trips without names or times leaking
// across devices, in SenML JSON and CBOR.
func TestPrefixedSenML(t *testing.T) {
	v := vector(t, "spec-senml-json-gateway-read")
	data, _ := v.Payload()
	base := Path{"d01", lwm2m.MustParsePath("/3303/0")}
	ns, err := Decode(lwm2m.FormatSenMLJSON, base, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	same(t, ns, []Node{
		pn("d01", "/3303/0/5700", lwm2m.Float(22.1)), pn("d01", "/3303/0/5601", lwm2m.Float(17.5)),
		pn("d01", "/3303/0/5602", lwm2m.Float(23.9)), pn("d01", "/3303/0/5701", lwm2m.String("Cel")),
	})
	b, err := Encode(lwm2m.FormatSenMLJSON, base, []Node{pn("", "/3303/0/5700", lwm2m.Float(22.1)), pn("", "/3303/0/5701", lwm2m.String("Cel"))})
	if err != nil || !strings.Contains(string(b), `"bn":"/d01/3303/0/"`) {
		t.Fatalf("single device %s %v", b, err)
	}

	timed := func(n Node, ts float64) Node { n.Time, n.HasTime = ts, true; return n }
	mixed := []Node{
		timed(pn("d01", "/3303/0/5700", lwm2m.Float(21.5)), 1700000000),
		pn("", "/3/0/9", lwm2m.Integer(80)),
		timed(pn("d02", "/3303/0/5700", lwm2m.Float(19)), 1700000060),
		pn("d02", "/3306/0/5850", lwm2m.Boolean(true)),
	}
	for _, cf := range []lwm2m.ContentFormat{lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR} {
		b, err := Encode(cf, Path{}, mixed)
		if err != nil {
			t.Fatal(err)
		}
		ns, err := Decode(cf, Path{}, b, nil)
		if err != nil {
			t.Fatal(err)
		}
		same(t, ns, mixed)
	}
	// Base name and base time carried from one device to the next record.
	rs := []senml.Record{
		{BaseName: "/d01/3303/0/", BaseTime: 1700000000, Name: "5700", Field: "v", Value: lwm2m.Float(1)},
		{Name: "5701", Time: 5, Field: "vs", Value: lwm2m.String("Cel")},
		{BaseName: "/d02/3/0/", Name: "9", Time: -1700000000, Field: "v", Value: lwm2m.Integer(5)},
	}
	b, _ = senml.JSON.EncodeRecords(rs)
	ns, err = Decode(lwm2m.FormatSenMLJSON, Path{}, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	same(t, ns, []Node{
		timed(pn("d01", "/3303/0/5700", lwm2m.Float(1)), 1700000000),
		timed(pn("d01", "/3303/0/5701", lwm2m.String("Cel")), 1700000005),
		pn("d02", "/3/0/9", lwm2m.Integer(5)),
	})
}

func gatewayReg(ver string) *server.Registration {
	return &server.Registration{Version: "1.2", Objects: []link.Object{
		{ID: 1, Instances: []uint16{0}}, {ID: 3, Instances: []uint16{0}}, {ID: 5, Instances: []uint16{0}},
		{ID: 25, Version: ver, Instances: []uint16{0, 1}},
	}}
}

// The /25 instances of GW §10 Tables 10.-1 and 10.-2.
func table10(t *testing.T) []lwm2m.Node {
	n := func(p string, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(lwm2m.MustParsePath(p), v) }
	return []lwm2m.Node{
		n("/25/0/0", lwm2m.String("urn:dev:os:32473-101")), n("/25/0/1", lwm2m.String("d01")),
		n("/25/0/3", lwm2m.Corelnk(*vector(t, "spec-lf-gateway-device-objects-d01").Text)),
		n("/25/1/0", lwm2m.String("urn:dev:os:32473-102")), n("/25/1/1", lwm2m.String("d02")),
		n("/25/1/3", lwm2m.Corelnk(*vector(t, "spec-lf-gateway-device-objects-d02").Text)),
	}
}

// Proves: GW-01, GW-02, GW-03
// The /25 model follows its "ver": 2.0 (GW TS) reads device objects from
// resource 3, 1.0 (Core E.12) from the /26 routing entry linked by
// resource 2 (empty Mapping Info = identity). The registry holds one
// device per /25 instance with unique Device ID and Prefix; duplicates
// are rejected. Device objects never come from the Register list (the
// GW §10 list has only /25 instances, a prefixed link there is ignored);
// they come from /25 res 3, and a change of /25 instances in an Update
// asks for a new read.
func TestRegistry(t *testing.T) {
	links, err := link.Parse(*vector(t, "spec-lf-register-gateway").Text + ",</d01/3/0>")
	if err != nil {
		t.Fatal(err)
	}
	lr, err := link.ParseRegistration(links)
	if err != nil || len(lr.Objects) != 4 {
		t.Fatalf("register objects %+v %v", lr.Objects, err)
	}
	reg := gatewayReg("2.0")
	if Version(reg) != "2.0" || Version(gatewayReg("")) != "1.0" {
		t.Fatal("Version")
	}
	if got := Instances(reg); len(got) != 2 {
		t.Fatalf("instances %v", got)
	}
	r, err := ParseDevices(Version(reg), table10(t))
	if err != nil {
		t.Fatal(err)
	}
	d1, ok := r.ByPrefix("d01")
	if !ok || d1.ID != "urn:dev:os:32473-101" || len(d1.Objects) != 3 || d1.Objects[0].Version != "1.2" || len(d1.Objects[2].Instances) != 2 {
		t.Fatalf("d01 %+v", d1)
	}
	d2, _ := r.ByPrefix("d02")
	if d2.Instance != 1 || len(d2.Objects) != 2 || d2.Objects[1].ID != 3306 {
		t.Fatalf("d02 %+v", d2)
	}

	n := func(p string, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(lwm2m.MustParsePath(p), v) }
	v1 := []lwm2m.Node{
		n("/25/0/0", lwm2m.String("urn:dev:a")), n("/25/0/1", lwm2m.String("a")), n("/25/0/2", lwm2m.Objlnk(26, 4)),
		n("/26/4/0", lwm2m.Unsigned(3303)), n("/26/4/1", lwm2m.Corelnk("")),
	}
	r1, err := ParseDevices("1.0", v1)
	if err != nil || len(r1.Devices) != 1 || r1.Devices[0].Objects[0].ID != 3303 || r1.Devices[0].Mapping != "" {
		t.Fatalf("v1.0 %+v %v", r1, err)
	}

	dup := append(table10(t), n("/25/2/0", lwm2m.String("urn:dev:other")), n("/25/2/1", lwm2m.String("d01")))
	if _, err := ParseDevices("2.0", dup); err == nil {
		t.Fatal("duplicate prefix accepted")
	}
	dupID := append(table10(t), n("/25/2/0", lwm2m.String("urn:dev:os:32473-101")), n("/25/2/1", lwm2m.String("d03")))
	if _, err := ParseDevices("2.0", dupID); err == nil {
		t.Fatal("duplicate Device ID accepted")
	}
	if _, err := ParseDevices("2.0", []lwm2m.Node{n("/25/0/0", lwm2m.String("x")), n("/25/0/1", lwm2m.String("42"))}); err == nil {
		t.Fatal("numeric prefix accepted")
	}

	upd := gatewayReg("2.0")
	upd.Objects[3].Instances = []uint16{0}
	if !Changed(reg, upd) || Changed(reg, gatewayReg("2.0")) {
		t.Fatal("Changed")
	}
}

// fakeGW answers requests from a table and records them.
type fakeGW struct {
	reqs []*server.Message
	resp func(*server.Message) *server.Message
}

func (f *fakeGW) exchange(_ context.Context, m *server.Message) (*server.Message, error) {
	f.reqs = append(f.reqs, m)
	if f.resp != nil {
		return f.resp(m), nil
	}
	return &server.Message{Code: codes.Changed}, nil
}

func mustPath(s string) Path {
	p, err := ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

// Proves: GW-05, GW-06, GW-09
// Every simple operation takes an optional prefix, mapped as an alternate
// path: Read of d01 /3303/0 is GET /d01/3303/0 (GW §10), behind the
// gateway's own alternate path /lwm2m/d01/3303/0; Discover, Write (PUT and
// POST), Write-Attributes, Execute, Create, Delete and Observe likewise.
// Without a prefix the path is the gateway's own object. An unknown prefix
// is refused before sending, and the gateway's 4.04 for one is passed on.
// Discover links have no prefix and are read as device paths (one with a
// prefix is stripped). Device objects need no registration or ACL entry:
// the server has full rights. A 1.0 gateway is refused.
func TestPrefixedOperations(t *testing.T) {
	devs, _ := ParseDevices("2.0", table10(t))
	gw := &fakeGW{}
	c := &Client{Exchange: gw.exchange, Version: "1.2", Devices: &devs}
	ctx := context.Background()
	d := mustPath("/d01/3303/0")
	if d.Prefix != "d01" || d.Path != lwm2m.MustParsePath("/3303/0") || d.String() != "/d01/3303/0" {
		t.Fatalf("ParsePath %+v", d)
	}

	gw.resp = func(m *server.Message) *server.Message {
		v := vector(t, "spec-senml-json-gateway-read")
		cf := lwm2m.FormatSenMLJSON
		return &server.Message{Code: codes.Content, Format: &cf, Payload: []byte(*v.Text)}
	}
	r, err := c.Read(ctx, d, nil)
	if err != nil || r.Code != codes.Content || len(r.Nodes) != 4 || r.Nodes[0].Prefix != "d01" {
		t.Fatalf("read %+v %v", r, err)
	}
	if m := gw.reqs[0]; m.Code != codes.GET || m.Path != "/d01/3303/0" || *m.Accept != lwm2m.FormatLwM2MCBOR {
		t.Fatalf("GET %+v", m)
	}
	c.RootPath = "/lwm2m"
	_, _ = c.Read(ctx, d, nil)
	if p := gw.reqs[1].Path; p != "/lwm2m/d01/3303/0" {
		t.Fatalf("alternate path %s", p)
	}
	c.RootPath = ""

	gw.resp = func(m *server.Message) *server.Message {
		lf := lwm2m.FormatLinkFormat
		return &server.Message{Code: codes.Content, Format: &lf, Payload: []byte("</3303>,</3303/0>,</d01/3303/1>,</3303/0/5700>;pmin=10")}
	}
	r, err = c.Discover(ctx, mustPath("/d01/3303"))
	if err != nil || len(r.Links) != 4 || r.Links[1].Path != lwm2m.MustParsePath("/3303/0") || r.Links[2].Path != lwm2m.MustParsePath("/3303/1") {
		t.Fatalf("discover %+v %v", r, err)
	}

	gw.resp, gw.reqs = nil, nil
	val := []Node{pn("", "/3306/0/5850", lwm2m.Boolean(true))} // /3306 is not in the gateway's registration
	ops := []func() (*Response, error){
		func() (*Response, error) { return c.Write(ctx, mustPath("/d02/3306/0"), val, true, nil) },
		func() (*Response, error) { return c.Write(ctx, mustPath("/d02/3306/0"), val, false, nil) },
		func() (*Response, error) {
			return c.WriteAttributes(ctx, mustPath("/d02/3306/0/5850"), []string{"pmin=10"})
		},
		func() (*Response, error) { return c.Execute(ctx, mustPath("/d01/5/0/2"), "") },
		func() (*Response, error) {
			return c.Create(ctx, mustPath("/d01/3303"), []Node{pn("", "/3303/2/5700", lwm2m.Float(1))}, nil)
		},
		func() (*Response, error) { return c.Delete(ctx, mustPath("/d01/3303/1")) },
		func() (*Response, error) { return c.Observe(ctx, mustPath("/d01/3303/0/5700"), nil) },
		func() (*Response, error) { return c.Read(ctx, mustPath("/3/0"), nil) },
	}
	want := []struct {
		code codes.Code
		path string
	}{
		{codes.PUT, "/d02/3306/0"}, {codes.POST, "/d02/3306/0"}, {codes.PUT, "/d02/3306/0/5850"}, {codes.POST, "/d01/5/0/2"},
		{codes.POST, "/d01/3303"}, {codes.DELETE, "/d01/3303/1"}, {codes.GET, "/d01/3303/0/5700"}, {codes.GET, "/3/0"},
	}
	for i, op := range ops {
		if _, err := op(); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
		if m := gw.reqs[i]; m.Code != want[i].code || m.Path != want[i].path {
			t.Fatalf("op %d: %v %s, want %v %s", i, m.Code, m.Path, want[i].code, want[i].path)
		}
	}
	if gw.reqs[6].Observe == nil || *gw.reqs[6].Observe != 0 || gw.reqs[2].Query[0] != "pmin=10" {
		t.Fatal("observe / attributes")
	}
	// The Write body names the device: ["d02", 3306, 0] ... in LwM2M CBOR.
	if ns, err := Decode(lwm2m.FormatLwM2MCBOR, Path{}, gw.reqs[0].Payload, nil); err != nil || ns[0].Prefix != "d02" {
		t.Fatalf("write body %v %v", ns, err)
	}

	n := len(gw.reqs)
	if _, err := c.Read(ctx, mustPath("/d09/3/0"), nil); !errors.Is(err, ErrUnknownPrefix) || len(gw.reqs) != n {
		t.Fatalf("unknown prefix: %v", err)
	}
	c.Devices = nil
	gw.resp = func(*server.Message) *server.Message { return &server.Message{Code: codes.NotFound} }
	if r, err := c.Read(ctx, mustPath("/d09/3/0"), nil); err != nil || r.Code != codes.NotFound {
		t.Fatalf("gateway 4.04: %+v %v", r, err)
	}
	c.Version = "1.0"
	if _, err := c.Read(ctx, d, nil); !errors.Is(err, ErrVersion) {
		t.Fatal("1.0 gateway accepted")
	}
	if _, err := ParsePath("/d01"); err == nil {
		t.Fatal("prefix without object accepted")
	}
}

// Proves: GW-07
// Read-, Observe- and Write-Composite mix devices and the gateway in one
// request: the FETCH body names /d01/3/0/0, /d01/3/0/9, /d02/3/0/0,
// /d02/3/0/9 and /3/0/9, has no Uri-Path, and the GW §10 LwM2M CBOR
// response decodes per device; the iPATCH body carries both prefixes.
func TestComposite(t *testing.T) {
	gw := &fakeGW{resp: func(*server.Message) *server.Message {
		b, _ := vector(t, "spec-lwcbor-gateway-prefix-composite").Payload()
		cf := lwm2m.FormatLwM2MCBOR
		return &server.Message{Code: codes.Content, Format: &cf, Payload: b}
	}}
	c := &Client{Exchange: gw.exchange, Version: "1.2"}
	ctx := context.Background()
	paths := []Path{mustPath("/d01/3/0/0"), mustPath("/d01/3/0/9"), mustPath("/d02/3/0/0"), mustPath("/d02/3/0/9"), mustPath("/3/0/9")}
	r, err := c.ReadComposite(ctx, paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	same(t, r.Nodes, composite)
	m := gw.reqs[0]
	if m.Code != codeFETCH || m.Path != "/" || *m.Format != lwm2m.FormatSenMLCBOR {
		t.Fatalf("FETCH %+v", m)
	}
	rs, _ := senml.CBOR.DecodeRecords(m.Payload)
	if len(rs) != 5 || rs[0].Name != "/d01/3/0/0" || rs[4].Name != "/3/0/9" {
		t.Fatalf("body %+v", rs)
	}
	if _, err := c.ObserveComposite(ctx, paths, nil); err != nil || gw.reqs[1].Observe == nil {
		t.Fatalf("observe-composite %v", err)
	}
	gw.resp = nil
	w := []Node{pn("d01", "/3303/0/5750", lwm2m.String("a")), pn("d02", "/3306/0/5850", lwm2m.Boolean(false))}
	if _, err := c.WriteComposite(ctx, w, nil); err != nil {
		t.Fatal(err)
	}
	if m := gw.reqs[2]; m.Code != codeIPATCH || m.Path != "/" {
		t.Fatalf("iPATCH %+v", m)
	}
	ns, err := Decode(lwm2m.FormatLwM2MCBOR, Path{}, gw.reqs[2].Payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	same(t, ns, w)
}

// Proves: SEND-03
// A gateway Send may report Device Object instances that were never
// registered, prefixed in the names; gateway objects must still be
// registered, and an unknown prefix is refused.
func TestSend(t *testing.T) {
	devs, _ := ParseDevices("2.0", table10(t))
	reg := gatewayReg("2.0")
	nodes := []Node{pn("", "/3/0/9", lwm2m.Integer(80)), pn("d01", "/3303/0/5700", lwm2m.Float(22.5)), pn("d02", "/3306/0/5850", lwm2m.Boolean(true))}
	for _, cf := range []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatLwM2MCBOR} {
		b, err := Encode(cf, Path{}, nodes)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(cf, Path{}, b, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := SendStatus(reg, devs, got); err != nil {
			t.Fatalf("%v: %v", cf, err)
		}
	}
	if SendStatus(reg, devs, []Node{pn("", "/3303/0/5700", lwm2m.Float(1))}) == nil {
		t.Fatal("unregistered gateway object accepted")
	}
	if !errors.Is(SendStatus(reg, devs, []Node{pn("d07", "/3/0/9", lwm2m.Integer(1))}), ErrUnknownPrefix) {
		t.Fatal("unknown prefix accepted")
	}
}

// Proves: GW-04
// Bootstrap never targets device objects: a prefixed Bootstrap-Discover,
// -Read, -Write or -Delete target, or Bootstrap Information (including a
// Bootstrap-Pack) with a device node, is refused; gateway objects pass.
func TestBootstrapExcludesDeviceObjects(t *testing.T) {
	if err := CheckBootstrap([]Path{mustPath("/1/0"), {}}, []Node{pn("", "/1/0/1", lwm2m.Integer(60))}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(CheckBootstrap([]Path{mustPath("/d01/3/0")}, nil), ErrBootstrap) {
		t.Fatal("device target accepted")
	}
	pack, _ := Encode(lwm2m.FormatSenMLCBOR, Path{}, []Node{pn("", "/1/0/1", lwm2m.Integer(60)), pn("d01", "/3/0/9", lwm2m.Integer(1))})
	ns, _ := Decode(lwm2m.FormatSenMLCBOR, Path{}, pack, nil)
	if !errors.Is(CheckBootstrap(nil, ns), ErrBootstrap) {
		t.Fatal("device object in a Bootstrap-Pack accepted")
	}
}
