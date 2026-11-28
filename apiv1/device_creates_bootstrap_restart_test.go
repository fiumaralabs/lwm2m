package apiv1_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceCreatePutBootstrapSQLiteRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	key := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write,credentials:write,bootstrap:write", key)))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}},"bootstrap":{"servers":[{"shortServerId":1,"security":{"psk":{"identity":"other","key":"hex:101112131415161718191a1b1c1d1e1f"}}}]}}`
	open := func() (*sqlite.Devices, *server.Server) {
		db, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return db, server.New(server.Config{Security: db.Security()})
	}
	call := func(db *sqlite.Devices, srv *server.Server, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/v1/device-creates/restart", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		apiv1.New(apiv1.Options{Keys: keys, Devices: db, Server: srv}).ServeHTTP(w, r)
		return w
	}
	db, srv := open()
	first := call(db, srv, body)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	outcome, err := db.GetDeviceCreate(t.Context(), "default", "restart")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(outcome)
	if strings.Contains(string(encoded), "00010203") || strings.Contains(string(encoded), "10111213") || strings.Contains(string(encoded), "other") || !strings.HasPrefix(outcome.Fingerprint, "salted-both:") {
		t.Fatal("unsafe outcome")
	}
	_ = srv.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, srv = open()
	defer db.Close()
	defer srv.Close()
	replay := call(db, srv, body)
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if _, ok := db.Security().ByEndpoint("pump"); !ok {
		t.Fatal("credential missing after restart")
	}
	if _, ok, err := db.GetBootstrapConfig(t.Context(), "default", "pump"); err != nil || !ok {
		t.Fatalf("bootstrap missing: %v", err)
	}
}
