package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestESTIsolationAndFailures(t *testing.T) {
	anchors := NewMemoryTrustAnchorStore(2)
	configs := NewMemoryESTConfigStore()
	h := NewESTConfigHandler(configs, anchors)
	call := func(method, tenant, scope string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/est", bytes.NewReader(b))
		r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: "," + scope + ","}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	c, k := estTestPair(t)
	c2, k2 := estTestPair(t)
	c3, k3 := estTestPair(t)
	data := func(cert, key, sel string) map[string]any {
		return map[string]any{"enabled": true, "issuer": map[string]string{"certificate": cert, "privateKey": key}, "validityDays": 1, "selector": sel}
	}
	if w := call("PUT", "a", "devices:read", data(c, k, "a")); w.Code != 403 {
		t.Fatalf("write scope: %d", w.Code)
	}
	for _, tc := range []struct{ tenant, cert, key, sel string }{{"a", c, k, "a"}, {"b", c2, k2, "b"}} {
		if w := call("PUT", tc.tenant, "credentials:write", data(tc.cert, tc.key, tc.sel)); w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	if w := call("GET", "a", "credentials:write", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"selector":"a"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("get response: %d %s cache=%s", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	if w := call("PUT", "a", "credentials:write", data(c3, k3, "new")); w.Code != 500 {
		t.Fatalf("anchor limit: %d", w.Code)
	}
	if w := call("GET", "a", "credentials:write", nil); !strings.Contains(w.Body.String(), `"selector":"a"`) {
		t.Fatalf("changed on failure: %s", w.Body.String())
	}
	if len(anchors.List("a")) != 1 || len(anchors.List("b")) != 1 {
		t.Fatal("anchor isolation failed")
	}
}
