package apiv1

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func anchorCertificate(t *testing.T, ca bool, expiry time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expiry, BasicConstraintsValid: true, IsCA: ca, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
func TestTrustAnchorEndpointContract(t *testing.T) {
	h := NewTrustAnchorHandler(NewMemoryTrustAnchorStore(20))
	request := func(method, path, tenant, scope string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: "," + scope + ","}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	ca := anchorCertificate(t, true, time.Now().Add(time.Hour))
	other := anchorCertificate(t, true, time.Now().Add(time.Hour))
	payload := func(p string) any { return map[string]string{"name": "factory", "certificates": p} }
	post := func(p string) *httptest.ResponseRecorder {
		return request("POST", "/v1/trust-anchors", "a", "credentials:write", payload(p))
	}
	for _, p := range []string{"bad", anchorCertificate(t, false, time.Now().Add(time.Hour)), anchorCertificate(t, true, time.Now().Add(-time.Minute)), ca + "junk", "-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n" + ca, "-----BEGIN CERTIFICATE-----\ninvalid\n" + ca} {
		w := post(p)
		if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("invalid: %d %s", w.Code, w.Body.String())
		}
	}
	w := post(ca + other)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var anchor TrustAnchor
	if err := json.Unmarshal(w.Body.Bytes(), &anchor); err != nil || anchor.ID == "" || len(anchor.Certificates) != 2 || anchor.Certificates[0].Fingerprint == "" {
		t.Fatalf("body: %s %v", w.Body.String(), err)
	}
	if w = post(ca); w.Code != 409 {
		t.Fatalf("duplicate %d", w.Code)
	}
	path := "/v1/trust-anchors/" + anchor.ID
	for _, method := range []string{"GET", "DELETE"} {
		if w = request(method, path, "b", "credentials:write", nil); w.Code != 404 {
			t.Fatalf("foreign %s %d", method, w.Code)
		}
	}
	if w = request("GET", path, "a", "credentials:write", nil); w.Code != 200 {
		t.Fatalf("get %d", w.Code)
	}
	if w = request("GET", "/v1/trust-anchors?limit=1", "a", "credentials:write", nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(anchor.ID)) {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	if w = request("POST", "/v1/trust-anchors", "a", "devices:read", payload(ca)); w.Code != 403 {
		t.Fatalf("scope %d", w.Code)
	}
	if w = request("DELETE", path, "a", "credentials:write", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if w = request("GET", path, "a", "credentials:write", nil); w.Code != 404 {
		t.Fatalf("deleted %d", w.Code)
	}
}
