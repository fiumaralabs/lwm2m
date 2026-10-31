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
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func estTestPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k}))
}
func TestESTConfigLifecycle(t *testing.T) {
	anchors := NewMemoryTrustAnchorStore(20)
	h := NewESTConfigHandler(NewMemoryESTConfigStore(), anchors)
	call := func(method, tenant, scope string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/est", bytes.NewReader(b))
		r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: "," + scope + ","}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	cert, key := estTestPair(t)
	_, otherKey := estTestPair(t)
	payload := func(c, k string, days int, sel string) map[string]any {
		return map[string]any{"enabled": true, "issuer": map[string]string{"certificate": c, "privateKey": k}, "validityDays": days, "selector": sel}
	}
	good := payload(cert, key, 365, "env=prod")
	for i := 0; i < 2; i++ {
		w := call("PUT", "a", "credentials:write", good)
		if w.Code != 200 && w.Code != 201 {
			t.Fatalf("put %d: %d %s", i, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "privateKey") || strings.Contains(w.Body.String(), key) {
			t.Fatal("key leaked on PUT")
		}
	}
	if len(anchors.List("a")) != 1 {
		t.Fatalf("anchors: %d", len(anchors.List("a")))
	}
	w := call("GET", "a", "credentials:write", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "privateKey") || !strings.Contains(w.Body.String(), "env=prod") {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	if w = call("GET", "b", "credentials:write", nil); w.Code != 404 {
		t.Fatalf("tenant: %d", w.Code)
	}
	if w = call("GET", "a", "devices:read", nil); w.Code != 403 {
		t.Fatalf("scope: %d", w.Code)
	}
	for _, p := range []map[string]any{payload("bad", key, 365, "x=y"), payload(cert, otherKey, 365, "x=y"), payload(cert, key, 0, "x=y"), payload(cert, key, 3651, "x=y"), payload(cert, key, 365, "x in (")} {
		if w = call("PUT", "a", "credentials:write", p); w.Code != 400 {
			t.Fatalf("invalid: %d %s", w.Code, w.Body.String())
		}
	}
	replacement := payload(cert, key, 10, "env=dev")
	if w = call("PUT", "a", "credentials:write", replacement); w.Code != 200 {
		t.Fatalf("replace: %d %s", w.Code, w.Body.String())
	}
	if w = call("GET", "a", "credentials:write", nil); !strings.Contains(w.Body.String(), "env=dev") {
		t.Fatalf("replacement: %s", w.Body.String())
	}
}
