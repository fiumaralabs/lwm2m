package apiv1

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestBootstrapCredentialLifecycle(t *testing.T) {
	devices := &memory.Devices{}
	_, _ = devices.Create("default", store.Device{Endpoint: "pump"})
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,bootstrap:write,", tenant: "default"}}}
	h := Authenticate(keys, NewBootstrapCredentialsHandler(devices, NewMemoryBootstrapCredentialStore()))
	call := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/devices/pump/bootstrap/credentials", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call(http.MethodPut, `{"psk":{"identity":"factory","key":"hex:000102030405060708090a0b0c0d0e0f"}}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	w = call(http.MethodGet, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"identity":"factory"`) || strings.Contains(w.Body.String(), `"key"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
}
