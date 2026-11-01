package apiv1

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDeviceCreateIdempotencyConcurrentTenants(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"a": {hash: sha256.Sum256([]byte("lwm_a_secret")), scopes: ",devices:read,devices:write,", tenant: "a"},
		"b": {hash: sha256.Sum256([]byte("lwm_b_secret")), scopes: ",devices:read,devices:write,", tenant: "b"},
	}}
	h := New(Options{Keys: keys})
	const n = 32
	out := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(`{"endpoint":"a"}`))
			r.Header.Set("Authorization", "Bearer lwm_a_secret")
			r.Header.Set("Idempotency-Key", "shared")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			out[i] = w
		}(i)
	}
	wg.Wait()
	for i, w := range out {
		if w.Code != 201 || w.Body.String() != out[0].Body.String() {
			t.Fatalf("retry %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(`{"endpoint":"b"}`))
	r.Header.Set("Authorization", "Bearer lwm_b_secret")
	r.Header.Set("Idempotency-Key", "shared")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"endpoint":"b"`) {
		t.Fatal(fmt.Sprintf("other tenant: %d %s", w.Code, w.Body.String()))
	}
}
