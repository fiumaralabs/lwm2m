package apiv1

import "testing"

func TestDeviceProfileActionValues(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys(), DeviceProfiles: NewMemoryDeviceProfileStore()})
	tests := []struct {
		name, action, value string
		status              int
	}{
		{"write missing value", "write", `{"path":"/1/0/1","type":"integer"}`, 400},
		{"create missing value", "create", `{"path":"/1/0/1","type":"integer"}`, 400},
		{"invalid integer", "write", `{"path":"/1/0/1","type":"integer","value":"bad"}`, 400},
		{"invalid boolean", "create", `{"path":"/1/0/1","type":"boolean","value":12}`, 400},
		{"null value", "write", `{"path":"/1/0/1","type":"string","value":null}`, 400},
		{"unknown type", "write", `{"path":"/1/0/1","type":"bad","value":1}`, 400},
		{"untyped object", "write", `{"path":"/1/0/1","value":{}}`, 400},
		{"valid value", "write", `{"path":"/1/0/1","type":"integer","value":1}`, 201},
		{"model inferred type", "write", `{"path":"/1/0/1","value":1}`, 201},
		{"empty instance", "create", `{"path":"/1/0"}`, 201},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"name":"probe","onRegister":[{"type":"` + tt.action + `","path":"/1","values":[` + tt.value + `]}]}`
			w := profileWriteRequest(h, "POST", "/v1/device-profiles", body, "lwm_test_secret", "")
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tt.status, w.Body.String())
			}
		})
	}
}
