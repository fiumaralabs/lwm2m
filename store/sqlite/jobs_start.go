package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"errors"
	"github.com/fiumaralabs/lwm2m/store"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func (s *Jobs) JobTenants() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT tenant FROM jobs ORDER BY tenant`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenant string
		if err = rows.Scan(&tenant); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}

func (s *Jobs) StartJob(tenant, id string, devices []string, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRow(`SELECT payload FROM jobs WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	j := decodeJob(b)
	if j.Status != "scheduled" || (j.Rollout.StartAt != nil && j.Rollout.StartAt.After(now)) {
		return false, nil
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM job_tasks WHERE tenant=? AND job_id=?`, tenant, id).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, errors.New("job already has tasks")
	}
	for _, device := range devices {
		if device == "" {
			return false, errors.New("empty endpoint")
		}
		var buf bytes.Buffer
		if err = gob.NewEncoder(&buf).Encode(apiv1.JobTask{Device: device, Status: "pending"}); err != nil {
			return false, err
		}
		if _, err = tx.Exec(`INSERT INTO job_tasks(tenant,job_id,device,payload) VALUES(?,?,?,?)`, tenant, id, device, buf.Bytes()); err != nil {
			return false, err
		}
	}
	j.Status = "running"
	j.StartedAt = &now
	b, err = encodeJob(j)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE jobs SET payload=? WHERE tenant=? AND id=?`, b, tenant, id); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// StartJobSnapshot resolves and commits against the same SQLite transaction.
func (s *Jobs) StartJobSnapshot(ctx context.Context, devices store.Devices, tenant, id string, now time.Time) (bool, error) {
	d, ok := devices.(*Devices)
	if !ok || d.db != s.db {
		return false, errors.New("job and device stores must share a database")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM jobs WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	j := decodeJob(b)
	if j.Status != "scheduled" || (j.Rollout.StartAt != nil && j.Rollout.StartAt.After(now)) {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM devices WHERE tenant=? ORDER BY endpoint`, tenant)
	if err != nil {
		return false, err
	}
	var view []store.Device
	for rows.Next() {
		var payload string
		if err = rows.Scan(&payload); err != nil {
			rows.Close()
			return false, err
		}
		var d store.Device
		if err = json.Unmarshal([]byte(payload), &d); err != nil {
			rows.Close()
			return false, err
		}
		view = append(view, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	endpoints, err := apiv1.MatchJobTarget(ctx, view, j.Target)
	if err != nil {
		return false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM job_tasks WHERE tenant=? AND job_id=?`, tenant, id).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, errors.New("job already has tasks")
	}
	for _, device := range endpoints {
		if device == "" {
			return false, errors.New("empty endpoint")
		}
		var buf bytes.Buffer
		if err = gob.NewEncoder(&buf).Encode(apiv1.JobTask{Device: device, Status: "pending"}); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO job_tasks(tenant,job_id,device,payload) VALUES(?,?,?,?)`, tenant, id, device, buf.Bytes()); err != nil {
			return false, err
		}
	}
	j.Status = "running"
	j.StartedAt = &now
	b, err = encodeJob(j)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET payload=? WHERE tenant=? AND id=?`, b, tenant, id); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
