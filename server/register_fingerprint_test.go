package server

import (
	"crypto/x509"
	"testing"
)

func TestRegisterFingerprintIdentityAndToken(t *testing.T) {
	m := &Message{Query: []string{"ep=node", "lt=600", "lwm2m=1.1"}, Payload: []byte("</3/0>"), Token: []byte{1}}
	for _, tc := range []struct {
		name         string
		first, other Identity
	}{
		{"psk", Identity{Mode: ModePSK, PSKIdentity: "one"}, Identity{Mode: ModePSK, PSKIdentity: "two"}},
		{"rpk", Identity{Mode: ModeRPK, PublicKey: []byte{1}}, Identity{Mode: ModeRPK, PublicKey: []byte{2}}},
		{"x509", Identity{Mode: ModeX509, Cert: &x509.Certificate{Raw: []byte{1}}}, Identity{Mode: ModeX509, Cert: &x509.Certificate{Raw: []byte{2}}}},
		{"nosec", Identity{Mode: ModeNoSec, Addr: "one"}, Identity{Mode: ModeNoSec, Addr: "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := registerFingerprint("node", tc.first, m)
			if first == registerFingerprint("node", tc.other, m) {
				t.Fatal("identity did not affect fingerprint")
			}
			changed := *m
			changed.Token = []byte{2}
			if first != registerFingerprint("node", tc.first, &changed) {
				t.Fatal("token affected fingerprint")
			}
		})
	}
}
