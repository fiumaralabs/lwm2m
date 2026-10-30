package cbor

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func FuzzDecode(f *testing.F) {
	vs, err := vectors.Load("cbor")
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
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		base := lwm2m.MustParsePath("/3/0/13")
		c := Codec{}
		nodes, err := c.Decode(base, b, nil)
		if err != nil {
			return
		}
		encoded, err := c.Encode(base, nodes)
		if err != nil {
			t.Fatalf("encode decoded value: %v", err)
		}
		again, err := c.Decode(base, encoded, nil)
		if err != nil {
			t.Fatalf("decode encoded value: %v", err)
		}
		reencoded, err := c.Encode(base, again)
		if err != nil || !bytes.Equal(encoded, reencoded) {
			t.Fatalf("CBOR round trip: %x != %x, error %v", encoded, reencoded, err)
		}
	})
}
