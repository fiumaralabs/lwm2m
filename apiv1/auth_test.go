package apiv1

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticate(t *testing.T) {
	token := "lwm_test_secret"
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read", sha256.Sum256([]byte(token)))))
	if err != nil {
		t.Fatal(err)
	}
	if TenantFrom(context.Background()) != "" {
		t.Fatal("unauthenticated tenant")
	}
	for i, headers := range [][]string{nil, {""}, {"Basic " + token}, {"Bearer lwm_test_bad"}, {"Bearer lwm_unknown_secret"}, {"Bearer lwm_test_"}, {"Bearer " + token + " extra"}, {"Bearer " + token, "Bearer " + token}, {"Bearer " + token}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header["Authorization"] = headers
			rec := httptest.NewRecorder()
			Authenticate(keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if TenantFrom(r.Context()) != "default" {
					t.Error("missing tenant")
				}
				w.WriteHeader(204)
			})).ServeHTTP(rec, req)
			want := 401
			if len(headers) == 1 && headers[0] == "Bearer "+token {
				want = 204
			}
			if rec.Code != want {
				t.Fatalf("status=%d want=%d", rec.Code, want)
			}
			if strings.Contains(rec.Body.String(), token) {
				t.Fatal("token leaked")
			}
		})
	}
}

func TestRequireScope(t *testing.T) {
	for _, tc := range []struct {
		scope string
		want  int
	}{{"devices:read", 403}, {"jobs:write", 204}, {"admin", 204}} {
		keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x %s", sha256.Sum256([]byte("lwm_test_secret")), tc.scope)))
		if err != nil {
			t.Fatal(err)
		}
		handler := RequireScope("jobs:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		req := httptest.NewRequest("GET", "/", nil)
		handler = Authenticate(keys, handler)
		req.Header.Set("Authorization", "Bearer lwm_test_secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%+v: status=%d", tc, rec.Code)
		}
	}
}

func TestRequirePlatformScope(t *testing.T) {
	for _, tc := range []struct {
		scopes string
		want   int
	}{
		{"admin", http.StatusForbidden},
		{"platform", http.StatusNoContent},
		{"admin,platform", http.StatusNoContent},
		{"devices:read", http.StatusForbidden},
	} {
		t.Run(tc.scopes, func(t *testing.T) {
			keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x %s", sha256.Sum256([]byte("lwm_test_secret")), tc.scopes)))
			if err != nil {
				t.Fatal(err)
			}
			handler := Authenticate(keys, RequireScope("platform")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})))
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer lwm_test_secret")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d", rec.Code, tc.want)
			}
		})
	}
}

func TestLoadKeys(t *testing.T) {
	line := fmt.Sprintf("key_test sha256:%x devices:read", sha256.Sum256([]byte("lwm_test_secret")))
	for _, tc := range []struct {
		text string
		bad  bool
	}{
		{"\n# comment\n" + line + "\n", false}, {"", false},
		{line + "\n" + line, true}, {"key_test sha256:00 devices:read", true},
		{strings.Replace(line, "devices:read", "unknown", 1), true},
		{strings.Replace(line, "key_test", "key_", 1), true},
		{strings.Replace(line, "sha256:", "md5:", 1), true},
		{line + " extra", true}, {strings.Replace(line, "devices:read", "devices:read,", 1), true},
	} {
		if _, err := LoadKeys(strings.NewReader(tc.text)); (err != nil) != tc.bad {
			t.Errorf("bad=%v: %v", tc.bad, err)
		}
	}
}
