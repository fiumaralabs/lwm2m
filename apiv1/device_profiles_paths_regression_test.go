package apiv1

import "testing"

func TestDeviceProfileActionPathAndValueFields(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys(), DeviceProfiles: NewMemoryDeviceProfileStore()})
	tests := []struct {
		name, action string
		status       int
	}{
		{"create resource", `{"type":"create","path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":1}]}`, 400},
		{"create instance", `{"type":"create","path":"/1/0","values":[{"path":"/1/0/1","type":"integer","value":1}]}`, 400},
		{"scalar object", `{"type":"write","values":[{"path":"/3","type":"integer","value":1}]}`, 400},
		{"scalar instance", `{"type":"write","values":[{"path":"/3/0","type":"integer","value":1}]}`, 400},
		{"prefix and timestamp", `{"type":"write","values":[{"path":"/3/0/1","type":"string","value":"x","prefix":"d01","timestamp":"2026-10-08T09:59:00Z"}]}`, 201},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := profileWriteRequest(h, "POST", "/v1/device-profiles", `{"name":"probe","onRegister":[`+tc.action+`]}`, "lwm_test_secret", "")
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
