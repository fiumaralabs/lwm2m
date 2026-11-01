package apiv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestESTRouterEventsDoNotExposePrivateKey(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x credentials:write", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	log := &memory.Events{}
	h := New(Options{Keys: keys, EventOptions: EventOptions{Events: log}})
	cert, key := estTestPair(t)
	body, err := json.Marshal(map[string]any{"enabled": true, "issuer": map[string]string{"certificate": cert, "privateKey": key}, "validityDays": 365, "selector": "env=prod"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", "/v1/est", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("PUT: status %d", w.Code)
	}
	events, _, err := log.List("default", store.EventFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	escapedKey, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, escapedKey) || bytes.Contains(encoded, []byte("privateKey")) {
		t.Fatal("private key present in event history")
	}
}

func TestESTRouterTenantIsolation(t *testing.T) {
	entries := map[string]credential{}
	for _, tenant := range []string{"alpha", "beta"} {
		entries[tenant] = credential{hash: sha256.Sum256([]byte("lwm_" + tenant + "_secret")), tenant: tenant, scopes: ",credentials:write,"}
	}
	h := New(Options{Keys: Keys{entries: entries}})
	call := func(tenant, method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_"+tenant+"_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	cert, key := estTestPair(t)
	body, err := json.Marshal(map[string]any{"enabled": true, "issuer": map[string]string{"certificate": cert, "privateKey": key}, "validityDays": 365, "selector": "env=alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if w := call("alpha", "PUT", "/v1/est", body); w.Code != 201 {
		t.Fatalf("alpha PUT: %d", w.Code)
	}
	if w := call("beta", "GET", "/v1/est", nil); w.Code != 404 {
		t.Fatalf("beta GET before configuration: %d", w.Code)
	}
	cert2, key2 := estTestPair(t)
	body2, err := json.Marshal(map[string]any{"enabled": true, "issuer": map[string]string{"certificate": cert2, "privateKey": key2}, "validityDays": 30, "selector": "env=beta"})
	if err != nil {
		t.Fatal(err)
	}
	if w := call("beta", "PUT", "/v1/est", body2); w.Code != 201 {
		t.Fatalf("beta PUT: %d", w.Code)
	}
	for _, tc := range []struct{ tenant, cert, key string }{{"alpha", cert, key}, {"beta", cert2, key2}} {
		w := call(tc.tenant, "GET", "/v1/est", nil)
		var got struct {
			Issuer struct {
				Certificate string `json:"certificate"`
			} `json:"issuer"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Issuer.Certificate != tc.cert || strings.Contains(w.Body.String(), tc.key) || strings.Contains(w.Body.String(), "privateKey") {
			t.Fatalf("%s GET: status %d, wrong certificate or leaked key", tc.tenant, w.Code)
		}
		w = call(tc.tenant, "GET", "/v1/trust-anchors", nil)
		var page struct {
			Items []TrustAnchor `json:"items"`
		}
		certs, ok := parseTrustAnchorCertificates(tc.cert)
		if err := json.Unmarshal(w.Body.Bytes(), &page); !ok || err != nil || w.Code != 200 || len(page.Items) != 1 || len(page.Items[0].Certificates) != 1 || page.Items[0].Certificates[0].Fingerprint != certs[0].Fingerprint {
			t.Fatalf("%s anchors: status %d, wrong anchor", tc.tenant, w.Code)
		}
	}
}

func TestESTRouterRoundTrip(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x credentials:write", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys})
	cert, key := estTestPair(t)
	body, _ := json.Marshal(map[string]any{"enabled": true, "issuer": map[string]string{"certificate": cert, "privateKey": key}, "validityDays": 365, "selector": "env=prod"})
	call := func(method string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/est", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("PUT", body); w.Code != 201 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := call("GET", nil); w.Code != 200 || !strings.Contains(w.Body.String(), cert[:25]) || strings.Contains(w.Body.String(), "privateKey") {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	cert2, key2 := estTestPair(t)
	body2, _ := json.Marshal(map[string]any{"enabled": true, "issuer": map[string]string{"certificate": cert2, "privateKey": key2}, "validityDays": 365, "selector": "env=prod"})
	if w := call("PUT", body2); w.Code != 200 {
		t.Fatalf("replace = %d: %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/v1/trust-anchors", nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var page struct {
		Items []TrustAnchor `json:"items"`
	}
	certs, valid := parseTrustAnchorCertificates(cert2)
	if err := json.Unmarshal(w.Body.Bytes(), &page); !valid || err != nil || w.Code != 200 || len(page.Items) != 1 || len(page.Items[0].Certificates) != 1 || page.Items[0].Certificates[0].Fingerprint != certs[0].Fingerprint {
		t.Fatalf("anchors after replacement: %d %s, %v", w.Code, w.Body.String(), err)
	}
}
