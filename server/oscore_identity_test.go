package server

import "testing"

func TestOSCOREIdentity(t *testing.T) {
	a := Identity{Mode: ModeOSCORE, OSCOREIDContext: []byte{1}, OSCORESenderID: []byte{2}, OSCOREEndpoint: "ep"}
	if !a.Secure() || a.Mode.String() != "oscore" {
		t.Fatal("OSCORE must be a secure object identity")
	}
	for _, tc := range []struct {
		name  string
		b     Identity
		equal bool
	}{
		{"same", Identity{Mode: ModeOSCORE, OSCOREIDContext: []byte{1}, OSCORESenderID: []byte{2}, OSCOREEndpoint: "other"}, true},
		{"context", Identity{Mode: ModeOSCORE, OSCOREIDContext: []byte{3}, OSCORESenderID: []byte{2}}, false},
		{"sender", Identity{Mode: ModeOSCORE, OSCOREIDContext: []byte{1}, OSCORESenderID: []byte{3}}, false},
		{"mode", Identity{Mode: ModeNoSec}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if a.Equal(tc.b) != tc.equal || tc.b.Equal(a) != tc.equal {
				t.Fatal("identity comparison")
			}
		})
	}
}
