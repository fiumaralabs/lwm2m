package cbor

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

var r = lwm2m.MustParsePath("/3/0/13")

func one(t lwm2m.Type, multiple bool) lwm2m.Schema {
	return lwm2m.SchemaFunc(func(p lwm2m.Path) (lwm2m.ResourceDef, bool) {
		return lwm2m.ResourceDef{Type: t, Multiple: multiple}, p == r
	})
}

// Table C-2 (CBOR column) and RFC 8949: what we emit. Integers shortest
// (§3.1), floats 64-bit, Time as tag 1 + epoch (§3.4.2), Objlnk "oid:iid"
// without NUL, Opaque as a byte string (empty is 0x40, not null).
func TestEncode(t *testing.T) {
	for _, c := range []struct {
		v    lwm2m.Value
		want string
	}{
		{lwm2m.Time(1367491215), "c11a5182428f"},
		{lwm2m.Time(-1), "c120"},
		{lwm2m.Unsigned(math.MaxUint64), "1bffffffffffffffff"},
		{lwm2m.Integer(-1 << 63), "3b7fffffffffffffff"},
		{lwm2m.Integer(23), "17"},
		{lwm2m.Float(1.5), "fb3ff8000000000000"},
		{lwm2m.Float(math.Inf(1)), "fb7ff0000000000000"},
		{lwm2m.Objlnk(65535, 65535), "6b36353533353a3635353335"},
		{lwm2m.Opaque(nil), "40"},
		{lwm2m.Corelnk("</3/0>"), "663c2f332f303e"},
		{lwm2m.Value{}, "f6"},
	} {
		got, err := Codec{}.Encode(r, []lwm2m.Node{lwm2m.ValueNode(r, c.v)})
		if err != nil || hex.EncodeToString(got) != c.want {
			t.Errorf("%v: got %x (%v) want %s", c.v, got, err, c.want)
		}
	}
	for _, ns := range [][]lwm2m.Node{nil, {lwm2m.ValueNode(r, lwm2m.Integer(1)), lwm2m.ValueNode(r, lwm2m.Integer(2))}} {
		if _, err := (Codec{}).Encode(r, ns); err == nil {
			t.Errorf("encoded %d nodes", len(ns))
		}
	}
	// Core §7.5.3: single resource or resource instance only.
	if _, err := (Codec{}).Encode(lwm2m.MustParsePath("/3/0"), nil); err == nil {
		t.Error("encoded at an instance path")
	}
}

// Table C-2 (CBOR column, CBOR-06/CBOR-12): Time is an integer, tag 0
// (RFC 3339, as Zephyr sends) or tag 1; floats of any width; Objlnk text
// with a trailing NUL is tolerated (C7); integral floats fit integers.
func TestDecode(t *testing.T) {
	for _, c := range []struct {
		hex  string
		typ  lwm2m.Type
		want lwm2m.Value
	}{
		{"c11a5182428f", lwm2m.TypeTime, lwm2m.Time(1367491215)},
		{"c07819313937302d30312d30315430303a30303a30312d30303a3030", lwm2m.TypeTime, lwm2m.Time(1)},
		{"c07819323031332d30352d30325431303a34303a31352b30323a3030", lwm2m.TypeTime, lwm2m.Time(1367484015)},
		{"1a5182428f", lwm2m.TypeTime, lwm2m.Time(1367491215)},
		{"c1fb41d46090a3c00000", lwm2m.TypeTime, lwm2m.Time(1367491215)},
		{"f93e00", lwm2m.TypeFloat, lwm2m.Float(1.5)},
		{"fa3fc00000", lwm2m.TypeFloat, lwm2m.Float(1.5)},
		{"02", lwm2m.TypeFloat, lwm2m.Float(2)},
		{"fb4000000000000000", lwm2m.TypeInteger, lwm2m.Integer(2)},
		{"1bffffffffffffffff", lwm2m.TypeUnsigned, lwm2m.Unsigned(math.MaxUint64)},
		{"6536363a3100", lwm2m.TypeObjlnk, lwm2m.Objlnk(66, 1)},
		{"7f623636623a31ff", lwm2m.TypeObjlnk, lwm2m.Objlnk(66, 1)}, // indefinite text
		{"40", lwm2m.TypeOpaque, lwm2m.Opaque(nil)},
		{"663c2f332f303e", lwm2m.TypeCorelnk, lwm2m.Corelnk("</3/0>")},
		// No schema: the type follows the CBOR major type.
		{"20", lwm2m.TypeNone, lwm2m.Integer(-1)},
		{"1bffffffffffffffff", lwm2m.TypeNone, lwm2m.Unsigned(math.MaxUint64)},
		{"c101", lwm2m.TypeNone, lwm2m.Time(1)},
		{"f5", lwm2m.TypeNone, lwm2m.Boolean(true)},
		{"4101", lwm2m.TypeNone, lwm2m.Opaque([]byte{1})},
	} {
		b, _ := hex.DecodeString(c.hex)
		ns, err := Codec{}.Decode(r, b, one(c.typ, false))
		if err != nil || len(ns) != 1 || ns[0].Path != r || !ns[0].Value.Equal(c.want) {
			t.Errorf("%s as %v: got %v (%v) want %v", c.hex, c.typ, ns, err, c.want)
		}
	}
}

// DT-02: the server type-checks retrieved values; malformed CBOR fails.
func TestDecodeRejects(t *testing.T) {
	for _, c := range []struct {
		hex string
		typ lwm2m.Type
	}{
		{"", lwm2m.TypeNone},
		{"0102", lwm2m.TypeNone},     // trailing item
		{"1a518242", lwm2m.TypeNone}, // truncated
		{"a10102", lwm2m.TypeNone},   // a map is not a single value
		{"6178", lwm2m.TypeInteger},  // text for an integer
		{"20", lwm2m.TypeUnsigned},   // negative for unsigned
		{"1bffffffffffffffff", lwm2m.TypeInteger},
		{"f93e00", lwm2m.TypeInteger}, // 1.5 is not an integer
		{"f5", lwm2m.TypeString},
		{"4101", lwm2m.TypeString},
		{"6431203a32", lwm2m.TypeObjlnk},               // "1 :2"
		{"6b36353533363a3635353335", lwm2m.TypeObjlnk}, // 65536
		{"6161", lwm2m.TypeTime},
		{"c482203a00", lwm2m.TypeNone}, // tag 4 is not a Table C-2 form
	} {
		b, _ := hex.DecodeString(c.hex)
		if ns, err := (Codec{}).Decode(r, b, one(c.typ, false)); err == nil {
			t.Errorf("%s as %v accepted: %v", c.hex, c.typ, ns)
		}
	}
	if _, err := (Codec{}).Decode(r, []byte{1}, one(lwm2m.TypeInteger, true)); err == nil {
		t.Error("decoded a single value into a multi-instance resource")
	}
	if _, err := (Codec{}).Decode(lwm2m.MustParsePath("/3"), []byte{1}, nil); err == nil {
		t.Error("decoded at an object path")
	}
}
