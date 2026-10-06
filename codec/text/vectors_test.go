package text_test

import (
	"math"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/codec/text"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// nonCanonical lists vectors whose source encoder makes a different (still
// spec-legal) choice than ours; they are proven by decode(payload)==expected
// and decode(Encode(expected))==expected instead of a byte-exact encode.
var nonCanonical = map[string]string{}

// errorType gives the resource type for "error" decode vectors (plain text is
// untyped; the type comes from the reader call or model named in the notes).
var errorType = map[string]lwm2m.Type{
	"text-zephyr-get-s64-overflow-0":    lwm2m.TypeInteger,
	"text-zephyr-get-s64-overflow-1":    lwm2m.TypeInteger,
	"text-zephyr-get-s32-empty":         lwm2m.TypeInteger,
	"text-zephyr-get-s64-empty":         lwm2m.TypeInteger,
	"text-zephyr-get-float-empty":       lwm2m.TypeFloat,
	"text-zephyr-get-bool-empty":        lwm2m.TypeBoolean,
	"text-zephyr-get-objlnk-empty":      lwm2m.TypeObjlnk,
	"text-leshan-decode-invalid-base64": lwm2m.TypeOpaque,
}

// encodeErrors gives the input for "encode" + "error" vectors, which have
// no payload or nodes (the notes describe the input).
var encodeErrors = map[string][]lwm2m.Node{
	"text-leshan-encode-multiple-error": {
		lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/6/0"), lwm2m.Integer(1)),
		lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/6/1"), lwm2m.Integer(5)),
	},
}

func load(t *testing.T) []vectors.Vector {
	vs, err := vectors.Load("plain_text")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"spec-examples", "objlnk"} {
		more, err := vectors.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range more {
			if f == "objlnk" || (v.ContentFormat != nil && *v.ContentFormat == int(lwm2m.FormatText)) {
				vs = append(vs, v)
			}
		}
	}
	return vs
}

func singleSchema(t lwm2m.Type) lwm2m.Schema {
	return lwm2m.SchemaFunc(func(lwm2m.Path) (lwm2m.ResourceDef, bool) { return lwm2m.ResourceDef{Type: t}, true })
}

// TestVectors runs plain_text.json, objlnk.json and the content-format 0
// entries of spec-examples.json (Core §7.4.1, 1.2.2 §7.5.1, Table C-2, and
// the Execute arguments of §6.3.5). Placeholder node paths (/0/0/0, "") are
// ignored per spec/vectors/README.md: the node path becomes the base.
//
// Proves: FMT-08
func TestVectors(t *testing.T) {
	c, err := codec.For(lwm2m.FormatText)
	if err != nil {
		t.Fatal(err)
	}
	vs := load(t)
	ids := map[string]bool{}
	for _, v := range vs {
		ids[v.ID] = true
	}
	for _, m := range []map[string]bool{keys(nonCanonical), keys(errorType), keys(encodeErrors)} {
		for id := range m {
			if !ids[id] {
				t.Errorf("unknown vector id %s in a test map", id)
			}
		}
	}
	for _, v := range vs {
		t.Run(v.ID, func(t *testing.T) {
			payload, err := v.Payload()
			if err != nil {
				t.Fatal(err)
			}
			helper := v.ContentFormat == nil // objlnk.json pure helper vectors
			switch shape := v.ExpectedShape(); shape {
			case "error":
				if v.Encodes() {
					nodes, ok := encodeErrors[v.ID]
					if !ok {
						t.Fatal("encode error vector without input in encodeErrors")
					}
					base, _ := v.BasePath()
					if got, err := c.Encode(base, nodes); err == nil {
						t.Fatalf("Encode = %q, want error", got)
					}
					return
				}
				if helper {
					if l, err := lwm2m.ParseObjLink(string(payload)); err == nil {
						t.Fatalf("ParseObjLink = %v, want error", l)
					}
					return
				}
				typ, ok := errorType[v.ID]
				if !ok {
					t.Fatal("decode error vector without a type in errorType")
				}
				base := lwm2m.MustParsePath("/0/0/0")
				if v.Path != "" {
					base, _ = v.BasePath()
				}
				if got, err := c.Decode(base, payload, singleSchema(typ)); err == nil {
					t.Fatalf("Decode = %v, want error", got)
				}
			case "execute_args":
				var want []struct {
					Digit uint8   `json:"digit"`
					Value *string `json:"value"`
				}
				if err := v.ExpectedObject("execute_args", &want); err != nil {
					t.Fatal(err)
				}
				got, err := text.ParseExecArgs(string(payload))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want) {
					t.Fatalf("ParseExecArgs = %+v, want %d args", got, len(want))
				}
				for i, w := range want {
					if got[i].Digit != w.Digit || got[i].HasValue != (w.Value != nil) || (w.Value != nil && got[i].Value != *w.Value) {
						t.Fatalf("arg %d = %+v, want %d %v", i, got[i], w.Digit, w.Value)
					}
				}
			case "nodes":
				want, err := v.Nodes()
				if err != nil {
					t.Fatal(err)
				}
				if len(want) != 1 {
					t.Fatalf("plain text vector with %d nodes", len(want))
				}
				if helper {
					if v.Decodes() {
						l, err := lwm2m.ParseObjLink(string(payload))
						if err != nil || l != want[0].Value.Link {
							t.Fatalf("ParseObjLink = %v, %v", l, err)
						}
					}
					if v.Encodes() && want[0].Value.Link.String() != string(payload) {
						t.Fatalf("ObjLink.String = %q", want[0].Value.Link.String())
					}
					return
				}
				base := want[0].Path
				if v.Path != "" {
					base, _ = v.BasePath()
				}
				want[0].Path = base
				schema := vectors.Schema(want)
				decode := func(b []byte) {
					t.Helper()
					got, err := c.Decode(base, b, schema)
					if err != nil {
						t.Fatalf("decode %q: %v", b, err)
					}
					if !lwm2m.NodesEqual(got, want) {
						t.Fatalf("decode %q = %s want %s", b, lwm2m.FormatNodes(got), lwm2m.FormatNodes(want))
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
					} else if string(got) != string(payload) {
						t.Fatalf("encode = %q, want %q", got, payload)
					}
				}
			default:
				t.Fatalf("unhandled expected shape %q", shape)
			}
		})
	}
}

func keys[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// TestValues covers Table C-2 plain-text rows no vector reaches (Core
// §7.4.1, 1.2.2 §7.5.1): unsigned range, float decimal form without an
// exponent, exponent input, strict booleans, corelnk, opaque padding.
//
// Proves: FMT-08
func TestValues(t *testing.T) {
	enc := []struct {
		v    lwm2m.Value
		want string
	}{
		{lwm2m.Float(6.667e-11), "0.00000000006667"}, // Table C-2 example
		{lwm2m.Float(1e21), "1000000000000000000000.0"},
		{lwm2m.Float(-0.5), "-0.5"},
		{lwm2m.Value{Type: lwm2m.TypeFloat, Float: float64(float32(0.1)), Float32: true}, "0.1"},
		{lwm2m.Unsigned(math.MaxUint64), "18446744073709551615"},
		{lwm2m.Corelnk("</3/0>;ver=1.1"), "</3/0>;ver=1.1"},
		{lwm2m.Opaque([]byte{1, 2, 3, 4}), "AQIDBA=="},
		{lwm2m.Opaque(nil), ""},
		{lwm2m.Objlnk(3, 65535), "3:65535"},
	}
	for _, e := range enc {
		got, err := text.FormatValue(e.v)
		if err != nil || got != e.want {
			t.Errorf("FormatValue(%v) = %q, %v; want %q", e.v, got, err, e.want)
		}
		if e.v.Float32 {
			continue // text has no width: "0.1" reads back as the float64 0.1
		}
		back, err := text.ParseValue(e.v.Type, got)
		if err != nil || !back.Equal(e.v) {
			t.Errorf("ParseValue(%v, %q) = %v, %v", e.v.Type, got, back, err)
		}
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if s, err := text.FormatValue(lwm2m.Float(f)); err == nil {
			t.Errorf("FormatValue(%v) = %q, want error", f, s)
		}
	}
	dec := []struct {
		t lwm2m.Type
		s string
		v lwm2m.Value
	}{
		{lwm2m.TypeFloat, "1.0E10", lwm2m.Float(1e10)}, // Leshan's Java form
		{lwm2m.TypeFloat, "-2.5e-3", lwm2m.Float(-0.0025)},
		{lwm2m.TypeUnsigned, "18", lwm2m.Unsigned(18)},
		{lwm2m.TypeOpaque, "", lwm2m.Opaque([]byte{})},
	}
	for _, d := range dec {
		got, err := text.ParseValue(d.t, d.s)
		if err != nil || !got.Equal(d.v) {
			t.Errorf("ParseValue(%v, %q) = %v, %v", d.t, d.s, got, err)
		}
	}
	bad := []struct {
		t lwm2m.Type
		s string
	}{
		{lwm2m.TypeBoolean, "true"}, {lwm2m.TypeBoolean, "false"}, {lwm2m.TypeBoolean, "10"}, {lwm2m.TypeBoolean, " 1"},
		{lwm2m.TypeFloat, "NaN"}, {lwm2m.TypeFloat, "Inf"}, {lwm2m.TypeFloat, "0x1p-2"}, {lwm2m.TypeFloat, "1_000"}, {lwm2m.TypeFloat, ""},
		{lwm2m.TypeUnsigned, "-1"}, {lwm2m.TypeUnsigned, "18446744073709551616"}, {lwm2m.TypeInteger, "1.5"}, {lwm2m.TypeTime, ""},
		{lwm2m.TypeObjlnk, " 1:2"}, {lwm2m.TypeObjlnk, "1:2:3"}, {lwm2m.TypeOpaque, "AQIDBA"}, {lwm2m.TypeString, "\xff"},
		{lwm2m.TypeNone, ""},
	}
	for _, b := range bad {
		if v, err := text.ParseValue(b.t, b.s); err == nil {
			t.Errorf("ParseValue(%v, %q) = %v, want error", b.t, b.s, v)
		}
	}
}

// TestSingleValue checks that plain text carries one value of a resource or
// resource instance only (1.2.2 §7.5.1, Table C-2).
//
// Proves: FMT-08
func TestSingleValue(t *testing.T) {
	c := text.Codec{}
	multi := lwm2m.SchemaFunc(func(lwm2m.Path) (lwm2m.ResourceDef, bool) {
		return lwm2m.ResourceDef{Type: lwm2m.TypeInteger, Multiple: true}, true
	})
	if _, err := c.Decode(lwm2m.MustParsePath("/3/0/6"), []byte("1"), multi); err == nil {
		t.Error("decoded a multi-instance resource path")
	}
	got, err := c.Decode(lwm2m.MustParsePath("/3/0/6/1"), []byte("5"), multi)
	if err != nil || !lwm2m.NodesEqual(got, []lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/6/1"), lwm2m.Integer(5))}) {
		t.Errorf("resource instance: %v %v", got, err)
	}
	for _, base := range []string{"/", "/3", "/3/0"} {
		if _, err := c.Decode(lwm2m.MustParsePath(base), []byte("1"), nil); err == nil {
			t.Errorf("decoded at %s", base)
		}
		if _, err := c.Encode(lwm2m.MustParsePath(base), nil); err == nil {
			t.Errorf("encoded at %s", base)
		}
	}
	// Encode refuses a node that is not at the base path.
	n := lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/1"), lwm2m.Integer(1))
	if _, err := c.Encode(lwm2m.MustParsePath("/3/0/0"), []lwm2m.Node{n}); err == nil {
		t.Error("encoded a node outside the base path")
	}
}

// TestExecArgs covers the Execute-argument ABNF of Core §6.3.5 (1.2.2):
// `arglist = arg *("," arg)`, `arg = DIGIT / DIGIT "=" "'" *CHAR "'"`,
// CHAR = "!" / %x23-26 / %x28-5B / %x5D-7E, each digit at most once (1.2.1).
func TestExecArgs(t *testing.T) {
	args := []text.ExecArg{{Digit: 0, Value: "U", HasValue: true}, {Digit: 1}, {Digit: 9, Value: "", HasValue: true}}
	s, err := text.FormatExecArgs(args)
	if err != nil || s != "0='U',1,9=''" {
		t.Fatalf("FormatExecArgs = %q, %v", s, err)
	}
	back, err := text.ParseExecArgs(s)
	if err != nil || len(back) != 3 || back[0] != args[0] || back[1] != args[1] || back[2] != args[2] {
		t.Fatalf("ParseExecArgs(%q) = %+v, %v", s, back, err)
	}
	if got, err := text.ParseExecArgs(""); err != nil || got != nil {
		t.Errorf("empty payload = %v, %v; want no arguments", got, err)
	}
	for _, bad := range []string{"a", "10", "1,1", "1,", ",1", "1='x", "1=x", "1='a'b", "1='a\\b'", "1='a\"b'", "1 ,2"} {
		if got, err := text.ParseExecArgs(bad); err == nil {
			t.Errorf("ParseExecArgs(%q) = %+v, want error", bad, got)
		}
	}
	// The emitter is strict: no SP (ambiguity A-23), quote, backslash, or repeated digit.
	for _, bad := range [][]text.ExecArg{
		{{Digit: 1, Value: " x", HasValue: true}}, {{Digit: 1, Value: "'", HasValue: true}},
		{{Digit: 1, Value: "\\", HasValue: true}}, {{Digit: 1}, {Digit: 1}}, {{Digit: 10}},
	} {
		if s, err := text.FormatExecArgs(bad); err == nil {
			t.Errorf("FormatExecArgs(%+v) = %q, want error", bad, s)
		}
	}
}
