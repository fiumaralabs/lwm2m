package opaque_test

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec/opaque"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func FuzzDecode(f *testing.F) {
	vs, err := vectors.Load("opaque")
	if err != nil {
		f.Fatal(err)
	}
	for _, v := range vs {
		if !v.HasPayload() {
			continue
		}
		b, err := v.Payload()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	// opaque.json is currently empty; retain the firmware vector from TestOpaque.
	f.Add([]byte{0x00, 0xff, 0x10, 0x80})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, base := range []lwm2m.Path{lwm2m.NewPath(3, 0, 0), lwm2m.NewPath(3, 0, 6, 0)} {
			c := opaque.Codec{}
			nodes, err := c.Decode(base, b, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != 1 || nodes[0].Path != base || nodes[0].Value.Type != lwm2m.TypeOpaque || !bytes.Equal(nodes[0].Value.Bytes, b) {
				t.Fatalf("decode did not preserve opaque payload at %v", base)
			}
			out, err := c.Encode(base, nodes)
			if err != nil || !bytes.Equal(out, b) {
				t.Fatalf("opaque round trip: %v", err)
			}
			if len(b) > 0 {
				// Decode must own its bytes, not alias the caller's buffer.
				nodes[0].Value.Bytes[0] ^= 0xff
				if nodes[0].Value.Bytes[0] == b[0] {
					t.Fatal("decoded bytes alias input")
				}
			}
		}
	})
}
