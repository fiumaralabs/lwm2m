package apiv1_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

func TestDeviceCreateGeneratedBootstrapSecretOnce(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices
			var security server.SecurityStore
			var audit apiv1.AuditStore = &apiv1.MemoryAuditStore{}
			if backend == "memory" {
				d := &memory.Devices{}
				devices, security = d, d.Security()
			} else {
				d, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.Close() })
				devices, security = d, d.Security()
				audit = d.Audit()
			}
			srv := server.New(server.Config{Security: security})
			t.Cleanup(func() { _ = srv.Close() })
			hash := sha256.Sum256([]byte("lwm_test_secret"))
			limited := sha256.Sum256([]byte("lwm_limited_secret"))
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write,credentials:write,bootstrap:write\nkey_limited sha256:%x devices:write,bootstrap:write", hash, limited)))
			if err != nil {
				t.Fatal(err)
			}
			h := apiv1.New(apiv1.Options{Keys: keys, Devices: devices, Server: srv, AuditOptions: apiv1.AuditOptions{Audit: audit}})
			body := `{"endpoint":"pump","bootstrap":{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}}]}}`
			call := func(method string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, "/v1/device-creates/op", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_test_secret")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			limitedCall := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("PUT", "/v1/device-creates/op", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_limited_secret")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := limitedCall(); w.Code != 403 {
				t.Fatalf("unprivileged create status %d", w.Code)
			}
			first := call("PUT")
			if first.Code != 201 {
				t.Fatalf("first status %d, expected 201", first.Code)
			}
			var response struct {
				GeneratedCredentials []struct {
					ServerIndex int    `json:"serverIndex"`
					Identity    string `json:"identity"`
					Key         string `json:"key"`
				} `json:"generatedCredentials"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil || len(response.GeneratedCredentials) != 1 {
				t.Fatal("missing generated credential")
			}
			credential := response.GeneratedCredentials[0]
			key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(credential.Key, "base64:"))
			if err != nil || len(key) != 32 || credential.Identity == "" || credential.ServerIndex != 0 {
				t.Fatal("invalid generated credential")
			}
			stored, ok := security.ByEndpoint("pump")
			if !ok || stored.PSKIdentity != credential.Identity || string(stored.PSKKey) != string(key) {
				t.Fatal("generated credential not installed")
			}
			outcome, err := devices.(store.DeviceCreateStore).GetDeviceCreate(t.Context(), "default", "op")
			if err != nil {
				t.Fatal(err)
			}
			entries, err := audit.List("default")
			if err != nil || len(entries) != 2 {
				t.Fatal("expected denied and successful audit entries")
			}
			for _, value := range []any{outcome, entries} {
				b, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), credential.Key) || strings.Contains(string(b), credential.Identity) || strings.Contains(string(b), base64.StdEncoding.EncodeToString(key)) {
					t.Fatal("secret in outcome or audit")
				}
			}
			for _, method := range []string{"PUT", "GET"} {
				replay := call(method)
				if replay.Code != 200 || strings.Contains(replay.Body.String(), credential.Key) || strings.Contains(replay.Body.String(), credential.Identity) || strings.Contains(replay.Body.String(), "generatedCredentials") {
					t.Fatalf("unsafe %s replay status %d", method, replay.Code)
				}
			}
			after, ok := security.ByEndpoint("pump")
			if !ok || string(after.PSKKey) != string(key) {
				t.Fatal("replay changed credential")
			}
			entries, err = audit.List("default")
			if err != nil || len(entries) != 2 {
				t.Fatal("replay created audit record")
			}
			if w := limitedCall(); w.Code != 403 {
				t.Fatalf("unprivileged replay status %d", w.Code)
			}

			// Competing retries must expose exactly one key, including when they
			// race before any outcome exists.
			results := make(chan *httptest.ResponseRecorder, 8)
			for i := 0; i < cap(results); i++ {
				go func() {
					r := httptest.NewRequest("PUT", "/v1/device-creates/race", strings.NewReader(strings.Replace(body, "pump", "race", 1)))
					r.Header.Set("Authorization", "Bearer lwm_test_secret")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					results <- w
				}()
			}
			created, replayed, exposed := 0, 0, 0
			for i := 0; i < cap(results); i++ {
				w := <-results
				if w.Code == 201 {
					created++
				} else if w.Code == 200 {
					replayed++
				} else {
					t.Fatalf("race status %d", w.Code)
				}
				if strings.Contains(w.Body.String(), "generatedCredentials") {
					exposed++
				}
			}
			if created != 1 || replayed != 7 || exposed != 1 {
				t.Fatalf("race: created=%d replayed=%d exposed=%d", created, replayed, exposed)
			}
		})
	}
}
