package apiv1

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type failingBootstrapStore struct{}

func (failingBootstrapStore) Put(string, string, json.RawMessage) error {
	return errors.New("injected")
}
func (failingBootstrapStore) Get(string, string) (json.RawMessage, bool, error) {
	return nil, false, errors.New("injected")
}
func (failingBootstrapStore) Delete(string, string) (bool, error) {
	return false, errors.New("injected")
}

func TestBootstrapStorageFailures(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys, BootstrapConfigs: failingBootstrapStore{}})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			w := deviceRequest(h, method, "/v1/devices/a/bootstrap", `{}`)
			if w.Code != 500 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
