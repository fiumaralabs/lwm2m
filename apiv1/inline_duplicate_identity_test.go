package apiv1_test

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestInlineDuplicatePSKIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var d store.Devices
			var security server.SecurityStore
			if backend == "memory" {
				m := &memory.Devices{}
				d, security = m, m.Security()
			} else {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				d, security = db, db.Security()
			}
			s := server.New(server.Config{Security: security})
			t.Cleanup(func() { _ = s.Close() })
			hash := sha256.Sum256([]byte("lwm_allowed_secret"))
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_allowed sha256:%x devices:write,credentials:write\n", hash)))
			if err != nil {
				t.Fatal(err)
			}
			h := apiv1.New(apiv1.Options{Keys: keys, Devices: d, Server: s})
			post := func(endpoint string) *httptest.ResponseRecorder {
				body := `{"endpoint":"` + endpoint + `","credentials":{"psk":{"identity":"shared","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
				r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_allowed_secret")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := post("first"); w.Code != 201 {
				t.Fatalf("first: %d %s", w.Code, w.Body)
			}
			if w := post("second"); w.Code != 409 || !strings.Contains(w.Body.String(), "duplicate_psk_identity") {
				t.Fatalf("duplicate: %d %s", w.Code, w.Body)
			}
			if _, err := d.Get("default", "second"); err == nil {
				t.Fatal("duplicate created device")
			}
			if _, ok := security.ByEndpoint("second"); ok {
				t.Fatal("duplicate installed credential")
			}
			if got, ok := security.ByPSKIdentity("shared"); !ok || got.Endpoint != "first" {
				t.Fatal("original credential changed")
			}
		})
	}
}
