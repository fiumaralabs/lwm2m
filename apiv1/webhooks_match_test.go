package apiv1

import (
	"crypto/sha256"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhooksIfMatchHTTPConditions(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		want            int
	}{
		{"wildcard", "*", 200},
		{"list", `"old", TAG`, 200},
		{"quoted comma", `"old,version", TAG`, 200},
		{"empty list elements", `, , TAG,`, 200},
		{"OWS", "\t TAG \t", 200},
		{"weak", "W/TAG", 412},
		{"weak and strong", `W/TAG, TAG`, 200},
		{"stale", `"old"`, 412},
		{"wildcard list invalid", `*, TAG`, 412},
		{"trailing garbage", `TAG garbage`, 412},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := keyHandler(t)
			created := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "", "")
			path, tag := created.Header().Get("Location"), created.Header().Get("ETag")
			condition := strings.ReplaceAll(tc.condition, "TAG", tag)
			w := webhookRequest(h, "PATCH", path, `{}`, "", condition)
			if w.Code != tc.want {
				t.Fatalf("If-Match %q: got %d want %d", condition, w.Code, tc.want)
			}
		})
	}
	// Multiple field lines form a list and must not weaken a weak validator.
	h := keyHandler(t)
	created := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "", "")
	r := httptest.NewRequest("PATCH", created.Header().Get("Location"), strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer lwm_root_synthetic")
	r.Header.Add("If-Match", `W/`+created.Header().Get("ETag"))
	r.Header.Add("If-Match", created.Header().Get("ETag"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("multiple condition fields: %d", w.Code)
	}
}

func TestWebhooksForeignTenantNotFoundBeforeCondition(t *testing.T) {
	k := Keys{entries: map[string]credential{}}
	for _, tenant := range []string{"root", "other"} {
		k.entries[tenant] = credential{hash: sha256.Sum256([]byte("lwm_" + tenant + "_synthetic")), tenant: tenant, scopes: ",admin,"}
	}
	h := New(Options{Keys: k})
	created := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "", "")
	for _, method := range []string{"PATCH", "DELETE"} {
		for _, condition := range []string{`"stale"`, "*", created.Header().Get("ETag")} {
			r := httptest.NewRequest(method, created.Header().Get("Location"), strings.NewReader(`{}`))
			r.Header.Set("Authorization", "Bearer lwm_other_synthetic")
			r.Header.Set("If-Match", condition)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatalf("foreign tenant %s with %s: %d", method, condition, w.Code)
			}
		}
	}
}
