package apiv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFirmwareContentRoundTrip(t *testing.T) {
	s := NewMemoryFirmwareStore()
	if err := s.Create("a", Firmware{ID: "fw", Status: "empty"}); err != nil {
		t.Fatal(err)
	}
	keys := Keys{entries: map[string]credential{"a": {hash: sha256.Sum256([]byte("lwm_a_secret")), scopes: ",devices:read,firmware:write,", tenant: "a"}}}
	h := New(Options{Firmware: s, Keys: keys})
	data := []byte{0, 1, 2, 255}
	sum := sha256.Sum256(data)
	r := httptest.NewRequest("PUT", "/v1/firmware/fw/content", bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer lwm_a_secret")
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(sum[:]))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 && w.Code != 204 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/firmware/fw/content", nil)
	r.Header.Set("Authorization", "Bearer lwm_a_secret")
	h.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("download: %d %x", w.Code, w.Body.Bytes())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/firmware/fw", nil)
	r.Header.Set("Authorization", "Bearer lwm_a_secret")
	h.ServeHTTP(w, r)
	var f struct {
		Status string `json:"status"`
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil || f.Status != "ready" || f.Size != len(data) || f.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("metadata: %+v %v", f, err)
	}
}

func TestFirmwareContentErrors(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"a":    {hash: sha256.Sum256([]byte("lwm_a_secret")), scopes: ",devices:read,firmware:write,", tenant: "a"},
		"b":    {hash: sha256.Sum256([]byte("lwm_b_secret")), scopes: ",devices:read,firmware:write,", tenant: "b"},
		"read": {hash: sha256.Sum256([]byte("lwm_read_secret")), scopes: ",devices:read,", tenant: "a"},
	}}
	s := NewMemoryFirmwareStoreWithLimit(4)
	_ = s.Create("a", Firmware{ID: "fw", Status: "empty"})
	h := New(Options{Keys: keys, Firmware: s})
	call := func(token, method, path, body, digest string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_"+token+"_secret")
		r.Header.Set("Content-Type", "application/octet-stream")
		if digest != "" {
			r.Header.Set("Digest", digest)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, token, w.Code, want, w.Body.String())
		}
	}
	path := "/v1/firmware/fw/content"
	call("a", "GET", path, "", "", 404)
	call("read", "PUT", path, "123", "", 403)
	call("b", "GET", path, "", "", 404)
	call("b", "PUT", path, "123", "", 404)
	call("a", "PUT", "/v1/firmware/missing/content", "123", "", 404)
	call("a", "PUT", path, "12345", "", 413)
	call("a", "PUT", path, "123", "sha-256=bad", 400)
	f, _ := s.Get("a", "fw")
	if f.Status != "empty" || f.Size != 0 || f.SHA256 != "" {
		t.Fatalf("failed upload mutated metadata: %+v", f)
	}
	call("a", "PUT", path, "1234", "", 204)
	call("a", "PUT", path, "123", "", 409)
}
