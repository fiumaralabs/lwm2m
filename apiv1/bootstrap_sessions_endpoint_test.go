package apiv1

import (
	"encoding/json"
	"testing"
)

func TestBootstrapSessionsEmpty(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := deviceRequest(h, "GET", "/v1/devices/pump/bootstrap/sessions", "")
	if w.Code != 200 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Items []BootstrapSession `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("got %s, err %v", w.Body.String(), err)
	}
}
