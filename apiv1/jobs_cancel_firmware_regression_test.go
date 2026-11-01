package apiv1

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJobCancelKeepsRunningFirmwareUpdate(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{FirmwareUpdate: []byte(`{"uri":"https://example.com/fw"}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("default", "job", "")
	tasks[0].Status = "running"
	tasks[0].FirmwareUpdate = "fw-1"
	if !jobs.PutTask("default", "job", tasks[0]) {
		t.Fatal("PutTask failed")
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/jobs/job/cancel", nil)
	req = req.WithContext(context.WithValue(req.Context(), credentialKey{}, credential{tenant: "default"}))
	serveJobAction(jobs, nil, nil, w, req)
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	got, _ := jobs.Get("default", "job")
	if got.Status != "cancelled" || got.Counts.Running != 1 {
		t.Fatalf("job: %+v", got)
	}
	tasks = jobs.ListTasks("default", "job", "running")
	if len(tasks) != 1 || tasks[0].FirmwareUpdate != "fw-1" {
		t.Fatalf("tasks: %+v", tasks)
	}
}
