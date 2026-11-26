package apiv1

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOpenAPITenancyContract(t *testing.T) {
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	type route struct {
		method, path string
		statuses     []string
	}
	routes := []route{
		{"delete", "/v1/api-keys/{id}", []string{"204"}}, {"get", "/v1/api-keys", []string{"200"}}, {"post", "/v1/api-keys", []string{"201"}},
		{"get", "/v1/tenants", []string{"200"}}, {"post", "/v1/tenants", []string{"201"}}, {"get", "/v1/tenants/{id}", []string{"200"}}, {"patch", "/v1/tenants/{id}", []string{"200"}}, {"delete", "/v1/tenants/{id}", []string{"204"}}, {"get", "/v1/tenants/{id}/usage", []string{"200"}},
		{"get", "/v1/info", []string{"200"}}, {"get", "/v1/settings", []string{"200"}}, {"patch", "/v1/settings", []string{"200"}}, {"get", "/v1/audit-log", []string{"200"}},
	}
	schemaFor := func(method, path, status string) json.RawMessage {
		t.Helper()
		var op struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		}
		_ = json.Unmarshal(doc.Paths[path][method], &op)
		response, ok := op.Responses[status]
		if !ok {
			t.Errorf("%s %s missing %s", method, path, status)
			return nil
		}
		if status == "204" {
			if len(response.Content) != 0 {
				t.Errorf("%s %s: 204 must not have a body", method, path)
			}
			return nil
		}
		media := "application/json"
		if status == "default" {
			media = "application/problem+json"
		}
		raw := response.Content[media].Schema
		var ref struct {
			Ref string `json:"$ref"`
		}
		_ = json.Unmarshal(raw, &ref)
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(ref.Ref, prefix) || len(doc.Components.Schemas[strings.TrimPrefix(ref.Ref, prefix)]) == 0 {
			t.Errorf("%s %s %s: missing component schema", method, path, status)
		}
		if status == "default" && ref.Ref != prefix+"Problem" {
			t.Errorf("%s %s: expected Problem", method, path)
		}
		return raw
	}
	for _, r := range routes {
		for _, status := range append(r.statuses, "default") {
			schemaFor(r.method, r.path, status)
		}
		var op struct {
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(doc.Paths[r.path][r.method], &op); err != nil {
			t.Fatal(err)
		}
		for status := range op.Responses {
			if len(status) == 3 && status[0] == '2' {
				schemaFor(r.method, r.path, status)
			}
		}
	}
	// Validate actual JSON returned by handlers, including non-empty pages.
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
	check := func(method, template, status string, body []byte) {
		t.Helper()
		raw := schemaFor(method, template, status)
		if len(raw) == 0 {
			return
		}
		var value any
		if err := json.Unmarshal(body, &value); err != nil {
			t.Fatal(err)
		}
		if err := validate(raw, value); err != nil {
			t.Errorf("%s %s: %v", method, template, err)
		}
	}

	// A list must never document the one-time token returned by creation.
	var metadata struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(doc.Components.Schemas["APIKeyMetadata"], &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := metadata.Properties["token"]; ok {
		t.Error("API key metadata documents a secret token")
	}
	var created struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(doc.Components.Schemas["APIKeyCreated"], &created); err != nil {
		t.Fatal(err)
	}
	if _, ok := created.Properties["token"]; !ok {
		t.Error("create response must document its one-time token")
	}
	h := tenantTestHandler(t)
	request := func(method, path, template, status, body string) {
		t.Helper()
		w := keyRequest(h, strings.ToUpper(method), path, "lwm_platform_test", body)
		if fmt.Sprint(w.Code) != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		check(method, template, status, w.Body.Bytes())
	}
	request("post", "/v1/tenants", "/v1/tenants", "201", `{"id":"acme","name":"Acme"}`)
	request("get", "/v1/tenants", "/v1/tenants", "200", "")
	request("get", "/v1/tenants/acme/usage", "/v1/tenants/{id}/usage", "200", "")
	request("get", "/v1/info", "/v1/info", "200", "")
	w := settingsRequest(keyHandler(t), "GET", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	check("get", "/v1/settings", "200", w.Body.Bytes())
	w = settingsRequest(keyHandler(t), "PATCH", `{"registrationProfiles":{"p":[{"id":3,"version":"1.1"}]}}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	check("patch", "/v1/settings", "200", w.Body.Bytes())
	w = keyRequest(h, "GET", "/v1/tenants/missing", "lwm_platform_test", "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	check("get", "/v1/tenants/{id}", "default", w.Body.Bytes())

	w = keyRequest(keyHandler(t), "POST", "/v1/api-keys", "lwm_root_synthetic", `{"name":"sample","scopes":["devices:read"]}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	check("post", "/v1/api-keys", "201", w.Body.Bytes())
	kh := keyHandler(t)
	w = keyRequest(kh, "POST", "/v1/api-keys", "lwm_root_synthetic", `{"name":"sample","scopes":["devices:read"]}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	w = keyRequest(kh, "GET", "/v1/api-keys", "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	check("get", "/v1/api-keys", "200", w.Body.Bytes())
	w = keyRequest(kh, "GET", "/v1/audit-log", "lwm_root_synthetic", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	check("get", "/v1/audit-log", "200", w.Body.Bytes())
}
