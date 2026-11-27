package apiv1

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func FuzzTrustAnchorCertificates(f *testing.F) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Unix(0, 0), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		f.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if certs, ok := parseTrustAnchorCertificates(ca); !ok || len(certs) != 1 {
		f.Fatal("valid CA seed was rejected")
	}
	for _, seed := range []string{ca, ca + ca, ca + "junk", "", "bad", "-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n", "-----BEGIN CERTIFICATE-----\ninvalid\n" + ca} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, bundle string) {
		certs, ok := parseTrustAnchorCertificates(bundle)
		if ok && len(certs) == 0 {
			t.Fatal("accepted an empty certificate bundle")
		}
	})
}

func FuzzBootstrapProfileValue(f *testing.F) {
	seeds := [][3]string{
		{"/23/0/1", "", "42"}, {"/23/0/2/0", "boolean", "true"},
		{"/1/0/1", "integer", "1"}, {"/1/0/1", "integer", `"bad"`},
		{"/1/0/1", "unsigned", "18446744073709551615"}, {"/1/0/1", "float", `"NaN"`},
		{"/1/0/1", "opaque", `"AQID"`}, {"/1/0/1", "time", `"2020-01-01T00:00:00Z"`},
		{"/1/0/1", "objlnk", `"1:2"`}, {"/1/0/1", "string", `"hello"`},
		{"/1/0/1", "corelnk", `"</1/0>"`}, {"/1/0", "", ""},
		{"/1/0/1", "boolean", "12"}, {"/3", "", "null"},
		{"", "unknown", "{"},
	}
	for _, seed := range seeds {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, path, kind, value string) {
		raw := json.RawMessage(value)
		_ = validBootstrapValue(kind, raw)
		if validProfileValue(path, kind, raw) && len(raw) > 0 && !validBootstrapValue(kind, raw) {
			t.Fatal("profile accepted a value rejected by bootstrap validation")
		}
	})
}

func FuzzPSKCredential(f *testing.F) {
	for _, seed := range []string{
		"hex:000102030405060708090a0b0c0d0e0f", "base64:AAECAwQFBgcICQoLDA0ODw==",
		"hex:000102030405060708090a0b0c0d0e", "base64:AAECAwQFBgcICQoLDA0O",
		"", "hex:", "base64:", "hex:zz", "base64:!", strings.Repeat("0", 32),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded string) {
		body, err := json.Marshal(map[string]any{"psk": map[string]string{"identity": "device", "key": encoded}})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
		_, _, key, generated, ok := decodePSKCredential(httptest.NewRecorder(), request)
		if ok && (generated || len(key) < 16) {
			t.Fatalf("accepted non-generated PSK with %d bytes (generated=%v)", len(key), generated)
		}
	})
}
