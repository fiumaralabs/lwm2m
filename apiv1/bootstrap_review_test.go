package apiv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestBootstrapGETRedactsSupportedSecuritySecrets(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	body := `{"servers":[{"shortServerId":1,"security":{"psk":{"identity":"visible-id","key":"hex:000102030405060708090a0b0c0d0e0f"},"x509":{"certificate":"visible-cert","privateKey":"private-secret"},"oscore":{"masterSecret":"oscore-secret","masterSalt":"visible-salt","senderId":"visible-sender"}}}],"writes":[{"path":"/21/0/2","value":"raw-oscore-secret"},{"path":"/23/0/0","value":"visible-value"}]}`
	if w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Read with only devices:read, independent of the write privilege.
	c.scopes = ",devices:read,"
	keys.entries["test"] = c
	w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, s := range []string{"0001020304050607", "private-secret", "oscore-secret", "raw-oscore-secret"} {
		if strings.Contains(w.Body.String(), s) {
			t.Errorf("GET leaked %q: %s", s, w.Body.String())
		}
	}
	for _, s := range []string{"visible-id", "visible-cert", "visible-salt", "visible-sender", "visible-value"} {
		if !strings.Contains(w.Body.String(), s) {
			t.Errorf("GET lost %q: %s", s, w.Body.String())
		}
	}
}

type blockedBootstrapPut struct {
	BootstrapConfigStore
	entered, release chan struct{}
}

func (s *blockedBootstrapPut) Put(tenant, endpoint string, b json.RawMessage) {
	close(s.entered)
	<-s.release
	s.BootstrapConfigStore.Put(tenant, endpoint, b)
}

func TestBootstrapPutConcurrentDeviceDeletion(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	configs := &blockedBootstrapPut{BootstrapConfigStore: NewMemoryBootstrapConfigStore(), entered: make(chan struct{}), release: make(chan struct{})}
	devices := &memory.Devices{}
	h := New(Options{Keys: keys, Devices: devices, BootstrapConfigs: configs})
	other := New(Options{Keys: keys, Devices: devices, BootstrapConfigs: configs})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	putDone := make(chan int, 1)
	go func() { putDone <- deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", `{}`).Code }()
	<-configs.entered
	deleted := make(chan int, 1)
	go func() { deleted <- deviceRequest(other, "DELETE", "/v1/devices/a", "").Code }()
	select {
	case code := <-deleted:
		close(configs.release)
		<-putDone
		t.Fatalf("device deletion completed before in-flight PUT: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	close(configs.release)
	if code := <-putDone; code != 204 {
		t.Fatalf("PUT: %d", code)
	}
	if code := <-deleted; code != 204 {
		t.Fatalf("DELETE: %d", code)
	}
	if _, ok := configs.Get("default", "a"); ok {
		t.Fatal("in-flight PUT restored deleted config")
	}
	if w := deviceRequest(other, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatalf("recreate: %d", w.Code)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", ""); w.Code != 404 {
		t.Fatalf("recreated device retained config: %d", w.Code)
	}
}

func TestBootstrapRemovedWhenDeviceRecreated(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	configs := NewMemoryBootstrapConfigStore()
	h := New(Options{Keys: keys, BootstrapConfigs: configs})
	for _, step := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"PUT", "/v1/devices/a/bootstrap", `{"servers":[{"shortServerId":1,"security":{"oscore":{"masterSecret":"old-secret"}}}]}`, 204},
		{"DELETE", "/v1/devices/a", "", 204},
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"GET", "/v1/devices/a/bootstrap", "", 404},
	} {
		if w := deviceRequest(h, step.method, step.path, step.body); w.Code != step.status {
			t.Fatalf("%s %s: got %d, want %d: %s", step.method, step.path, w.Code, step.status, w.Body.String())
		}
	}
	if _, ok := configs.Get("default", "a"); ok {
		t.Fatal("deleted device bootstrap config remains in store")
	}
}
