package tlv_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/codec/tlv"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// nonCanonical lists vectors whose source encoder makes a different (still
// spec-legal) choice than ours. They are proven by decode(payload)==expected
// and decode(Encode(expected))==expected instead of a byte-exact encode.
var nonCanonical = map[string]string{}

// errorLayer says which layer a header-less "error" vector targets: "raw"
// for ParseRaw, otherwise the type passed to DecodeValue.
var errorLayer = map[string]string{
	"tlv-leshan-raw-error-broken":           "raw",
	"tlv-leshan-raw-error-truncated-value":  "raw",
	"tlv-leshan-value-error-objlnk-3-bytes": "objlnk",
}

type rawJSON struct {
	Type     string    `json:"type"`
	ID       uint16    `json:"id"`
	ValueHex string    `json:"value_hex"`
	Children []rawJSON `json:"children"`
}

func toRecords(t *testing.T, js []rawJSON) []tlv.Record {
	var out []tlv.Record
	for _, j := range js {
		var r tlv.Record
		switch j.Type {
		case "OBJECT_INSTANCE":
			r.Type = tlv.ObjectInstance
		case "RESOURCE_INSTANCE":
			r.Type = tlv.ResourceInstance
		case "MULTIPLE_RESOURCE":
			r.Type = tlv.MultipleResource
		case "RESOURCE_VALUE":
			r.Type = tlv.ResourceValue
		default:
			t.Fatalf("unknown record type %q", j.Type)
		}
		r.ID = j.ID
		if j.Children != nil {
			r.Children = toRecords(t, j.Children)
		} else {
			b, err := hex.DecodeString(j.ValueHex)
			if err != nil {
				t.Fatal(err)
			}
			r.Value = b
		}
		out = append(out, r)
	}
	return out
}

func recordsEqual(a, b []tlv.Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].ID != b[i].ID || !bytes.Equal(a[i].Value, b[i].Value) || !recordsEqual(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

func loadTLV(t *testing.T) []vectors.Vector {
	vs, err := vectors.Load("tlv")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range spec {
		if v.ContentFormat != nil && *v.ContentFormat == int(lwm2m.FormatTLV) {
			vs = append(vs, v)
		}
	}
	return vs
}

// TestVectors runs every TLV vector in tlv.json and the 11542 entries of
// spec-examples.json (Core §7.4.4, 1.2.2 §7.5.5, Table C-2).
//
// Proves: FMT-04
func TestVectors(t *testing.T) {
	c, err := codec.For(lwm2m.FormatTLV)
	if err != nil {
		t.Fatal(err)
	}
	vs := loadTLV(t)
	ids := map[string]bool{}
	for _, v := range vs {
		ids[v.ID] = true
	}
	for id := range nonCanonical {
		if !ids[id] {
			t.Errorf("nonCanonical lists unknown vector %s", id)
		}
	}
	for id := range errorLayer {
		if !ids[id] {
			t.Errorf("errorLayer lists unknown vector %s", id)
		}
	}
	for _, v := range vs {
		t.Run(v.ID, func(t *testing.T) {
			payload, err := v.Payload()
			if err != nil {
				t.Fatal(err)
			}
			switch shape := v.ExpectedShape(); shape {
			case "error":
				if layer, ok := errorLayer[v.ID]; ok {
					if layer == "raw" {
						_, err = tlv.ParseRaw(payload)
					} else {
						typ, _ := lwm2m.ParseType(layer)
						_, err = tlv.DecodeValue(typ, payload)
					}
				} else {
					base, perr := v.BasePath()
					if perr != nil {
						t.Fatal(perr)
					}
					_, err = c.Decode(base, payload, nil)
				}
				if err == nil {
					t.Fatal("decode succeeded, want error")
				}
			case "tlv":
				var js []rawJSON
				if err := v.ExpectedObject("tlv", &js); err != nil {
					t.Fatal(err)
				}
				want := toRecords(t, js)
				if v.Decodes() {
					got, err := tlv.ParseRaw(payload)
					if err != nil {
						t.Fatal(err)
					}
					if !recordsEqual(got, want) {
						t.Fatalf("ParseRaw = %+v, want %+v", got, want)
					}
				}
				if v.Encodes() {
					got, err := tlv.EncodeRaw(want)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						t.Fatalf("EncodeRaw = %x, want %x", got, payload)
					}
				}
			case "tlv_value":
				var j vectors.JSONNode
				if err := v.ExpectedObject("tlv_value", &j); err != nil {
					t.Fatal(err)
				}
				n, err := j.Node()
				if err != nil {
					t.Fatal(err)
				}
				if v.Decodes() {
					got, err := tlv.DecodeValue(n.Value.Type, payload)
					if err != nil {
						t.Fatal(err)
					}
					if !got.Equal(n.Value) {
						t.Fatalf("DecodeValue = %v, want %v", got, n.Value)
					}
				}
				if v.Encodes() {
					got, err := tlv.EncodeValue(n.Value)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						t.Fatalf("EncodeValue = %x, want %x", got, payload)
					}
				}
			case "nodes":
				want, err := v.Nodes()
				if err != nil {
					t.Fatal(err)
				}
				base, err := v.BasePath()
				if err != nil {
					t.Fatal(err)
				}
				schema := vectors.Schema(want)
				decode := func(b []byte) {
					t.Helper()
					got, err := c.Decode(base, b, schema)
					if err != nil {
						t.Fatalf("decode %x: %v", b, err)
					}
					if !lwm2m.NodesEqual(got, want) {
						t.Fatalf("decode %x:\n%swant:\n%s", b, lwm2m.FormatNodes(got), lwm2m.FormatNodes(want))
					}
				}
				_, nc := nonCanonical[v.ID]
				if v.Decodes() || nc {
					decode(payload)
				}
				if v.Encodes() {
					got, err := c.Encode(base, want)
					if err != nil {
						t.Fatal(err)
					}
					if nc {
						decode(got)
					} else if !bytes.Equal(got, payload) {
						t.Fatalf("encode = %x\n   want %x", got, payload)
					}
				}
			default:
				t.Fatalf("unhandled expected shape %q", shape)
			}
		})
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

func p(s string) lwm2m.Path { return lwm2m.MustParsePath(s) }

func schema(defs map[string]lwm2m.ResourceDef) lwm2m.Schema {
	return lwm2m.SchemaFunc(func(q lwm2m.Path) (lwm2m.ResourceDef, bool) {
		d, ok := defs[q.String()]
		return d, ok
	})
}

// TestTypeByte checks the type-byte layout of Table 7.5.5-1 (1.2.2 §7.5.5):
// 16-bit IDs (bit 5), 8/16/24-bit length fields (bits 4-3), and that bits
// 2-0 are ignored when a length field is present.
//
// Proves: FMT-04
func TestTypeByte(t *testing.T) {
	cases := []struct {
		rec tlv.Record
		hex string
	}{
		{tlv.Record{Type: tlv.ResourceValue, ID: 1, Value: make([]byte, 7)}, "c701" + strings.Repeat("00", 7)},
		{tlv.Record{Type: tlv.ResourceValue, ID: 1, Value: make([]byte, 8)}, "c80108" + strings.Repeat("00", 8)},
		{tlv.Record{Type: tlv.ResourceValue, ID: 256, Value: make([]byte, 256)}, "f0010001 00" + strings.Repeat("00", 256)},
		{tlv.Record{Type: tlv.ResourceValue, ID: 2, Value: make([]byte, 65536)}, "d802010000" + strings.Repeat("00", 65536)},
	}
	for _, c := range cases {
		want := mustHex(c.hex)
		got, err := tlv.EncodeRaw([]tlv.Record{c.rec})
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("EncodeRaw(id %d, %d bytes) = %x..., %v", c.rec.ID, len(c.rec.Value), got[:min(len(got), 6)], err)
		}
		back, err := tlv.ParseRaw(want)
		if err != nil || len(back) != 1 || back[0].ID != c.rec.ID || len(back[0].Value) != len(c.rec.Value) {
			t.Errorf("ParseRaw(%x...) = %v", want[:6], err)
		}
	}
	// 0xcf: 8-bit length field present, bits 2-0 = 7 must be ignored.
	got, err := tlv.ParseRaw(mustHex("cf0102 aabb"))
	if err != nil || !bytes.Equal(got[0].Value, []byte{0xaa, 0xbb}) {
		t.Errorf("bits 2-0 not ignored: %v %v", got, err)
	}
	// 24-bit length limit (16.7 MB).
	if _, err := tlv.EncodeRaw([]tlv.Record{{Type: tlv.ResourceValue, Value: make([]byte, 1<<24)}}); err == nil {
		t.Error("value of 2^24 bytes encoded")
	}
	// Nesting: a resource instance cannot sit directly in an object instance,
	// and a multiple resource cannot hold a resource TLV.
	for _, h := range []string{"0300 410001", "8303 c10001"} {
		if _, err := tlv.ParseRaw(mustHex(h)); err == nil {
			t.Errorf("ParseRaw(%s) accepted invalid nesting", h)
		}
	}
}

// TestValueEncodings covers Table C-2 rows no vector reaches: Unsigned
// minimal length, Float 4/8 bytes, boolean strictness, integer lengths.
//
// Proves: FMT-04
func TestValueEncodings(t *testing.T) {
	enc := []struct {
		v   lwm2m.Value
		hex string
	}{
		{lwm2m.Unsigned(255), "ff"},
		{lwm2m.Unsigned(256), "0100"},
		{lwm2m.Unsigned(65536), "00010000"},
		{lwm2m.Unsigned(1 << 63), "8000000000000000"},
		{lwm2m.Integer(128), "0080"},
		{lwm2m.Integer(-129), "ff7f"},
		{lwm2m.Float(1.5), "3ff8000000000000"},
		{lwm2m.Value{Type: lwm2m.TypeFloat, Float: 1.5, Float32: true}, "3fc00000"},
		{lwm2m.Corelnk("</1>"), "3c2f313e"},
		{lwm2m.Opaque(nil), ""},
	}
	for _, c := range enc {
		got, err := tlv.EncodeValue(c.v)
		if err != nil || hex.EncodeToString(got) != c.hex {
			t.Errorf("EncodeValue(%v) = %x, %v; want %s", c.v, got, err, c.hex)
		}
		back, err := tlv.DecodeValue(c.v.Type, got)
		if err != nil || !back.Equal(c.v) {
			t.Errorf("DecodeValue(%v, %x) = %v, %v", c.v.Type, got, back, err)
		}
	}
	if v, err := tlv.DecodeValue(lwm2m.TypeFloat, mustHex("3fc00000")); err != nil || !v.Float32 {
		t.Errorf("4-byte float should set Float32: %v %v", v, err)
	}
	bad := []struct {
		t   lwm2m.Type
		hex string
	}{
		{lwm2m.TypeInteger, ""}, {lwm2m.TypeInteger, "000000"}, {lwm2m.TypeTime, "0000000000"},
		{lwm2m.TypeUnsigned, "000000"}, {lwm2m.TypeFloat, "0000"}, {lwm2m.TypeFloat, "000000000000"},
		{lwm2m.TypeBoolean, ""}, {lwm2m.TypeBoolean, "0001"}, {lwm2m.TypeBoolean, "02"},
		{lwm2m.TypeObjlnk, "0000000000"}, {lwm2m.TypeString, "ff"}, {lwm2m.TypeNone, "00"},
	}
	for _, c := range bad {
		if v, err := tlv.DecodeValue(c.t, mustHex(c.hex)); err == nil {
			t.Errorf("DecodeValue(%v, %s) = %v, want error", c.t, c.hex, v)
		}
	}
}

// TestBasePathRules covers the Object Instance and Multiple Resource TLV
// rules of 1.2.2 §7.5.5: OI wrapping at an object path, MR for 0..n
// instances, resource-instance paths, and the tolerances listed in the
// package doc.
//
// Proves: FMT-04
func TestBasePathRules(t *testing.T) {
	c := tlv.Codec{}
	s := schema(map[string]lwm2m.ResourceDef{
		"/3/0/0": {Type: lwm2m.TypeString}, "/3/0/6": {Type: lwm2m.TypeInteger, Multiple: true},
		"/3/1/0": {Type: lwm2m.TypeString},
	})
	enc := []struct {
		base  string
		nodes []lwm2m.Node
		hex   string
	}{
		// MR TLV even for a single instance at a resource path.
		{"/3/0/6", []lwm2m.Node{lwm2m.ValueNode(p("/3/0/6/0"), lwm2m.Integer(1))}, "8306 410001"},
		// Empty multiple resource: MR of length 0.
		{"/3/0/6", []lwm2m.Node{{Path: p("/3/0/6"), Kind: lwm2m.KindEmptyMultiple}}, "8006"},
		// Resource instance path: a bare RI TLV.
		{"/3/0/6/1", []lwm2m.Node{lwm2m.ValueNode(p("/3/0/6/1"), lwm2m.Integer(5))}, "410105"},
		// Object path: every instance wrapped, empty instance is an empty OI.
		{"/3", []lwm2m.Node{lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("a")), {Path: p("/3/1"), Kind: lwm2m.KindEmptyInstance}}, "0300 c10061 0001"},
		// Empty instance at an instance path: empty payload.
		{"/3/0", []lwm2m.Node{{Path: p("/3/0"), Kind: lwm2m.KindEmptyInstance}}, ""},
		{"/3", nil, ""},
	}
	for _, e := range enc {
		got, err := c.Encode(p(e.base), e.nodes)
		if err != nil || !bytes.Equal(got, mustHex(e.hex)) {
			t.Errorf("Encode(%s) = %x, %v; want %s", e.base, got, err, e.hex)
			continue
		}
		back, err := c.Decode(p(e.base), got, s)
		if err != nil || !lwm2m.NodesEqual(back, e.nodes) {
			t.Errorf("Decode(%s, %x) = %v, %v", e.base, got, back, err)
		}
	}
	badEnc := []struct {
		base  string
		nodes []lwm2m.Node
	}{
		{"/", []lwm2m.Node{lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("a"))}},
		{"/3/0", []lwm2m.Node{lwm2m.ValueNode(p("/4/0/0"), lwm2m.String("a"))}},
		{"/3/0/0", nil},
		{"/3/0", []lwm2m.Node{lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("a")), lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("b"))}},
		{"/3/0", []lwm2m.Node{lwm2m.ValueNode(p("/3/0/6"), lwm2m.Integer(1)), lwm2m.ValueNode(p("/3/0/6/0"), lwm2m.Integer(1))}},
	}
	for _, e := range badEnc {
		if got, err := c.Encode(p(e.base), e.nodes); err == nil {
			t.Errorf("Encode(%s, %v) = %x, want error", e.base, e.nodes, got)
		}
	}
	badDec := []struct{ base, hex string }{
		{"/", "c10001"},            // TLV cannot carry an object id
		{"/3/0", "c10601"},         // multi-instance resource sent as a single resource TLV
		{"/3/0", "8000"},           // single-instance resource sent as an MR TLV
		{"/3/0/0", "c10161"},       // resource TLV id differs from the path
		{"/3/0/6/1", "410005"},     // resource instance id differs from the path
		{"/3", "0000c10061"},       // OI mixed with bare resources at an object path
		{"/3/0/6/0", ""},           // empty payload at a resource instance
		{"/3/0", "080103c10061"},   // OI wrapper with the wrong instance id
		{"/3/0", "0000c10061"},     // OI next to a resource at an instance path
		{"/3/0/0", "c10011c10012"}, // duplicate resource
	}
	for _, d := range badDec {
		if got, err := c.Decode(p(d.base), mustHex(d.hex), s); err == nil {
			t.Errorf("Decode(%s, %s) = %v, want error", d.base, d.hex, got)
		}
	}
	// Unknown resources decode as opaque (the server cannot type them).
	got, err := c.Decode(p("/9/0"), mustHex("c10101"), s)
	if err != nil || !lwm2m.NodesEqual(got, []lwm2m.Node{lwm2m.ValueNode(p("/9/0/1"), lwm2m.Opaque([]byte{1}))}) {
		t.Errorf("unknown resource: %v %v", got, err)
	}
}
