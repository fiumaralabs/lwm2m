package apiv1

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFirmwareCreate(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x firmware:write,devices:read", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys})
	r := httptest.NewRequest("POST", "/v1/firmware", strings.NewReader(`{"name":"pump","version":"1"}`))
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"status":"empty"`) {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
}
