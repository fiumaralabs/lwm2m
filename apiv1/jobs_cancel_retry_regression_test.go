package apiv1

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type cancelFailureStore struct {
	*memory.Operations
	fail bool
}

type observedCancelLock struct {
	mu       sync.Mutex
	attempts chan struct{}
}

func (l *observedCancelLock) Lock() {
	l.attempts <- struct{}{}
	l.mu.Lock()
}
func (l *observedCancelLock) Unlock() { l.mu.Unlock() }

func (s *cancelFailureStore) Update(ctx context.Context, tenant, id string, from operations.Status, c operations.Change) error {
	if c.Status == operations.Cancelled && s.fail {
		s.fail = false
		return errors.New("injected update failure")
	}
	return s.Operations.Update(ctx, tenant, id, from, c)
}
func cancelTestRequest(jobs JobStore, ex jobCanceller, runner *JobRunner) int {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/jobs/job/cancel", nil)
	r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: "default"}))
	serveJobAction(jobs, ex, runner, w, r)
	return w.Code
}
func cancelTestJob(t *testing.T) JobStore {
	t.Helper()
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return jobs
}
func TestJobCancelOperationStoreFailureRetry(t *testing.T) {
	ctx := context.Background()
	jobs := cancelTestJob(t)
	srv := server.New(server.Config{})
	defer srv.Close()
	ops := &cancelFailureStore{Operations: memory.NewOperations(), fail: true}
	ex := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	op := operations.New("default", "dev", "read", []byte(`{"paths":["/3/0/0"]}`), nil, time.Now())
	op, err := ex.Submit(ctx, "default", op)
	if err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("default", "job", "")[0]
	task.Status = "running"
	task.Operation = op.ID
	if !jobs.PutTask("default", "job", task) {
		t.Fatal("PutTask")
	}
	if code := cancelTestRequest(jobs, ex, nil); code != 500 {
		t.Fatalf("first cancel = %d", code)
	}
	got, err := ops.Get(ctx, "default", op.ID)
	if err != nil || got.Status != operations.Queued {
		t.Fatalf("operation after failure: %+v %v", got, err)
	}
	job, _ := jobs.Get("default", "job")
	if job.Status != "running" {
		t.Fatalf("job after failure: %+v", job)
	}
	if code := cancelTestRequest(jobs, ex, nil); code != 200 {
		t.Fatalf("retry = %d", code)
	}
	got, err = ops.Get(ctx, "default", op.ID)
	if err != nil || got.Status != operations.Cancelled {
		t.Fatalf("operation after retry: %+v %v", got, err)
	}
	job, _ = jobs.Get("default", "job")
	if job.Status != "cancelled" || job.Counts.Cancelled != 1 {
		t.Fatalf("job after retry: %+v", job)
	}
}
func TestJobCancelConcurrentSubmission(t *testing.T) {
	ctx := context.Background()
	jobs := cancelTestJob(t)
	srv := server.New(server.Config{})
	defer srv.Close()
	ops := memory.NewOperations()
	ex := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	lock := &observedCancelLock{attempts: make(chan struct{}, 2)}
	runner := JobRunner{Jobs: jobs, Executor: ex, Operations: ops, cancelMu: lock}
	runner.submit = func(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
		close(entered)
		<-release
		return ex.Submit(ctx, tenant, op)
	}
	tickDone := make(chan error, 1)
	go func() { tickDone <- runner.Tick(ctx) }()
	<-entered
	<-lock.attempts // Tick owns the lock while submission is paused.
	cancelDone := make(chan int, 1)
	go func() { cancelDone <- cancelTestRequest(jobs, ex, &runner) }()
	select {
	case <-lock.attempts: // Cancellation has attempted the same lock.
	case code := <-cancelDone:
		close(release)
		<-tickDone
		t.Fatalf("cancellation bypassed lock, response %d", code)
	case <-time.After(5 * time.Second):
		close(release)
		<-tickDone
		t.Fatal("cancellation did not reach lock")
	}
	close(release)
	if err := <-tickDone; err != nil {
		t.Fatal(err)
	}
	if code := <-cancelDone; code != 200 {
		t.Fatalf("cancel = %d", code)
	}
	tasks := jobs.ListTasks("default", "job", "")
	if len(tasks) != 1 || tasks[0].Status != "cancelled" || tasks[0].Operation == "" {
		t.Fatalf("tasks: %+v", tasks)
	}
	got, err := ops.Get(ctx, "default", tasks[0].Operation)
	if err != nil || got.Status != operations.Cancelled {
		t.Fatalf("operation: %+v %v", got, err)
	}
}
