package apiv1

import (
	"crypto/sha256"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const settingsDefault = `{"registration":{"autoCreateDevices":false,"allowNoSec":false,"defaultProfile":null},"queueMode":{"smsWakeUp":false},"operations":{"defaultTtlSeconds":86400},"events":{"retentionDays":7},"registrationProfiles":{}}`

func settingsRequest(h http.Handler, method, body, match string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/settings", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer lwm_root_synthetic")
	r.Header.Set("Content-Type", "application/merge-patch+json")
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestSettingsMerge(t *testing.T) {
	h := keyHandler(t)
	first := settingsRequest(h, "PATCH", `{"registration":{"autoCreateDevices":true,"allowNoSec":true,"defaultProfile":"p"},"queueMode":{"smsWakeUp":true},"operations":{"defaultTtlSeconds":90000},"events":{"retentionDays":30},"registrationProfiles":{"p":[{"id":0,"version":"1.1"},{"id":65535}],"q":[]}}`, "")
	if first.Code != 200 {
		t.Fatalf("patch: %d %s", first.Code, first.Body)
	}
	second := settingsRequest(h, "PATCH", `{"registration":{"defaultProfile":null},"registrationProfiles":{"p":null}}`, "")
	want := `{"registration":{"autoCreateDevices":true,"allowNoSec":true,"defaultProfile":null},"queueMode":{"smsWakeUp":true},"operations":{"defaultTtlSeconds":90000},"events":{"retentionDays":30},"registrationProfiles":{"q":[]}}`
	if second.Code != 200 || strings.TrimSpace(second.Body.String()) != want || second.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatalf("merge: %d %s", second.Code, second.Body)
	}
	third := settingsRequest(h, "PATCH", `{"registrationProfiles":{}}`, "")
	if strings.TrimSpace(third.Body.String()) != want {
		t.Fatal(third.Body)
	}
	if got := settingsRequest(h, "GET", "", ""); got.Body.String() != third.Body.String() {
		t.Fatal(got.Body)
	}
}
func TestSettingsInvalid(t *testing.T) {
	h := keyHandler(t)
	for _, body := range []string{`null`, `[]`, `1`, `{} {}`, `{"unknown":1}`, `{"Registration":{}}`, `{"registration":null}`, `{"registration":false}`, `{"registration":{"unknown":true}}`, `{"registration":{"autoCreateDevices":null}}`, `{"registration":{"allowNoSec":"true"}}`, `{"registration":{"defaultProfile":3}}`, `{"queueMode":null}`, `{"queueMode":{"smsWakeUp":null}}`, `{"operations":null}`, `{"operations":{"defaultTtlSeconds":null}}`, `{"operations":{"defaultTtlSeconds":0}}`, `{"operations":{"defaultTtlSeconds":-1}}`, `{"operations":{"defaultTtlSeconds":1.5}}`, `{"events":null}`, `{"events":{"retentionDays":null}}`, `{"events":{"retentionDays":0}}`, `{"events":{"retentionDays":"1"}}`, `{"registrationProfiles":null}`, `{"registrationProfiles":[]}`, `{"registrationProfiles":{"p":{}}}`, `{"registrationProfiles":{"p":[{}]}}`, `{"registrationProfiles":{"p":[null]}}`, `{"registrationProfiles":{"p":[{"id":null}]}}`, `{"registrationProfiles":{"p":[{"id":-1}]}}`, `{"registrationProfiles":{"p":[{"id":65536}]}}`, `{"registrationProfiles":{"p":[{"id":1.2}]}}`, `{"registrationProfiles":{"p":[{"id":1,"extra":true}]}}`, `{"registrationProfiles":{"p":[{"id":1,"version":null}]}}`, `{"registrationProfiles":{"p":[{"id":1,"version":""}]}}`, `{"registrationProfiles":{"p":[{"id":1,"version":2}]}}`} {
		t.Run(body, func(t *testing.T) {
			w := settingsRequest(h, "PATCH", body, "")
			if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("invalid: %d %s", w.Code, w.Body)
			}
			if got := settingsRequest(h, "GET", "", ""); strings.TrimSpace(got.Body.String()) != settingsDefault || got.Header().Get("ETag") != `"settings-1"` {
				t.Fatal("invalid mutation", got.Body)
			}
		})
	}
}
func TestSettingsAccessAndTransport(t *testing.T) {
	k := Keys{entries: map[string]credential{}}
	for _, v := range []struct{ id, tenant, scope string }{{"root", "a", ",admin,"}, {"reader", "a", ",devices:read,"}, {"other", "b", ",admin,"}, {"writer", "a", ",devices:write,"}} {
		k.entries[v.id] = credential{hash: sha256.Sum256([]byte("lwm_" + v.id + "_synthetic")), tenant: v.tenant, scopes: v.scope}
	}
	h := New(Options{Keys: k})
	for _, tc := range []struct {
		method, token, content string
		status                 int
	}{{"GET", "root", "", 200}, {"GET", "reader", "", 403}, {"PATCH", "reader", "application/merge-patch+json", 403}, {"GET", "writer", "", 403}, {"GET", "", "", 401}, {"PATCH", "root", "application/json", 415}, {"PATCH", "root", "", 415}, {"PATCH", "root", "application/merge-patch+json; charset=utf-8", 200}, {"PUT", "root", "", 405}} {
		r := httptest.NewRequest(tc.method, "/v1/settings", strings.NewReader(`{}`))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer lwm_"+tc.token+"_synthetic")
		}
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%+v: %d %s", tc, w.Code, w.Body)
		}
		if tc.status == 403 && w.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%+v: expected problem+json, got %q", tc, w.Header().Get("Content-Type"))
		}
	}
	settingsRequest(h, "PATCH", `{"events":{"retentionDays":90}}`, "")
	if got := keyRequest(h, "GET", "/v1/settings", "lwm_other_synthetic", ""); strings.TrimSpace(got.Body.String()) != settingsDefault {
		t.Fatal("tenant leaked", got.Body)
	}
}
func TestSettingsConditions(t *testing.T) {
	h := keyHandler(t)
	tag := settingsRequest(h, "GET", "", "").Header().Get("ETag")
	for _, match := range []string{`"stale"`, "W/" + tag, "malformed"} {
		if w := settingsRequest(h, "PATCH", `{}`, match); w.Code != 412 {
			t.Fatalf("%s: %d %s", match, w.Code, w.Body)
		}
	}
	for _, match := range []string{`"stale", ` + tag, "*"} {
		w := settingsRequest(h, "PATCH", `{}`, match)
		if w.Code != 200 || w.Header().Get("ETag") == tag {
			t.Fatalf("match %s: %d", match, w.Code)
		}
		tag = w.Header().Get("ETag")
	}
	start := make(chan struct{})
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; results <- settingsRequest(h, "PATCH", `{"events":{"retentionDays":80}}`, tag).Code }()
	}
	close(start)
	a, b := <-results, <-results
	if !((a == 200 && b == 412) || (a == 412 && b == 200)) {
		t.Fatalf("competing: %d %d", a, b)
	}
}
func TestSettingsInjectedStoreOwnership(t *testing.T) {
	s := &memory.Settings{}
	for _, raw := range []string{`{} {}`, `{} trailing`} {
		if _, err := s.Patch("default", []byte(raw), store.WebhookMatch{}); err != store.ErrSettingsInvalid {
			t.Fatalf("raw patch %q: %v", raw, err)
		}
	}
	v, err := s.Patch("default", []byte(`{"operations":{"defaultTtlSeconds":9223372036854775807},"registration":{"defaultProfile":"p"},"registrationProfiles":{"p":[{"id":1,"version":"1.0"}]}}`), store.WebhookMatch{})
	if err != nil {
		t.Fatal(err)
	}
	*v.Registration.DefaultProfile = "changed"
	*v.RegistrationProfiles["p"][0].Version = "changed"
	delete(v.RegistrationProfiles, "p")
	k := Keys{entries: map[string]credential{"root": {hash: sha256.Sum256([]byte("lwm_root_synthetic")), tenant: "default", scopes: ",admin,"}}}
	h := New(Options{Keys: k, Settings: s})
	w := settingsRequest(h, "PATCH", `{"events":{"retentionDays":1000000000}}`, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"defaultTtlSeconds":9223372036854775807`) || !strings.Contains(w.Body.String(), `"version":"1.0"`) || !strings.Contains(w.Body.String(), `"defaultProfile":"p"`) {
		t.Fatalf("owned, precise merge: %d %s", w.Code, w.Body)
	}
	got, _ := s.Get("default")
	got.RegistrationProfiles["p"][0].ID = 42
	*got.RegistrationProfiles["p"][0].Version = "changed"
	*got.Registration.DefaultProfile = "changed"
	again, _ := s.Get("default")
	other, _ := s.Get("other")
	if again.RegistrationProfiles["p"][0].ID != 1 || *again.RegistrationProfiles["p"][0].Version != "1.0" || *again.Registration.DefaultProfile != "p" || other.Events.RetentionDays != 7 {
		t.Fatal("store ownership or isolation")
	}
}
func TestSettingsDefaults(t *testing.T) {
	w := settingsRequest(keyHandler(t), "GET", "", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != settingsDefault || w.Header().Get("ETag") == "" {
		t.Fatalf("defaults: %d %s %v", w.Code, w.Body, w.Header())
	}
}
