package apiv1_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

type interruptedResponse struct{ *httptest.ResponseRecorder }

func (w interruptedResponse) Write(p []byte) (int, error) {
	_, _ = w.ResponseRecorder.Write(p)
	panic("simulated crash after commit")
}

func TestDeviceCreateInlineSQLiteRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	key := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write,credentials:write\n", key)))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	call := func(h http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/v1/device-creates/restart", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{Security: db.Security()})
	first := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected interrupted response")
			}
		}()
		r := httptest.NewRequest("PUT", "/v1/device-creates/restart", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		apiv1.New(apiv1.Options{Keys: keys, Devices: db, Server: srv}).ServeHTTP(interruptedResponse{first}, r)
	}()
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	outcome, err := db.GetDeviceCreate(t.Context(), "default", "restart")
	if err != nil {
		t.Fatal(err)
	}
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	unsalted := sha256.Sum256(canonical)
	if strings.Contains(outcome.Fingerprint, "000102030405") || strings.Contains(outcome.Fingerprint, fmt.Sprintf("%x", unsalted)) {
		t.Fatal("secret or unsalted digest retained")
	}
	_ = srv.Close()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv = server.New(server.Config{Security: db.Security()})
	defer srv.Close()
	replay := call(apiv1.New(apiv1.Options{Keys: keys, Devices: db, Server: srv}))
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	devices, _, err := db.List("default", store.DeviceFilter{}, "", 100)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices %v: %v", devices, err)
	}
	if _, ok := db.Security().ByEndpoint("pump"); !ok {
		t.Fatal("security missing after restart")
	}
}
