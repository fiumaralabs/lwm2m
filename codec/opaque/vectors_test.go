package opaque_test

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/codec/opaque"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// TestVectors runs opaque.json and any content-format 42 entries of
// spec-examples.json. Both are empty today (see spec/vectors/README.md), so
// TestOpaque carries the proof; this keeps future vectors from going unrun.
func TestVectors(t *testing.T) {
	c, err := codec.For(lwm2m.FormatOpaque)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := vectors.Load("opaque")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range spec {
		if v.ContentFormat != nil && *v.ContentFormat == int(lwm2m.FormatOpaque) {
			vs = append(vs, v)
		}
	}
	for _, v := range vs {
		t.Run(v.ID, func(t *testing.T) {
			payload, _ := v.Payload()
			base, err := v.BasePath()
			if err != nil {
				t.Fatal(err)
			}
			if v.IsError() {
				if _, err := c.Decode(base, payload, nil); err == nil {
					t.Fatal("decode succeeded, want error")
				}
				return
			}
			want, err := v.Nodes()
			if err != nil {
				t.Fatal(err)
			}
			if v.Decodes() {
				got, err := c.Decode(base, payload, vectors.Schema(want))
				if err != nil || !lwm2m.NodesEqual(got, want) {
					t.Fatalf("decode = %v, %v", got, err)
				}
			}
			if v.Encodes() {
				got, err := c.Encode(base, want)
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("encode = %x, %v", got, err)
				}
			}
		})
	}
}

// TestOpaque proves Core §7.4.2 (1.2.2 §7.5.2): the payload is the raw octets
// of one Opaque resource or resource instance.
//
// Proves: FMT-09
func TestOpaque(t *testing.T) {
	c := opaque.Codec{}
	p := lwm2m.MustParsePath
	typed := func(typ lwm2m.Type, multi bool) lwm2m.Schema {
		return lwm2m.SchemaFunc(func(lwm2m.Path) (lwm2m.ResourceDef, bool) {
			return lwm2m.ResourceDef{Type: typ, Multiple: multi}, true
		})
	}
	fw := []byte{0x00, 0xff, 0x10, 0x80}
	for _, base := range []string{"/5/0/0", "/19/0/0/3"} {
		n := []lwm2m.Node{lwm2m.ValueNode(p(base), lwm2m.Opaque(fw))}
		b, err := c.Encode(p(base), n)
		if err != nil || !bytes.Equal(b, fw) {
			t.Errorf("Encode(%s) = %x, %v", base, b, err)
		}
		got, err := c.Decode(p(base), b, typed(lwm2m.TypeOpaque, base != "/5/0/0"))
		if err != nil || !lwm2m.NodesEqual(got, n) {
			t.Errorf("Decode(%s) = %v, %v", base, got, err)
		}
	}
	// An empty payload is an empty opaque value.
	got, err := c.Decode(p("/5/0/0"), nil, nil)
	if err != nil || !lwm2m.NodesEqual(got, []lwm2m.Node{lwm2m.ValueNode(p("/5/0/0"), lwm2m.Opaque([]byte{}))}) {
		t.Errorf("empty payload = %v, %v", got, err)
	}
	// Opaque type only; resource or resource-instance paths only; one node.
	if _, err := c.Decode(p("/3/0/0"), []byte("x"), typed(lwm2m.TypeString, false)); err == nil {
		t.Error("decoded a string resource as opaque")
	}
	if _, err := c.Decode(p("/19/0/0"), []byte("x"), typed(lwm2m.TypeOpaque, true)); err == nil {
		t.Error("decoded a multi-instance resource path")
	}
	for _, base := range []string{"/", "/5", "/5/0"} {
		if _, err := c.Decode(p(base), fw, nil); err == nil {
			t.Errorf("decoded at %s", base)
		}
	}
	bad := [][]lwm2m.Node{
		nil,
		{lwm2m.ValueNode(p("/5/0/0"), lwm2m.String("x"))},
		{lwm2m.ValueNode(p("/5/0/1"), lwm2m.Opaque(fw))},
		{lwm2m.ValueNode(p("/5/0/0"), lwm2m.Opaque(fw)), lwm2m.ValueNode(p("/5/0/0"), lwm2m.Opaque(fw))},
	}
	for _, n := range bad {
		if b, err := c.Encode(p("/5/0/0"), n); err == nil {
			t.Errorf("Encode(%v) = %x, want error", n, b)
		}
	}
}
