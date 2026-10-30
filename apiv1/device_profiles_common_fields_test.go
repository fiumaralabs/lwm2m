package apiv1

import (
	"net/http"
	"testing"
)

func TestDeviceProfileOperationCommonFields(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys()})
	for _, action := range []string{
		`{"type":"sync-clock","format":"bogus"}`,
		`{"type":"sync-clock","accept":"bogus"}`,
		`{"type":"sync-clock","whenOffline":"bogus"}`,
		`{"type":"sync-clock","ttlSeconds":-1}`,
	} {
		w := profileWriteRequest(h, http.MethodPost, "/v1/device-profiles", `{"name":"x","onRegister":[`+action+`]}`, "lwm_test_secret", "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", action, w.Code)
		}
	}
	for _, action := range []string{
		`{"type":"sync-clock","format":"senml-cbor","accept":"tlv","whenOffline":"reject","ttlSeconds":0}`,
	} {
		w := profileWriteRequest(h, http.MethodPost, "/v1/device-profiles", `{"name":"x","onRegister":[`+action+`]}`, "lwm_test_secret", "")
		if w.Code != http.StatusCreated {
			t.Errorf("%s: got %d", action, w.Code)
		}
	}
}
