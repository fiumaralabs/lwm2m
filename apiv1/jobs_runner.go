package apiv1

import (
	"context"
	"fmt"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// JobRunner starts due jobs. Delivery and completion are handled separately.
type JobRunner struct {
	Jobs    JobStore
	Devices store.Devices
	Clock   func() time.Time
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
			// A snapshot is complete when none of its tasks can still run.
			if job.Status == "running" && job.Counts.Pending == 0 && job.Counts.Running == 0 {
				status := "succeeded"
				if job.Counts.Failed+job.Counts.Expired+job.Counts.Cancelled > 0 {
					status = "completed_with_failures"
				}
				r.Jobs.UpdateStatus(tenant, job.ID, status)
				continue
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
