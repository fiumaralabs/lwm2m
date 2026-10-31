package main

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
)

func TestSecurityRoutesDaemon(t *testing.T) {
	const token = "lwm_test_synthetic"
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,credentials:write,bootstrap:write", sha256.Sum256([]byte(token)))))
	if err != nil {
		t.Fatal(err)
	}
	srv, h, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys)
	defer srv.Close()
	defer executor.Close()
	defer dispatcher.Close()
	call := func(method, path, body string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
	}
	call("POST", "/v1/devices", `{"endpoint":"pump"}`, 201)
	call("GET", "/v1/trust-anchors", "", 200)
	call("PUT", "/v1/devices/pump/bootstrap/credentials", `{"psk":{"identity":"factory","key":"hex:000102030405060708090a0b0c0d0e0f"}}`, 201)
	call("GET", "/v1/devices/pump/bootstrap/credentials", "", 200)
}
