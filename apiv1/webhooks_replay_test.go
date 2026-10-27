package apiv1

import (
	"crypto/sha256"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestWebhooksRotateReplayConcurrent(t *testing.T) {
	h := keyHandler(t)
	created := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "shared-key", "")
	path := created.Header().Get("Location")
	var wg sync.WaitGroup
	replies := make([]*httptest.ResponseRecorder, 16)
	for i := range replies {
		wg.Go(func() { replies[i] = webhookRequest(h, "POST", path+"/rotate-secret", "", "shared-key", "") })
	}
	wg.Wait()
	first := replies[0]
	for _, w := range replies {
		if w.Code != 200 || w.Body.String() != first.Body.String() {
			t.Fatalf("rotation retry differed: %d %s", w.Code, w.Body)
		}
	}
	if first.Body.String() == created.Body.String() {
		t.Fatal("create key collided with rotate endpoint")
	}
	webhookRequest(h, "PATCH", path, `{"status":"disabled"}`, "", "")
	webhookRequest(h, "POST", path+"/rotate-secret", "", "", "")
	webhookRequest(h, "DELETE", path, "", "", "")
	if w := webhookRequest(h, "POST", path+"/rotate-secret", "", "shared-key", ""); w.Code != 200 || w.Body.String() != first.Body.String() {
		t.Fatalf("original rotation lost after mutation/deletion: %d %s", w.Code, w.Body)
	}
}

func TestWebhooksReplayTenantIsolation(t *testing.T) {
	k := Keys{entries: map[string]credential{}}
	for _, tenant := range []string{"root", "other"} {
		k.entries[tenant] = credential{hash: sha256.Sum256([]byte("lwm_" + tenant + "_synthetic")), tenant: tenant, scopes: ",admin,"}
	}
	h := New(Options{Keys: k})
	request := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/webhooks", strings.NewReader(webhookBody))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "same-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	a, b := request("lwm_root_synthetic"), request("lwm_other_synthetic")
	if a.Code != 201 || b.Code != 201 || a.Body.String() == b.Body.String() {
		t.Fatal("tenants shared replay")
	}
	for _, tc := range []struct {
		token    string
		original *httptest.ResponseRecorder
	}{{"lwm_root_synthetic", a}, {"lwm_other_synthetic", b}} {
		if w := request(tc.token); w.Body.String() != tc.original.Body.String() {
			t.Fatal("tenant retry changed")
		}
	}
}
