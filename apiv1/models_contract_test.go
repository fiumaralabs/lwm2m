package apiv1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelsListContract(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	w := deviceRequest(h, "GET", "/v1/models?q=DeViCe&class=oma", "")
	var body struct {
		Items []struct {
			ID       uint16   `json:"id"`
			Name     string   `json:"name"`
			URN      string   `json:"urn"`
			Versions []string `json:"versions"`
			Class    string   `json:"class"`
			Source   string   `json:"source"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	found := false
	for _, item := range body.Items {
		if item.ID == 3 {
			found = item.Name == "Device" && item.URN == "urn:oma:lwm2m:oma:3" && item.Class == "oma" && item.Source == "builtin" && len(item.Versions) == 4
		}
	}
	if !found {
		t.Fatalf("missing Device: %s", w.Body.String())
	}
}

func TestModelsAuth(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	for _, path := range []string{"/v1/models", "/v1/models/3"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestModelsDetails(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	for _, tc := range []struct{ path, version, urn string }{
		{"/v1/models/3", "1.3", "urn:oma:lwm2m:oma:3:1.3"},
		{"/v1/models/3?version=1.0", "1.0", "urn:oma:lwm2m:oma:3"},
	} {
		w := deviceRequest(h, "GET", tc.path, "")
		var d modelDefinition
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || d.ID != 3 || d.Version != tc.version || d.URN != tc.urn || d.Name != "Device" {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		foundRead, foundExec := false, false
		for _, r := range d.Resources {
			if r.ID == 0 {
				foundRead = r.Name == "Manufacturer" && r.Type == "string" && r.Operations == "R"
			}
			if r.ID == 4 {
				foundExec = r.Name == "Reboot" && r.Type == "none" && r.Operations == "E"
			}
		}
		if !foundRead || !foundExec {
			t.Fatalf("missing resources: %s", w.Body.String())
		}
	}
}
func TestModelsNotFoundAndMethods(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	for _, path := range []string{"/v1/models/65000", "/v1/models/3?version=1.99", "/v1/models/3?version=bogus", "/v1/models/nope", "/v1/models/3/versions/1.0"} {
		registrationProblem(t, h, "GET", path, "not_found", 404)
	}
	for _, path := range []string{"/v1/models/3"} {
		w := deviceRequest(h, "POST", path, "")
		if w.Code != 405 || w.Header().Get("Allow") != "GET" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
func TestModelsFilters(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	for _, path := range []string{"/v1/models?q=not-a-real-model", "/v1/models?class=oma-ext", "/v1/models?q=device&class=vendor"} {
		w := deviceRequest(h, "GET", path, "")
		if w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := deviceRequest(h, "GET", "/v1/models?class=oma", "")
	var body struct {
		Items []struct {
			Class string `json:"class"`
		} `json:"items"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) == 0 {
		t.Fatal(w.Body.String())
	}
	for _, item := range body.Items {
		if item.Class != "oma" {
			t.Fatal(w.Body.String())
		}
	}
}

func TestModelsPagination(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	w := deviceRequest(h, "GET", "/v1/models?class=oma&limit=1", "")
	var first struct {
		Items []builtinSummary `json:"items"`
		Next  string           `json:"nextCursor"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || len(first.Items) != 1 || first.Next == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = deviceRequest(h, "GET", "/v1/models?class=oma&limit=1&cursor="+first.Next, "")
	var second struct {
		Items []builtinSummary `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &second) != nil || len(second.Items) != 1 || second.Items[0].ID <= first.Items[0].ID {
		t.Fatal(w.Code, w.Body.String())
	}
	registrationProblem(t, h, "GET", "/v1/models?class=other&cursor="+first.Next, "invalid_cursor", 400)
	registrationProblem(t, h, "GET", "/v1/models?limit=0", "invalid_limit", 400)
	registrationProblem(t, h, "GET", "/v1/models?cursor=bogus", "invalid_cursor", 400)
}

func TestModelsScope(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes = ",devices:write,"
	keys.entries["test"] = entry
	h := New(Options{Keys: keys})
	for _, path := range []string{"/v1/models", "/v1/models/3"} {
		registrationProblem(t, h, "GET", path, "forbidden", 403)
	}
}

const uploadModel = `<LWM2M><Object><Name>Vendor</Name><ObjectID>50000</ObjectID><ObjectURN>urn:oma:lwm2m:x:50000</ObjectURN><MultipleInstances>Multiple</MultipleInstances><Mandatory>Optional</Mandatory><Resources></Resources></Object></LWM2M>`

func modelRequest(h interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	r.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestModelsUploadContract(t *testing.T) {
	keys := deviceKeys()
	h := New(Options{Keys: keys})
	w := modelRequest(h, "POST", "/v1/models", uploadModel)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"source":"tenant"`) {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/models/50000", "/v1/models?q=Vendor"} {
		w = modelRequest(h, "GET", path, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":50000`) {
			t.Fatalf("get: %d %s", w.Code, w.Body.String())
		}
	}
	entry := keys.entries["test"]
	entry.tenant = "other"
	keys.entries["test"] = entry
	if w = modelRequest(h, "GET", "/v1/models/50000", ""); w.Code != 404 {
		t.Fatalf("foreign get: %d", w.Code)
	}
	if w = modelRequest(h, "GET", "/v1/models?q=Vendor", ""); strings.Contains(w.Body.String(), `"id":50000`) {
		t.Fatal(w.Body.String())
	}
	if w = modelRequest(h, "DELETE", "/v1/models/50000/versions/1.0", ""); w.Code != 404 {
		t.Fatalf("foreign delete: %d", w.Code)
	}
	entry.tenant = "default"
	keys.entries["test"] = entry
	if w = modelRequest(h, "DELETE", "/v1/models/3/versions/1.0", ""); w.Code != 409 {
		t.Fatalf("builtin delete: %d", w.Code)
	}
	if w = modelRequest(h, "DELETE", "/v1/models/50000/versions/1.0", ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w = modelRequest(h, "GET", "/v1/models/50000", ""); w.Code != 404 {
		t.Fatalf("after deletion: %d", w.Code)
	}
	entry.scopes = ",devices:read,"
	keys.entries["test"] = entry
	if w = modelRequest(h, "POST", "/v1/models", uploadModel); w.Code != 403 {
		t.Fatalf("write scope: %d", w.Code)
	}
	if w = modelRequest(h, "DELETE", "/v1/models/3/versions/1.0", ""); w.Code != 403 {
		t.Fatalf("delete scope: %d", w.Code)
	}
	entry.scopes = ",devices:read,devices:write,"
	keys.entries["test"] = entry
	if w = modelRequest(h, "POST", "/v1/models", "<invalid>"); w.Code != 400 {
		t.Fatalf("invalid: %d", w.Code)
	}
}

func TestModelsUploadRejectsTrailingXML(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	for _, body := range []string{uploadModel + "<broken>", uploadModel + uploadModel, "<LWM2M><Object/></LWM2M>"} {
		w := modelRequest(h, "POST", "/v1/models", body)
		if w.Code != 400 {
			t.Fatalf("invalid input: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestModelsUploadVersionsUseLatestSummary(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	newer := strings.Replace(uploadModel, "<Name>Vendor</Name>", "<Name>New Vendor</Name>", 1)
	newer = strings.Replace(newer, "<ObjectURN>urn:oma:lwm2m:x:50000</ObjectURN>", "<ObjectURN>urn:oma:lwm2m:x:50000:1.1</ObjectURN><ObjectVersion>1.1</ObjectVersion>", 1)
	for _, body := range []string{newer, uploadModel} {
		if w := modelRequest(h, "POST", "/v1/models", body); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for i := 0; i < 50; i++ {
		w := modelRequest(h, "GET", "/v1/models?q=New%20Vendor", "")
		if !strings.Contains(w.Body.String(), `"name":"New Vendor"`) || !strings.Contains(w.Body.String(), `"versions":["1.0","1.1"]`) {
			t.Fatal(w.Body.String())
		}
	}
}
