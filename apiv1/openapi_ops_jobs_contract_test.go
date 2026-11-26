package apiv1

import (
	"context"
	"encoding/json"
	"time"

	"fmt"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"strings"
	"testing"
)

func TestOpenAPIOpsJobsContract(t *testing.T) {
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
		{"get", "/v1/devices/{ep}/operations", []string{"200"}}, {"post", "/v1/devices/{ep}/operations", []string{"200", "202"}},
		{"get", "/v1/devices/{ep}/observations", []string{"200"}}, {"post", "/v1/devices/{ep}/observations", []string{"201"}},
		{"get", "/v1/jobs", []string{"200"}}, {"post", "/v1/jobs", []string{"201"}}, {"get", "/v1/jobs/{id}", []string{"200"}},
		{"get", "/v1/jobs/{id}/tasks", []string{"200"}}, {"post", "/v1/jobs/{id}/pause", []string{"200"}}, {"post", "/v1/jobs/{id}/resume", []string{"200"}}, {"post", "/v1/jobs/{id}/cancel", []string{"200"}},
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
	st := memory.NewOperations()
	opHandler := New(Options{Keys: deviceKeys(), Operations: st})
	if w := deviceRequest(opHandler, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	op := operations.New("default", "pump", "read", json.RawMessage(`{"path":"/3/0"}`), nil, time.Now())
	if err := st.Create(context.Background(), "default", op); err != nil {
		t.Fatal(err)
	}
	wop := deviceRequest(opHandler, "GET", "/v1/devices/pump/operations", "")
	if wop.Code != 200 {
		t.Fatal(wop.Code, wop.Body)
	}
	check("get", "/v1/devices/{ep}/operations", "200", wop.Body.Bytes())
	h, _ := observationAPI(t)
	obs := deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", `{"paths":["/3/0"],"attributes":{"pmin":10}}`)
	if obs.Code != 201 {
		t.Fatal(obs.Code, obs.Body)
	}
	check("post", "/v1/devices/{ep}/observations", "201", obs.Body.Bytes())
	// A mismatched map value schema must reject the real handler response.
	original := doc.Components.Schemas["ObservationResponse"]
	var mutated map[string]any
	if err := json.Unmarshal(original, &mutated); err != nil {
		t.Fatal(err)
	}
	attributes := mutated["properties"].(map[string]any)["attributes"].(map[string]any)
	attributes["additionalProperties"].(map[string]any)["type"] = "string"
	doc.Components.Schemas["ObservationResponse"], _ = json.Marshal(mutated)
	var observation any
	if err := json.Unmarshal(obs.Body.Bytes(), &observation); err != nil {
		t.Fatal(err)
	}
	if err := validate(schemaFor("post", "/v1/devices/{ep}/observations", "201"), observation); err == nil {
		t.Error("numeric observation attribute accepted by string additionalProperties")
	}
	doc.Components.Schemas["ObservationResponse"] = original
	page := deviceRequest(h, "GET", "/v1/devices/pump%2Fa/observations", "")
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body)
	}
	check("get", "/v1/devices/{ep}/observations", "200", page.Body.Bytes())
	jobs := NewMemoryJobStore()
	j := validJob()
	j.ID = "job_contract"
	if err := jobs.Create("a", j); err != nil {
		t.Fatal(err)
	}
	jobs.PutTask("a", j.ID, JobTask{Device: "pump", Status: "pending"})
	for _, tc := range []struct{ path, template string }{{"/v1/jobs", "/v1/jobs"}, {"/v1/jobs/job_contract", "/v1/jobs/{id}"}, {"/v1/jobs/job_contract/tasks", "/v1/jobs/{id}/tasks"}} {
		w := jobRequest(NewJobsHandler(jobs), "GET", tc.path, "", "a", "devices:read")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		check("get", tc.template, "200", w.Body.Bytes())
	}
}
