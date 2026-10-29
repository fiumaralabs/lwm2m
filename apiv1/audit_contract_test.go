package apiv1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditMutations(t *testing.T) {
	h := keyHandler(t)
	for _, call := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"POST", "/v1/api-keys", `{"name":"sync","scopes":["devices:read"]}`, 201},
		{"DELETE", "/v1/devices/a", "", 204},
	} {
		w := keyRequest(h, call.method, call.path, "lwm_root_synthetic", call.body)
		if w.Code != call.status {
			t.Fatalf("%s %s: %d %s", call.method, call.path, w.Code, w.Body)
		}
	}
	w := keyRequest(h, "GET", "/v1/audit-log", "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatalf("audit: %d %s", w.Code, w.Body)
	}
	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("expected 3 entries: %s", w.Body)
	}
	for i, action := range []string{"device.delete", "api_key.create", "device.create"} {
		if page.Items[i].Action != action || !strings.HasPrefix(page.Items[i].ID, "aud_") {
			t.Fatalf("unexpected entry: %s", w.Body)
		}
	}
	if strings.Contains(w.Body.String(), "synthetic") {
		t.Fatal("secret leaked")
	}
}
