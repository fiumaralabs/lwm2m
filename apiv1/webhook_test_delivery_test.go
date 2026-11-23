package apiv1

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebhookTestDelivery(t *testing.T) {
	var count atomic.Int32
	var secret string
	received := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		body, _ := io.ReadAll(r.Body)
		var event struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		_ = json.Unmarshal(body, &event)
		key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		mac := hmac.New(sha256.New, key)
		fmt.Fprintf(mac, "%s.%s.%s", r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), body)
		if event.Type != "lwm2m.webhook.test" || event.ID != r.Header.Get("webhook-id") || r.Header.Get("webhook-signature") != "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
			received <- "invalid"
		} else {
			received <- event.ID
		}
		w.WriteHeader(500)
	}))
	defer server.Close()
	k := Keys{entries: map[string]credential{
		"root":   {hash: sha256.Sum256([]byte("lwm_root_synthetic")), tenant: "default", scopes: ",admin,"},
		"other":  {hash: sha256.Sum256([]byte("lwm_other_synthetic")), tenant: "other", scopes: ",admin,"},
		"reader": {hash: sha256.Sum256([]byte("lwm_reader_synthetic")), tenant: "default", scopes: ",devices:read,"},
	}}
	h := New(Options{Keys: k})
	created := keyRequest(h, "POST", "/v1/webhooks", "lwm_root_synthetic", fmt.Sprintf(`{"url":%q,"types":["lwm2m.registration.*"]}`, server.URL))
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	var hook struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &hook); err != nil {
		t.Fatal(err)
	}
	secret = hook.Secret
	path := "/v1/webhooks/" + hook.ID + "/test"
	for _, tc := range []struct {
		path, token string
		want        int
	}{{"/v1/webhooks/missing/test", "lwm_root_synthetic", 404}, {path, "lwm_other_synthetic", 404}, {path, "lwm_reader_synthetic", 403}} {
		if w := keyRequest(h, "POST", tc.path, tc.token, ""); w.Code != tc.want {
			t.Errorf("%s: %d %s", tc.token, w.Code, w.Body)
		}
	}
	w := keyRequest(h, "POST", path, "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatalf("test: %d %s", w.Code, w.Body)
	}
	var result struct {
		EventID    string   `json:"eventId"`
		HTTPStatus int      `json:"httpStatus"`
		LatencyMS  *float64 `json:"latencyMs"`
		Error      string   `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.EventID == "" || result.EventID != <-received || result.HTTPStatus != 500 || result.LatencyMS == nil || result.Error == "" || count.Load() != 1 {
		t.Fatalf("result %+v deliveries %d", result, count.Load())
	}
	// An explicit test bypasses disabled status and event filters, without enabling the webhook.
	disabled := keyRequest(h, "PATCH", "/v1/webhooks/"+hook.ID, "lwm_root_synthetic", `{"status":"disabled"}`)
	if disabled.Code != 200 {
		t.Fatal(disabled.Code, disabled.Body)
	}
	w = keyRequest(h, "POST", path, "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.EventID != <-received || count.Load() != 2 {
		t.Fatal("disabled test not delivered once")
	}
	state := keyRequest(h, "GET", "/v1/webhooks/"+hook.ID, "lwm_root_synthetic", "")
	if !strings.Contains(state.Body.String(), `"status":"disabled"`) {
		t.Fatal(state.Body)
	}
	if w := keyRequest(h, "POST", "/v1/webhooks/"+hook.ID+"/rotate-secret/test", "lwm_root_synthetic", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}

}
