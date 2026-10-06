package senml

import (
	"bytes"
	"encoding/hex"
	"math"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

var p = lwm2m.MustParsePath

func schemaOf(defs map[string]lwm2m.ResourceDef) lwm2m.Schema {
	return lwm2m.SchemaFunc(func(q lwm2m.Path) (lwm2m.ResourceDef, bool) {
		d, ok := defs[q.String()]
		return d, ok
	})
}

func mustEncode(t *testing.T, c Codec, base lwm2m.Path, ns []lwm2m.Node) []byte {
	t.Helper()
	b, err := c.Encode(base, ns)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func roundTrip(t *testing.T, c Codec, base lwm2m.Path, ns []lwm2m.Node) {
	t.Helper()
	b := mustEncode(t, c, base, ns)
	got, err := c.Decode(base, b, nil)
	if err != nil {
		t.Fatalf("decode %s: %v", show(c, b), err)
	}
	if !lwm2m.NodesEqual(got, ns) {
		t.Errorf("round trip of %s:\n%swant\n%s", show(c, b), lwm2m.FormatNodes(got), lwm2m.FormatNodes(ns))
	}
}

// Core §7.5.6 (Tables 7.5.6-2, -5, -7) and FMT-05: the emitted name forms,
// one contiguous run per object instance, Table 7.5.6-1 value fields.
//
// Proves: FMT-05
func TestEmitForms(t *testing.T) {
	cases := []struct {
		base  string
		nodes []lwm2m.Node
		want  string
	}{
		{"/", []lwm2m.Node{ // Table 7.5.6-7: root, full path in n; instances kept contiguous
			lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(95)),
			lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(86400)),
			lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("Open Mobile Alliance")),
		}, `[{"n":"/3/0/0","vs":"Open Mobile Alliance"},{"n":"/3/0/9","v":95},{"n":"/1/0/1","v":86400}]`},
		{"/3/0/0", []lwm2m.Node{ // Table 7.5.6-5: single resource, bn only
			lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("a<b>&")),
		}, `[{"bn":"/3/0/0","vs":"a<b>&"}]`},
		{"/65/0", []lwm2m.Node{ // Table 7.5.6-2 form: bn + relative n; vlo for Objlnk
			lwm2m.ValueNode(p("/65/0/0/1"), lwm2m.Objlnk(66, 1)),
			lwm2m.ValueNode(p("/65/0/0/0"), lwm2m.Objlnk(65535, 65535)),
			lwm2m.ValueNode(p("/65/0/2"), lwm2m.Float(3)),
			lwm2m.ValueNode(p("/65/0/3"), lwm2m.Boolean(true)),
			lwm2m.ValueNode(p("/65/0/4"), lwm2m.Opaque([]byte{0xfb, 0xff})),
			lwm2m.ValueNode(p("/65/0/5"), lwm2m.Unsigned(math.MaxUint64)),
			lwm2m.ValueNode(p("/65/0/6"), lwm2m.Time(1367491215)),
		}, `[{"bn":"/65/0/","n":"0/0","vlo":"65535:65535"},{"n":"0/1","vlo":"66:1"},{"n":"2","v":3.0},{"n":"3","vb":true},` +
			`{"n":"4","vd":"-_8"},{"n":"5","v":18446744073709551615},{"n":"6","v":1367491215}]`},
	}
	for _, c := range cases {
		got := mustEncode(t, JSON, p(c.base), c.nodes)
		if string(got) != c.want {
			t.Errorf("Encode(%s):\n got %s\nwant %s", c.base, got, c.want)
		}
		ns := c.nodes
		if c.base == "/65/0" { // the decoder types numbers via the schema
			s := schemaOf(map[string]lwm2m.ResourceDef{"/65/0/2": {Type: lwm2m.TypeFloat}, "/65/0/6": {Type: lwm2m.TypeTime}})
			got, err := JSON.Decode(p(c.base), got, s)
			if err != nil || !lwm2m.NodesEqual(got, ns) {
				t.Errorf("decode: %v\n%s", err, lwm2m.FormatNodes(got))
			}
			continue
		}
		roundTrip(t, JSON, p(c.base), ns)
	}
}

// Core §7.5.7 / FMT-07: SenML CBOR uses the RFC 8428 §6 integer labels and
// the TEXT key "vlo" for Objlnk; Opaque is a byte string; floats are 64-bit.
//
// Proves: FMT-07
func TestCBORLabels(t *testing.T) {
	got := mustEncode(t, CBOR, p("/65/0/0"), []lwm2m.Node{
		lwm2m.ValueNode(p("/65/0/0/1"), lwm2m.Objlnk(66, 1)),
		lwm2m.ValueNode(p("/65/0/0/2"), lwm2m.Opaque([]byte{1})),
		lwm2m.ValueNode(p("/65/0/0/3"), lwm2m.Float(1.5)),
	})
	want := "83" +
		"a3" + "21" + "682f36352f302f302f" + "00" + "6131" + "63766c6f" + "6436363a31" + // {-2:"/65/0/0/", 0:"1", "vlo":"66:1"}
		"a2" + "00" + "6132" + "08" + "4101" + // {0:"2", 8:h'01'}
		"a2" + "00" + "6133" + "02" + "fb3ff8000000000000" // {0:"3", 2:1.5 (double)}
	if hex.EncodeToString(got) != want {
		t.Errorf("got  %x\nwant %s", got, want)
	}
	// Decoding accepts any key order, text labels and a half float (RFC 8949 §3.3).
	in, _ := hex.DecodeString("81a3" + "02f93e00" + "616e" + "672f332f302f3131" + "62626e" + "60") // {2: 1.5h, "n":"/3/0/11", "bn":""}
	ns, err := CBOR.Decode(lwm2m.Root, in, nil)
	if err != nil || len(ns) != 1 || ns[0].Path != p("/3/0/11") || !ns[0].Value.Equal(lwm2m.Float(1.5)) {
		t.Errorf("decode: %v %v", err, ns)
	}
}

// RFC 8428 §4.5.3/§4.6, Core §7.5.6: bt once on the first timestamped
// record, t relative to it; a node without time resolves to 0 ("now").
func TestTimes(t *testing.T) {
	r := lwm2m.MustParsePath("/3303/0/5700")
	ns := []lwm2m.Node{
		{Path: r, Value: lwm2m.Float(1.5), Time: 1699877805.766, HasTime: true},
		{Path: r, Value: lwm2m.Float(2.5), Time: 1699877805.867, HasTime: true},
		{Path: r, Value: lwm2m.Float(3.5)},
	}
	got := string(mustEncode(t, JSON, r, ns))
	want := `[{"bn":"/3303/0/5700","bt":1699877805.766,"v":1.5},{"v":2.5,"t":0.10100007057189941},{"v":3.5,"t":-1699877805.766}]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	roundTrip(t, JSON, r, ns)
	roundTrip(t, CBOR, r, ns)
}

// Core Table C-2 vs Table 7.5.6-1 (A-24): emit base64url without padding,
// accept the standard alphabet and padding. Core §7.5.6-4: "FFFF:FFFF" is
// the null link. C7: Objlnk with a trailing NUL, under vs or a text v.
func TestTolerances(t *testing.T) {
	s := schemaOf(map[string]lwm2m.ResourceDef{"/3/0/7": {Type: lwm2m.TypeObjlnk}, "/3/0/8": {Type: lwm2m.TypeOpaque}})
	in := `[{"bn":"/3/0/","n":"8","vd":"+/8="},{"n":"7","vlo":"FFFF:FFFF"},{"n":"7","vs":"1:2\u0000"},{"n":"7","v":"3:4"}]`
	got, err := JSON.Decode(p("/3/0"), []byte(in), s)
	want := []lwm2m.Node{
		lwm2m.ValueNode(p("/3/0/8"), lwm2m.Opaque([]byte{0xfb, 0xff})),
		lwm2m.ValueNode(p("/3/0/7"), lwm2m.Objlnk(65535, 65535)),
		lwm2m.ValueNode(p("/3/0/7"), lwm2m.Objlnk(1, 2)),
		lwm2m.ValueNode(p("/3/0/7"), lwm2m.Objlnk(3, 4)),
	}
	if err != nil || !lwm2m.NodesEqual(got, want) {
		t.Errorf("%v\n%s", err, lwm2m.FormatNodes(got))
	}
}

// Invalid input is refused: DT-02 type check, RFC 8428 §4.4 must-understand
// fields, one value per record, null outside ETCH, empty single resource.
func TestRejects(t *testing.T) {
	str := schemaOf(map[string]lwm2m.ResourceDef{"/3/0/0": {Type: lwm2m.TypeString}, "/3/0/9": {Type: lwm2m.TypeInteger}})
	for _, c := range []struct{ base, in string }{
		{"/", `[{"n":"/3/0/0","v":1}]`},             // number for a string resource
		{"/", `[{"n":"/3/0/9","v":1.5}]`},           // fraction for an integer resource
		{"/", `[{"n":"/3/0/9","vs":"1"}]`},          // string for an integer resource
		{"/", `[{"n":"/3/0/0","vs":"x","foo_":1}]`}, // must-understand field
		{"/", `[{"n":"/3/0/0","vs":"x","vb":true}]`},
		{"/", `[{"n":"/3/0/9","v":null}]`}, // null is ETCH only
		{"/", `[{"n":"/3/0","vs":"x"}]`},   // value on an instance
		{"/", `[{"n":"/3/0/0"}]`},          // no value
		{"/3/0/9", ``},                     // empty read of a single resource
		{"/", `[{"n":"/3/0/9","v":18446744073709551616}]`},
	} {
		if ns, err := JSON.Decode(p(c.base), []byte(c.in), str); err == nil {
			t.Errorf("%s accepted: %v", c.in, ns)
		}
	}
	if _, err := JSON.Encode(lwm2m.Root, []lwm2m.Node{lwm2m.ValueNode(p("/3/0/9"), lwm2m.Value{})}); err == nil {
		t.Error("plain SenML encoded a null value")
	}
	if _, err := JSON.Encode(p("/3/0"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(1))}); err == nil {
		t.Error("encoded a node outside the request path")
	}
	// Empty pack: empty instance (Leshan, OMA issue 494).
	ns, err := CBOR.Decode(p("/3/0"), []byte{0x80}, nil)
	if err != nil || len(ns) != 1 || ns[0].Kind != lwm2m.KindEmptyInstance {
		t.Errorf("empty instance: %v %v", err, ns)
	}
}

// T §6.4.1 (GEN-08) and GW §9: names carry the alternate path or gateway
// prefix; the Bootstrap form (no RootPath) has none.
func TestRootPath(t *testing.T) {
	c := JSON.WithRootPath("/lwm2m/")
	ns := []lwm2m.Node{lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("x"))}
	if got := string(mustEncode(t, c, lwm2m.Root, ns)); got != `[{"n":"/lwm2m/3/0/0","vs":"x"}]` {
		t.Errorf("root: %s", got)
	}
	if got := string(mustEncode(t, c, p("/3/0"), ns)); got != `[{"bn":"/lwm2m/3/0/","n":"0","vs":"x"}]` {
		t.Errorf("instance: %s", got)
	}
	roundTrip(t, c, p("/3/0"), ns)
}

// GW §9: a leading non-numeric name segment is an end-device prefix,
// carried in Node.Prefix, after the alternate path when there is one.
//
// Proves: GW-08
func TestNodePrefix(t *testing.T) {
	ns := []lwm2m.Node{
		lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(80)),
		{Prefix: "d01", Path: p("/3303/0/5700"), Value: lwm2m.Float(22.5)},
	}
	c := JSON.WithRootPath("/lwm2m")
	if got := string(mustEncode(t, c, lwm2m.Root, ns)); got != `[{"n":"/lwm2m/3/0/9","v":80},{"n":"/lwm2m/d01/3303/0/5700","v":22.5}]` {
		t.Errorf("root: %s", got)
	}
	roundTrip(t, c, lwm2m.Root, ns)
	roundTrip(t, CBOR, lwm2m.Root, ns)
	one := ns[1:]
	if got := string(mustEncode(t, JSON, p("/3303/0"), one)); got != `[{"bn":"/d01/3303/0/","n":"5700","v":22.5}]` {
		t.Errorf("instance: %s", got)
	}
	roundTrip(t, JSON, p("/3303/0"), one)
	if b, err := JSON.Encode(p("/3303/0"), []lwm2m.Node{lwm2m.ValueNode(p("/3303/0/1"), lwm2m.Integer(1)), ns[1]}); err == nil {
		t.Errorf("two prefixes under one base name: %s", b)
	}
	if ps, err := JSON.DecodePaths([]byte(`[{"n":"/d01/3/0"}]`)); err == nil {
		t.Errorf("prefixed path list accepted: %v", ps)
	}
}

// RFC 8790 §7.1, Core Table 7.5-3: both ETCH formats are registered.
//
// Proves: ETCH-01
func TestETCHRegistered(t *testing.T) {
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatSenMLETCHJSON, lwm2m.FormatSenMLETCHCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR} {
		c, err := codec.For(f)
		if err != nil || c.Format() != f {
			t.Errorf("%v: %v", f, err)
		}
	}
}

// RFC 8790 §3.1 (Fetch Pack): at least one record; each has n and/or bn;
// only n, bn, t, bt, u, bu are allowed and anything else MUST be rejected.
// Names resolve as bn + n. Core §6.3.8 / §6.4.4 use it for Read- and
// Observe-Composite; ETCH-06: names carry the alternate path.
//
// Proves: ETCH-04, ETCH-06
func TestETCHFetchPack(t *testing.T) {
	in := `[{"bn":"/3/0/","n":"0"},{"n":"9","t":-5,"u":"%"},{"bn":"/","n":"1"}]`
	got, err := ETCHJSON.DecodePaths([]byte(in))
	want := []lwm2m.Path{p("/3/0/0"), p("/3/0/9"), p("/1")}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v %v", got, err)
	}
	// Plain SenML path lists refuse a time (Leshan decodePaths).
	if _, err := JSON.DecodePaths([]byte(in)); err == nil {
		t.Error("SenML 110 path list accepted t")
	}
	for _, bad := range []string{
		`[]`,                         // at least one record
		`[{"t":1}]`,                  // neither n nor bn
		`[{"n":"/3/0/0","vs":"x"}]`,  // value field
		`[{"n":"/3/0/0","s":1}]`,     // field outside n, bn, t, bt, u, bu
		`[{"n":"/3/0/0","bver":10}]`, // likewise
		`[{"n":"/3/0/0","v":null}]`,  // null is a value field too
		`[{"n":"/3/0/0/0/0"}]`,       // not an LwM2M path
	} {
		if ps, err := ETCHJSON.DecodePaths([]byte(bad)); err == nil {
			t.Errorf("%s accepted: %v", bad, ps)
		}
	}
	// CBOR Fetch Pack: [{0:"/3/0/0"},{0:"/1/0/1"}].
	b, err := ETCHCBOR.EncodePaths([]lwm2m.Path{p("/3/0/0"), p("/1/0/1")})
	if hex.EncodeToString(b) != "82a100662f332f302f30a100662f312f302f31" || err != nil {
		t.Errorf("CBOR paths: %x %v", b, err)
	}
	// ETCH-06: alternate path in names.
	alt := ETCHCBOR.WithRootPath("/lwm2m")
	b, _ = alt.EncodePaths([]lwm2m.Path{p("/3/0/0")})
	if !bytes.Contains(b, []byte("/lwm2m/3/0/0")) {
		t.Errorf("alternate path missing: %x", b)
	}
	if got, err := alt.DecodePaths(b); err != nil || got[0] != p("/3/0/0") {
		t.Errorf("alternate path decode: %v %v", got, err)
	}
}

// RFC 8790 §3.2 (Patch Pack) and Core §7.5.6: every record carries a value;
// "v": null (CBOR label 2 = 0xf6) removes the matched record, which in
// LwM2M deletes a resource instance; vlo counts as a value (A-11).
// Unknown fields are not an error (RFC 8790 §5). An invalid record fails
// the whole pack (all-or-nothing at the codec layer).
//
// Proves: ETCH-05
func TestETCHPatchPack(t *testing.T) {
	in := `[{"n":"/3/0/14","vs":"+02:00","x":1},{"n":"/34/0/1/2","v":null},{"n":"/65/0/0/0","vlo":"66:0"}]`
	want := []lwm2m.Node{
		lwm2m.ValueNode(p("/3/0/14"), lwm2m.String("+02:00")),
		lwm2m.ValueNode(p("/34/0/1/2"), lwm2m.Value{}),
		lwm2m.ValueNode(p("/65/0/0/0"), lwm2m.Objlnk(66, 0)),
	}
	got, err := ETCHJSON.Decode(lwm2m.Root, []byte(in), nil)
	if err != nil || !lwm2m.NodesEqual(got, want) {
		t.Fatalf("%v\n%s", err, lwm2m.FormatNodes(got))
	}
	if _, err := JSON.Decode(lwm2m.Root, []byte(in), nil); err == nil {
		t.Error("SenML 110 accepted v:null")
	}
	b := mustEncode(t, ETCHJSON, lwm2m.Root, want)
	if string(b) != `[{"n":"/3/0/14","vs":"+02:00"},{"n":"/34/0/1/2","v":null},{"n":"/65/0/0/0","vlo":"66:0"}]` {
		t.Errorf("JSON patch: %s", b)
	}
	b = mustEncode(t, ETCHCBOR, lwm2m.Root, want[1:2])
	if hex.EncodeToString(b) != "81a200692f33342f302f312f3202f6" {
		t.Errorf("CBOR patch: %x", b)
	}
	roundTrip(t, ETCHCBOR, lwm2m.Root, want)
	for _, bad := range []string{
		`[{"n":"/34/0/1","v":null}]`,                        // delete on a resource, not an instance
		`[{"n":"/34/0/1/2"}]`,                               // no value
		`[{"n":"/3/0/14","vs":"a"},{"n":"/3/0/15","v":{}}]`, // one bad record fails all
	} {
		if ns, err := ETCHJSON.Decode(lwm2m.Root, []byte(bad), nil); err == nil {
			t.Errorf("%s accepted: %v", bad, ns)
		}
	}
}
