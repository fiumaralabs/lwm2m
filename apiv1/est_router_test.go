package apiv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Items) != 1 {
		t.Fatalf("anchors after replacement: %d %s, %v", w.Code, w.Body.String(), err)
	}
}
