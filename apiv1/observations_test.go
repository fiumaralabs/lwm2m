package apiv1

import (
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"net/http"
	"testing"
)

func observationAPI(t *testing.T) (http.Handler, Keys) {
	t.Helper()
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes = ",devices:read,devices:write,operations:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys})
	for _, ep := range []string{"pump/a", "observations"} {
		if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"`+ep+`"}`); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	return h, keys
}
func TestObservationMutations(t *testing.T) {
	h, keys := observationAPI(t)
	w := deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", `{"paths":["/3303/0/5700"],"attributes":{"pmin":10}}`)
	var v store.Observation
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	path := "/v1/observations/" + v.ID
	for _, body := range []string{`{}`, `null`, `{"attributes":null}`, `{"persistent":false}`, `{"paths":["/3"]}`, `{"attributes":{"pmin":-1}}`, `{"attributes":{}} {}`} {
		w = deviceRequest(h, "PATCH", path, body)
		if w.Code != 400 {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w = deviceRequest(h, "GET", path, "")
	var unchanged store.Observation
	if err := json.Unmarshal(w.Body.Bytes(), &unchanged); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(v)
	after, _ := json.Marshal(unchanged)
	if w.Code != 200 || string(before) != string(after) {
		t.Fatalf("rejected patch changed record: %s", w.Body.String())
	}
	w = deviceRequest(h, "PATCH", path, `{"attributes":{"pmax":300,"st":0.5}}`)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	var got store.Observation
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	v.Attributes = got.Attributes
	if string(got.Attributes["pmax"]) != "300" || len(got.Attributes) != 2 {
		t.Fatal("attributes not replaced")
	}
	want, _ := json.Marshal(v)
	actual, _ := json.Marshal(got)
	if string(want) != string(actual) {
		t.Fatalf("metadata changed: %s != %s", want, actual)
	}
	c := keys.entries["test"]
	c.tenant = "other"
	keys.entries["test"] = c
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		w = deviceRequest(h, method, path, `{"attributes":{}}`)
		if w.Code != 404 {
			t.Errorf("isolated %s: %d", method, w.Code)
		}
	}
	for _, method := range []string{"GET", "POST"} {
		w = deviceRequest(h, method, "/v1/devices/pump%2Fa/observations", `{"paths":["/3/0"]}`)
		if w.Code != 404 {
			t.Errorf("isolated collection %s: %d", method, w.Code)
		}
	}
	// Provisioning the same endpoint in another tenant must not expose its observations.
	if created := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump/a"}`); created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	if isolated := deviceRequest(h, "GET", "/v1/devices/pump%2Fa/observations", ""); isolated.Code != 200 || isolated.Body.String() != "{\"items\":[]}\n" {
		t.Fatal("tenant list", isolated.Code, isolated.Body.String())
	}
	c.tenant = "default"
	keys.entries["test"] = c
	w = deviceRequest(h, "DELETE", path+"?mode=invalid", "")
	if w.Code != 400 {
		t.Errorf("mode: %d", w.Code)
	}
	for _, mode := range []string{"?mode=passive", "", "?mode=active"} {
		w = deviceRequest(h, "DELETE", path+mode, "")
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
	}
	w = deviceRequest(h, "GET", path, "")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Status != "ended" {
		t.Fatal("not ended", w.Body.String())
	}
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		if w = deviceRequest(h, method, "/v1/observations/missing", `{"attributes":{}}`); w.Code != 404 {
			t.Errorf("missing %s: %d", method, w.Code)
		}
	}
}
func TestObservationScopesAndProfile(t *testing.T) {
	h, keys := observationAPI(t)
	c := keys.entries["test"]
	c.scopes = ",devices:read,"
	keys.entries["test"] = c
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		path := "/v1/observations/id"
		if method == "POST" {
			path = "/v1/devices/pump%2Fa/observations"
		}
		w := deviceRequest(h, method, path, `{}`)
		if w.Code != 403 {
			t.Fatal(method, w.Code)
		}
	}
	c.scopes = ",operations:write,"
	keys.entries["test"] = c
	for _, path := range []string{"/v1/devices/pump%2Fa/observations", "/v1/observations/id"} {
		w := deviceRequest(h, "GET", path, "")
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	c.scopes = ",admin,"
	keys.entries["test"] = c
	s := &memory.Observations{}
	if err := s.Create("default", store.Observation{Tenant: "default", ID: "profile", Source: "profile", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	h = New(Options{Keys: keys, Observations: s})
	w := deviceRequest(h, "DELETE", "/v1/observations/profile", "")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	v, _ := s.Get("default", "profile")
	if v.Status != "active" {
		t.Fatal("profile ended")
	}
	w = deviceRequest(h, "GET", "/v1/observations/profile", "")
	if w.Code != 200 {
		t.Fatal("admin read", w.Code)
	}
}

func TestObservationValidation(t *testing.T) {
	h, _ := observationAPI(t)
	for _, body := range []string{
		`{}`, `null`, `{"paths":[]}`, `{"paths":["/"]}`, `{"paths":["3/0"]}`, `{"paths":["/bad"]}`, `{"paths":["/3/0","/3/0"]}`,
		`{"paths":["/3/0"],"unknown":1}`, `{"paths":["/3/0"]} {}`,
	} {
		t.Run(body, func(t *testing.T) {
			w := deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", body)
			if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, attrs := range []string{`{"pmin":-1}`, `{"pmax":1.5}`, `{"pmin":"10"}`, `{"pmin":null}`, `{"pmin":true}`, `{"bogus":1}`, `{"dim":1}`, `{"edge":2}`, `{"con":true}`, `{"hqmax":0.5}`, `{"st":-1}`, `{"gt":1e400}`, `{"pmin":20,"pmax":10}`, `{"lt":5,"gt":2}`, `{"lt":1,"gt":3,"st":1}`, `{"epmin":1,"epmax":1}`} {
		t.Run(attrs, func(t *testing.T) {
			w := deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", `{"paths":["/3303/0/5700"],"attributes":`+attrs+`}`)
			if w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	w := deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", `{"paths":["/3303/0/5700"],"persistent":false,"attributes":{"pmin":0,"pmax":0,"epmin":1,"epmax":2,"lt":-5,"gt":10,"st":0.5,"edge":1,"con":0,"hqmax":18446744073709551615}}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v store.Observation
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.Persistent {
		t.Fatal("false persistent lost")
	}
	w = deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", `{"paths":["/3"],"attributes":{"gt":1}}`)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestObservationCreateRead(t *testing.T) {
	h, _ := observationAPI(t)
	w := deviceRequest(h, "POST", "/v1/devices/pump%2Fa/observations", `{"paths":["/3303/0/5700"],"attributes":{"pmin":10,"st":0.5},"prefix":"gw","format":"senml-json","accept":"tlv"}`)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var v store.Observation
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.ID == "" || v.Device != "pump/a" || !v.Persistent || v.Status != "pending" || v.Source != "api" || v.EstablishedAt != nil || v.LastNotificationAt != nil || v.NotificationCount != 0 || string(v.Error) != "null" || v.Prefix != "gw" || v.Format != "senml-json" || v.Accept != "tlv" {
		t.Fatalf("defaults: %+v", v)
	}
	for _, path := range []string{"/v1/observations/" + v.ID, "/v1/devices/pump%2Fa/observations", "/v1/devices/observations"} {
		if got := deviceRequest(h, "GET", path, ""); got.Code != 200 {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body.String())
		}
	}
	w = deviceRequest(h, "GET", "/v1/devices/observations/observations", "")
	if w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
}
