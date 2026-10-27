package apiv1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func webhookRequest(h http.Handler, method, path, body, key, match string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer lwm_root_synthetic")
	r.Header.Set("Idempotency-Key", key)
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWebhooksCreateReplayConcurrent(t *testing.T) {
	h := keyHandler(t)
	var wg sync.WaitGroup
	replies := make([]*httptest.ResponseRecorder, 16)
	for i := range replies {
		wg.Go(func() { replies[i] = webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "create-key", "") })
	}
	wg.Wait()
	first := replies[0]
	for _, w := range replies {
		if w.Code != 201 || w.Body.String() != first.Body.String() || w.Header().Get("Location") != first.Header().Get("Location") {
			t.Fatalf("create retry differed: %d %s", w.Code, w.Body)
		}
	}
	w := webhookRequest(h, "GET", "/v1/webhooks", "", "", "")
	if strings.Count(w.Body.String(), `"id":`) != 1 {
		t.Fatalf("duplicate effects: %s", w.Body)
	}
	webhookRequest(h, "PATCH", first.Header().Get("Location"), `{"status":"disabled"}`, "", "")
	replay := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "create-key", "")
	if replay.Body.String() != first.Body.String() {
		t.Fatal("replay was not original snapshot")
	}
}
