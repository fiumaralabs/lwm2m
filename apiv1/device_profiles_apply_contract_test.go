package apiv1

import (
	"context"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"slices"
	"testing"
	"time"
)

func TestDeviceProfileApplyContract(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	for tenant, id := range map[string]string{"default": "dp_owned", "other": "dp_foreign"} {
		if err := profiles.Create(tenant, DeviceProfile{ID: id, Name: "pump"}); err != nil {
			t.Fatal(err)
		}
	}
	jobs := NewMemoryJobStore()
	keys := profileWriteKeys()
	cred := keys.entries["test"]
	cred.scopes += "jobs:write,"
	keys.entries["test"] = cred
	h := New(Options{Keys: keys, DeviceProfiles: profiles, Jobs: jobs})
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/v1/device-profiles/dp_missing/apply", "{}", 404},
		{"/v1/device-profiles/dp_foreign/apply", "{}", 404},
		{"/v1/device-profiles/dp_owned/apply", `{"unknown":true}`, 400},
	} {
		w := profileWriteRequest(h, "POST", tc.path, tc.body, "lwm_test_secret", "")
		if w.Code != tc.status {
			t.Errorf("%s %s: got %d %s", tc.path, tc.body, w.Code, w.Body.String())
		}
	}
	w := profileWriteRequest(h, "POST", "/v1/device-profiles/dp_owned/apply", "{}", "lwm_test_secret", "replay")
	var response struct {
		ID string `json:"id"`
	}
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.ID == "" {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	job, ok := jobs.Get("default", response.ID)
	if !ok || job.Action.ProfileApply == nil || job.Action.ProfileApply.Profile != "dp_owned" {
		t.Fatalf("job: %+v", job)
	}
	if _, ok := jobs.Get("other", response.ID); ok {
		t.Fatal("job leaked across tenants")
	}
	retry := profileWriteRequest(h, "POST", "/v1/device-profiles/dp_owned/apply", "{}", "lwm_test_secret", "replay")
	if retry.Code != 202 || retry.Body.String() != w.Body.String() || len(jobs.List("default")) != 1 {
		t.Fatalf("replay: %d %s", retry.Code, retry.Body.String())
	}
	bad := profileWriteRequest(h, "POST", "/v1/device-profiles/dp_owned/apply", `{"bad":true}`, "lwm_test_secret", "bad")
	retryBad := profileWriteRequest(h, "POST", "/v1/device-profiles/dp_owned/apply", `{}`, "lwm_test_secret", "bad")
	if bad.Code != 400 || retryBad.Code != bad.Code || retryBad.Body.String() != bad.Body.String() {
		t.Fatalf("error replay: %d %s -> %d %s", bad.Code, bad.Body.String(), retryBad.Code, retryBad.Body.String())
	}
	if denied := profileWriteRequest(h, "POST", "/v1/device-profiles/dp_owned/apply", `{}`, "lwm_write_secret", ""); denied.Code != 403 {
		t.Fatalf("missing jobs scope: %d", denied.Code)
	}
	other := validJob()
	other.ID = "collision"
	if _, err := jobs.CreateJobResult("default", "profile-apply:ZHBfb3duZWQ:collision", JobCreateResult{Job: &other}); err != nil {
		t.Fatal(err)
	}
	if collision := profileWriteRequest(h, "POST", "/v1/device-profiles/dp_owned/apply", "{}", "lwm_test_secret", "collision"); collision.Code != 409 {
		t.Fatalf("collision: %d %s", collision.Code, collision.Body.String())
	}
}

func TestDeviceProfileApplyTargetsRegisteredDevices(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	if err := profiles.Create("default", DeviceProfile{ID: "dp", Name: "pump"}); err != nil {
		t.Fatal(err)
	}
	devices := &memory.Devices{}
	for _, item := range []struct{ tenant, ep, profile string }{{"default", "online", "dp"}, {"default", "offline", "dp"}, {"default", "expired", "dp"}, {"default", "different", "another"}, {"other", "foreign", "dp"}} {
		if _, err := devices.Create(item.tenant, store.Device{Endpoint: item.ep, Profile: item.profile}); err != nil {
			t.Fatal(err)
		}
	}
	srv := server.New(server.Config{})
	t.Cleanup(func() { srv.Close() })
	for _, item := range []struct {
		ep string
		at time.Time
	}{{"online", time.Now()}, {"expired", time.Now().Add(-time.Hour)}, {"different", time.Now()}, {"foreign", time.Now()}} {
		srv.Store().Add(&server.Registration{ID: item.ep, Endpoint: item.ep, LastUpdate: item.at, Lifetime: time.Minute})
	}
	keys := profileWriteKeys()
	c := keys.entries["test"]
	c.scopes += "jobs:write,"
	keys.entries["test"] = c
	jobs := NewMemoryJobStore()
	h := New(Options{Keys: keys, Devices: devices, DeviceProfiles: profiles, Jobs: jobs, Server: srv})
	w := profileWriteRequest(h, "POST", "/v1/device-profiles/dp/apply", "{}", "lwm_test_secret", "")
	var response struct {
		ID string `json:"id"`
	}
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	job, ok := jobs.Get("default", response.ID)
	if !ok || !slices.Equal(job.Target.Devices, []string{"online"}) {
		t.Fatalf("target: %+v", job.Target)
	}
	runner := JobRunner{Jobs: jobs, Devices: devices}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("default", job.ID, "")
	if len(tasks) != 1 || tasks[0].Device != "online" {
		t.Fatalf("tasks: %+v", tasks)
	}
}
