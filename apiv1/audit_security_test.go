package apiv1

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestAuditRedactsCredentialPaths(t *testing.T) {
	h := keyHandler(t)
	created := keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"name":"test","scopes":["devices:read"]}`)
	var key struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil || key.Token == "" {
		t.Fatal("key creation failed")
	}
	secret := key.Token
	for _, path := range []string{"/v1/devices/" + secret, "/v1/webhooks/" + secret + "/rotate-secret", "/v1/unknown/" + secret, "/v1/devices/" + url.PathEscape(secret) + "/operations"} {
		keyRequest(h, "DELETE", path, "lwm_root_synthetic", "")
	}
	log := keyRequest(h, "GET", "/v1/audit-log", "lwm_root_synthetic", "").Body.String()
	if strings.Contains(log, secret) || strings.Contains(log, "lwm_root_synthetic") {
		t.Fatal("credential leaked through audit path")
	}
}

func TestAuditWebhookRotationActionFilter(t *testing.T) {
	h := keyHandler(t)
	hook := webhookCreate(t, h)
	path := "/v1/webhooks/" + hook["id"].(string) + "/rotate-secret"
	if w := keyRequest(h, "POST", path, "lwm_root_synthetic", ""); w.Code != 200 {
		t.Fatalf("rotate: %d", w.Code)
	}
	w := keyRequest(h, "GET", "/v1/audit-log?action=webhook.rotate_secret", "lwm_root_synthetic", "")
	var p struct {
		Items []AuditEntry `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || len(p.Items) != 1 || p.Items[0].Action != "webhook.rotate_secret" {
		t.Fatalf("rotation filter: %s", w.Body)
	}
	w = keyRequest(h, "GET", "/v1/audit-log?action=webhook.create", "lwm_root_synthetic", "")
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || len(p.Items) != 1 {
		t.Fatalf("create filter: %s", w.Body)
	}
}
