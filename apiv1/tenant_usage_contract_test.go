package apiv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"testing"
)

func TestTenantUsageContract(t *testing.T) {
	h := tenantTestHandler(t)
	const platform = "lwm_platform_test"
	if w := keyRequest(h, "POST", "/v1/tenants", platform, `{"id":"acme","name":"Acme"}`); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/v1/tenants/acme/usage", platform, 200},
		{"/v1/tenants/acme/usage", "lwm_acme_test", 403},
		{"/v1/tenants/missing/usage", platform, 404},
	} {
		w := keyRequest(h, "GET", tc.path, tc.token, "")
		if w.Code != tc.status {
			t.Errorf("%s: got %d %s, want %d", tc.path, w.Code, w.Body, tc.status)
			continue
		}
		if tc.status == 200 {
			var v map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v["devices"] != float64(0) || v["registered"] != float64(0) {
				t.Errorf("usage: %s (%v)", w.Body, err)
			}
		}
	}
}

func TestTenantUsageRetainedCounters(t *testing.T) {
	keys := deviceKeys()
	keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
	ops := memory.NewOperations()
	events := &memory.Events{}
	op := operations.New("default", "pump", "read", nil, nil, time.Now().UTC())
	if err := ops.Create(context.Background(), "default", op); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append("default", "sample", "pump", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Operations: ops, EventOptions: EventOptions{Events: events}})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body)
	}
	w := keyRequest(h, "GET", "/v1/tenants/default/usage", "lwm_platform_test", "")
	var v struct {
		Devices    int `json:"devices"`
		Registered int `json:"registered"`
		Daily      map[string]struct {
			Operations int `json:"operations"`
			Events     int `json:"events"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	d := v.Daily[op.CreatedAt.Format(time.DateOnly)]
	if w.Code != 200 || v.Devices != 1 || v.Registered != 0 || d.Operations != 1 || d.Events != 2 {
		t.Fatalf("usage: %d %s", w.Code, w.Body)
	}
}

func TestTenantOperationLimit(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
	srv := server.New(server.Config{})
	defer srv.Close()
	h := New(Options{Keys: keys, Server: srv})
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":1}}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body)
	}
	if w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"]}`); w.Code != 202 {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"]}`)
	if w.Code != 429 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") || !strings.Contains(w.Body.String(), `"code":"limit_exceeded"`) {
		t.Fatalf("limit: %d %s", w.Code, w.Body)
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices/pump/operations", strings.NewReader(`{"type":"read","paths":["/3/0/0"]}`))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", "limited")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request(); w.Code != 429 {
		t.Fatalf("keyed limit: %d %s", w.Code, w.Body)
	}
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":0}}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := request(); w.Code != 429 {
		t.Fatalf("keyed replay: %d %s", w.Code, w.Body)
	}
}

func TestTenantDeviceLimitConcurrent(t *testing.T) {
	h := tenantTestHandler(t)
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"devices":3}}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	codes := make(chan int, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := keyRequest(h, "POST", "/v1/devices", "lwm_admin_test", fmt.Sprintf(`{"endpoint":"%d"}`, i))
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[201] != 3 || counts[429] != 17 {
		t.Fatal(counts)
	}
}

func TestTenantDeviceLimit(t *testing.T) {
	h := tenantTestHandler(t)
	const p = "lwm_platform_test"
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", p, `{"limits":{"devices":1}}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := keyRequest(h, "POST", "/v1/devices", "lwm_admin_test", `{"endpoint":"one"}`); w.Code != 201 {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	w := keyRequest(h, "POST", "/v1/devices", "lwm_admin_test", `{"endpoint":"two"}`)
	if w.Code != 429 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") || !strings.Contains(w.Body.String(), `"code":"limit_exceeded"`) {
		t.Fatalf("limit: %d %s", w.Code, w.Body)
	}
}
