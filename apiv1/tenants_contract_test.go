package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func tenantTestHandler(t *testing.T) http.Handler {
	t.Helper()
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_platform sha256:%x platform\nkey_admin sha256:%x admin", sha256.Sum256([]byte("lwm_platform_test")), sha256.Sum256([]byte("lwm_admin_test")))))
	if err != nil {
		t.Fatal(err)
	}
	keys.entries["acme"] = credential{hash: sha256.Sum256([]byte("lwm_acme_test")), scopes: ",devices:read,", tenant: "acme"}
	return New(Options{Keys: keys})
}

func TestTenantsCreateAndRetrieve(t *testing.T) {
	h := tenantTestHandler(t)
	w := keyRequest(h, "POST", "/v1/tenants", "lwm_platform_test", `{"id":"acme","name":"Acme","limits":{"devices":12}}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = keyRequest(h, "GET", "/v1/tenants/acme", "lwm_platform_test", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Acme"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
}

func TestTenantPatchLimitsPreservesOmittedAndAcceptsZero(t *testing.T) {
	h := tenantTestHandler(t)
	const token = "lwm_platform_test"
	w := keyRequest(h, "POST", "/v1/tenants", token, `{"id":"acme","name":"Acme","limits":{"devices":12,"operationsPerMinute":50,"eventsPerSecond":7,"firmwareStorageBytes":90,"timeseriesRetentionDays":30}}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = keyRequest(h, "PATCH", "/v1/tenants/acme", token, `{"limits":{"devices":13,"eventsPerSecond":0}}`)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	w = keyRequest(h, "GET", "/v1/tenants/acme", token, "")
	var got Tenant
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := (TenantLimits{Devices: 13, OperationsPerMinute: 50, EventsPerSecond: 0, FirmwareStorageBytes: 90, TimeseriesRetentionDays: 30})
	if got.Limits != want {
		t.Fatalf("limits: got %+v, want %+v", got.Limits, want)
	}
	w = keyRequest(h, "PATCH", "/v1/tenants/acme", token, `{"name":"changed","limits":{"devices":-1}}`)
	if w.Code != 422 {
		t.Fatalf("invalid patch: %d %s", w.Code, w.Body)
	}
	w = keyRequest(h, "GET", "/v1/tenants/acme", token, "")
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Acme" || got.Limits != want {
		t.Fatalf("invalid patch mutated tenant: %+v", got)
	}
}

func TestTenantContract(t *testing.T) {
	h := tenantTestHandler(t)
	request := func(method, path, token, body string, status int, code string) {
		t.Helper()
		w := keyRequest(h, method, path, token, body)
		if w.Code != status || (code != "" && (!strings.Contains(w.Body.String(), `"code":"`+code+`"`) || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json"))) {
			t.Fatalf("%s %s: got %d %s, want %d %s", method, path, w.Code, w.Body, status, code)
		}
	}
	const p = "lwm_platform_test"
	request("GET", "/v1/tenants", "lwm_admin_test", "", 403, "forbidden")
	request("GET", "/v1/tenants", p, "", 200, "")
	request("POST", "/v1/tenants", p, `{"id":"acme","name":"Acme","limits":{"devices":-1}}`, 422, "invalid_limits")
	request("POST", "/v1/tenants", p, `{"id":"acme","name":"Acme"}`, 201, "")
	request("POST", "/v1/tenants", p, `{"id":"acme","name":"Acme"}`, 409, "conflict")
	request("GET", "/v1/tenants/missing", p, "", 404, "not_found")
	request("PATCH", "/v1/tenants/missing", p, `{"name":"x"}`, 404, "not_found")
	request("DELETE", "/v1/tenants/missing", p, "", 404, "not_found")
	request("DELETE", "/v1/tenants/default", p, "", 403, "forbidden")
	request("PATCH", "/v1/tenants/acme", p, `{"limits":{"devices":-1}}`, 422, "invalid_limits")
	request("GET", "/v1/devices", "lwm_acme_test", "", 200, "")
	request("PATCH", "/v1/tenants/acme", p, `{"status":"suspended"}`, 200, "")
	request("GET", "/v1/devices", "lwm_acme_test", "", 403, "tenant_suspended")
	request("PATCH", "/v1/tenants/acme", p, `{"status":"active","name":"New"}`, 200, "")
	request("GET", "/v1/devices", "lwm_acme_test", "", 200, "")
	request("DELETE", "/v1/tenants/acme", p, "", 204, "")
	request("GET", "/v1/tenants/acme", p, "", 404, "not_found")
}

func TestSuspendedPlatformTenantCannotCallAPI(t *testing.T) {
	h := tenantTestHandler(t)
	w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"status":"suspended"}`)
	if w.Code != 200 {
		t.Fatalf("suspend: %d %s", w.Code, w.Body)
	}
	w = keyRequest(h, "GET", "/v1/tenants", "lwm_platform_test", "")
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"code":"tenant_suspended"`) {
		t.Fatalf("suspended platform call: %d %s", w.Code, w.Body)
	}
}
