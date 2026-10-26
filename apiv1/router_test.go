package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRouter(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Keys: keys, Version: "test", LwM2MVersions: []string{"1.0", "1.1", "1.2"}, Capabilities: map[string]bool{"bootstrap": true}, Endpoints: map[string]map[string]string{"server": {"coap": "coap://localhost:5683"}}}
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/v1/info", "", 401}, {"/v1/info", "invalid", 401}, {"/v1/info", "lwm_test_bad", 401},
		{"/v1/missing", "lwm_test_secret", 404}, {"/v1/info", "lwm_test_secret", 200},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		New(opts).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var got Options
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			got.Keys = keys
			if !reflect.DeepEqual(got, opts) {
				t.Fatalf("info=%+v", got)
			}
		} else {
			var p Problem
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if w.Header().Get("Content-Type") != "application/problem+json" || p.Status != tc.status || p.Type != "https://lwm2m.dev/errors/"+strings.ReplaceAll(p.Code, "_", "-") || p.Title == "" || p.Detail == "" {
				t.Fatalf("invalid problem: %+v", p)
			}
		}
	}
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/info", nil))
	if w.Code != 401 {
		t.Fatal("no keys must deny all requests")
	}
}

func TestDecodeJSON(t *testing.T) {
	for _, body := range []string{`{"typo":1}`, `{"name":`, `{} {}`, `{}`} {
		w := httptest.NewRecorder()
		var value struct {
			Name string `json:"name"`
		}
		ok := DecodeJSON(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), &value)
		if ok != (body == `{}`) {
			t.Fatalf("body=%s ok=%v", body, ok)
		}
		if body == `{"typo":1}` && (w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"unknown_field"`)) {
			t.Fatal(w.Body.String())
		}
	}
}
