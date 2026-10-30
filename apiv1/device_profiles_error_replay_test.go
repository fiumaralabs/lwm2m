package apiv1

import (
	"net/http"
	"testing"
)

func TestDeviceProfileValidationErrorReplay(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys()})
	for _, body := range []string{`{"name":""}`, `{"name":"pump","unknown":true}`, `{`} {
		key := body
		first := profileWriteRequest(h, http.MethodPost, "/v1/device-profiles", body, "lwm_test_secret", key)
		second := profileWriteRequest(h, http.MethodPost, "/v1/device-profiles", `{"name":"valid"}`, "lwm_test_secret", key)
		if first.Code != 400 || second.Code != first.Code || second.Body.String() != first.Body.String() || second.Header().Get("Content-Type") != first.Header().Get("Content-Type") {
			t.Errorf("first %d %s; replay %d %s", first.Code, first.Body, second.Code, second.Body)
		}
	}
}
