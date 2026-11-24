package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestOpenAPIDeviceRequestBodies(t *testing.T) {
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
				OneOf      []struct {
					Required []string `json:"required"`
				} `json:"oneOf"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	targets := []string{
		"POST /v1/devices", "PATCH /v1/devices/{ep}", "PUT /v1/devices/{ep}/credentials", "PUT /v1/devices/{ep}/bootstrap", "PUT /v1/devices/{ep}/bootstrap/credentials", "POST /v1/devices/{ep}/operations", "POST /v1/devices/{ep}/observations", "POST /v1/devices/{ep}/firmware-updates",
	}
	allowed := []string{
		"PATCH /v1/device-profiles/{id}", "PATCH /v1/firmware/{id}", "PATCH /v1/observations/{id}", "PATCH /v1/settings", "PATCH /v1/tenants/{id}", "PATCH /v1/webhooks/{id}",
		"POST /v1/api-keys", "POST /v1/device-profiles", "POST /v1/firmware", "POST /v1/jobs", "POST /v1/jobs/{id}/cancel", "POST /v1/jobs/{id}/pause", "POST /v1/jobs/{id}/resume", "POST /v1/models", "POST /v1/tenants", "POST /v1/trust-anchors", "POST /v1/webhooks", "POST /v1/webhooks/{id}/deliveries/{deliveryId}/retry", "POST /v1/webhooks/{id}/rotate-secret", "POST /v1/webhooks/{id}/test", "PUT /v1/device-creates/{id}", "PUT /v1/est", "PUT /v1/firmware/{id}/content",
	}
	var missing []string
	for path, methods := range doc.Paths {
		for method, raw := range methods {
			if method != "post" && method != "put" && method != "patch" {
				continue
			}
			var op struct {
				RequestBody json.RawMessage `json:"requestBody"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatal(err)
			}
			if len(op.RequestBody) == 0 {
				missing = append(missing, strings.ToUpper(method)+" "+path)
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(allowed)
	if !reflect.DeepEqual(missing, allowed) {
		t.Errorf("missing request bodies: got %v, want %v", missing, allowed)
	}
	srv := server.New(server.Config{})
	defer srv.Close()
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,operations:write,firmware:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Server: srv})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`); w.Code != 201 {
		t.Fatalf("fixture: %d %s", w.Code, w.Body.String())
	}
	for _, key := range targets {
		t.Run(key, func(t *testing.T) {
			parts := strings.SplitN(key, " ", 2)
			method, path := strings.ToLower(parts[0]), parts[1]
			var op struct {
				RequestBody struct {
					Required bool `json:"required"`
					Content  map[string]struct {
						Schema struct {
							Ref string `json:"$ref"`
						} `json:"schema"`
						Example json.RawMessage `json:"example"`
					} `json:"content"`
				} `json:"requestBody"`
			}
			if err := json.Unmarshal(doc.Paths[path][method], &op); err != nil {
				t.Fatal(err)
			}
			media, ok := op.RequestBody.Content["application/json"]
			if !ok || !op.RequestBody.Required || len(media.Example) == 0 {
				t.Fatal("missing required JSON request body or example")
			}
			const prefix = "#/components/schemas/"
			if !strings.HasPrefix(media.Schema.Ref, prefix) {
				t.Fatalf("missing component ref: %q", media.Schema.Ref)
			}
			schema, ok := doc.Components.Schemas[strings.TrimPrefix(media.Schema.Ref, prefix)]
			if !ok {
				t.Fatal("unknown schema")
			}
			wantRequired := map[string][]string{
				"POST /v1/devices":                           {"endpoint"},
				"PUT /v1/devices/{ep}/credentials":           {"psk"},
				"PUT /v1/devices/{ep}/bootstrap/credentials": {"psk"},
				"POST /v1/devices/{ep}/operations":           {"type"},
				"POST /v1/devices/{ep}/observations":         {"paths"},
			}
			if !reflect.DeepEqual(schema.Required, wantRequired[key]) {
				t.Fatalf("required fields: got %v want %v", schema.Required, wantRequired[key])
			}
			actualPath := strings.ReplaceAll(path, "{ep}", "one")
			w := deviceRequest(h, parts[0], actualPath, string(media.Example))
			if w.Code < 200 || w.Code >= 300 {
				t.Fatalf("example: %d %s", w.Code, w.Body.String())
			}
			if key == "POST /v1/devices/{ep}/firmware-updates" {
				if len(schema.OneOf) != 2 || !reflect.DeepEqual(schema.OneOf[0].Required, []string{"firmware"}) || !reflect.DeepEqual(schema.OneOf[1].Required, []string{"uri"}) {
					t.Fatal("firmware/uri oneOf is missing")
				}
				w := deviceRequest(h, parts[0], actualPath, `{}`)
				if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
					t.Fatalf("without firmware or uri: %d %s", w.Code, w.Body.String())
				}
			}
			if key == "PUT /v1/devices/{ep}/bootstrap" {
				var serversSchema struct {
					Items struct {
						Required []string `json:"required"`
					} `json:"items"`
				}
				if err := json.Unmarshal(schema.Properties["servers"], &serversSchema); err != nil || !reflect.DeepEqual(serversSchema.Items.Required, []string{"shortServerId"}) {
					t.Fatal("missing required shortServerId schema")
				}
				var example map[string]any
				if err := json.Unmarshal(media.Example, &example); err != nil {
					t.Fatal(err)
				}
				servers := example["servers"].([]any)
				delete(servers[0].(map[string]any), "shortServerId")
				body, _ := json.Marshal(example)
				w := deviceRequest(h, parts[0], actualPath, string(body))
				if w.Code != 422 || w.Header().Get("Content-Type") != "application/problem+json" {
					t.Fatalf("without shortServerId: %d %s", w.Code, w.Body.String())
				}
			}
			for _, field := range schema.Required {
				t.Run("without_"+field, func(t *testing.T) {
					var example map[string]json.RawMessage
					if err := json.Unmarshal(media.Example, &example); err != nil {
						t.Fatal(err)
					}
					if _, ok := example[field]; !ok {
						t.Fatalf("example missing required %s", field)
					}
					delete(example, field)
					body, _ := json.Marshal(example)
					w := deviceRequest(h, parts[0], actualPath, string(body))
					if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
						t.Fatalf("without %s: %d %s", field, w.Code, w.Body.String())
					}
				})
			}
		})
	}
}

func TestDeviceBodyExistingValidationContract(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"PATCH", "/v1/devices/one", `{}`, 200}, {"PUT", "/v1/devices/one/bootstrap", `{}`, 204}, {"PUT", "/v1/devices/one/bootstrap", `{"servers":[{}]}`, 422},
	} {
		w := deviceRequest(h, tc.method, tc.path, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.status == 422 && w.Header().Get("Content-Type") != "application/problem+json" {
			t.Error("wrong content type")
		}
	}
}
