package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceProfileApplyContract(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	for _, p := range []struct{ tenant, id string }{{"default", "dp_target"}, {"default", "dp_other"}, {"other", "dp_foreign"}} {
		if err := profiles.Create(p.tenant, DeviceProfile{ID: p.id, Name: p.id}); err != nil {
			t.Fatal(err)
		}
	}
	devices := &memory.Devices{}
	srv := server.New(server.Config{})
	defer srv.Close()
	for _, d := range []struct {
		tenant, endpoint, profile string
		registered                bool
	}{
		{"default", "match-a", "dp_target", true},
		{"default", "match-b", "dp_target", true},
		{"default", "offline", "dp_target", false},
		{"default", "wrong-profile", "dp_other", true},
		{"other", "wrong-tenant", "dp_target", true},
	} {
		if _, err := devices.Create(d.tenant, store.Device{Endpoint: d.endpoint, Profile: d.profile}); err != nil {
			t.Fatal(err)
		}
		if d.registered {
			srv.Store().Add(&server.Registration{ID: "reg-" + d.endpoint, Endpoint: d.endpoint, LastUpdate: time.Now()})
		}
	}
	jobs := NewMemoryJobStore()
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, DeviceProfiles: profiles, Devices: devices, Jobs: jobs, Server: srv})

	// Exercise the profile dispatch separately: the public OpenAPI gate currently
	// rejects this undocumented path before it reaches the profile handler.
	t.Run("profile handler dispatch", func(t *testing.T) {
		o := Options{DeviceProfiles: profiles, Devices: devices, Jobs: jobs, Server: srv, ServerTenant: "default"}
		handler := RequireScope("devices:write")(http.HandlerFunc(o.deviceProfiles))
		for _, tc := range []struct {
			id   string
			want int
		}{{"dp_target", 202}, {"dp_missing", 404}, {"dp_foreign", 404}} {
			w := jobRequest(handler, "POST", "/v1/device-profiles/"+tc.id+"/apply", "", "default", "devices:write")
			if w.Code != tc.want {
				t.Errorf("%s: got %d, want %d: %s", tc.id, w.Code, tc.want, w.Body.String())
			}
		}
	})

	t.Run("accepted job targets registered matching devices", func(t *testing.T) {
		w := deviceRequest(h, "POST", "/v1/device-profiles/dp_target/apply", "")
		if w.Code != 202 {
			t.Fatalf("apply: got %d, want 202: %s", w.Code, w.Body.String())
		}
		var job Job
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(job.ID, "job_") || job.Status == "" || job.CreatedAt.IsZero() {
			t.Fatalf("invalid job resource: %s", w.Body.String())
		}
		stored, ok := jobs.Get("default", job.ID)
		if !ok {
			t.Fatalf("job %q not stored for tenant", job.ID)
		}
		if _, ok := jobs.Get("other", job.ID); ok {
			t.Fatal("job visible to another tenant")
		}
		if !reflect.DeepEqual(job.Target, stored.Target) {
			t.Fatalf("response target %+v differs from stored %+v", job.Target, stored.Target)
		}
		got := append([]string(nil), stored.Target.Devices...)
		sort.Strings(got)
		if want := []string{"match-a", "match-b"}; !reflect.DeepEqual(got, want) || stored.Target.Selector != "" {
			t.Errorf("job target = %+v, want devices %v", stored.Target, want)
		}
	})

	for _, tc := range []struct{ name, id string }{{"unknown profile", "dp_missing"}, {"another tenant profile", "dp_foreign"}} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(jobs.List("default"))
			w := deviceRequest(h, "POST", "/v1/device-profiles/"+tc.id+"/apply", "")
			if w.Code != 404 {
				t.Errorf("got %d, want 404: %s", w.Code, w.Body.String())
			}
			if len(jobs.List("default")) != before {
				t.Error("rejected request created a job")
			}
		})
	}
	t.Run("write scope required", func(t *testing.T) {
		readKeys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,", tenant: "default"}}}
		readOnly := New(Options{Keys: readKeys, DeviceProfiles: profiles, Devices: devices, Jobs: jobs, Server: srv})
		before := len(jobs.List("default"))
		w := deviceRequest(readOnly, "POST", "/v1/device-profiles/dp_target/apply", "")
		if w.Code != 403 {
			t.Errorf("got %d, want 403: %s", w.Code, w.Body.String())
		}
		if len(jobs.List("default")) != before {
			t.Error("unauthorized request created a job")
		}
	})
}
