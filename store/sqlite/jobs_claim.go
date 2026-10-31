package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// ClaimTasks reserves rollout slots while holding the SQLite write lock.
func (s *Jobs) ClaimTasks(tenant, id string) ([]apiv1.JobTask, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	var b []byte
	err = conn.QueryRowContext(ctx, `SELECT payload FROM jobs WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job := decodeJob(b)
	if job.Status != "running" {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, `SELECT payload FROM job_tasks WHERE tenant=? AND job_id=? ORDER BY device`, tenant, id)
	if err != nil {
		return nil, err
	}
	available := job.Rollout.MaxConcurrent
	var pending []apiv1.JobTask
	for rows.Next() {
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
		var task apiv1.JobTask
		if err = gob.NewDecoder(bytes.NewReader(b)).Decode(&task); err != nil {
			rows.Close()
			return nil, err
		}
		switch task.Status {
		case "running", "dispatching":
			available--
		case "pending":
			pending = append(pending, task)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var claimed []apiv1.JobTask
	for _, task := range pending {
		if available <= 0 {
			break
		}
		task.Status = "dispatching"
		var buf bytes.Buffer
		if err = gob.NewEncoder(&buf).Encode(task); err != nil {
			return nil, err
		}
		if _, err = conn.ExecContext(ctx, `UPDATE job_tasks SET payload=? WHERE tenant=? AND job_id=? AND device=?`, buf.Bytes(), tenant, id, task.Device); err != nil {
			return nil, err
		}
		claimed = append(claimed, task)
		available--
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	return claimed, nil
}
