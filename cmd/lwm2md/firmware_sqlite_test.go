package main

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
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestSQLiteFirmwareDaemonReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.db")
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x firmware:write,devices:read", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	open := func() (*sqlite.Devices, http.Handler) {
		d, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, h, _, _ := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys, d)
		return d, h
	}
	d, h := open()
	if _, err := d.Create("default", store.Device{Endpoint: "pump"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/firmware", strings.NewReader(`{"name":"pump","version":"1"}`))
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created apiv1.Firmware
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	updateReq := httptest.NewRequest("POST", "/v1/devices/pump/firmware-updates", strings.NewReader(`{"uri":"coaps://example/fw"}`))
	updateReq.Header.Set("Authorization", "Bearer lwm_test_secret")
	updateResult := httptest.NewRecorder()
	h.ServeHTTP(updateResult, updateReq)
	var update apiv1.FirmwareUpdate
	if updateResult.Code != 201 || json.Unmarshal(updateResult.Body.Bytes(), &update) != nil || update.ID == "" {
		t.Fatalf("update create: %d %s", updateResult.Code, updateResult.Body)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, h = open()
	defer d.Close()
	updateReq = httptest.NewRequest("GET", "/v1/firmware-updates/"+update.ID, nil)
	updateReq.Header.Set("Authorization", "Bearer lwm_test_secret")
	updateResult = httptest.NewRecorder()
	h.ServeHTTP(updateResult, updateReq)
	if updateResult.Code != 200 {
		t.Fatalf("update after reopen: %d %s", updateResult.Code, updateResult.Body)
	}
	r = httptest.NewRequest("GET", "/v1/firmware/"+created.ID, nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got apiv1.Firmware
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID != created.ID || got.Name != "pump" {
		t.Fatalf("reopen: %d %s", w.Code, w.Body)
	}
}
