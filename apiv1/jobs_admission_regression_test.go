package apiv1

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobRunnerRespectsOperationQuota(t *testing.T) {
	keys := deviceKeys()
	keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
	srv := server.New(server.Config{})
	defer srv.Close()
	ops := memory.NewOperations()
	exec := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer exec.Close()
	jobs := NewMemoryJobStore()
	runner := JobRunner{Jobs: jobs, Executor: exec, Operations: ops}
	h := New(Options{Keys: keys, Server: srv, Executor: exec, Jobs: jobs, JobRunner: &runner})
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":1}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"a", "b"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 2}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"a", "b"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("default", "job", "")
	if len(tasks) != 2 || tasks[0].Status != "running" || tasks[1].Status != "pending" {
		t.Fatalf("quota should defer second task: %+v", tasks)
	}
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":0}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks = jobs.ListTasks("default", "job", "")
	if tasks[1].Status != "running" || tasks[1].Operation == "" {
		t.Fatalf("deferred task was not retried: %+v", tasks)
	}
}

type admissionBlockingStore struct {
	operations.Operations
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *admissionBlockingStore) List(ctx context.Context, tenant string, f operations.ListFilter, cursor string, limit int) ([]operations.Operation, string, error) {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.Operations.List(ctx, tenant, f, cursor, limit)
}

func TestJobAndPOSTShareAtomicOperationAdmission(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
	srv := server.New(server.Config{})
	defer srv.Close()
	ops := &admissionBlockingStore{Operations: memory.NewOperations(), entered: make(chan struct{}), release: make(chan struct{})}
	exec := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer exec.Close()
	jobs := NewMemoryJobStore()
	runner := JobRunner{}
	h := New(Options{Keys: keys, Server: srv, Executor: exec, Jobs: jobs, JobRunner: &runner})
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":1}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"a", "b"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 2}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"a", "b"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	post := make(chan int, 1)
	go func() {
		post <- deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"]}`).Code
	}()
	select {
	case <-ops.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("POST did not enter admission")
	}
	done := make(chan error, 1)
	go func() { done <- runner.Tick(context.Background()) }()
	select {
	case err := <-done:
		close(ops.release)
		<-post
		t.Fatalf("runner passed locked admission: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(ops.release)
	if code := <-post; code != 202 {
		t.Fatalf("POST status %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("default", "job", "")
	if len(tasks) != 2 || tasks[0].Status != "pending" || tasks[1].Status != "pending" {
		t.Fatalf("tasks=%+v", tasks)
	}
	items, _, err := ops.Operations.List(context.Background(), "default", operations.ListFilter{}, "", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("operations=%d err=%v", len(items), err)
	}
}

func TestUnconfiguredJobRunnerFailsClosed(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	called := false
	r := JobRunner{Jobs: jobs, Executor: jobSubmitterFunc(func(context.Context, string, operations.Operation) (operations.Operation, error) {
		called = true
		return operations.Operation{}, nil
	})}
	if err := r.Tick(context.Background()); err == nil {
		t.Fatal("expected missing admission error")
	}
	tasks := jobs.ListTasks("default", "job", "")
	if called || len(tasks) != 1 || tasks[0].Status != "pending" {
		t.Fatalf("called=%v tasks=%+v", called, tasks)
	}
}
