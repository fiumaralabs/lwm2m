package apiv1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestWebhookOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for name, raw := range doc.Components.Schemas {
		if !strings.HasPrefix(name, "Webhook") {
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		_, hasSecret := schema.Properties["secret"]
		if hasSecret != (name == "WebhookCreated") {
			t.Errorf("%s secret property: %v", name, hasSecret)
		}
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer receiver.Close()
	h := keyHandler(t)
	var checkSchema func(*testing.T, json.RawMessage, any)
	checkSchema = func(t *testing.T, raw json.RawMessage, body any) {
		t.Helper()
		var schema struct {
			Ref        string                     `json:"$ref"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
			Items      json.RawMessage            `json:"items"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if schema.Ref != "" {
			const p = "#/components/schemas/"
			if !strings.HasPrefix(schema.Ref, p) {
				t.Fatal(schema.Ref)
			}
			ref, ok := doc.Components.Schemas[strings.TrimPrefix(schema.Ref, p)]
			if !ok {
				t.Fatal(schema.Ref)
			}
			checkSchema(t, ref, body)
			return
		}
		if v, ok := body.(map[string]any); ok {
			if schema.Properties == nil {
				t.Fatal("object without documented properties")
			}
			for _, k := range schema.Required {
				if _, ok := v[k]; !ok {
					t.Errorf("missing required %s", k)
				}
			}
			for k, value := range v {
				field, ok := schema.Properties[k]
				if !ok {
					t.Errorf("undocumented field %s", k)
					continue
				}
				checkSchema(t, field, value)
			}
		}
		if v, ok := body.([]any); ok && len(schema.Items) > 0 {
			for _, item := range v {
				checkSchema(t, schema.Items, item)
			}
		}
	}
	check := func(method, template, path, body string, status int) map[string]any {
		t.Helper()
		var op struct {
			RequestBody json.RawMessage `json:"requestBody"`
			Responses   map[string]struct {
				Content map[string]struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		}
		if err := json.Unmarshal(doc.Paths[template][strings.ToLower(method)], &op); err != nil {
			t.Fatal(err)
		}
		if (method == "POST" && template == "/v1/webhooks" || method == "PATCH") != (len(op.RequestBody) > 0) {
			t.Errorf("%s %s request body mismatch", method, template)
		}
		problem, ok := op.Responses["default"].Content["application/problem+json"]
		if !ok || !strings.Contains(string(problem.Schema), `#/components/schemas/Problem`) {
			t.Errorf("%s %s missing Problem", method, template)
		}
		w := keyRequest(h, method, path, "lwm_root_synthetic", body)
		if w.Code != status {
			t.Fatalf("%s %s got %d: %s", method, path, w.Code, w.Body)
		}
		response, ok := op.Responses[strconv.Itoa(status)]
		if !ok {
			t.Fatalf("%s %s missing response %d", method, template, status)
		}
		if status == 204 {
			if w.Body.Len() != 0 {
				t.Fatal("204 has a body")
			}
			return nil
		}
		media, ok := response.Content["application/json"]
		var schemaRef struct {
			Ref string `json:"$ref"`
		}
		if !ok || json.Unmarshal(media.Schema, &schemaRef) != nil || !strings.HasPrefix(schemaRef.Ref, "#/components/schemas/") {
			t.Fatalf("%s %s missing component success schema", method, template)
		}
		secretAllowed := template == "/v1/webhooks" && method == "POST" || template == "/v1/webhooks/{id}/rotate-secret"
		if (schemaRef.Ref == "#/components/schemas/WebhookCreated") != secretAllowed {
			t.Errorf("%s %s wrong secret-bearing schema %s", method, template, schemaRef.Ref)
		}
		var value map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		checkSchema(t, media.Schema, value)
		return value
	}
	created := check("POST", "/v1/webhooks", "/v1/webhooks", fmt.Sprintf(`{"url":%q,"types":["lwm2m.notification"]}`, receiver.URL), 201)
	id := created["id"].(string)
	base := "/v1/webhooks/" + id
	check("GET", "/v1/webhooks", "/v1/webhooks", "", 200)
	check("GET", "/v1/webhooks/{id}", base, "", 200)
	check("PATCH", "/v1/webhooks/{id}", base, `{"selector":"env=prod"}`, 200)
	check("POST", "/v1/webhooks/{id}/rotate-secret", base+"/rotate-secret", "", 200)
	check("POST", "/v1/webhooks/{id}/test", base+"/test", "", 200)
	page := check("GET", "/v1/webhooks/{id}/deliveries", base+"/deliveries", "", 200)
	deliveries := page["items"].([]any)
	if len(deliveries) == 0 {
		t.Fatal("no delivery")
	}
	delivery := deliveries[0].(map[string]any)["id"].(string)
	check("POST", "/v1/webhooks/{id}/deliveries/{deliveryId}/retry", base+"/deliveries/"+delivery+"/retry", "", 200)
	check("DELETE", "/v1/webhooks/{id}", base, "", 204)
}
