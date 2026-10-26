package apiv1

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deviceRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestDeviceCreateGet(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/devices", `{"endpoint":"pump/a","name":"Pump","labels":{"site":"eu"}}`, 201},
		{"GET", "/v1/devices/pump%2Fa", "", 200},
		{"POST", "/v1/devices", `{"endpoint":"pump/a"}`, 409},
		{"POST", "/v1/devices", `{"endpoint":"b","typo":true}`, 400},
		{"POST", "/v1/devices", `{"endpoint":""}`, 400},
	} {
		w := deviceRequest(h, tc.method, tc.path, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
}
