package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"errors"
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
