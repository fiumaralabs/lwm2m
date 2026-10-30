package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type firmwareInUse struct{ FirmwareStore }

func (s firmwareInUse) Delete(tenant, id string) (bool, bool) {
	_, ok := s.Get(tenant, id)
	return ok, ok
}
func TestFirmwareContract(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"a":    {hash: sha256.Sum256([]byte("lwm_a_secret")), scopes: ",devices:read,firmware:write,", tenant: "a"},
		"b":    {hash: sha256.Sum256([]byte("lwm_b_secret")), scopes: ",devices:read,firmware:write,", tenant: "b"},
		"read": {hash: sha256.Sum256([]byte("lwm_read_secret")), scopes: ",devices:read,", tenant: "a"},
	}}
	store := NewMemoryFirmwareStore()
	h := New(Options{Keys: keys, Firmware: store})
	call := func(h http.Handler, token, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer lwm_"+token+"_secret")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		if want >= 400 && w.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatal(w.Header())
		}
		return w
	}
	call(h, "", "GET", "/v1/firmware", "", 401)
	call(h, "read", "POST", "/v1/firmware", `{}`, 403)
	call(h, "a", "POST", "/v1/firmware", `{}`, 400)
	call(h, "a", "POST", "/v1/firmware", `{"name":"pump","version":"1","bad":true}`, 400)
	w := call(h, "a", "POST", "/v1/firmware", `{"name":"pump","version":"1","description":"first","labels":{"hw":"rev-a"}}`, 201)
	var f Firmware
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil || f.ID == "" || f.Status != "empty" || f.CreatedAt.IsZero() {
		t.Fatalf("%+v %v", f, err)
	}
	path := "/v1/firmware/" + f.ID
	call(h, "b", "GET", path, "", 404)
	call(h, "b", "PATCH", path, `{"name":"foreign"}`, 404)
	call(h, "b", "DELETE", path, "", 404)
	w = call(h, "b", "GET", "/v1/firmware", "", 200)
	if strings.Contains(w.Body.String(), f.ID) {
		t.Fatal(w.Body.String())
	}
	w = call(h, "a", "GET", "/v1/firmware", "", 200)
	if !strings.Contains(w.Body.String(), f.ID) {
		t.Fatal(w.Body.String())
	}
	call(h, "a", "PATCH", path, `{"version":"2"}`, 400)
	call(h, "a", "PATCH", path, `{"name":""}`, 400)
	w = call(h, "a", "PATCH", path, `{"name":"new","description":"second","labels":{"hw":"rev-b"}}`, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil || f.Name != "new" || f.Version != "1" || f.Labels["hw"] != "rev-b" {
		t.Fatalf("%+v %v", f, err)
	}
	call(h, "a", "GET", path, "", 200)
	busy := New(Options{Keys: keys, Firmware: firmwareInUse{store}})
	w = call(busy, "a", "DELETE", path, "", 409)
	if !strings.Contains(w.Body.String(), `"code":"in_use"`) {
		t.Fatal(w.Body.String())
	}
	call(h, "a", "GET", path, "", 200)
	call(h, "a", "DELETE", path, "", 204)
	call(h, "a", "DELETE", path, "", 404)
	call(h, "a", "GET", path, "", 404)
	call(h, "a", "PUT", "/v1/firmware", "", 405)
	call(h, "a", "GET", path+"/content", "", 404)
}
func TestFirmwareSnapshots(t *testing.T) {
	s := NewMemoryFirmwareStore()
	f := Firmware{ID: "fw_1", Labels: map[string]string{"k": "v"}}
	if err := s.Create("a", f); err != nil {
		t.Fatal(err)
	}
	f.Labels["k"] = "changed"
	got, _ := s.Get("a", "fw_1")
	if got.Labels["k"] != "v" {
		t.Fatal(got)
	}
	got.Labels["k"] = "changed"
	got = s.List("a")[0]
	if got.Labels["k"] != "v" {
		t.Fatal(got)
	}
}
