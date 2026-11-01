package main

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestESTDaemonLoadsSQLiteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.ESTConfigs().Put("default", apiv1.ESTConfig{Enabled: true, Certificate: "certificate", PrivateKey: "sensitive", ValidityDays: 30, Selector: "env=prod"})
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x credentials:write", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	srv, h, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys, d)
	defer srv.Close()
	defer executor.Close()
	defer dispatcher.Close()
	r := httptest.NewRequest("GET", "/v1/est", nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "env=prod") || strings.Contains(w.Body.String(), "sensitive") {
		t.Fatalf("GET after reopen: %d %s", w.Code, w.Body.String())
	}
}
