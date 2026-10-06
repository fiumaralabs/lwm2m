package coapws

import (
	"bytes"
	"testing"
)

// Proves: TCP-03
// WebSocket framing (no length, RFC 8323 §4.2) round-trips through the
// TCP framing (§3.2) at every Len boundary, and a partial TCP message is
// held until complete.
func TestFramingRoundTrip(t *testing.T) {
	for _, l := range []int{0, 12, 13, 268, 269, 65804, 65805, 70000} {
		ws := append([]byte{0x02, 0x45, 0xAA, 0xBB}, bytes.Repeat([]byte{0xFF}, l)...)
		tcp, err := ToTCP(ws)
		if err != nil {
			t.Fatal(err)
		}
		if _, n, _ := FromTCP(tcp[:len(tcp)-1]); n != 0 {
			t.Fatalf("len %d: partial message consumed", l)
		}
		back, n, err := FromTCP(append(tcp, 0x99))
		if err != nil || n != len(tcp) || !bytes.Equal(back, ws) {
			t.Fatalf("len %d: n=%d err=%v", l, n, err)
		}
	}
	if _, err := ToTCP([]byte{0x10, 0x45}); err == nil {
		t.Fatal("non-zero Len nibble accepted")
	}
}
