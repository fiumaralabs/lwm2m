package apiv1

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

// The JSON action is deliberately decoded rather than constructing an unimplemented
// Go field, so the contract compiles and runs against the pre-feature baseline.
func profileApplyJob(t *testing.T, jobs JobStore, id string) {
	t.Helper()
	var action JobAction
	if err := json.Unmarshal([]byte(`{"profileApply":{"profile":"profile"}}`), &action); err != nil {
		t.Fatal(err)
	}
	job := Job{ID: id, Target: JobTarget{Devices: []string{"a", "b"}}, Action: action, Rollout: JobRollout{MaxConcurrent: 2}, TTLSeconds: 3600}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatalf("profile-apply action must be accepted: %v", err)
	}
	if started, err := jobs.StartJob("tenant", id, []string{"a", "b"}, time.Now()); err != nil || !started {
		t.Fatalf("start profile apply: started=%v error=%v", started, err)
	}
}

type profileApplyOperations struct {
	accepted []operations.Operation
	rejected []operations.Operation
	reject   string
}

func (s *profileApplyOperations) Submit(_ context.Context, _ string, op operations.Operation) (operations.Operation, error) {
	if op.Type == s.reject {
		s.rejected = append(s.rejected, op)
		return operations.Operation{}, operations.ErrInvalid
	}
	op.Status = operations.Queued
	s.accepted = append(s.accepted, op)
	return op, nil
}

func (s *profileApplyOperations) Get(_ context.Context, _, id string) (operations.Operation, error) {
	for _, op := range s.accepted {
		if op.ID == id {
			op.Status = operations.Succeeded
			return op, nil
		}
	}
	return operations.Operation{}, fmt.Errorf("unknown operation %s", id)
}

// The runner does not yet expose these dependencies. Set them when implemented,
// while keeping the baseline compilable so RED is a behavioral failure.
func profileApplyWire(r *JobRunner, name string, dependency any) {
	field := reflect.ValueOf(r).Elem().FieldByName(name)
	value := reflect.ValueOf(dependency)
	if field.IsValid() && field.CanSet() && value.Type().AssignableTo(field.Type()) {
		field.Set(value)
	}
}

func profileApplyFixture(t *testing.T, profile DeviceProfile) (JobStore, DeviceProfileStore, *memory.Observations, *profileApplyOperations, JobRunner) {
	t.Helper()
	jobs := NewMemoryJobStore()
	profiles := NewMemoryDeviceProfileStore()
	if err := profiles.Create("tenant", profile); err != nil {
		t.Fatal(err)
	}
	observations := &memory.Observations{}
	ops := &profileApplyOperations{}
	runner := JobRunner{Jobs: jobs, ServerTenant: "tenant", Executor: ops, Operations: ops}
	profileApplyWire(&runner, "DeviceProfiles", profiles)
	profileApplyWire(&runner, "Observations", observations)
	return jobs, profiles, observations, ops, runner
}

func profileApplyTicks(t *testing.T, runner JobRunner) {
	t.Helper()
	for i := 0; i < 12; i++ {
		if err := tickUnrestricted(runner, context.Background()); err != nil {
			t.Fatalf("profile apply tick: %v", err)
		}
	}
}

func TestProfileApplyTwoDevicesAndRerun(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", Attributes: json.RawMessage(`[{"path":"/3/0/9","attributes":{"pmin":10}}]`), Observations: json.RawMessage(`[{"paths":["/3303/0/5700"],"attributes":{"pmin":60}}]`), OnRegister: json.RawMessage(`[{"type":"discover","path":"/3"}]`)}
	jobs, _, observations, ops, runner := profileApplyFixture(t, profile)
	profileApplyJob(t, jobs, "first")
	profileApplyTicks(t, runner)
	for _, device := range []string{"a", "b"} {
		var write, discover bool
		for _, op := range ops.accepted {
			if op.Device != device {
				continue
			}
			if op.Type == "write-attributes" {
				var request struct {
					Path       string         `json:"path"`
					Attributes map[string]int `json:"attributes"`
				}
				if err := json.Unmarshal(op.Request, &request); err != nil {
					t.Fatal(err)
				}
				write = request.Path == "/3/0/9" && request.Attributes["pmin"] == 10
			}
			if op.Type == "discover" && op.WhenOffline == "queue" {
				discover = true
			}
		}
		if !write || !discover {
			t.Errorf("%s: attribute write=%v queued discover=%v operations=%+v", device, write, discover, ops.accepted)
		}
	}
	for _, task := range jobs.ListTasks("tenant", "first", "") {
		if task.Status != "succeeded" {
			t.Errorf("profile apply failed: %+v", task)
		}
	}
	t.Run("observations and rerun", func(t *testing.T) {
		for _, device := range []string{"a", "b"} {
			items, err := observations.List("tenant", device)
			if err != nil || len(items) != 1 || items[0].Source != "profile" || items[0].ProfileID != "profile" || len(items[0].Paths) != 1 || items[0].Paths[0] != "/3303/0/5700" || string(items[0].Attributes["pmin"]) != "60" || !items[0].Persistent {
				t.Errorf("%s observations=%+v err=%v", device, items, err)
			}
		}
		profileApplyJob(t, jobs, "second")
		profileApplyTicks(t, runner)
		for _, device := range []string{"a", "b"} {
			items, err := observations.List("tenant", device)
			if err != nil || len(items) != 1 {
				t.Errorf("%s rerun observations=%+v err=%v", device, items, err)
			}
		}
	})
}

func TestProfileApplyDeletedProfileFailsTasks(t *testing.T) {
	jobs, profiles, _, _, runner := profileApplyFixture(t, DeviceProfile{ID: "profile", Name: "pump"})
	profileApplyJob(t, jobs, "deleted")
	if found, inUse := profiles.Delete("tenant", "profile"); !found || inUse {
		t.Fatalf("delete found=%v inUse=%v", found, inUse)
	}
	profileApplyTicks(t, runner)
	tasks := jobs.ListTasks("tenant", "deleted", "")
	if len(tasks) != 2 {
		t.Fatalf("deleted profile tasks=%+v, want two", tasks)
	}
	for _, task := range tasks {
		if task.Status != "failed" || task.Error == "" {
			t.Errorf("deleted profile task=%+v", task)
		}
	}
}

func TestProfileApplyRejectedOperationFailsTarget(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", OnRegister: json.RawMessage(`[{"type":"discover","path":"/3"}]`)}
	jobs, _, _, ops, runner := profileApplyFixture(t, profile)
	ops.reject = "discover"
	profileApplyJob(t, jobs, "unsupported")
	profileApplyTicks(t, runner)
	if len(ops.rejected) != 2 {
		t.Errorf("executor rejections=%+v, want one for each device", ops.rejected)
	}
	tasks := jobs.ListTasks("tenant", "unsupported", "")
	if len(tasks) != 2 {
		t.Fatalf("unsupported operation tasks=%+v, want two", tasks)
	}
	for _, task := range tasks {
		if task.Status != "failed" || task.Error != "unsupported_operation" {
			t.Errorf("unsupported operation task=%+v", task)
		}
	}
}

func TestProfileApplyChangedProfileFailsInsteadOfSkippingStep(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", Attributes: json.RawMessage(`[{"path":"/3/0/9","attributes":{"pmin":10}}]`), OnRegister: json.RawMessage(`[{"type":"discover","path":"/3"}]`)}
	jobs, profiles, _, ops, runner := profileApplyFixture(t, profile)
	profileApplyJob(t, jobs, "changed")
	if err := tickUnrestricted(runner, context.Background()); err != nil {
		t.Fatal(err)
	}
	profile.Attributes = nil
	if !profiles.Replace("tenant", profile) {
		t.Fatal("replace")
	}
	profileApplyTicks(t, runner)
	for _, task := range jobs.ListTasks("tenant", "changed", "") {
		if task.Status != "failed" {
			t.Errorf("changed profile task=%+v", task)
		}
	}
	if len(ops.accepted) != 2 {
		t.Errorf("unexpected operations after profile changed: %+v", ops.accepted)
	}
}

func TestProfileApplyObservationsFailClosed(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", Observations: json.RawMessage(`[{"paths":["/3303/0/5700"]}]`)}
	jobs, _, _, _, runner := profileApplyFixture(t, profile)
	runner.Observations = nil
	profileApplyJob(t, jobs, "observations")
	profileApplyTicks(t, runner)
	for _, task := range jobs.ListTasks("tenant", "observations", "") {
		if task.Status != "failed" || task.Error == "" {
			t.Errorf("unsupported observations must fail: %+v", task)
		}
	}
}

func TestProfileApplyRevisionPersistedBeforeFirstSubmit(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", OnRegister: json.RawMessage(`[{"type":"discover","path":"/3"},{"type":"discover","path":"/4"}]`)}
	jobs, _, _, ops, runner := profileApplyFixture(t, profile)
	profileApplyJob(t, jobs, "recovery")
	snapshots := map[string]JobTask{}
	runner.Executor = jobSubmitterFunc(func(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
		for _, task := range jobs.ListTasks(tenant, "recovery", "") {
			if task.Device == op.Device {
				snapshots[task.Device] = task
			}
		}
		return ops.Submit(ctx, tenant, op)
	})
	if err := tickUnrestricted(runner, context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops.accepted {
		task := snapshots[op.Device]
		if task.ProfileRevision == "" || task.Status != "dispatching" {
			t.Errorf("before submit: %+v", task)
		}
		task.Status, task.Operation = "running", op.ID
		if !jobs.PutTask("tenant", "recovery", task) {
			t.Fatal("restore recovered task")
		}
	}
	profileApplyTicks(t, runner)
	for _, task := range jobs.ListTasks("tenant", "recovery", "") {
		if task.Status != "succeeded" {
			t.Errorf("recovered task: %+v", task)
		}
	}
}

func TestProfileApplyDriftDoesNotSucceed(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", Observations: json.RawMessage(`[{"paths":["/3/0/9"],"attributes":{"pmin":60}}]`)}
	jobs, _, observations, _, runner := profileApplyFixture(t, profile)
	profileApplyJob(t, jobs, "first-drift")
	profileApplyTicks(t, runner)
	items, _ := observations.List("tenant", "a")
	if len(items) != 1 {
		t.Fatalf("observations: %+v", items)
	}
	if _, err := observations.UpdateAttributes("tenant", items[0].ID, map[string]json.RawMessage{"pmin": json.RawMessage(`10`)}); err != nil {
		t.Fatal(err)
	}
	profileApplyJob(t, jobs, "second-drift")
	profileApplyTicks(t, runner)
	seen := false
	for _, task := range jobs.ListTasks("tenant", "second-drift", "") {
		if task.Device == "a" {
			seen = true
			if task.Status != "failed" || task.Error != "profile observation conflict" {
				t.Fatalf("drifted observation must fail: %+v", task)
			}
		}
	}
	if !seen {
		t.Fatal("missing device task")
	}
}

func TestProfileApplyRenewsEndedNonpersistentObservation(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", Observations: json.RawMessage(`[{"paths":["/3/0/9"],"persistent":false}]`)}
	jobs, _, observations, _, runner := profileApplyFixture(t, profile)
	profileApplyJob(t, jobs, "first-ended")
	profileApplyTicks(t, runner)
	delivery := NewObservationDelivery(nil, observations, nil, nil, "tenant")
	delivery.drop("a")
	old, _ := observations.List("tenant", "a")
	if len(old) != 1 || old[0].Status != "ended" {
		t.Fatalf("drop: %+v", old)
	}
	profileApplyJob(t, jobs, "second-ended")
	profileApplyTicks(t, runner)
	for _, task := range jobs.ListTasks("tenant", "second-ended", "") {
		if task.Status != "succeeded" {
			t.Fatalf("reapply: %+v", task)
		}
	}
	items, _ := observations.List("tenant", "a")
	if len(items) != 2 {
		t.Fatalf("expected old and fresh intent: %+v", items)
	}
	var pending int
	for _, v := range items {
		if v.Status == "pending" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("expected one fresh intent: %+v", items)
	}
	profileApplyJob(t, jobs, "third-ended")
	profileApplyTicks(t, runner)
	items, _ = observations.List("tenant", "a")
	if len(items) != 2 {
		t.Fatalf("rerun duplicated intent: %+v", items)
	}
}

func TestProfileApplyConcurrentRenewalConverges(t *testing.T) {
	profile := DeviceProfile{ID: "profile", Name: "pump", Observations: json.RawMessage(`[{"paths":["/3/0/9"],"persistent":false}]`)}
	_, _, observations, _, runner := profileApplyFixture(t, profile)
	task := JobTask{Device: "a"}
	if _, _, err := runner.profileStep("tenant", "profile", &task); err != nil {
		t.Fatal(err)
	}
	NewObservationDelivery(nil, observations, nil, nil, "tenant").drop("a")
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task := JobTask{Device: "a"}
			_, done, err := runner.profileStep("tenant", "profile", &task)
			if err != nil {
				errs <- err
			} else if !done {
				errs <- fmt.Errorf("apply incomplete")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	items, err := observations.List("tenant", "a")
	if err != nil || len(items) != 2 {
		t.Fatalf("renewal must converge: %+v %v", items, err)
	}
}
