package apiv1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

type jobSubmitterFunc func(context.Context, string, operations.Operation) (operations.Operation, error)

func (f jobSubmitterFunc) Submit(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
	return f(ctx, tenant, op)
}

func TestJobRunnerDispatchesClaimedOperation(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	submit := jobSubmitterFunc(func(_ context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
		calls++
		var request map[string]json.RawMessage
		if err := json.Unmarshal(op.Request, &request); err != nil {
			t.Fatal(err)
		}
		if _, ok := request["type"]; ok {
			t.Fatalf("request contains type: %s", op.Request)
		}
		if tenant != "tenant" || op.Device != "dev" || op.Type != "read" || op.Job == nil || *op.Job != "job" {
			t.Fatalf("unexpected op: %+v", op)
		}
		return op, nil
	})
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: submit}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("tenant", "job", "")
	if calls != 1 || len(tasks) != 1 || tasks[0].Status != "running" || tasks[0].Operation == "" {
		t.Fatalf("calls=%d tasks=%+v", calls, tasks)
	}
}

func TestJobRunnerPassesOperationOptions(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"],"ttlSeconds":7,"whenOffline":"reject","prefix":"d1","format":"a","accept":"b"}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	r := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(_ context.Context, _ string, op operations.Operation) (operations.Operation, error) {
		if op.TTLSeconds != 7 || op.WhenOffline != "reject" || op.Prefix != "d1" || op.Format != "a" || op.Accept != "b" {
			t.Fatalf("options: %+v", op)
		}
		return op, nil
	})}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestJobRunnerSubmitErrorFailsTaskWithoutRetry(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(context.Context, string, operations.Operation) (operations.Operation, error) {
		calls++
		return operations.Operation{}, errors.New("submit failed")
	})}
	for i := 0; i < 2; i++ {
		if err := r.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	task := jobs.ListTasks("tenant", "job", "")[0]
	if calls != 1 || task.Status != "failed" || task.Error != "submit failed" {
		t.Fatalf("calls=%d task=%+v", calls, task)
	}
}

func TestJobRunnerReconcilesTerminalOperation(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ops := memory.NewOperations()
	r := JobRunner{ServerTenant: "tenant", Jobs: jobs, Operations: ops, Executor: jobSubmitterFunc(func(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
		return op, ops.Create(ctx, tenant, op)
	})}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("tenant", "job", "")[0]
	if err := ops.Update(context.Background(), "tenant", task.Operation, operations.Queued, operations.Change{Status: operations.Sent, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := ops.Update(context.Background(), "tenant", task.Operation, operations.Sent, operations.Change{Status: operations.Succeeded, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := r.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	task = jobs.ListTasks("tenant", "job", "")[0]
	j, _ := jobs.Get("tenant", "job")
	if task.Status != "succeeded" || j.Status != "succeeded" {
		t.Fatalf("task=%+v job=%+v", task, j)
	}
}

func TestJobRunnerConcurrentTicksRespectMaxConcurrent(t *testing.T) {
	jobs := NewMemoryJobStore()
	devices := make([]string, 20)
	for i := range devices {
		devices[i] = fmt.Sprintf("dev-%d", i)
	}
	job := Job{ID: "job", Target: JobTarget{Devices: devices}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 3}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", devices, time.Now()); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	r := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(_ context.Context, _ string, op operations.Operation) (operations.Operation, error) {
		count.Add(1)
		return op, nil
	})}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Tick(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	tasks := jobs.ListTasks("tenant", "job", "")
	running := 0
	for _, task := range tasks {
		if task.Status == "running" {
			running++
			if task.Operation == "" {
				t.Fatal("missing operation")
			}
		}
	}
	if count.Load() != 3 || running != 3 {
		t.Fatalf("submitted=%d running=%d", count.Load(), running)
	}
}

func TestJobRunnerClaimBeforePauseMaySubmit(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"a", "b"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"a", "b"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	r := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(_ context.Context, _ string, op operations.Operation) (operations.Operation, error) {
		calls.Add(1)
		close(entered)
		<-release
		return op, nil
	})}
	go func() { done <- r.Tick(context.Background()) }()
	<-entered
	if !jobs.UpdateStatus("tenant", "job", "paused") {
		t.Fatal("pause")
	}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	tasks := jobs.ListTasks("tenant", "job", "")
	if tasks[0].Status != "running" || tasks[1].Status != "pending" {
		t.Fatalf("tasks=%+v", tasks)
	}
}

func TestJobRunnerRealExecutorDelivery(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"execute","path":"/3/0/4"}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ops := memory.NewOperations()
	var ex *operations.Executor
	srv := server.New(server.Config{OnEvent: func(ev server.Event) { ex.HandleEvent(ev) }})
	defer srv.Close()
	ex = operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	coapServer := coap.New(srv)
	defer coapServer.Close()
	addr, err := coapServer.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Operations: ops, Executor: ex}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("tenant", "job", "")[0]
	client := testclient.New(testclient.Config{Endpoint: "dev"})
	defer client.Close()
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := ex.Wait(ctx, "tenant", task.Operation)
	if err != nil || op.Status != operations.Succeeded {
		t.Fatalf("op=%+v err=%v", op, err)
	}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	task = jobs.ListTasks("tenant", "job", "")[0]
	j, _ := jobs.Get("tenant", "job")
	if task.Status != "succeeded" || j.Status != "succeeded" {
		t.Fatalf("task=%+v job=%+v", task, j)
	}
}

func TestJobRunnerRejectsUnknownOperationFields(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"],"typo":true}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(_ context.Context, _ string, op operations.Operation) (operations.Operation, error) {
		calls++
		return op, nil
	})}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("tenant", "job", "")[0]
	if calls != 0 || task.Status != "failed" || task.Error == "" {
		t.Fatalf("calls=%d task=%+v", calls, task)
	}
}
