package apiv1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceModelBoundaries(t *testing.T) {
	for _, scenario := range []string{"scope", "expired", "foreign", "foreign-same-name", "unknown", "method", "paths", "missing", "nil-server"} {
		t.Run(scenario, func(t *testing.T) {
			o, r := registrationFixture(t)
			code, status := "not_registered", 404
			switch scenario {
			case "scope":
				entry := o.Keys.entries["test"]
				entry.scopes = ",devices:write,"
				o.Keys.entries["test"] = entry
				code, status = "forbidden", 403
			case "expired":
				r.Lifetime = time.Second
				r.LastUpdate = time.Now().Add(-time.Hour)
			case "foreign", "foreign-same-name":
				entry := o.Keys.entries["test"]
				entry.tenant = "other"
				o.Keys.entries["test"] = entry
				if scenario == "foreign" {
					code = "not_found"
				} else if _, err := o.Devices.Create("other", store.Device{Endpoint: "pump"}); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				o.Devices = &memory.Devices{}
				code = "not_found"
			case "missing":
				o.Server.Store().Remove(r.ID)
			case "nil-server":
				o.Server = nil
			case "method":
				h := New(o)
				for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
					registrationProblem(t, h, method, "/v1/devices/pump/model", "method_not_allowed", 405)
					if w := deviceRequest(h, method, "/v1/devices/pump/model", ""); w.Header().Get("Allow") != "GET" {
						t.Fatal(w.Header())
					}
				}
				return
			case "paths":
				for _, ep := range []string{"model", "a/b"} {
					if _, err := o.Devices.Create("default", store.Device{Endpoint: ep}); err != nil {
						t.Fatal(err)
					}
					clone := *r
					clone.ID, clone.Endpoint = ep, ep
					o.Server.Store().Add(&clone)
				}
				h := New(o)
				for _, path := range []string{"/v1/devices/model", "/v1/devices/model/model", "/v1/devices/a%2Fb/model"} {
					if w := deviceRequest(h, "GET", path, ""); w.Code != 200 {
						t.Fatal(path, w.Code, w.Body.String())
					}
				}
				for _, path := range []string{"/v1/devices/a/b/model", "/v1/devices//model"} {
					registrationProblem(t, h, "GET", path, "not_found", 404)
				}
				return
			}
			registrationProblem(t, New(o), "GET", "/v1/devices/pump/model", code, status)
		})
	}
}

func TestDeviceModelImplicitCoreVersions(t *testing.T) {
	for _, version := range []string{"1.0", "1.1", "1.2"} {
		t.Run(version, func(t *testing.T) {
			o, r := registrationFixture(t)
			r.Version = version
			r.Objects = []link.Object{{ID: 3, Instances: []uint16{0}}, {ID: 3, Version: "1.0"}, {ID: 65000}}
			w := deviceRequest(New(o), "GET", "/v1/devices/pump/model", "")
			var body struct {
				Objects []deviceModelObject `json:"objects"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Objects) != 3 {
				t.Fatal(w.Code, w.Body.String())
			}
			implicit := body.Objects[0]
			if implicit.Version != version || implicit.Definition == nil || implicit.Definition.Version != version {
				t.Errorf("implicit Device must resolve to %s: %s", version, w.Body.String())
			}
			if implicit.ID != 3 || !reflect.DeepEqual(implicit.Instances, []uint16{0}) {
				t.Errorf("registered object identity changed: %s", w.Body.String())
			}
			explicit := body.Objects[1]
			if explicit.Version != "1.0" || explicit.Definition == nil || explicit.Definition.Version != "1.0" {
				t.Errorf("explicit version must override registration version: %s", w.Body.String())
			}
			unknown := body.Objects[2]
			if unknown.Version != "1.0" || unknown.Definition != nil {
				t.Errorf("unknown object must retain default version and no definition: %s", w.Body.String())
			}
		})
	}
}

func TestDeviceModelVersions(t *testing.T) {
	o, r := registrationFixture(t)
	r.Objects = []link.Object{{ID: 3}, {ID: 65000}, {ID: 3, Version: "1.99"}, {ID: 3, Version: "9.0"}, {ID: 3, Version: "invalid"}, {ID: 3, Version: "01.02"}}
	w := deviceRequest(New(o), "GET", "/v1/devices/pump/model", "")
	var body struct {
		Objects []struct {
			Version    string
			Instances  []uint16
			Definition *struct{ Version string }
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Objects) != 6 {
		t.Fatal(w.Code, w.Body.String())
	}
	var raw struct {
		Objects []map[string]json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{1, 3, 4} {
		if string(raw.Objects[i]["definition"]) != "null" {
			t.Fatalf("object %d must include explicit null definition: %s", i, w.Body.String())
		}
	}
	for i, version := range []string{"1.0", "1.0", "1.99", "9.0", "invalid", "01.02"} {
		obj := body.Objects[i]
		if obj.Version != version || obj.Instances == nil {
			t.Fatalf("object %d: %s", i, w.Body.String())
		}
		if i == 1 || i == 3 || i == 4 {
			if obj.Definition != nil {
				t.Fatalf("unknown definition %d: %s", i, w.Body.String())
			}
		} else {
			v, _ := model.ParseVersion(version)
			def, _ := model.Default().Get(3, v)
			if obj.Definition == nil || obj.Definition.Version != def.Version.String() {
				t.Fatalf("resolved version %d: %s", i, w.Body.String())
			}
		}
	}
	r.Objects = nil
	w = deviceRequest(New(o), "GET", "/v1/devices/pump/model", "")
	if w.Code != 200 || w.Body.String() != "{\"objects\":[]}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDeviceModelKnown(t *testing.T) {
	o, r := registrationFixture(t)
	r.Objects = []link.Object{{ID: 3, Version: "1.2", Instances: []uint16{0, 2}}}
	w := deviceRequest(New(o), "GET", "/v1/devices/pump/model", "")
	var got map[string]any
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	def, ok := model.Default().Get(3, model.Version{Major: 1, Minor: 2})
	if !ok {
		t.Fatal("builtin Device missing")
	}
	resources := []any{}
	types := map[string]string{"string": "string", "integer": "integer", "unsigned": "unsigned", "float": "float", "boolean": "boolean", "opaque": "opaque", "time": "time", "objlnk": "objlnk", "corelnk": "corelnk", "none": "none"}
	for id := 0; id < 65535; id++ {
		res, ok := def.Resources[uint16(id)]
		if !ok {
			continue
		}
		typ := types[res.Type.String()]
		if res.Executable() {
			typ = "none"
		}
		if typ == "" {
			t.Fatalf("unmapped builtin type %s", res.Type)
		}
		resources = append(resources, map[string]any{"id": float64(res.ID), "name": res.Name, "operations": res.Operations.String(), "type": typ, "multiple": res.Multiple, "mandatory": res.Mandatory, "units": res.Units, "range": res.Range, "description": res.Description})
	}
	want := map[string]any{"objects": []any{map[string]any{"id": float64(3), "version": "1.2", "instances": []any{float64(0), float64(2)}, "definition": map[string]any{"id": float64(def.ID), "version": def.Version.String(), "name": def.Name, "urn": def.URN, "lwm2mVersion": def.LwM2MVersion, "multiple": def.Multiple, "mandatory": def.Mandatory, "description": def.Description, "resources": resources}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model response differs: %s", w.Body.String())
	}
}
