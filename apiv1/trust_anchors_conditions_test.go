package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestTrustAnchorReplayAndConditions(t *testing.T) {
	s := NewMemoryTrustAnchorStore(100)
	h := NewTrustAnchorHandler(s)
	ca := anchorCertificate(t, true, time.Now().Add(time.Hour))
	body, _ := json.Marshal(map[string]string{"name": "ca", "certificates": ca})
	request := func(method, path, tenant, key, match string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: ",credentials:write,"}))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const path = "/v1/trust-anchors"
	const n = 16
	results := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i] = request("POST", path, "a", "key", "", body) }(i)
	}
	wg.Wait()
	first := results[0]
	if first.Code != 201 || first.Header().Get("ETag") == "" {
		t.Fatalf("create: %d tag %q", first.Code, first.Header().Get("ETag"))
	}
	for _, w := range results {
		if w.Code != 201 || w.Body.String() != first.Body.String() || w.Header().Get("Location") != first.Header().Get("Location") || w.Header().Get("ETag") != first.Header().Get("ETag") {
			t.Fatalf("replay %d %s", w.Code, w.Body)
		}
	}
	resource, tag := first.Header().Get("Location"), first.Header().Get("ETag")
	if got := request("GET", resource, "a", "", "", nil); got.Code != 200 || got.Header().Get("ETag") != tag {
		t.Fatalf("get %d %q", got.Code, got.Header().Get("ETag"))
	}
	if got := request("DELETE", resource, "b", "", tag, nil); got.Code != 404 {
		t.Fatalf("foreign %d", got.Code)
	}
	if got := request("DELETE", resource, "a", "", `"wrong"`, nil); got.Code != 412 {
		t.Fatalf("mismatch %d", got.Code)
	}
	if got := request("DELETE", resource, "a", "", tag, nil); got.Code != 204 {
		t.Fatalf("delete %d", got.Code)
	}
	if got := request("POST", path, "a", "key", "", []byte("invalid")); got.Code != 201 || got.Body.String() != first.Body.String() {
		t.Fatalf("replay after deletion %d %s", got.Code, got.Body)
	}
	if got := request("POST", path, "b", "key", "", body); got.Code != 201 || got.Body.String() == first.Body.String() {
		t.Fatalf("tenant replay %d", got.Code)
	}
	bad := request("POST", path, "a", "bad-key", "", []byte("invalid"))
	if replay := request("POST", path, "a", "bad-key", "", body); bad.Code != 400 || replay.Code != bad.Code || replay.Body.String() != bad.Body.String() {
		t.Fatalf("failure replay: %d %d", bad.Code, replay.Code)
	}
	created := request("POST", path, "c", "", "", body)
	deletePath, deleteTag := created.Header().Get("Location"), created.Header().Get("ETag")
	deletes := make([]*httptest.ResponseRecorder, n)
	for i := range deletes {
		wg.Add(1)
		go func(i int) { defer wg.Done(); deletes[i] = request("DELETE", deletePath, "c", "", deleteTag, nil) }(i)
	}
	wg.Wait()
	success := 0
	for _, w := range deletes {
		if w.Code == 204 {
			success++
		} else if w.Code != 404 {
			t.Fatalf("concurrent delete: %d", w.Code)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent successful deletes: %d", success)
	}
}
