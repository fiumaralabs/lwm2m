package apiv1

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestOpenAPIProfilesFirmwareResponses(t *testing.T) {
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	routes := []struct{ path, method, status, media string }{
		{"/v1/device-profiles", "get", "200", "application/json"}, {"/v1/device-profiles", "post", "201", "application/json"},
		{"/v1/device-profiles/{id}", "get", "200", "application/json"}, {"/v1/device-profiles/{id}", "patch", "200", "application/json"}, {"/v1/device-profiles/{id}", "delete", "204", ""},
		{"/v1/device-profiles/{id}/apply", "post", "202", "application/json"},
		{"/v1/firmware", "get", "200", "application/json"}, {"/v1/firmware", "post", "201", "application/json"},
		{"/v1/firmware/{id}", "get", "200", "application/json"}, {"/v1/firmware/{id}", "patch", "200", "application/json"}, {"/v1/firmware/{id}", "delete", "204", ""},
		{"/v1/firmware/{id}/content", "get", "200", "application/octet-stream"}, {"/v1/firmware/{id}/content", "put", "204", ""},
		{"/v1/devices/{ep}/firmware-updates", "get", "200", "application/json"}, {"/v1/devices/{ep}/firmware-updates", "post", "201", "application/json"},
		{"/v1/firmware-updates/{id}", "get", "200", "application/json"}, {"/v1/firmware-updates/{id}", "delete", "204", ""},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			var op struct {
				Responses map[string]struct {
					Content map[string]struct {
						Schema struct {
							Ref string `json:"$ref"`
						} `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			}
			if err := json.Unmarshal(doc.Paths[r.path][r.method], &op); err != nil {
				t.Fatal(err)
			}
			response, ok := op.Responses[r.status]
			if !ok {
				t.Fatalf("missing success status %s", r.status)
			}
			if r.media != "" {
				ref := response.Content[r.media].Schema.Ref
				const prefix = "#/components/schemas/"
				if !strings.HasPrefix(ref, prefix) || len(doc.Components.Schemas[strings.TrimPrefix(ref, prefix)]) == 0 {
					t.Errorf("missing component schema for %s", r.media)
				}
			} else if len(response.Content) != 0 {
				t.Error("204 must not have content")
			}
			if _, ok := op.Responses["default"]; !ok {
				t.Error("missing default Problem response")
			}
			for status, entry := range op.Responses {
				if status == "default" || (len(status) == 3 && status[0] >= '4' && status[0] <= '5') {
					if ref := entry.Content["application/problem+json"].Schema.Ref; ref != "#/components/schemas/Problem" {
						t.Errorf("%s error schema = %q", status, ref)
					}
				} else if len(status) == 3 && status[0] == '2' && status != "204" {
					found := false
					for _, media := range entry.Content {
						const prefix = "#/components/schemas/"
						if strings.HasPrefix(media.Schema.Ref, prefix) && len(doc.Components.Schemas[strings.TrimPrefix(media.Schema.Ref, prefix)]) > 0 {
							found = true
						}
					}
					if !found {
						t.Errorf("%s missing success component schema", status)
					}
				}
			}
		})
	}
}

func TestOpenAPIProfilesFirmwareHandlerContracts(t *testing.T) {
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	var validate func(json.RawMessage, any) error
	validate = func(raw json.RawMessage, value any) error {
		var s struct {
			Ref        string                     `json:"$ref"`
			Type       any                        `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
			Items      json.RawMessage            `json:"items"`
			Additional json.RawMessage            `json:"additionalProperties"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		if s.Ref != "" {
			return validate(doc.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")], value)
		}
		if s.Type != nil {
			allowed := false
			types := []any{s.Type}
			if a, ok := s.Type.([]any); ok {
				types = a
			}
			for _, typ := range types {
				switch typ {
				case "null":
					allowed = allowed || value == nil
				case "object":
					_, ok := value.(map[string]any)
					allowed = allowed || ok
				case "array":
					_, ok := value.([]any)
					allowed = allowed || ok
				case "string":
					_, ok := value.(string)
					allowed = allowed || ok
				case "integer":
					n, ok := value.(float64)
					allowed = allowed || (ok && n == float64(int64(n)))
				case "number":
					_, ok := value.(float64)
					allowed = allowed || ok
				case "boolean":
					_, ok := value.(bool)
					allowed = allowed || ok
				}
			}
			if !allowed {
				return fmt.Errorf("type %v does not match %T", s.Type, value)
			}
		}
		if obj, ok := value.(map[string]any); ok {
			for _, k := range s.Required {
				if _, ok := obj[k]; !ok {
					return fmt.Errorf("missing %s", k)
				}
			}
			for k, v := range obj {
				if p, ok := s.Properties[k]; ok {
					if err := validate(p, v); err != nil {
						return fmt.Errorf("%s: %w", k, err)
					}
				} else if len(s.Additional) > 0 {
					if string(s.Additional) == "false" {
						return fmt.Errorf("undocumented field %s", k)
					}
					if string(s.Additional) != "true" {
						if err := validate(s.Additional, v); err != nil {
							return fmt.Errorf("%s: %w", k, err)
						}
					}
				} else if len(s.Properties) > 0 {
					return fmt.Errorf("undocumented field %s", k)
				}
			}
		}
		if a, ok := value.([]any); ok && len(s.Items) > 0 {
			for _, v := range a {
				if err := validate(s.Items, v); err != nil {
					return err
				}
			}
		}
		return nil
	}

	check := func(method, template string, want int, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("%s %s: got %d: %s", method, template, w.Code, w.Body)
		}
		var op struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		}
		if err := json.Unmarshal(doc.Paths[template][strings.ToLower(method)], &op); err != nil {
			t.Fatal(err)
		}
		r := op.Responses[fmt.Sprint(want)]
		if want == 204 {
			if w.Body.Len() != 0 || len(r.Content) != 0 {
				t.Fatalf("nonempty 204")
			}
			return
		}
		media := "application/json"
		if template == "/v1/firmware/{id}/content" {
			media = "application/octet-stream"
		}
		if !strings.HasPrefix(w.Header().Get("Content-Type"), media) {
			t.Fatalf("content-type %q", w.Header().Get("Content-Type"))
		}
		raw := r.Content[media].Schema
		var value any
		if media == "application/octet-stream" {
			value = w.Body.String()
			if value != "firmware bytes" {
				t.Fatal("binary content mismatch")
			}
		} else if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if err := validate(raw, value); err != nil {
			t.Errorf("%s %s: %v", method, template, err)
		}
	}
	keys := profileWriteKeys()
	cred := keys.entries["test"]
	cred.scopes += "firmware:write,jobs:write,"
	keys.entries["test"] = cred
	devices := &memory.Devices{}
	if _, err := devices.Create("default", store.Device{Endpoint: "dev"}); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Devices: devices, DeviceProfiles: NewMemoryDeviceProfileStore(), Firmware: NewMemoryFirmwareStore(), FirmwareUpdates: NewMemoryFirmwareUpdateStore(), Jobs: NewMemoryJobStore()})
	req := func(method, path, body, media string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		if media != "" {
			r.Header.Set("Content-Type", media)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	call := func(method, path, template, body, media string, status int) *httptest.ResponseRecorder {
		w := req(method, path, body, media)
		check(method, template, status, w)
		return w
	}
	const js = "application/json"
	nullable := call("POST", "/v1/device-profiles", "/v1/device-profiles", `{"name":"nullable","observations":[{"paths":["/3/0/1"],"attributes":null,"persistent":null,"prefix":null,"format":null,"accept":null}],"attributes":[{"path":"/3/0/1","attributes":null}],"timeseries":{"retentionDays":null,"fromReads":null}}`, js, 201)
	for _, field := range []string{`"retentionDays":null`, `"fromReads":null`, `"attributes":null`, `"persistent":null`, `"prefix":null`, `"format":null`, `"accept":null`} {
		if !strings.Contains(nullable.Body.String(), field) {
			t.Errorf("handler did not preserve %s: %s", field, nullable.Body.String())
		}
	}
	w := call("POST", "/v1/device-profiles", "/v1/device-profiles", `{"name":"pump","onRegister":[{"type":"sync-clock"}],"observations":[{"paths":["/3/0/1"],"attributes":{"pmin":3}}],"attributes":[{"path":"/3/0/1","attributes":{"pmax":20}}],"timeseries":{"retentionDays":30,"fromReads":true}}`, js, 201)
	var profile DeviceProfile
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	// Verify the validator rejects an incompatible type, not just valid fixtures.
	var fixture any
	if err := json.Unmarshal(w.Body.Bytes(), &fixture); err != nil {
		t.Fatal(err)
	}
	fixture.(map[string]any)["name"] = float64(42)
	if err := validate(doc.Components.Schemas["DeviceProfileResponse"], fixture); err == nil {
		t.Fatal("accepted numeric profile name")
	}
	pp := "/v1/device-profiles/" + profile.ID
	call("GET", "/v1/device-profiles", "/v1/device-profiles", "", "", 200)
	call("GET", pp, "/v1/device-profiles/{id}", "", "", 200)
	call("PATCH", pp, "/v1/device-profiles/{id}", `{"name":"updated"}`, "application/merge-patch+json", 200)
	call("POST", pp+"/apply", "/v1/device-profiles/{id}/apply", "{}", js, 202)
	call("DELETE", pp, "/v1/device-profiles/{id}", "", "", 204)
	w = call("POST", "/v1/firmware", "/v1/firmware", `{"name":"pump","version":"1","labels":{"hw":"a"}}`, js, 201)
	var fw Firmware
	if err := json.Unmarshal(w.Body.Bytes(), &fw); err != nil {
		t.Fatal(err)
	}
	fp := "/v1/firmware/" + fw.ID
	call("GET", "/v1/firmware", "/v1/firmware", "", "", 200)
	call("GET", fp, "/v1/firmware/{id}", "", "", 200)
	call("PATCH", fp, "/v1/firmware/{id}", `{"description":"v1"}`, js, 200)
	call("PUT", fp+"/content", "/v1/firmware/{id}/content", "firmware bytes", "application/octet-stream", 204)
	call("GET", fp+"/content", "/v1/firmware/{id}/content", "", "", 200)
	call("DELETE", fp, "/v1/firmware/{id}", "", "", 204)
	const up = "/v1/devices/dev/firmware-updates"
	const ut = "/v1/devices/{ep}/firmware-updates"
	w = call("POST", up, ut, `{"uri":"coaps://example/fw"}`, js, 201)
	var update FirmwareUpdate
	if err := json.Unmarshal(w.Body.Bytes(), &update); err != nil {
		t.Fatal(err)
	}
	path := "/v1/firmware-updates/" + update.ID
	call("GET", up, ut, "", "", 200)
	call("GET", path, "/v1/firmware-updates/{id}", "", "", 200)
	call("DELETE", path, "/v1/firmware-updates/{id}", "", "", 204)
}
