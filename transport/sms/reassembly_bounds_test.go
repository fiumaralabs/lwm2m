package sms

import "testing"

func TestReassemblyBoundsPendingSets(t *testing.T) {
	b := New(Config{})
	for i := 0; i < 1025; i++ {
		b.reassemble(string(rune(i+1)), SMS{UDH: []byte{0, 3, 1, 2, 1}, Data: []byte("first")})
	}
	if len(b.parts) > 1024 {
		t.Fatalf("pending sets = %d, want at most 1024", len(b.parts))
	}
}
