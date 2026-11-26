package apiv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

type lifecycleFailureStore struct {
	BootstrapConfigStore
	failPut, failDelete bool
}

func (s *lifecycleFailureStore) Put(t, e string, b json.RawMessage) error {
	if s.failPut {
		return errors.New("injected")
	}
	return s.BootstrapConfigStore.Put(t, e, b)
}
func (s *lifecycleFailureStore) Delete(t, e string) (bool, error) {
	if s.failDelete {
		return false, errors.New("injected")
	}
	return s.BootstrapConfigStore.Delete(t, e)
}
func lifecycleKeys() Keys {
	k := deviceKeys()
	c := k.entries["test"]
	c.scopes += "bootstrap:write,"
	k.entries["test"] = c
	return k
}

func TestGeneratedBootstrapFailedPutPreservesCredential(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	configs := &lifecycleFailureStore{BootstrapConfigStore: NewMemoryBootstrapConfigStore()}
	h := New(Options{Keys: lifecycleKeys(), Server: s, BootstrapConfigs: configs})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	body := `{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}}]}`
	if w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	old, ok := s.Security().ByEndpoint("a")
	if !ok {
		t.Fatal("missing old credential")
	}
	s.Store().Add(&server.Registration{ID: "reg-a", Endpoint: "a"})
	raw, _, _ := configs.Get("default", "a")
	configs.failPut = true
	if w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body); w.Code != 500 {
		t.Fatal(w.Code)
	}
	current, ok := s.Security().ByEndpoint("a")
	if !ok || current.PSKIdentity != old.PSKIdentity || !bytes.Equal(current.PSKKey, old.PSKKey) {
		t.Fatal("failed write changed credential")
	}
	if _, ok := s.Store().ByEndpoint("a"); !ok {
		t.Fatal("failed write removed registration")
	}
	after, _, _ := configs.Get("default", "a")
	if !bytes.Equal(raw, after) {
		t.Fatal("failed write changed config")
	}
}

func TestDeviceDeleteBootstrapFailureRetryAndRecreate(t *testing.T) {
	configs := &lifecycleFailureStore{BootstrapConfigStore: NewMemoryBootstrapConfigStore()}
	h := New(Options{Keys: lifecycleKeys(), BootstrapConfigs: configs})
	for _, step := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"PUT", "/v1/devices/a/bootstrap", `{"servers":[{"shortServerId":1}]}`, 204},
	} {
		if w := deviceRequest(h, step.method, step.path, step.body); w.Code != step.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	configs.failDelete = true
	if w := deviceRequest(h, "DELETE", "/v1/devices/a", ""); w.Code != 500 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/a", ""); w.Code != 200 {
		t.Fatalf("device lost after failed cleanup: %d", w.Code)
	}
	if events := history(t, h, "/v1/devices/a/events").Items; len(events) != 1 || events[0].Type != "lwm2m.device.created" {
		t.Fatalf("unexpected events after failure: %v", events)
	}
	configs.failDelete = false
	for _, step := range []struct {
		method, path, body string
		status             int
	}{
		{"DELETE", "/v1/devices/a", "", 204},
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"GET", "/v1/devices/a/bootstrap", "", 404},
	} {
		if w := deviceRequest(h, step.method, step.path, step.body); w.Code != step.status {
			t.Fatal(step.method, step.path, w.Code, w.Body.String())
		}
	}
	events := history(t, h, "/v1/devices/a/events").Items
	if len(events) != 3 || events[1].Type != "lwm2m.device.deleted" {
		t.Fatalf("missing deletion event after retry: %v", events)
	}
}
