package sqlite_test

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
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceHTTPReplayAfterReopen(t *testing.T) {
	hash := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\n", hash)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "devices.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := func() http.Handler {
		return apiv1.New(apiv1.Options{Keys: keys, Devices: db, EventOptions: apiv1.EventOptions{Events: db.Events()}})
	}
	post := func(h http.Handler, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", "once")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := post(handler(), `{"endpoint":"pump"}`)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replay := post(handler(), `{"endpoint":"changed"}`)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() || replay.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	devices, _, err := db.List("default", store.DeviceFilter{}, "", 100)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices: %v %v", devices, err)
	}
	events, _, err := db.Events().List("default", store.EventFilter{Limit: 100})
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %v %v", events, err)
	}
}
