package apiv1

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceCreateIdempotencyReplay(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	post := func(key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := post("one", `{"endpoint":"a"}`)
	retry := post("one", `{"endpoint":"a"}`)
	if first.Code != 201 || retry.Code != first.Code || retry.Body.String() != first.Body.String() || retry.Header().Get("Location") != first.Header().Get("Location") {
		t.Errorf("first %d %s; retry %d %s", first.Code, first.Body, retry.Code, retry.Body)
	}
	changedSuccess := post("one", `{"endpoint":"different"}`)
	if changedSuccess.Code != first.Code || changedSuccess.Body.String() != first.Body.String() {
		t.Fatalf("changed successful retry: %d %s", changedSuccess.Code, changedSuccess.Body)
	}
	deleted := deviceRequest(h, "DELETE", "/v1/devices/a", "")
	if deleted.Code != 204 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	afterDelete := post("one", `{"endpoint":"a"}`)
	if afterDelete.Code != first.Code || afterDelete.Body.String() != first.Body.String() || afterDelete.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("retry after delete: %d %s", afterDelete.Code, afterDelete.Body)
	}
	invalid := post("invalid", `{"endpoint":""}`)
	changed := post("invalid", `{"endpoint":"b"}`)
	if invalid.Code != 400 || changed.Code != invalid.Code || changed.Body.String() != invalid.Body.String() {
		t.Fatalf("invalid %d %s; changed %d %s", invalid.Code, invalid.Body, changed.Code, changed.Body)
	}
}
