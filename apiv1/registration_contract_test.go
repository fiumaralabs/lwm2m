package apiv1

import (
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func registrationFixture(t *testing.T) (Options, *server.Registration) {
	t.Helper()
	s := server.New(server.Config{ExpiryCheck: time.Hour})
	t.Cleanup(func() { s.Close() })
	d := &memory.Devices{}
	if _, err := d.Create("default", store.Device{Endpoint: "pump"}); err != nil {
		t.Fatal(err)
	}
	r := &server.Registration{ID: "rd_test", Endpoint: "pump", Version: "1.2", Binding: "U", LastUpdate: time.Now()}
	s.Store().Add(r)
	return Options{Keys: deviceKeys(), Devices: d, Server: s}, r
}

func registrationProblem(t *testing.T, h http.Handler, method, path, code string, status int) {
	t.Helper()
	w := deviceRequest(h, method, path, "")
	var p Problem
	if w.Code != status || w.Header().Get("Content-Type") != "application/problem+json" || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Code != code {
		t.Fatalf("%s %s: %d %s, want %d %s", method, path, w.Code, w.Body.String(), status, code)
	}
}

func TestRegistrationJSON(t *testing.T) {
	o, r := registrationFixture(t)
	r.RegisteredAt = time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	r.LastUpdate = time.Date(2099, 10, 8, 9, 58, 12, 0, time.UTC)
	r.Lifetime, r.Binding, r.QueueMode = 24*time.Hour, "UQ", true
	r.SMS, r.ProfileIDs = "+316****0000", []string{"oma:1"}
	r.Objects = []link.Object{{ID: 3, Version: "1.2", Instances: []uint16{0}}, {ID: 3303, Version: "1.1", Instances: []uint16{0, 1}}}
	r.ContentFormats = []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR, lwm2m.FormatTLV}
	r.Identity = server.Identity{Mode: server.ModePSK, PSKIdentity: "pump-7"}
	r.Addr = &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 52114}
	h := New(o)
	w := deviceRequest(h, "GET", "/v1/devices/pump/registration", "")
	want := `{"id":"rd_test","lwm2mVersion":"1.2","lifetimeSeconds":86400,"binding":"UQ","queueMode":true,"sms":"+316****0000","profileIds":["oma:1"],"rootPath":"","objects":[{"id":3,"version":"1.2","instances":[0]},{"id":3303,"version":"1.1","instances":[0,1]}],"contentFormats":["senml-cbor","lwm2m-cbor","tlv"],"identity":{"mode":"psk","pskIdentity":"pump-7"},"transport":"U","address":"203.0.113.7:52114","awake":false,"registeredAt":"2026-10-08T06:00:00Z","lastUpdateAt":"2099-10-08T09:58:12Z","expiresAt":"2099-10-09T09:58:12Z"}`
	var got, expected any
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(want), &expected) != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var device struct{ Registration any }
	dw := deviceRequest(h, "GET", "/v1/devices/pump", "")
	if json.Unmarshal(dw.Body.Bytes(), &device) != nil || !reflect.DeepEqual(got, device.Registration) {
		t.Fatal("device and registration differ", dw.Body.String())
	}
}

func TestRegistrationUnknownFields(t *testing.T) {
	o, r := registrationFixture(t)
	r.LastUpdate = time.Time{}
	r.Identity.Mode = server.SecurityMode(255)
	r.ContentFormats = []lwm2m.ContentFormat{65535}
	h := New(o)
	w := deviceRequest(h, "GET", "/v1/devices/pump/registration", "")
	var got, want any
	expected := `{"id":"rd_test","lwm2mVersion":"1.2","lifetimeSeconds":0,"binding":"U","queueMode":false,"rootPath":"","objects":[],"contentFormats":[],"transport":"U","awake":true}`
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(expected), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, mode := range []server.SecurityMode{server.ModeNoSec, server.ModePSK, server.ModeRPK, server.ModeX509} {
		r.Identity.Mode = mode
		w = deviceRequest(h, "GET", "/v1/devices/pump/registration", "")
		var body struct{ Identity map[string]any }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Identity) != 1 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestRegistrationMissingAndTenants(t *testing.T) {
	for _, scenario := range []string{"missing", "expired", "nil-server", "unknown-device", "foreign", "foreign-same-name"} {
		t.Run(scenario, func(t *testing.T) {
			o, r := registrationFixture(t)
			code := "not_registered"
			switch scenario {
			case "missing":
				o.Server.Store().Remove(r.ID)
			case "expired":
				r.Lifetime = time.Second
				r.LastUpdate = time.Now().Add(-time.Hour)
			case "nil-server":
				o.Server = nil
			case "unknown-device":
				o.Devices = &memory.Devices{}
				code = "not_found"
			case "foreign", "foreign-same-name":
				entry := o.Keys.entries["test"]
				entry.tenant = "other"
				o.Keys.entries["test"] = entry
				if scenario == "foreign" {
					code = "not_found"
				} else if _, err := o.Devices.Create("other", store.Device{Endpoint: "pump"}); err != nil {
					t.Fatal(err)
				}
			}
			h := New(o)
			for _, method := range []string{"GET", "DELETE"} {
				registrationProblem(t, h, method, "/v1/devices/pump/registration", code, 404)
			}
			if o.Server != nil && scenario != "missing" {
				if _, ok := o.Server.Store().ByEndpoint("pump"); !ok {
					t.Fatal("session removed")
				}
			}
		})
	}
}

func TestRegistrationScopes(t *testing.T) {
	for _, method := range []string{"GET", "DELETE"} {
		o, _ := registrationFixture(t)
		entry := o.Keys.entries["test"]
		entry.scopes = ",devices:write,"
		if method == "DELETE" {
			entry.scopes = ",devices:read,"
		}
		o.Keys.entries["test"] = entry
		registrationProblem(t, New(o), method, "/v1/devices/pump/registration", "forbidden", 403)
	}
}

func TestRegistrationDelete(t *testing.T) {
	o, _ := registrationFixture(t)
	var removed *server.Deregistered
	// Use the real server removal event, not a handler-local imitation.
	o.Server.Close()
	o.Server = server.New(server.Config{OnEvent: func(e server.Event) {
		if e, ok := e.(server.Deregistered); ok {
			removed = &e
		}
	}})
	t.Cleanup(func() { o.Server.Close() })
	o.Server.Store().Add(&server.Registration{ID: "delete", Endpoint: "pump"})
	h := New(o)
	w := deviceRequest(h, "DELETE", "/v1/devices/pump/registration", "")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if removed == nil || removed.Reason != server.ReasonRemoved || removed.Registration.ID != "delete" {
		t.Fatalf("missing removal event: %+v", removed)
	}
	registrationProblem(t, h, "GET", "/v1/devices/pump/registration", "not_registered", 404)
	registrationProblem(t, h, "DELETE", "/v1/devices/pump/registration", "not_registered", 404)
	if _, err := o.Devices.Get("default", "pump"); err != nil {
		t.Fatal("provisioned device removed", err)
	}
}

func TestRegistrationPathsAndMethods(t *testing.T) {
	o, r := registrationFixture(t)
	for _, ep := range []string{"a/b", "registration"} {
		if _, err := o.Devices.Create("default", store.Device{Endpoint: ep}); err != nil {
			t.Fatal(err)
		}
	}
	clone := *r
	clone.ID, clone.Endpoint = "slash", "a/b"
	o.Server.Store().Add(&clone)
	h := New(o)
	if w := deviceRequest(h, "GET", "/v1/devices/a%2Fb/registration", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/devices/a/b/registration", "/v1/devices//registration"} {
		registrationProblem(t, h, "GET", path, "not_found", 404)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/registration", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "HEAD", "OPTIONS"} {
		w := deviceRequest(h, method, "/v1/devices/pump/registration", "")
		if w.Code != 405 || w.Header().Get("Allow") != "GET, DELETE" {
			t.Fatal(method, w.Code, w.Header(), w.Body.String())
		}
	}
	if w := deviceRequest(h, "DELETE", "/v1/devices/a%2Fb/registration", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
}
