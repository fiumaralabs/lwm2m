package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestInlineCredentialOpenAPI(t *testing.T) {
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Ref string `json:"$ref"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Components.Schemas["DeviceCreate"].Properties["credentials"].Ref; got != "#/components/schemas/DeviceCredentials" {
		t.Errorf("credential schema reference = %q", got)
	}
}

func TestInlineCredentials(t *testing.T) {
	s := server.New(server.Config{})
	t.Cleanup(func() { _ = s.Close() })
	d := &memory.Devices{}
	events := &memory.Events{}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}, "foreign": {hash: sha256.Sum256([]byte("lwm_foreign_secret")), scopes: ",devices:read,devices:write,", tenant: "other"}}}
	h := New(Options{Keys: keys, Devices: d, Server: s, EventOptions: EventOptions{Events: events}})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	key := "000102030405060708090a0b0c0d0e0f"
	cred := `{"psk":{"identity":"identity-a","key":"hex:` + key + `"}}`
	w := request("POST", "/v1/devices", "lwm_test_secret", `{"endpoint":"a","credentials":`+cred+`}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), key) {
		t.Error("secret in response")
	}
	info, ok := s.Security().ByEndpoint("a")
	if !ok || info.PSKIdentity != "identity-a" {
		t.Error("credential missing")
	}
	ev, _, err := events.List("default", store.EventFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ev {
		if strings.Contains(string(e.Data), key) {
			t.Error("secret in event")
		}
	}
	w = request("POST", "/v1/devices", "lwm_test_secret", `{"endpoint":"invalid","credentials":{"psk":{"identity":"bad","key":"hex:00"}}}`)
	if w.Code != 400 {
		t.Errorf("invalid: %d", w.Code)
	}
	if _, err := d.Get("default", "invalid"); err == nil {
		t.Error("invalid created device")
	}
	for _, body := range []string{
		`{"endpoint":"null","credentials":null}`,
		`{"endpoint":"generate","credentials":{"psk":{"generate":true}}}`,
		`{"endpoint":"unknown","credentials":{"psk":{"identity":"a","key":"hex:` + key + `","extra":true}}}`,
	} {
		w = request("POST", "/v1/devices", "lwm_test_secret", body)
		if w.Code != 400 {
			t.Errorf("invalid input %s: %d", body, w.Code)
		}
	}
	w = request("POST", "/v1/devices", "lwm_test_secret", `{"endpoint":"other","credentials":`+cred+`}`)
	if w.Code != 409 {
		t.Errorf("identity conflict: %d", w.Code)
	}
	if _, err := d.Get("default", "other"); err == nil {
		t.Error("conflict created device")
	}
	w = request("POST", "/v1/devices", "lwm_test_secret", `{"endpoint":"a","credentials":{"psk":{"identity":"identity-b","key":"hex:`+key+`"}}}`)
	if w.Code != 409 {
		t.Errorf("endpoint conflict: %d", w.Code)
	}
	info, _ = s.Security().ByEndpoint("a")
	if info.PSKIdentity != "identity-a" {
		t.Error("existing credential changed")
	}
	w = request("GET", "/v1/devices/a/credentials", "lwm_foreign_secret", "")
	if w.Code != 404 {
		t.Errorf("foreign read: %d", w.Code)
	}
	w = request("POST", "/v1/devices", "lwm_foreign_secret", `{"endpoint":"foreign","credentials":`+cred+`}`)
	if w.Code == 201 {
		t.Error("foreign credential accepted")
	}
	if _, err := d.Get("other", "foreign"); err == nil {
		t.Error("foreign device created")
	}
}
