package lwm2mcbor

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// Proves: CBOR-01
func TestRegistered(t *testing.T) {
	c, err := codec.For(11544)
	if err != nil || c.Format() != lwm2m.FormatLwM2MCBOR {
		t.Fatalf("codec.For(11544) = %v, %v", c, err)
	}
}

// The encoder layout reproduces the Core 1.2.2 §7.5.4.1-§7.5.4.5 examples
// byte for byte. The others are alternative layouts of the same data.
// Proves: CBOR-02, CBOR-04, CBOR-05
func TestEncodeMatchesSpecExamples(t *testing.T) {
	want := map[string]bool{
		"spec-lwcbor-read-3-0-0-array-key":     true,  // §7.5.4.1
		"spec-lwcbor-read-3-0-0-nested-maps":   false, // §7.5.4.1 "also valid"
		"spec-lwcbor-read-3-0-6":               true,  // §7.5.4.2
		"spec-lwcbor-read-3-0":                 true,  // §7.5.4.3
		"spec-lwcbor-read-1":                   true,  // §7.5.4.4
		"spec-lwcbor-composite-response":       true,  // §7.5.4.5
		"spec-lwcbor-create-2-instance-5":      false, // §7.5.4.6 uses [2,102] inside the instance map
		"spec-lwcbor-create-2-no-instance-id":  false,
		"spec-lwcbor-gateway-prefix-composite": true, // GW §10
	}
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, v := range vs {
		exact, ok := want[v.ID]
		if !ok {
			continue
		}
		seen++
		_, base := testPath(t, v.Path)
		payload, _ := v.Payload()
		enc, err := Codec{}.Encode(base, expected(t, v))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(enc, payload) != exact {
			t.Errorf("%s: byte-exact=%v, want %v\n got %x\nwant %x", v.ID, !exact, exact, enc, payload)
		}
	}
	if seen != len(want) {
		t.Fatalf("found %d of %d spec examples", seen, len(want))
	}
}

func schema(p string, typ lwm2m.Type, multiple bool) lwm2m.Schema {
	rp := lwm2m.MustParsePath(p)
	return lwm2m.SchemaFunc(func(q lwm2m.Path) (lwm2m.ResourceDef, bool) {
		return lwm2m.ResourceDef{Type: typ, Multiple: multiple}, q == rp
	})
}

// Values are type-checked against the model (Core App. C Tbl C-2; DT-02):
// the CBOR major type must fit the resource type, null only for "none" (A-8).
// Proves: CBOR-06
func TestDecodeTypeChecks(t *testing.T) {
	cases := []struct {
		name string
		hex  []byte
		typ  lwm2m.Type
		ok   bool
	}{
		{"text for integer", []byte{0xa1, 0x83, 3, 0, 9, 0x61, '1'}, lwm2m.TypeInteger, false},
		{"uint64 above int64 for integer", append([]byte{0xa1, 0x83, 3, 0, 9, 0x1b}, 0x80, 0, 0, 0, 0, 0, 0, 0), lwm2m.TypeInteger, false},
		{"uint64 above int64 for unsigned", append([]byte{0xa1, 0x83, 3, 0, 9, 0x1b}, 0x80, 0, 0, 0, 0, 0, 0, 0), lwm2m.TypeUnsigned, true},
		{"negative for unsigned", []byte{0xa1, 0x83, 3, 0, 9, 0x20}, lwm2m.TypeUnsigned, false},
		{"null for integer", []byte{0xa1, 0x83, 3, 0, 9, 0xf6}, lwm2m.TypeInteger, false},
		{"int for float", []byte{0xa1, 0x83, 3, 0, 9, 0x05}, lwm2m.TypeFloat, false},
		{"bytes for string", []byte{0xa1, 0x83, 3, 0, 9, 0x41, 1}, lwm2m.TypeString, false},
		{"text for opaque", []byte{0xa1, 0x83, 3, 0, 9, 0x61, 'a'}, lwm2m.TypeOpaque, false},
		{"bad objlnk text", []byte{0xa1, 0x83, 3, 0, 9, 0x62, '3', ':'}, lwm2m.TypeObjlnk, false},
		{"tag 1 for integer", []byte{0xa1, 0x83, 3, 0, 9, 0xc1, 0x01}, lwm2m.TypeInteger, false},
		{"uint for time", []byte{0xa1, 0x83, 3, 0, 9, 0x01}, lwm2m.TypeTime, true},
		{"text for corelnk", []byte{0xa1, 0x83, 3, 0, 9, 0x61, 'a'}, lwm2m.TypeCorelnk, true},
	}
	for _, c := range cases {
		_, err := Codec{}.Decode(lwm2m.Root, c.hex, schema("/3/0/9", c.typ, false))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
	// Single vs multi-instance must match the model (§7.5.4.2).
	if _, err := (Codec{}).Decode(lwm2m.Root, []byte{0xa1, 0x83, 3, 0, 6, 1}, schema("/3/0/6", lwm2m.TypeInteger, true)); err == nil {
		t.Error("single value accepted for a multi-instance resource")
	}
	if _, err := (Codec{}).Decode(lwm2m.Root, []byte{0xa1, 0x84, 3, 0, 9, 0, 1}, schema("/3/0/9", lwm2m.TypeInteger, false)); err == nil {
		t.Error("resource instance accepted for a single-instance resource")
	}
}

// Gateway TS §9 prefixes decode into Node.Prefix and encode back.
// Proves: CBOR-11
func TestPrefixRoundTrip(t *testing.T) {
	in := []byte{0xa1, 0x61, 'd', 0xa1, 3, 0xa1, 0, 0xa1, 0, 1}
	got, err := Codec{}.Decode(lwm2m.Root, in, nil)
	want := []lwm2m.Node{{Prefix: "d", Path: lwm2m.MustParsePath("/3/0/0"), Value: lwm2m.Integer(1)}}
	if err != nil || !lwm2m.NodesEqual(got, want) {
		t.Fatalf("decode: %v %v", got, err)
	}
	enc, err := Codec{}.Encode(lwm2m.Root, append(want, lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/9"), lwm2m.Integer(5))))
	// {["d", 3, 0, 0]: 1, [3, 0, 9]: 5}
	if wantEnc := []byte{0xa2, 0x84, 0x61, 'd', 3, 0, 0, 1, 0x83, 3, 0, 9, 5}; err != nil || !bytes.Equal(enc, wantEnc) {
		t.Fatalf("encode: %x %v, want %x", enc, err, wantEnc)
	}
}

func TestEncodeRejects(t *testing.T) {
	p := lwm2m.MustParsePath
	cases := map[string][]lwm2m.Node{
		"timestamp":                  {{Path: p("/3/0/9"), Value: lwm2m.Integer(1), HasTime: true, Time: 5}},
		"outside base":               {lwm2m.ValueNode(p("/3/0/10"), lwm2m.Integer(1))},
		"value at instance":          {lwm2m.ValueNode(p("/3/0"), lwm2m.Integer(1))},
		"duplicate":                  {lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(1)), lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(2))},
		"resource and its ri":        {lwm2m.ValueNode(p("/3/0/6"), lwm2m.Integer(1)), lwm2m.ValueNode(p("/3/0/6/0"), lwm2m.Integer(2))},
		"ri then its resource":       {lwm2m.ValueNode(p("/3/0/6/0"), lwm2m.Integer(1)), lwm2m.ValueNode(p("/3/0/6"), lwm2m.Integer(2))},
		"empty instance with value":  {{Path: p("/3/0"), Kind: lwm2m.KindEmptyInstance}, lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(1))},
		"empty multiple at instance": {{Path: p("/3/0"), Kind: lwm2m.KindEmptyMultiple}},
		"no nodes":                   {},
	}
	for name, ns := range cases {
		base := lwm2m.MustParsePath("/3/0/9")
		if name != "outside base" && name != "timestamp" {
			base = lwm2m.Root
		}
		if b, err := (Codec{}).Encode(base, ns); err == nil {
			t.Errorf("%s: encoded %x, want error", name, b)
		}
	}
}
