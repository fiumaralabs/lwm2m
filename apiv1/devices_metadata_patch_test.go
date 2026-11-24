package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestDevicePatchNameContract(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"one","name":"old","labels":{"site":"a"}}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w := deviceRequest(h, "PATCH", "/v1/devices/one", `{"name":"new"}`)
	var got struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || got.Name != "new" || got.Labels["site"] != "a" {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body.String())
	}
}

func TestOpenAPIDevicePatchMetadata(t *testing.T) {
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "labels", "profile"} {
		if len(doc.Components.Schemas["DevicePatch"].Properties[field]) == 0 {
			t.Errorf("missing %s", field)
		}
	}
}

func TestDevicePatchOtherTenant(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"test":  {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"},
		"other": {hash: sha256.Sum256([]byte("lwm_other_secret")), scopes: ",devices:write,", tenant: "other"},
	}}
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`); w.Code != 201 {
		t.Fatal(w.Body)
	}
	if w := keyRequest(h, "PATCH", "/v1/devices/one", "lwm_other_secret", `{"name":"x"}`); w.Code != 404 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
