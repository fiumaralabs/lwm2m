package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuditFiltersAndIsolation(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"root":   {hash: sha256.Sum256([]byte("lwm_root_synthetic")), scopes: ",admin,", tenant: "one"},
		"other":  {hash: sha256.Sum256([]byte("lwm_other_synthetic")), scopes: ",admin,", tenant: "two"},
		"reader": {hash: sha256.Sum256([]byte("lwm_reader_synthetic")), scopes: ",devices:read,", tenant: "one"},
	}}
	h := New(Options{Keys: keys})
	before := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/devices", `{"endpoint":"a"}`},
		{"POST", "/v1/api-keys", `{"name":"sync","scopes":["devices:read"]}`},
		{"DELETE", "/v1/devices/a", ""},
	} {
		w := keyRequest(h, c.method, c.path, "lwm_root_synthetic", c.body)
		if w.Code >= 300 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	after := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	type page struct {
		Items []AuditEntry `json:"items"`
		Next  string       `json:"nextCursor"`
	}
	get := func(query, token string) page {
		t.Helper()
		w := keyRequest(h, "GET", "/v1/audit-log"+query, token, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body)
		}
		var p page
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := get("?limit=2", "lwm_root_synthetic")
	if len(p.Items) != 2 || p.Next == "" {
		t.Fatal(p)
	}
	if p.Items[0].Actor != (AuditActor{Kind: "apikey", ID: "root", Name: "root"}) || p.Items[0].Status != 204 || p.Items[0].IP != "192.0.2.1" || p.Items[0].Time.IsZero() {
		t.Fatalf("incorrect audit metadata: %+v", p.Items[0])
	}
	p2 := get("?limit=2&cursor="+url.QueryEscape(p.Next), "lwm_root_synthetic")
	if len(p2.Items) != 1 || p2.Next != "" || p2.Items[0].ID == p.Items[0].ID {
		t.Fatal(p2)
	}
	for query, n := range map[string]int{"?actor=root": 3, "?actor=other": 0, "?action=device.delete": 1, "?resource=" + url.QueryEscape("/v1/devices/a"): 1, "?from=" + url.QueryEscape(before) + "&to=" + url.QueryEscape(after): 3, "?to=2000-01-01T00:00:00Z": 0} {
		if got := get(query, "lwm_root_synthetic"); len(got.Items) != n {
			t.Fatalf("%s: %v", query, got)
		}
	}
	if got := get("", "lwm_other_synthetic"); len(got.Items) != 0 {
		t.Fatal(got)
	}
	for _, query := range []string{"?limit=0", "?cursor=bad", "?from=invalid", "?limit=1&limit=2", "?actor=other&cursor=" + url.QueryEscape(p.Next)} {
		w := keyRequest(h, "GET", "/v1/audit-log"+query, "lwm_root_synthetic", "")
		if w.Code != 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body)
		}
	}
	if w := keyRequest(h, "GET", "/v1/audit-log", "lwm_reader_synthetic", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestAuditNeverCopiesSecrets(t *testing.T) {
	h := keyHandler(t)
	w := keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"name":"test","scopes":["devices:read"]}`)
	var key struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil || key.Token == "" {
		t.Fatal("key creation failed")
	}
	for _, body := range []string{`{"secret":"webhook-sensitive"}`, `{"psk":"psk-sensitive"}`} {
		keyRequest(h, "POST", "/v1/devices", "lwm_root_synthetic", body)
	}
	log := keyRequest(h, "GET", "/v1/audit-log", "lwm_root_synthetic", "").Body.String()
	for _, secret := range []string{key.Token, "lwm_root_synthetic", "webhook-sensitive", "psk-sensitive"} {
		if strings.Contains(log, secret) {
			t.Fatal("secret leaked")
		}
	}
}
