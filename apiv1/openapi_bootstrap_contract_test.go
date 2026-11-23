package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestOpenAPIBootstrapNonEmptyContract(t *testing.T) {
	body := `{"servers":[{"uri":"coaps://example.test:5684","shortServerId":1,"lifetimeSeconds":86400,"binding":"UQ","defaultMinPeriod":10,"defaultMaxPeriod":3600,"disableTimeoutSeconds":30,"notifyWhenDisabled":false,"bootstrapOnRegistrationFailure":true,"security":{"x509":{"certificate":"cert","privateKey":"private","serverCertificate":"ca"},"sni":"example.test","none":{},"oscore":{"senderId":"01","recipientId":"02","masterSalt":"03","idContext":"04","aeadAlgorithm":10,"hmacAlgorithm":5}}}],"bootstrapServer":{"accountTimeoutSeconds":0,"clientHoldOffSeconds":0},"acls":[{"object":3,"instance":0,"owner":1,"rights":{"1":["read","write"]}}],"delete":["/0","/1"],"writes":[{"path":"/23/0","values":[{"path":"/23/0/0","type":"string","value":"hello","timestamp":"2024-01-01T00:00:00Z","prefix":"/23"}]}],"options":{"format":"senml-cbor","renumberSecurityInstances":true,"discover":false,"read":["/1"],"refusePack":false}}`
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := deviceRequest(h, "PUT", "/v1/devices/one/bootstrap", body); w.Code != 204 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	w := deviceRequest(h, "GET", "/v1/devices/one/bootstrap", "")
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["writes"].([]any)[0].(map[string]any)["values"].([]any)[0].(map[string]any)["value"] != "hello" {
		t.Fatal("public raw write did not survive")
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(doc.Components.Schemas["DeviceBootstrap"], &schema); err != nil {
		t.Fatal(err)
	}
	var example any
	if err := json.Unmarshal([]byte(body), &example); err != nil {
		t.Fatal(err)
	}
	var check func(string, any, map[string]any)
	check = func(path string, value any, s map[string]any) {
		t.Helper()
		var typ string
		switch value.(type) {
		case map[string]any:
			typ = "object"
		case []any:
			typ = "array"
		case string:
			typ = "string"
		case bool:
			typ = "boolean"
		case float64:
			typ = "integer"
		}
		if path == "$.writes[].values[].value" {
			if len(s) == 0 {
				t.Errorf("%s lacks value type", path)
			}
			return
		}
		if s["type"] != typ {
			t.Errorf("%s: type %v, want %s", path, s["type"], typ)
			return
		}
		switch v := value.(type) {
		case map[string]any:
			props, _ := s["properties"].(map[string]any)
			for k, child := range v {
				childSchema, ok := props[k].(map[string]any)
				if !ok {
					childSchema, ok = s["additionalProperties"].(map[string]any)
				}
				if !ok {
					t.Errorf("%s.%s is undocumented", path, k)
					continue
				}
				check(path+"."+k, child, childSchema)
			}
		case []any:
			items, ok := s["items"].(map[string]any)
			if !ok {
				t.Errorf("%s items undocumented", path)
				return
			}
			for _, child := range v {
				check(path+"[]", child, items)
			}
		}
	}
	check("$", example, schema)
}
