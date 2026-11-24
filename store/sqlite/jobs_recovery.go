package sqlite

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/operations"
)

// RecoverJobClaims reconciles claims left by a stopped runner. It must be
// called before starting a new runner, with no other runner using this database.
func (s *Jobs) RecoverJobClaims(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	rows, err := conn.QueryContext(ctx, `SELECT t.tenant,t.job_id,t.payload,j.payload FROM job_tasks t JOIN jobs j ON j.tenant=t.tenant AND j.id=t.job_id`)
	if err != nil {
		return err
	}
	type claim struct {
		tenant, job string
		task        apiv1.JobTask
		cancelled   bool
	}
	var claims []claim
	for rows.Next() {
		var c claim
		var taskData, jobData []byte
		if err = rows.Scan(&c.tenant, &c.job, &taskData, &jobData); err != nil {
			break
		}
		if err = gob.NewDecoder(bytes.NewReader(taskData)).Decode(&c.task); err != nil {
			break
		}
		if c.task.Status == "dispatching" {
			c.cancelled = decodeJob(jobData).Status == "cancelled"
			claims = append(claims, c)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range claims {
		opRows, err := conn.QueryContext(ctx, `SELECT body, COALESCE((SELECT step FROM operation_job_steps js WHERE js.tenant=operations.tenant AND js.operation_id=operations.id),0) FROM operations WHERE tenant=? AND device=?`, c.tenant, c.task.Device)
		if err != nil {
			return err
		}
		var found string
		for opRows.Next() {
			var body string
			var step int
			var op operations.Operation
			if err = opRows.Scan(&body, &step); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(body), &op); err != nil {
				break
			}
			if op.Job != nil && *op.Job == c.job && step == c.task.Step {
				if found != "" {
					// Decision #2: preserve fail-closed recovery, including legacy untagged step 0 operations.
					err = fmt.Errorf("multiple operations for orphaned job %q device %q step %d", c.job, c.task.Device, c.task.Step)
					break
				}
				found = op.ID
			}
		}
		if err == nil {
			err = opRows.Err()
		}
		opRows.Close()
		if err != nil {
			return err
		}
		if found != "" {
			c.task.Status, c.task.Operation = "running", found
		} else if c.cancelled {
			c.task.Status, c.task.Operation = "cancelled", ""
		} else {
			c.task.Status, c.task.Operation = "pending", ""
		}
		var b bytes.Buffer
		if err = gob.NewEncoder(&b).Encode(c.task); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, `UPDATE job_tasks SET payload=? WHERE tenant=? AND job_id=? AND device=?`, b.Bytes(), c.tenant, c.job, c.task.Device); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
