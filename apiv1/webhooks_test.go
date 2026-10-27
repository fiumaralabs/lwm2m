package apiv1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const webhookBody = `{"url":"https://example.com/hook","types":["lwm2m.registration.*"],"selector":"env=prod"}`

func webhookCreate(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	w := keyRequest(h, "POST", "/v1/webhooks", "lwm_root_synthetic", webhookBody)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestWebhooksLifecycle(t *testing.T) {
	h := keyHandler(t)
	v := webhookCreate(t, h)
	path := "/v1/webhooks/" + v["id"].(string)
	secret := v["secret"].(string)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(raw) != 32 || !strings.HasPrefix(secret, "whsec_") {
		t.Fatal("invalid secret")
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", path, "", 200}, {"GET", "/v1/webhooks", "", 200}, {"PATCH", path, `{"url":"http://example.com/new","types":["lwm2m.notification"],"status":"disabled","selector":null}`, 200}, {"POST", path + "/rotate-secret", "", 200}, {"DELETE", path, "", 204}, {"GET", path, "", 404},
	} {
		w := keyRequest(h, tc.method, tc.path, "lwm_root_synthetic", tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.method, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), secret) || (tc.method != "POST" && strings.Contains(w.Body.String(), `"secret"`)) || strings.Contains(w.Body.String(), "previousSecrets") {
			t.Fatal("secret leaked")
		}
		if tc.method == "PATCH" && (!strings.Contains(w.Body.String(), `"selector":""`) || strings.Contains(w.Body.String(), `"secret"`)) {
			t.Fatal(w.Body)
		}
		if tc.path == path+"/rotate-secret" && !strings.Contains(w.Body.String(), `"secret":"whsec_`) {
			t.Fatal("missing rotated secret")
		}
	}
}
func TestWebhooksValidation(t *testing.T) {
	h := keyHandler(t)
	path := "/v1/webhooks/" + webhookCreate(t, h)["id"].(string)
	for _, body := range []string{`null`, `[]`, `{} {}`, `{"unknown":1}`, `{"url":null}`, `{"types":null}`, `{"status":null}`, `{"url":""}`, `{"url":"/relative"}`, `{"url":"ftp://example.com"}`, `{"url":"https://u:p@example.com"}`, `{"url":"https://example.com/#x"}`, `{"url":"https://example.com/#"}`, `{"types":[]}`, `{"types":["other.event"]}`, `{"types":["lwm2m..event"]}`, `{"types":["lwm2m.*.x"]}`, `{"types":["lwm2m"]}`, `{"types":["lwm2m.e vent"]}`, `{"status":"unknown"}`, `{"selector":"env in ("}`, `{"selector":42}`} {
		for _, method := range []string{"POST", "PATCH"} {
			target := "/v1/webhooks"
			if method == "PATCH" {
				target = path
			}
			w := keyRequest(h, method, target, "lwm_root_synthetic", body)
			if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("%s %s: %d %s", method, body, w.Code, w.Body)
			}
			if strings.Contains(body, `"selector"`) && !strings.Contains(w.Body.String(), `"code":"invalid_selector"`) {
				t.Error(w.Body)
			}
		}
	}
	for _, body := range []string{`{}`, `{"url":"https://example.com"}`, `{"types":["lwm2m.*"]}`} {
		if w := keyRequest(h, "POST", "/v1/webhooks", "lwm_root_synthetic", body); w.Code != 400 {
			t.Errorf("missing fields: %s %d", body, w.Code)
		}
	}
}
func TestWebhooksAccessPagination(t *testing.T) {
	k := Keys{entries: map[string]credential{}}
	for _, entry := range []struct{ id, tenant, scopes string }{{"root", "default", ",admin,"}, {"other", "other", ",admin,"}, {"reader", "default", ",devices:read,"}} {
		k.entries[entry.id] = credential{hash: sha256.Sum256([]byte("lwm_" + entry.id + "_synthetic")), tenant: entry.tenant, scopes: entry.scopes}
	}
	h := New(Options{Keys: k})
	v := webhookCreate(t, h)
	path := "/v1/webhooks/" + v["id"].(string)
	for _, tc := range []struct{ method, path, body string }{{"GET", "/v1/webhooks", ""}, {"POST", "/v1/webhooks", webhookBody}, {"GET", path, ""}, {"PATCH", path, `{}`}, {"DELETE", path, ""}, {"POST", path + "/rotate-secret", ""}} {
		for _, token := range []string{"lwm_reader_synthetic", ""} {
			want := 403
			if token == "" {
				want = 401
			}
			if w := keyRequest(h, tc.method, tc.path, token, tc.body); w.Code != want {
				t.Errorf("scope %s %s: %d", tc.method, tc.path, w.Code)
			}
		}
		if tc.path != "/v1/webhooks" {
			if w := keyRequest(h, tc.method, tc.path, "lwm_other_synthetic", tc.body); w.Code != 404 {
				t.Errorf("tenant %s: %d", tc.method, w.Code)
			}
		}
	}
	if w := keyRequest(h, "GET", "/v1/webhooks", "lwm_other_synthetic", ""); w.Code != 200 || strings.Contains(w.Body.String(), v["id"].(string)) {
		t.Fatal("tenant list", w.Body)
	}
	for i := 0; i < 50; i++ {
		webhookCreate(t, h)
	}
	var page struct {
		Items []map[string]any
		Next  string `json:"nextCursor"`
	}
	w := keyRequest(h, "GET", "/v1/webhooks", "lwm_root_synthetic", "")
	json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 50 || page.Next == "" {
		t.Fatal("default pagination", w.Body)
	}
	cursor := page.Next
	last := page.Items[49]["id"].(string)
	page.Next = ""
	w = keyRequest(h, "GET", "/v1/webhooks?cursor="+url.QueryEscape(cursor), "lwm_root_synthetic", "")
	json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Next != "" || page.Items[0]["id"].(string) <= last {
		t.Fatal("continuation", w.Body)
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=x", "limit=", "cursor=", "cursor=bad", "cursor=" + base64.RawURLEncoding.EncodeToString([]byte("null"))} {
		w := keyRequest(h, "GET", "/v1/webhooks?"+q, "lwm_root_synthetic", "")
		if w.Code != 400 {
			t.Errorf("query %s: %d", q, w.Code)
		}
	}
	if w := keyRequest(h, "GET", "/v1/webhooks?cursor="+url.QueryEscape(cursor), "lwm_other_synthetic", ""); w.Code != 400 {
		t.Fatal("foreign cursor", w.Code)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"PUT", "/v1/webhooks", 405}, {"POST", path, 405}, {"GET", path + "/rotate-secret", 405}, {"POST", path + "/test", 404}, {"GET", path + "/deliveries", 404}, {"GET", "/v1/webhooks/", 404}, {"PATCH", "/v1/webhooks/missing", 404}, {"DELETE", "/v1/webhooks/missing", 404}, {"POST", "/v1/webhooks/missing/rotate-secret", 404}} {
		w := keyRequest(h, tc.method, tc.path, "lwm_root_synthetic", `{}`)
		if w.Code != tc.want || w.Header().Get("Content-Type") != "application/problem+json" {
			t.Error(fmt.Sprint(tc), w.Code, w.Body)
		}
	}
}
func TestWebhooksCreate(t *testing.T) {
	w := keyRequest(keyHandler(t), "POST", "/v1/webhooks", "lwm_root_synthetic", `{"url":"https://example.com/hook","types":["lwm2m.registration.*"],"selector":"env=prod"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "url", "types", "selector", "status", "createdAt", "updatedAt", "secret"} {
		if v[field] == nil {
			t.Errorf("missing %s", field)
		}
	}
}
