package apiv1

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

func TestWebhookRetryDelivery(t *testing.T) {
	var secret string
	bodies := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		mac := hmac.New(sha256.New, key)
		fmt.Fprintf(mac, "%s.%s.%s", r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), b)
		if r.Header.Get("webhook-signature") != "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
			t.Error("bad signature")
		}
		bodies <- b
		w.WriteHeader(204)
	}))
	defer server.Close()
	keys := Keys{entries: map[string]credential{
		"root":   {hash: sha256.Sum256([]byte("lwm_root_synthetic")), tenant: "default", scopes: ",admin,"},
		"other":  {hash: sha256.Sum256([]byte("lwm_other_synthetic")), tenant: "other", scopes: ",admin,"},
		"reader": {hash: sha256.Sum256([]byte("lwm_reader_synthetic")), tenant: "default", scopes: ",devices:read,"},
	}}
	h := New(Options{Keys: keys})
	created := keyRequest(h, "POST", "/v1/webhooks", "lwm_root_synthetic", fmt.Sprintf(`{"url":%q,"types":["lwm2m.registration.*"]}`, server.URL))
	var hook struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &hook) != nil {
		t.Fatal(created.Code, created.Body)
	}
	secret = hook.Secret
	base := "/v1/webhooks/" + hook.ID
	if w := keyRequest(h, "POST", base+"/test", "lwm_root_synthetic", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	original := <-bodies
	list := keyRequest(h, "GET", base+"/deliveries", "lwm_root_synthetic", "")
	var deliveries struct {
		Items []store.WebhookDelivery `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &deliveries); err != nil || len(deliveries.Items) != 1 {
		t.Fatal(list.Code, list.Body, err)
	}
	path := base + "/deliveries/" + deliveries.Items[0].ID + "/retry"
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/v1/webhooks/missing/deliveries/x/retry", "lwm_root_synthetic", 404},
		{base + "/deliveries/missing/retry", "lwm_root_synthetic", 404},
		{path, "lwm_other_synthetic", 404}, {path, "lwm_reader_synthetic", 403},
	} {
		if w := keyRequest(h, "POST", tc.path, tc.token, ""); w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	if w := keyRequest(h, "PATCH", base, "lwm_root_synthetic", `{"status":"disabled"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w := keyRequest(h, "POST", path, "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	var result struct {
		EventID    string   `json:"eventId"`
		HTTPStatus int      `json:"httpStatus"`
		LatencyMS  *float64 `json:"latencyMs"`
		Error      string   `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.EventID != deliveries.Items[0].EventID || result.HTTPStatus != 204 || result.LatencyMS == nil || result.Error != "" {
		t.Fatalf("invalid retry response: %s (%v)", w.Body, err)
	}
	if got := <-bodies; !bytes.Equal(got, original) {
		t.Fatalf("event changed: %s != %s", got, original)
	}
	list = keyRequest(h, "GET", base+"/deliveries", "lwm_root_synthetic", "")
	if err := json.Unmarshal(list.Body.Bytes(), &deliveries); err != nil || len(deliveries.Items) != 2 || deliveries.Items[0].ID == deliveries.Items[1].ID {
		t.Fatal(list.Body, err)
	}
}
