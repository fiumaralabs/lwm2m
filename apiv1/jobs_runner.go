package apiv1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

// JobRunner starts due jobs and reconciles submitted operations. To dispatch,
// pass it as Options.JobRunner to New before calling Tick; standalone runners
// without shared admission fail closed rather than bypassing tenant limits.
type JobRunner struct {
	cancelMu       sync.Locker
	Jobs           JobStore
	Devices        store.Devices
	DeviceProfiles DeviceProfileStore
	Observations   store.Observations
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
	if r.cancelMu != nil {
		r.cancelMu.Lock()
		defer r.cancelMu.Unlock()
	}
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
						if job.Action.ProfileApply != nil && job.Status != "cancelled" {
							task.Status, task.Operation, task.Step = "pending", "", task.Step+1
						} else {
							task.Status = "succeeded"
						}
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
			if job.Status == "running" && r.Executor != nil && tenant == serverTenant && (len(job.Action.Operation) > 0 || job.Action.ProfileApply != nil) {
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
					action := job.Action.Operation
					if job.Action.ProfileApply != nil {
						var done bool
						var actionErr error
						action, done, actionErr = r.profileStep(tenant, job.Action.ProfileApply.Profile, &task)
						if actionErr != nil || done {
							if actionErr != nil {
								task.Status, task.Error = "failed", actionErr.Error()
							} else {
								task.Status = "succeeded"
							}
							if !r.Jobs.PutTask(tenant, job.ID, task) {
								return fmt.Errorf("cannot record task")
							}
							continue
						}
						// Keep the claim recoverable with its revision before submitting an operation.
						if !r.Jobs.PutTask(tenant, job.ID, task) {
							return fmt.Errorf("cannot record profile revision")
						}
					}
					var input struct {
						Type        string                     `json:"type"`
						TTL         *int                       `json:"ttlSeconds"`
						WhenOffline string                     `json:"whenOffline"`
						Prefix      string                     `json:"prefix"`
						Format      string                     `json:"format"`
						Accept      string                     `json:"accept"`
						Paths       []string                   `json:"paths,omitempty"`
						Path        string                     `json:"path,omitempty"`
						Depth       *int                       `json:"depth,omitempty"`
						Values      []json.RawMessage          `json:"values,omitempty"`
						Mode        string                     `json:"mode,omitempty"`
						Arguments   string                     `json:"arguments,omitempty"`
						Attributes  map[string]json.RawMessage `json:"attributes,omitempty"`
					}
					decoder := json.NewDecoder(bytes.NewReader(action))
					decoder.DisallowUnknownFields()
					parseErr := decoder.Decode(&input)
					// Profile operations may contain fields the executor cannot handle.
					// Preserve invalid-input errors for supported types, but classify
					// valid unsupported types consistently even when decoding rejects
					// their type-specific fields.
					if parseErr != nil && validProfileOperation(action) {
						switch input.Type {
						case "read", "write", "execute", "delete", "write-attributes", "sync-clock", "discover", "create":
						default:
							parseErr = operations.ErrInvalid
						}
					}
					if parseErr == nil && (input.TTL != nil && (*input.TTL < 0 || *input.TTL > 2147483647) || input.WhenOffline != "" && input.WhenOffline != "queue" && input.WhenOffline != "reject") {
						parseErr = fmt.Errorf("invalid operation options")
					}
					request, _ := json.Marshal(struct {
						Paths      []string                   `json:"paths,omitempty"`
						Path       string                     `json:"path,omitempty"`
						Depth      *int                       `json:"depth,omitempty"`
						Values     []json.RawMessage          `json:"values,omitempty"`
						Mode       string                     `json:"mode,omitempty"`
						Arguments  string                     `json:"arguments,omitempty"`
						Attributes map[string]json.RawMessage `json:"attributes,omitempty"`
					}{input.Paths, input.Path, input.Depth, input.Values, input.Mode, input.Arguments, input.Attributes})
					op := operations.New(tenant, task.Device, input.Type, request, input.TTL, now)
					op.Job = &job.ID
					if job.Action.ProfileApply != nil {
						step := task.Step
						op.JobStep = &step
					}
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
						if errors.Is(err, operations.ErrInvalid) && job.Action.ProfileApply != nil {
							task.Error = "unsupported_operation"
						} else if errors.Is(err, operations.ErrInvalid) {
							switch input.Type {
							case "read", "write", "execute", "delete", "write-attributes", "sync-clock", "discover", "create":
							default:
								task.Error = "unsupported_operation"
							}
						}
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

// profileStep selects one sequential operation from the tenant-scoped profile.
func (r JobRunner) profileStep(tenant, id string, task *JobTask) (json.RawMessage, bool, error) {
	if r.DeviceProfiles == nil {
		return nil, false, fmt.Errorf("profile store unavailable")
	}
	profile, ok := r.DeviceProfiles.Get(tenant, id)
	if !ok {
		return nil, false, fmt.Errorf("profile not found")
	}
	revision := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte(nil), profile.Attributes...), append([]byte{0}, profile.OnRegister...)...)))
	if task.ProfileRevision == "" && task.Step == 0 {
		task.ProfileRevision = revision
	}
	if task.ProfileRevision != revision {
		return nil, false, fmt.Errorf("profile changed during apply")
	}
	step := task.Step
	var attrs []struct {
		Path       string          `json:"path"`
		Attributes json.RawMessage `json:"attributes"`
	}
	var ops []json.RawMessage
	if len(profile.Attributes) > 0 && json.Unmarshal(profile.Attributes, &attrs) != nil {
		return nil, false, fmt.Errorf("invalid profile attributes")
	}
	if len(profile.OnRegister) > 0 && json.Unmarshal(profile.OnRegister, &ops) != nil {
		return nil, false, fmt.Errorf("invalid profile operations")
	}
	if step < 0 {
		return nil, false, fmt.Errorf("invalid profile step")
	}
	if step < len(attrs) {
		raw, err := json.Marshal(struct {
			Type       string          `json:"type"`
			Path       string          `json:"path"`
			Attributes json.RawMessage `json:"attributes"`
		}{"write-attributes", attrs[step].Path, attrs[step].Attributes})
		return raw, false, err
	}
	step -= len(attrs)
	if step >= len(ops) {
		var observations []json.RawMessage
		if len(profile.Observations) > 0 && json.Unmarshal(profile.Observations, &observations) != nil {
			return nil, false, fmt.Errorf("invalid profile observations")
		}
		if len(observations) > 0 {
			return nil, false, fmt.Errorf("profile observations not supported")
		}
		return nil, true, nil
	}
	return ops[step], false, nil
}
