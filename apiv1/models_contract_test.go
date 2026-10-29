package apiv1

import (
	"encoding/json"
	"net/http/httptest"
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
	for _, path := range []string{"/v1/models", "/v1/models/3"} {
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
