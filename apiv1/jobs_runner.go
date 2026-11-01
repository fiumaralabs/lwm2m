package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

// JobRunner starts due jobs and reconciles submitted operations. To dispatch,
// pass it as Options.JobRunner to New before calling Tick; standalone runners
// without shared admission fail closed rather than bypassing tenant limits.
type JobRunner struct {
	Jobs    JobStore
	Devices store.Devices
	// ServerTenant is the sole tenant served by the executor (default: default).
	ServerTenant string
	Executor     interface {
		Submit(context.Context, string, operations.Operation) (operations.Operation, error)
	}
	Operations interface {
		Get(context.Context, string, string) (operations.Operation, error)
	}
	Clock  func() time.Time
	submit func(context.Context, string, operations.Operation) (operations.Operation, error)
}

// Tick discovers tenants from the job store and snapshots each due job once.
func (r JobRunner) Tick(ctx context.Context) error {
	now := time.Now().UTC()
	if r.Clock != nil {
		now = r.Clock().UTC()
	}
	tenants, err := r.Jobs.JobTenants()
	if err != nil {
		return err
	}
	for _, tenant := range tenants {
		for _, job := range r.Jobs.List(tenant) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if r.Operations != nil && (job.Status == "running" || job.Status == "paused" || job.Status == "cancelled") {
				for _, task := range r.Jobs.ListTasks(tenant, job.ID, "running") {
					if task.Operation == "" {
						continue
					}
					op, err := r.Operations.Get(ctx, tenant, task.Operation)
					if err != nil {
						return err
					}
					switch op.Status {
					case operations.Succeeded:
						task.Status = "succeeded"
					case operations.Failed, operations.Timeout, operations.Dropped:
						task.Status = "failed"
					case operations.Expired:
						task.Status = "expired"
					case operations.Cancelled:
						task.Status = "cancelled"
					default:
						continue
					}
					if op.Error != nil {
						task.Error = op.Error.Message
					}
					if !r.Jobs.PutTask(tenant, job.ID, task) {
						return fmt.Errorf("cannot record task")
					}
				}
				job, _ = r.Jobs.Get(tenant, job.ID)
			}
			// A snapshot is complete when none of its tasks can still run.
			if job.Status == "running" && job.Counts.Pending == 0 && job.Counts.Running == 0 {
				status := "succeeded"
				if job.Counts.Failed+job.Counts.Expired+job.Counts.Cancelled > 0 {
					status = "completed_with_failures"
				}
				r.Jobs.UpdateStatus(tenant, job.ID, status)
				continue
			}
			serverTenant := r.ServerTenant
			if serverTenant == "" {
				serverTenant = "default"
			}
			if job.Status == "running" && r.Executor != nil && tenant == serverTenant && len(job.Action.Operation) > 0 {
				if r.submit == nil {
					return fmt.Errorf("job runner requires handler operation admission")
				}
				claimer, ok := r.Jobs.(interface {
					ClaimTasks(string, string) ([]JobTask, error)
				})
				if !ok {
					return fmt.Errorf("job store does not support atomic claims")
				}
				// TODO(#177): recover claims orphaned by a crash before recording the operation.
				claimed, err := claimer.ClaimTasks(tenant, job.ID)
				if err != nil {
					return err
				}
				var admissionErr error
				for _, task := range claimed {
					if admissionErr != nil {
						task.Status = "pending"
						if !r.Jobs.PutTask(tenant, job.ID, task) {
							return fmt.Errorf("cannot release task")
						}
						continue
					}
					var input struct {
						Type        string            `json:"type"`
						TTL         *int              `json:"ttlSeconds"`
						WhenOffline string            `json:"whenOffline"`
						Prefix      string            `json:"prefix"`
						Format      string            `json:"format"`
						Accept      string            `json:"accept"`
						Paths       []string          `json:"paths,omitempty"`
						Path        string            `json:"path,omitempty"`
						Values      []json.RawMessage `json:"values,omitempty"`
						Mode        string            `json:"mode,omitempty"`
						Arguments   string            `json:"arguments,omitempty"`
					}
					decoder := json.NewDecoder(bytes.NewReader(job.Action.Operation))
					decoder.DisallowUnknownFields()
					parseErr := decoder.Decode(&input)
					if parseErr == nil && (input.TTL != nil && (*input.TTL < 0 || *input.TTL > 2147483647) || input.WhenOffline != "" && input.WhenOffline != "queue" && input.WhenOffline != "reject") {
						parseErr = fmt.Errorf("invalid operation options")
					}
					request, _ := json.Marshal(struct {
						Paths     []string          `json:"paths,omitempty"`
						Path      string            `json:"path,omitempty"`
						Values    []json.RawMessage `json:"values,omitempty"`
						Mode      string            `json:"mode,omitempty"`
						Arguments string            `json:"arguments,omitempty"`
					}{input.Paths, input.Path, input.Values, input.Mode, input.Arguments})
					op := operations.New(tenant, task.Device, input.Type, request, input.TTL, now)
					op.Job = &job.ID
					if input.WhenOffline != "" {
						op.WhenOffline = input.WhenOffline
					}
					op.Prefix, op.Format, op.Accept = input.Prefix, input.Format, input.Accept
					if parseErr != nil {
						err = parseErr
						op = operations.Operation{}
					} else {
						op, err = r.submit(ctx, tenant, op)
					}
					if errors.Is(err, errOperationLimit) || errors.Is(err, errOperationAdmission) {
						task.Status = "pending"
						if errors.Is(err, errOperationAdmission) {
							admissionErr = err
						}
					} else if err != nil && op.ID == "" {
						task.Status, task.Error = "failed", err.Error()
					} else {
						task.Status, task.Operation = "running", op.ID
					}
					if !r.Jobs.PutTask(tenant, job.ID, task) {
						return fmt.Errorf("cannot record task")
					}
				}
				if admissionErr != nil {
					return admissionErr
				}
			}
			if job.Status != "scheduled" || (job.Rollout.StartAt != nil && job.Rollout.StartAt.After(now)) {
				continue
			}
			starter, ok := r.Jobs.(interface {
				StartJobSnapshot(context.Context, store.Devices, string, string, time.Time) (bool, error)
			})
			if !ok {
				return fmt.Errorf("job store does not support atomic snapshots")
			}
			if _, err = starter.StartJobSnapshot(ctx, r.Devices, tenant, job.ID, now); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
