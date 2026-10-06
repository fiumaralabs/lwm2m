package omajson

import (
	"math"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

// 11543 and the aliases clients use for it (50 from Zephyr, C7; pre-IANA
// 1543, FMT-06) resolve to this codec.
// Proves: FMT-10
func TestRegistered(t *testing.T) {
	for _, f := range []lwm2m.ContentFormat{11543, 50, 1543} {
		c, err := codec.For(f)
		if err != nil || c.Format() != lwm2m.FormatOMAJSON {
			t.Errorf("codec.For(%d) = %v, %v", f, c, err)
		}
	}
}

func schema(p string, typ lwm2m.Type) lwm2m.Schema {
	rp := lwm2m.MustParsePath(p)
	return lwm2m.SchemaFunc(func(q lwm2m.Path) (lwm2m.ResourceDef, bool) {
		return lwm2m.ResourceDef{Type: typ}, q == rp
	})
}

// TS 1.0.2 §6.4.4 Table 21 / App. C: Opaque is base64 in "sv", Time and
// Unsigned are numbers in "v", Objlnk is "ov", Boolean is "bv". Values not
// covered by the vectors round-trip with a model.
func TestTypedRoundTrip(t *testing.T) {
	base := lwm2m.MustParsePath("/3442/0")
	cases := []struct {
		typ  lwm2m.Type
		v    lwm2m.Value
		text string
	}{
		{lwm2m.TypeOpaque, lwm2m.Opaque([]byte{1, 2, 3, 4, 5}), `{"bn":"/3442/0/","e":[{"n":"1","sv":"AQIDBAU="}]}`},
		{lwm2m.TypeUnsigned, lwm2m.Unsigned(18446744073709551615), `{"bn":"/3442/0/","e":[{"n":"1","v":18446744073709551615}]}`},
		{lwm2m.TypeCorelnk, lwm2m.Corelnk("</3/0>"), `{"bn":"/3442/0/","e":[{"n":"1","sv":"</3/0>"}]}`},
		{lwm2m.TypeTime, lwm2m.Time(-5), `{"bn":"/3442/0/","e":[{"n":"1","v":-5}]}`},
		{lwm2m.TypeFloat, lwm2m.Float(1e300), `{"bn":"/3442/0/","e":[{"n":"1","v":1e+300}]}`},
		{lwm2m.TypeFloat, lwm2m.Float(6.667e-11), `{"bn":"/3442/0/","e":[{"n":"1","v":6.667e-11}]}`},
	}
	for _, c := range cases {
		ns := []lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/3442/0/1"), c.v)}
		b, err := Codec{}.Encode(base, ns)
		if err != nil || string(b) != c.text {
			t.Errorf("%v: encode %s, %v; want %s", c.typ, b, err, c.text)
			continue
		}
		got, err := Codec{}.Decode(base, b, schema("/3442/0/1", c.typ))
		if err != nil || !lwm2m.NodesEqual(got, ns) {
			t.Errorf("%v: decode %v, %v", c.typ, got, err)
		}
	}
}

// Server MUST type-check received values (DT-02); a 1.0 entry has exactly
// one value field; bt is a base for relative t (TS 1.0.2 §6.4.4).
func TestDecodeRules(t *testing.T) {
	base := lwm2m.MustParsePath("/3/0")
	bad := map[string]string{
		"sv for integer":   `{"bn":"/3/0/","e":[{"n":"9","sv":"1"}]}`,
		"bv for integer":   `{"bn":"/3/0/","e":[{"n":"9","bv":true}]}`,
		"float for int":    `{"bn":"/3/0/","e":[{"n":"9","v":1.5}]}`,
		"two values":       `{"bn":"/3/0/","e":[{"n":"9","v":1,"sv":"1"}]}`,
		"outside base":     `{"bn":"/4/0/","e":[{"n":"9","v":1}]}`,
		"instance level":   `{"bn":"/3/","e":[{"n":"0","v":1}]}`,
		"bad name":         `{"bn":"/3/0/","e":[{"n":"x","v":1}]}`,
		"trailing slash":   `{"bn":"/3/0/9/","e":[{"v":1}]}`,
		"id over 65535":    `{"bn":"/3/0/","e":[{"n":"65536","v":1}]}`,
		"not an object":    `[]`,
		"bad base64":       `{"bn":"/3/0/","e":[{"n":"9","sv":"!!"}]}`,
		"trailing garbage": `{"e":[]}x`,
	}
	for name, doc := range bad {
		s := schema("/3/0/9", lwm2m.TypeInteger)
		if name == "bad base64" {
			s = schema("/3/0/9", lwm2m.TypeOpaque)
		}
		if got, err := (Codec{}).Decode(base, []byte(doc), s); err == nil {
			t.Errorf("%s: decoded %v, want error", name, got)
		}
	}
	got, err := Codec{}.Decode(base, []byte(`{"bt":100,"bn":"/3/0/","e":[{"n":"9","v":1,"t":-10},{"n":"10","v":2}]}`), nil)
	want := []lwm2m.Node{
		{Path: lwm2m.MustParsePath("/3/0/9"), Value: lwm2m.Integer(1), Time: 90, HasTime: true},
		{Path: lwm2m.MustParsePath("/3/0/10"), Value: lwm2m.Integer(2), Time: 100, HasTime: true},
	}
	if err != nil || !lwm2m.NodesEqual(got, want) {
		t.Errorf("bt/t: got %v, %v", got, err)
	}
}

func TestEncodeRejects(t *testing.T) {
	p := lwm2m.MustParsePath
	cases := map[string][]lwm2m.Node{
		"NaN":              {lwm2m.ValueNode(p("/3/0/9"), lwm2m.Float(math.NaN()))},
		"none":             {lwm2m.ValueNode(p("/3/0/9"), lwm2m.Value{})},
		"outside base":     {lwm2m.ValueNode(p("/4/0/9"), lwm2m.Integer(1))},
		"empty plus value": {{Path: p("/3/0"), Kind: lwm2m.KindEmptyInstance}, lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(1))},
	}
	for name, ns := range cases {
		if b, err := (Codec{}).Encode(p("/3/0"), ns); err == nil {
			t.Errorf("%s: encoded %s, want error", name, b)
		}
	}
}
