package apiv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func profileWriteRequest(h http.Handler, method, path, body, token, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func profileWriteKeys() Keys {
	keys := deviceKeys()
	keys.entries["other"] = credential{hash: sha256.Sum256([]byte("lwm_other_secret")), scopes: ",devices:read,devices:write,", tenant: "other"}
	keys.entries["read"] = credential{hash: sha256.Sum256([]byte("lwm_read_secret")), scopes: ",devices:read,", tenant: "default"}
	keys.entries["write"] = credential{hash: sha256.Sum256([]byte("lwm_write_secret")), scopes: ",devices:write,", tenant: "default"}
	return keys
}

func profileProblem(t *testing.T, w *httptest.ResponseRecorder, codes ...int) {
	t.Helper()
	valid := false
	for _, code := range codes {
		if w.Code == code {
			valid = true
		}
	}
	var p struct {
		Type, Title, Code string
		Status            int
	}
	if !valid || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Type != "about:blank" || p.Title == "" || p.Code == "" || p.Status != w.Code {
		t.Fatalf("expected problem %v, got %d %s %s", codes, w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func TestDeviceProfileWriteFixtureCredentials(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys()})
	for _, tc := range []struct {
		token  string
		status int
	}{
		{"lwm_test_secret", 200}, {"lwm_other_secret", 200}, {"lwm_read_secret", 200}, {"lwm_write_secret", 403}, {"", 401},
	} {
		w := profileWriteRequest(h, "GET", "/v1/device-profiles", "", tc.token, "")
		if w.Code != tc.status {
			t.Errorf("fixture credential status = %d, want %d", w.Code, tc.status)
		}
	}
}

func TestDeviceProfileWriteCreateAndReplay(t *testing.T) {
	store := NewMemoryDeviceProfileStore()
	h := New(Options{Keys: profileWriteKeys(), DeviceProfiles: store})
	const path = "/v1/device-profiles"
	const body = `{"name":"pump","onRegister":[{"type":"sync-clock"}]}`
	first := profileWriteRequest(h, "POST", path, body, "lwm_test_secret", "create")
	var p DeviceProfile
	if first.Code != 201 || json.Unmarshal(first.Body.Bytes(), &p) != nil || p.ID == "" || p.Name != "pump" || first.Header().Get("Location") != path+"/"+p.ID || first.Header().Get("ETag") == "" {
		t.Fatalf("create: %d %s %v", first.Code, first.Body, first.Header())
	}
	got := profileWriteRequest(h, "GET", first.Header().Get("Location"), "", "lwm_test_secret", "")
	var saved DeviceProfile
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &saved) != nil || saved.ID != p.ID || string(saved.OnRegister) != `[{"type":"sync-clock"}]` {
		t.Fatalf("stored: %d %s", got.Code, got.Body)
	}
	replay := profileWriteRequest(h, "POST", path, body, "lwm_test_secret", "create")
	if replay.Code != 201 || replay.Body.String() != first.Body.String() || replay.Header().Get("Location") != first.Header().Get("Location") || replay.Header().Get("ETag") != first.Header().Get("ETag") || len(store.List("default")) != 1 {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	mismatch := profileWriteRequest(h, "POST", path, `{"name":"other"}`, "lwm_test_secret", "create")
	if mismatch.Code != 201 || mismatch.Body.String() != first.Body.String() || mismatch.Header().Get("ETag") != first.Header().Get("ETag") || mismatch.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("different-body replay: %d %s", mismatch.Code, mismatch.Body)
	}
	if len(store.List("default")) != 1 {
		t.Fatal("different body created a second profile")
	}
}

func TestDeviceProfileWriteActionsStoredOnly(t *testing.T) {
	actions := []string{
		`{"type":"read","paths":["/3/0/1"]}`,
		`{"type":"write","path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":100}]}`,
		`{"type":"execute","path":"/3/0/4"}`,
		`{"type":"create","path":"/3","values":[{"path":"/3/0/1","type":"string","value":"x"}]}`,
		`{"type":"delete","path":"/3/0"}`,
		`{"type":"discover","path":"/3/0","depth":1}`,
		`{"type":"write-attributes","path":"/3/0/1","attributes":{"pmin":60}}`,
		`{"type":"write-attributes","path":"/3/0/1","attributes":{"pmin":null}}`,
		`{"type":"read-acl"}`,
		`{"type":"set-rights","aclInstance":0,"shortServerId":1,"rights":["read","write"]}`,
		`{"type":"set-lifetime","lifetimeSeconds":300}`,
		`{"type":"set-binding","binding":"U"}`,
		`{"type":"set-preferred-transport","binding":"U"}`,
		`{"type":"set-profile-hash-algorithm","suite":1}`,
		`{"type":"advertise-versions"}`,
		`{"type":"trigger-update"}`,
		`{"type":"trigger-bootstrap"}`,
		`{"type":"disable"}`,
		`{"type":"sync-clock"}`,
		`{"type":"firmware-reset","viaPackage":false}`,
		`{"type":"raw","method":"GET","path":"/3/0/1"}`,
	}
	ops := memory.NewOperations()
	profiles := NewMemoryDeviceProfileStore()
	h := New(Options{Keys: profileWriteKeys(), DeviceProfiles: profiles, Operations: ops})
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			body := `{"name":"test","onRegister":[` + action + `]}`
			w := profileWriteRequest(h, "POST", "/v1/device-profiles", body, "lwm_test_secret", "")
			var p DeviceProfile
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
				t.Fatalf("create: %d %s", w.Code, w.Body)
			}
			stored, ok := profiles.Get("default", p.ID)
			var expected, actual any
			if !ok || json.Unmarshal([]byte("["+action+"]"), &expected) != nil || json.Unmarshal(stored.OnRegister, &actual) != nil {
				t.Fatalf("action not stored: %s", stored.OnRegister)
			}
			a, _ := json.Marshal(expected)
			b, _ := json.Marshal(actual)
			if string(a) != string(b) {
				t.Fatalf("action changed: %s", b)
			}
		})
	}
	list, _, err := ops.List(context.Background(), "default", operations.ListFilter{}, "", 50)
	if err != nil || len(list) != 0 {
		t.Fatalf("actions executed: %v %v", list, err)
	}
}

func TestDeviceProfileWriteInvalid(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys()})
	for _, body := range []string{`{`, `{"name":"pump","typo":true}`, `{}`, `{"name":""}`, `{"name":"pump","onRegister":[{"type":"unknown"}]}`, `{"name":"pump","onRegister":[{"type":"read"}]}`, `{"name":"pump","onRegister":[{"type":"execute","path":"bad"}]}`, `{"name":"pump","onRegister":[{"type":"write","values":[{}]}]}`, `{"name":"pump","onRegister":[{"type":"write-attributes","path":"/3/0/1"}]}`, `{"name":"pump","onRegister":[{"type":"delete","path":"/3/0/1"}]}`, `{"name":"pump","onRegister":[{"type":"set-lifetime"}]}`, `{"name":"pump","onRegister":[{"type":"raw","path":"/3/0/1","method":"BOGUS"}]}`, `{"name":"pump","onRegister":[{"type":"sync-clock","bogus":true}]}`, `{"name":"pump","bootstrapTemplate":{"servers":[{"server":"self","security":{"psk":{"key":"test"}}}]}}`} {
		t.Run(body, func(t *testing.T) {
			profileProblem(t, profileWriteRequest(h, "POST", "/v1/device-profiles", body, "lwm_test_secret", ""), 400, 422)
		})
	}
}

func TestDeviceProfileWriteTenantDeleteAndScope(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	h := New(Options{Keys: profileWriteKeys(), DeviceProfiles: profiles})
	const path = "/v1/device-profiles"
	for _, method := range []string{"POST", "DELETE"} {
		target := path
		if method == "DELETE" {
			target += "/missing"
		}
		profileProblem(t, profileWriteRequest(h, method, target, `{"name":"a"}`, "", ""), 401)
		profileProblem(t, profileWriteRequest(h, method, target, `{"name":"a"}`, "lwm_read_secret", ""), 403)
	}
	first := profileWriteRequest(h, "POST", path, `{"name":"a"}`, "lwm_write_secret", "same")
	if first.Code != 201 {
		t.Fatalf("write-only create: %d %s", first.Code, first.Body)
	}
	other := profileWriteRequest(h, "POST", path, `{"name":"b"}`, "lwm_other_secret", "same")
	if other.Code != 201 || first.Header().Get("Location") == other.Header().Get("Location") || len(profiles.List("default")) != 1 || len(profiles.List("other")) != 1 {
		t.Fatalf("tenant create: %d %s", other.Code, other.Body)
	}
	for _, target := range []string{path + "/missing", other.Header().Get("Location")} {
		profileProblem(t, profileWriteRequest(h, "DELETE", target, "", "lwm_test_secret", ""), 404)
	}
	if w := profileWriteRequest(h, "GET", first.Header().Get("Location"), "", "lwm_other_secret", ""); w.Code != 404 {
		t.Fatalf("foreign GET: %d", w.Code)
	}
	if w := profileWriteRequest(h, "DELETE", first.Header().Get("Location"), "", "lwm_write_secret", ""); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := profileWriteRequest(h, "GET", first.Header().Get("Location"), "", "lwm_test_secret", ""); w.Code != 404 {
		t.Fatalf("deleted GET: %d", w.Code)
	}
	if w := profileWriteRequest(h, "GET", other.Header().Get("Location"), "", "lwm_other_secret", ""); w.Code != 200 {
		t.Fatalf("foreign resource changed: %d", w.Code)
	}
}
