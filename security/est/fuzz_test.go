package est_test

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m/security/est"
)

func FuzzParseCertsOnly(f *testing.F) {
	// The same CA fixture used by the EST integration tests supplies a real certificate.
	ca, err := est.NewTestCA("EST CA")
	if err != nil {
		f.Fatal(err)
	}
	seed, err := est.CertsOnly(ca.Cert)
	if err != nil {
		f.Fatal(err)
	}
	empty, err := est.CertsOnly()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add(empty)
	f.Add(ca.Cert.Raw)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, input []byte) {
		certs, err := est.ParseCertsOnly(input)
		if err != nil {
			return
		}
		encoded, err := est.CertsOnly(certs...)
		if err != nil {
			t.Fatal(err)
		}
		again, err := est.ParseCertsOnly(encoded)
		if err != nil || len(certs) != len(again) {
			t.Fatalf("certificates round trip: %d => %d, %v", len(certs), len(again), err)
		}
		for i := range certs {
			if !bytes.Equal(certs[i].Raw, again[i].Raw) {
				t.Fatalf("certificate %d changed", i)
			}
		}
	})
}
