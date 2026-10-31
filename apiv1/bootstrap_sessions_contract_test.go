package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBootstrapSessionsPaginationAndIsolation(t *testing.T) {
	sessions := NewMemoryBootstrapSessionStore()
	keys := deviceKeys()
	keys.entries["other"] = credential{hash: sha256.Sum256([]byte("lwm_other_secret")), scopes: ",devices:read,devices:write,", tenant: "other"}
	keys.entries["limited"] = credential{hash: sha256.Sum256([]byte("lwm_limited_secret")), scopes: ",bootstrap:write,", tenant: "default"}
	h := New(Options{Keys: keys, BootstrapSessions: sessions})
	request := func(token, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"lwm_test_secret", "lwm_other_secret"} {
		w := request(token, "POST", "/v1/devices", `{"endpoint":"pump"}`)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	started := time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	ended := started.Add(3 * time.Second)
	for _, id := range []string{"first", "second"} {
		if err := sessions.Append("default", "pump", BootstrapSession{ID: id, StartedAt: started, EndedAt: ended, Kind: "request", Status: "succeeded", Format: "senml-cbor", Identity: json.RawMessage(`{"mode":"psk","pskIdentity":"factory-7"}`), Steps: []BootstrapSessionStep{{Method: "POST", Path: "/bs", Code: "2.04"}}, Discover: json.RawMessage(`[{"path":"/0/0","attributes":{"ssid":0}}]`), Reads: json.RawMessage(`{"/1":["values"]}`)}); err != nil {
			t.Fatal(err)
		}
	}
	w := request("lwm_test_secret", "GET", "/v1/devices/pump/bootstrap/sessions?limit=1", "")
	var page struct {
		Items []BootstrapSession `json:"items"`
		Next  string             `json:"nextCursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); w.Code != 200 || err != nil || len(page.Items) != 1 || page.Items[0].ID != "second" || string(page.Items[0].Identity) != `{"mode":"psk","pskIdentity":"factory-7"}` || len(page.Items[0].Steps) != 1 || page.Next == "" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got := page.Items[0]
	if !got.StartedAt.Equal(started) || !got.EndedAt.Equal(ended) || got.Kind != "request" || got.Format != "senml-cbor" || got.Status != "succeeded" || got.Steps[0] != (BootstrapSessionStep{Method: "POST", Path: "/bs", Code: "2.04"}) || string(got.Discover) != `[{"path":"/0/0","attributes":{"ssid":0}}]` || string(got.Reads) != `{"/1":["values"]}` || string(got.Error) != "null" {
		t.Fatalf("session fields: %s", w.Body.String())
	}
	cursor := page.Next
	w = request("lwm_test_secret", "GET", "/v1/devices/pump/bootstrap/sessions?limit=1&cursor="+cursor, "")
	page.Items = nil
	page.Next = ""
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != "first" || page.Next != "" {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		token, path string
		status      int
	}{
		{"lwm_other_secret", "/v1/devices/pump/bootstrap/sessions", 200},
		{"lwm_other_secret", "/v1/devices/pump/bootstrap/sessions?cursor=" + cursor, 400},
		{"lwm_test_secret", "/v1/devices/missing/bootstrap/sessions", 404},
		{"lwm_limited_secret", "/v1/devices/pump/bootstrap/sessions", 403},
		{"lwm_test_secret", "/v1/devices/pump/bootstrap/sessions?limit=0", 400},
		{"lwm_test_secret", "/v1/devices/pump/bootstrap/sessions?cursor=", 400},
	} {
		w := request(tc.token, "GET", tc.path, "")
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.token == "lwm_other_secret" && tc.status == 200 && strings.Contains(w.Body.String(), "second") {
			t.Fatal(w.Body.String())
		}
	}
}
