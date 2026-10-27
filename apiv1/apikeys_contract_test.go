package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func keyRequest(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func keyHandler(t *testing.T) http.Handler {
	t.Helper()
	k, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_root sha256:%x admin", sha256.Sum256([]byte("lwm_root_synthetic")))))
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Keys: k})
}
func TestAPIKeysCreateList(t *testing.T) {
	h := keyHandler(t)
	w := keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"name":"sync","scopes":["devices:read"]}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	token, _ := created["token"].(string)
	if !strings.HasPrefix(token, "lwm_"+fmt.Sprint(created["id"])+"_") {
		t.Fatalf("invalid token response")
	}
	if w := keyRequest(h, "GET", "/v1/devices", token, ""); w.Code != 200 {
		t.Fatalf("new key: %d", w.Code)
	}
	w = keyRequest(h, "GET", "/v1/api-keys", "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("seed and new key: %s", w.Body)
	}
	for _, item := range list.Items {
		for _, field := range []string{"id", "name", "scopes", "createdAt", "expiresAt", "lastUsedAt"} {
			if _, ok := item[field]; !ok {
				t.Errorf("missing %s", field)
			}
		}
		if len(item) != 6 {
			t.Errorf("unexpected metadata: %v", item)
		}
		if item["id"] == created["id"] && item["lastUsedAt"] == nil {
			t.Error("successful authentication not recorded")
		}
	}
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "hash") {
		t.Fatal("credential leaked")
	}
}
func TestAPIKeysRevokeExpiry(t *testing.T) {
	h := keyHandler(t)
	expiry := time.Now().Add(150 * time.Millisecond)
	w := keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"name":"short","scopes":["devices:read"],"expiresAt":"`+expiry.UTC().Format(time.RFC3339Nano)+`"}`)
	var key struct{ ID, Token string }
	json.Unmarshal(w.Body.Bytes(), &key)
	if w.Code != 201 {
		t.Fatalf("create: %d", w.Code)
	}
	if got := keyRequest(h, "GET", "/v1/devices", key.Token, "").Code; got != 200 {
		t.Fatalf("authenticate: %d", got)
	}
	time.Sleep(time.Until(expiry) + time.Millisecond)
	if got := keyRequest(h, "GET", "/v1/devices", key.Token, "").Code; got != 401 {
		t.Errorf("expired: %d", got)
	}
	w = keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"scopes":["devices:read"]}`)
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil || w.Code != 201 {
		t.Fatalf("create revocable key: %d %v", w.Code, err)
	}
	if got := keyRequest(h, "GET", "/v1/devices", key.Token, "").Code; got != 200 {
		t.Fatalf("before revoke: %d", got)
	}
	if got := keyRequest(h, "DELETE", "/v1/api-keys/"+key.ID, "lwm_root_synthetic", "").Code; got != 204 {
		t.Fatalf("revoke: %d", got)
	}
	if got := keyRequest(h, "GET", "/v1/devices", key.Token, "").Code; got != 401 {
		t.Errorf("revoked: %d", got)
	}
}
func TestAPIKeysValidation(t *testing.T) {
	h := keyHandler(t)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/v1/api-keys", `{"scopes":["unknown"]}`, 400},
		{"POST", "/v1/api-keys", `{"scopes":["admin,platform"]}`, 400},
		{"POST", "/v1/api-keys", `{"scopes":["devices:read,devices:write"]}`, 400},
		{"POST", "/v1/api-keys", `{"scopes":["platform"]}`, 403},
		{"POST", "/v1/api-keys", `{"scopes":["devices:read"],"expiresAt":"2000-01-01T00:00:00Z"}`, 400},
		{"POST", "/v1/api-keys", `{"unexpected":true}`, 400},
		{"POST", "/v1/api-keys", `{} {}`, 400},
		{"PUT", "/v1/api-keys", ``, 405},
		{"DELETE", "/v1/api-keys", ``, 405},
		{"GET", "/v1/api-keys/missing", ``, 405},
		{"DELETE", "/v1/api-keys/missing", ``, 404},
		{"POST", "/v1/api-keys/", `{}`, 404},
		{"DELETE", "/v1/api-keys/a/b", ``, 404},
	} {
		w := keyRequest(h, tc.method, tc.path, "lwm_root_synthetic", tc.body)
		if w.Code != tc.want || w.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s %s: %d %s", tc.method, tc.path, tc.body, w.Code, w.Body)
		}
	}
	if w := keyRequest(New(Options{}), "GET", "/v1/api-keys", "lwm_root_synthetic", ""); w.Code != 401 {
		t.Fatal("zero keys accepted")
	}
	k := Keys{entries: map[string]credential{"other": {hash: sha256.Sum256([]byte("lwm_other_synthetic")), tenant: "other", scopes: ",admin,"}, "reader": {hash: sha256.Sum256([]byte("lwm_reader_synthetic")), tenant: "default", scopes: ",devices:read,"}, "root": {hash: sha256.Sum256([]byte("lwm_root_synthetic")), tenant: "default", scopes: ",admin,platform,"}}}
	h = New(Options{Keys: k})
	if w := keyRequest(h, "DELETE", "/v1/api-keys/other", "lwm_root_synthetic", ""); w.Code != 404 {
		t.Fatal("cross tenant delete", w.Code)
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := "/v1/api-keys"
		if method == "DELETE" {
			path += "/root"
		}
		if w := keyRequest(h, method, path, "lwm_reader_synthetic", `{}`); w.Code != 403 {
			t.Fatal("reader allowed", method, w.Code)
		}
	}
	if w := keyRequest(h, "GET", "/v1/api-keys", "lwm_root_synthetic", ""); strings.Contains(w.Body.String(), `"id":"other"`) {
		t.Fatal("cross tenant list")
	}
	if w := keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"scopes":["platform"]}`); w.Code != 201 {
		t.Fatal("operator denied", w.Code)
	}
}
func TestAPIKeysConcurrent(t *testing.T) {
	h := keyHandler(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := keyRequest(h, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"scopes":["devices:read"]}`)
			var k struct{ ID, Token string }
			json.Unmarshal(w.Body.Bytes(), &k)
			if w.Code != 201 {
				t.Error("create", w.Code)
				return
			}
			if w := keyRequest(h, "GET", "/v1/devices", k.Token, ""); w.Code != 200 {
				t.Error("auth", w.Code)
			}
			if w := keyRequest(h, "GET", "/v1/api-keys", "lwm_root_synthetic", ""); w.Code != 200 {
				t.Error("list", w.Code)
			}
			if w := keyRequest(h, "DELETE", "/v1/api-keys/"+k.ID, "lwm_root_synthetic", ""); w.Code != 204 {
				t.Error("delete", w.Code)
			}
			if w := keyRequest(h, "GET", "/v1/devices", k.Token, ""); w.Code != 401 {
				t.Error("revoked", w.Code)
			}
		}()
	}
	wg.Wait()
}
