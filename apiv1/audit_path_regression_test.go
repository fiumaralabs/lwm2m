package apiv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestAuditResourceActionableAndFailedPathsRedacted(t *testing.T) {
	h := keyHandler(t)
	if w := keyRequest(h, "POST", "/v1/devices", "lwm_root_synthetic", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := keyRequest(h, "DELETE", "/v1/devices/a", "lwm_root_synthetic", ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	secret := "psk-sensitive"
	for _, path := range []string{"/v1/devices/" + secret, "/v1/unknown/" + secret, "/v1/devices/a/" + secret} {
		if w := keyRequest(h, "DELETE", path, "lwm_root_synthetic", ""); w.Code < 400 {
			t.Fatalf("expected failure: %s %d", path, w.Code)
		}
	}
	w := keyRequest(h, "GET", "/v1/audit-log", "lwm_root_synthetic", "")
	var p struct {
		Items []AuditEntry `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range p.Items {
		if e.Action == "device.delete" && e.Status == 204 && e.Resource == "/v1/devices/a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing actionable deletion path: %s", w.Body)
	}
	sum := sha256.Sum256([]byte(secret))
	if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), hex.EncodeToString(sum[:])) {
		t.Fatal("failed path leaked credential or unsalted hash")
	}
	filtered := keyRequest(h, "GET", "/v1/audit-log?resource="+url.QueryEscape("/v1/devices/a"), "lwm_root_synthetic", "")
	if err := json.Unmarshal(filtered.Body.Bytes(), &p); err != nil || len(p.Items) != 1 || p.Items[0].Resource != "/v1/devices/a" {
		t.Fatalf("filter: %s", filtered.Body)
	}
}
