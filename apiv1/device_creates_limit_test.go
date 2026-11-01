package apiv1_test

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func limitRouter(t *testing.T, backend string) http.Handler {
	t.Helper()
	var devices store.Devices = &memory.Devices{}
	if backend == "sqlite" {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		devices = db
	}
	h1 := sha256.Sum256([]byte("lwm_test_secret"))
	h2 := sha256.Sum256([]byte("lwm_platform_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\nkey_platform sha256:%x platform\n", h1, h2)))
	if err != nil {
		t.Fatal(err)
	}
	h := apiv1.New(apiv1.Options{Keys: keys, Devices: devices})
	w := limitRequest(h, "PATCH", "/v1/tenants/default", `{"limits":{"devices":1}}`, "lwm_platform_secret")
	if w.Code != 200 {
		t.Fatalf("set limit: %d %s", w.Code, w.Body)
	}
	return h
}
func limitRequest(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestDeviceCreateOutcomeConcurrentPostLimit(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				t.Run(fmt.Sprint(i), func(t *testing.T) {
					h := limitRouter(t, backend)
					start := make(chan struct{})
					results := make(chan *httptest.ResponseRecorder, 2)
					go func() {
						<-start
						results <- limitRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`, "lwm_test_secret")
					}()
					go func() {
						<-start
						results <- limitRequest(h, "PUT", "/v1/device-creates/op", `{"endpoint":"two"}`, "lwm_test_secret")
					}()
					close(start)
					a, b := <-results, <-results
					if !((a.Code == 201 && b.Code == 429) || (a.Code == 429 && b.Code == 201)) {
						t.Fatalf("concurrent results: %d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
					}
					list := limitRequest(h, "GET", "/v1/devices", "", "lwm_test_secret")
					if list.Code != 200 || strings.Count(list.Body.String(), `"endpoint"`) != 1 {
						t.Fatalf("device list: %d %s", list.Code, list.Body)
					}
				})
			}
		})
	}
}

func TestDeviceCreateOutcomeTenantLimit(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			h := limitRouter(t, backend)
			post := limitRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`, "lwm_test_secret")
			if post.Code != 201 {
				t.Fatalf("POST: %d %s", post.Code, post.Body)
			}
			put := limitRequest(h, "PUT", "/v1/device-creates/op2", `{"endpoint":"two"}`, "lwm_test_secret")
			if put.Code != 429 || !strings.Contains(put.Body.String(), "limit_exceeded") {
				t.Fatalf("PUT: %d %s", put.Code, put.Body)
			}
			fence := limitRequest(h, "GET", "/v1/device-creates/fenced", "", "lwm_test_secret")
			replay := limitRequest(h, "PUT", "/v1/device-creates/fenced", `{"endpoint":"three"}`, "lwm_test_secret")
			if fence.Code != 200 || replay.Code != 200 || replay.Body.String() != fence.Body.String() {
				t.Fatalf("fenced replay: %d %s / %d %s", fence.Code, fence.Body, replay.Code, replay.Body)
			}
			if d := limitRequest(h, "DELETE", "/v1/devices/one", "", "lwm_test_secret"); d.Code != 204 {
				t.Fatalf("delete: %d %s", d.Code, d.Body)
			}
			first := limitRequest(h, "PUT", "/v1/device-creates/op2", `{"endpoint":"two"}`, "lwm_test_secret")
			if first.Code != 201 {
				t.Fatalf("retry after capacity: %d %s", first.Code, first.Body)
			}
			again := limitRequest(h, "PUT", "/v1/device-creates/op2", `{"endpoint":"two"}`, "lwm_test_secret")
			if again.Code != 200 || again.Body.String() != first.Body.String() {
				t.Fatalf("committed replay: %d %s", again.Code, again.Body)
			}
			conflict := limitRequest(h, "PUT", "/v1/device-creates/op2", `{"endpoint":"different"}`, "lwm_test_secret")
			if conflict.Code != 409 {
				t.Fatalf("fingerprint conflict: %d %s", conflict.Code, conflict.Body)
			}
		})
	}
}
