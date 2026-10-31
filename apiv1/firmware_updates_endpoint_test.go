package apiv1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFirmwareUpdateEndpoints(t *testing.T) {
	store := NewMemoryFirmwareUpdateStore()
	h := NewFirmwareUpdatesHandler(store, NewMemoryFirmwareStore())
	collection := "/v1/devices/dev/firmware-updates"
	request := func(method, path, body, tenant, scope string, want int) *httpResult {
		t.Helper()
		w := jobRequest(h, method, path, body, tenant, scope)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return &httpResult{body: w.Body.String()}
	}
	for _, body := range []string{`{}`, `{"uri":"x","firmware":"y"}`, `{"uri":"x","method":"bad"}`, `{"uri":"x","severity":"bad"}`, `{"uri":"x","maxDeferSeconds":-1}`, `{"uri":"x","pushAttempts":-1}`, `{"uri":"x","extra":1}`} {
		request("POST", collection, body, "a", "firmware:write", 400)
	}
	request("POST", collection, `{"firmware":"missing"}`, "a", "firmware:write", 404)
	request("POST", collection, `{"uri":"x"}`, "a", "devices:read", 403)
	created := request("POST", collection, `{"uri":"coaps://example/fw"}`, "a", "firmware:write", 201)
	var u FirmwareUpdate
	if err := json.Unmarshal([]byte(created.body), &u); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.ID, "fwu_") || u.Status != "pending" || u.Method != "auto" || u.PushTimeoutSeconds != 300 || u.PushAttempts != 1 || u.PollSeconds != 30 || u.AutoUpgrade || u.Resume || u.CreatedAt.IsZero() {
		t.Fatalf("defaults: %+v", u)
	}
	conflict := request("POST", collection, `{"uri":"x"}`, "a", "firmware:write", 409)
	if !strings.Contains(conflict.body, "update_in_progress") {
		t.Fatal(conflict.body)
	}
	path := "/v1/firmware-updates/" + u.ID
	request("GET", path, "", "b", "devices:read", 404)
	request("DELETE", path, "", "b", "firmware:write", 404)
	request("GET", path, "", "a", "firmware:write", 403)
	request("GET", path, "", "a", "devices:read", 200)
	request("DELETE", path, "", "a", "firmware:write", 204)
	request("DELETE", path, "", "a", "firmware:write", 409)
	request("POST", collection, `{"uri":"x"}`, "a", "firmware:write", 201)
	list := request("GET", collection+"?limit=1", "", "a", "devices:read", 200)
	var page struct {
		Items []FirmwareUpdate `json:"items"`
		Next  string           `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(list.body), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID == u.ID || page.Next == "" {
		t.Fatalf("first page: %s", list.body)
	}
	list = request("GET", collection+"?limit=1&cursor="+page.Next, "", "a", "devices:read", 200)
	page.Items = nil
	page.Next = ""
	if err := json.Unmarshal([]byte(list.body), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != u.ID || page.Next != "" {
		t.Fatalf("second page: %s", list.body)
	}
}

type httpResult struct{ body string }

func TestFirmwareUpdateArtifactsAndCursorIsolation(t *testing.T) {
	updates := NewMemoryFirmwareUpdateStore()
	artifacts := NewMemoryFirmwareStore()
	if err := artifacts.Create("a", Firmware{ID: "fw_one"}); err != nil {
		t.Fatal(err)
	}
	h := NewFirmwareUpdatesHandler(updates, artifacts)
	collection := "/v1/devices/dev/firmware-updates"
	for _, tc := range []struct {
		tenant string
		code   int
	}{{"b", 404}, {"a", 201}} {
		w := jobRequest(h, "POST", collection, `{"firmware":"fw_one","method":"pull","severity":"critical","maxDeferSeconds":0,"autoUpgrade":true,"resume":true}`, tc.tenant, "firmware:write")
		if w.Code != tc.code {
			t.Fatalf("artifact tenant %s: %d %s", tc.tenant, w.Code, w.Body.String())
		}
		if tc.code == 201 {
			var u FirmwareUpdate
			if json.Unmarshal(w.Body.Bytes(), &u) != nil || u.Firmware != "fw_one" || u.Method != "pull" || u.MaxDeferSeconds == nil || *u.MaxDeferSeconds != 0 || !u.AutoUpgrade || !u.Resume {
				t.Fatalf("artifact options: %s", w.Body.String())
			}
		}
	}
	for _, path := range []string{collection + "?limit=0", collection + "?cursor=", collection + "?cursor=bad", collection + "?limit=1&limit=2"} {
		if w := jobRequest(h, "GET", path, "", "a", "devices:read"); w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if w := jobRequest(h, "DELETE", "/v1/firmware-updates/unknown", "", "a", "devices:read"); w.Code != 403 {
		t.Fatalf("delete scope: %d", w.Code)
	}
}
