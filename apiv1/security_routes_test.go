package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestSecurityRoutesViaRouter(t *testing.T) {
	entries := map[string]credential{}
	for _, k := range []struct{ id, tenant, scopes string }{
		{"owner", "default", ",devices:read,bootstrap:write,credentials:write,"},
		{"other", "other", ",devices:read,bootstrap:write,credentials:write,"},
		{"wrong", "default", ",devices:write,"},
	} {
		entries[k.id] = credential{hash: sha256.Sum256([]byte("lwm_" + k.id + "_secret")), tenant: k.tenant, scopes: k.scopes}
	}
	devices := &memory.Devices{}
	if _, err := devices.Create("default", store.Device{Endpoint: "pump"}); err != nil {
		t.Fatal(err)
	}
	if _, err := devices.Create("other", store.Device{Endpoint: "pump"}); err != nil {
		t.Fatal(err)
	}
	anchorStore := NewMemoryTrustAnchorStore(20)
	credentialStore := NewMemoryBootstrapCredentialStore()
	h := New(Options{Keys: Keys{entries: entries}, Devices: devices, TrustAnchors: anchorStore, BootstrapCredentials: credentialStore})
	call := func(method, path, key, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer lwm_"+key+"_secret")
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s %s as %s: got %d want %d: %s", method, path, key, w.Code, want, w.Body.String())
		}
		return w
	}
	anchors := "/v1/trust-anchors"
	creds := "/v1/devices/pump/bootstrap/credentials"
	for _, path := range []string{anchors, creds} {
		call("GET", path, "", "", 401)
		call("GET", path, "wrong", "", 403)
	}
	call("POST", anchors, "wrong", "{}", 403)
	call("PUT", creds, "wrong", "{}", 403)
	call("DELETE", creds, "wrong", "", 403)
	cert := anchorCertificate(t, true, time.Now().Add(time.Hour))
	payload, _ := json.Marshal(map[string]string{"name": "CA", "certificates": cert})
	w := call("POST", anchors, "owner", string(payload), 201)
	var anchor TrustAnchor
	if err := json.Unmarshal(w.Body.Bytes(), &anchor); err != nil || anchor.ID == "" {
		t.Fatalf("invalid anchor response: %s", w.Body.String())
	}
	if _, ok := anchorStore.Get("default", anchor.ID); !ok {
		t.Fatal("anchor not stored in supplied store")
	}
	item := anchors + "/" + anchor.ID
	call("GET", anchors, "owner", "", 200)
	call("GET", item, "owner", "", 200)
	call("GET", item, "other", "", 404)
	call("DELETE", item, "other", "", 404)
	call("DELETE", item, "owner", "", 204)
	call("PUT", creds, "owner", `{"psk":{"identity":"factory","key":"hex:000102030405060708090a0b0c0d0e0f"}}`, 201)
	if _, ok := credentialStore.Get("default", "pump"); !ok {
		t.Fatal("credential not stored in supplied store")
	}
	w = call("GET", creds, "owner", "", 200)
	if strings.Contains(w.Body.String(), `"key"`) || strings.Contains(w.Body.String(), "000102030405060708090a0b0c0d0e0f") {
		t.Error("credential secret returned")
	}
	call("GET", creds, "other", "", 404)
	call("DELETE", creds, "other", "", 404)
	call("DELETE", creds, "owner", "", 204)
}
