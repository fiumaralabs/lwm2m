package apiv1

import (
	"context"
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
			if job.Status != "scheduled" || (job.Rollout.StartAt != nil && job.Rollout.StartAt.After(now)) {
				continue
			}
			endpoints, err := resolveJobTarget(ctx, r.Devices, tenant, job.Target)
			if err != nil {
				return err
			}
			if _, err = r.Jobs.StartJob(tenant, job.ID, endpoints, now); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
