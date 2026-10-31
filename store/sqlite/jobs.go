package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// Jobs shares its database with Devices. Operations without error results panic on database errors.
type Jobs struct{ db *sql.DB }

var _ apiv1.JobStore = (*Jobs)(nil)

func (s *Devices) Jobs() *Jobs { return &Jobs{db: s.db} }

type jobRecord struct {
	Job            apiv1.Job
	DevicesPresent bool
}

func encodeJob(j apiv1.Job) ([]byte, error) {
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(jobRecord{Job: j, DevicesPresent: j.Target.Devices != nil})
	return b.Bytes(), err
}
func decodeJob(b []byte) apiv1.Job {
	var r jobRecord
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&r); err != nil {
		panic(fmt.Errorf("job decode: %w", err))
	}
	if r.DevicesPresent && r.Job.Target.Devices == nil {
		r.Job.Target.Devices = []string{}
	}
	return r.Job
}
func failJob(err error) {
	if err != nil {
		panic(fmt.Errorf("jobs: %w", err))
	}
}
func (s *Jobs) Create(tenant string, j apiv1.Job) error {
	if err := apiv1.ValidateJob(j); err != nil {
		return err
	}
	j.Status = "scheduled"
	j.Counts = apiv1.JobCounts{}
	b, err := encodeJob(j)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO jobs(tenant,id,payload) VALUES(?,?,?)`, tenant, j.ID, b)
	return err
}
func (s *Jobs) Get(tenant, id string) (apiv1.Job, bool) {
	var b []byte
	err := s.db.QueryRow(`SELECT payload FROM jobs WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return apiv1.Job{}, false
	}
	failJob(err)
	j := decodeJob(b)
	j.Counts = apiv1.JobCounts{}
	for _, t := range s.ListTasks(tenant, id, "") {
		j.Counts.Total++
		switch t.Status {
		case "pending":
			j.Counts.Pending++
		case "running":
			j.Counts.Running++
		case "succeeded":
			j.Counts.Succeeded++
		case "failed":
			j.Counts.Failed++
		case "expired":
			j.Counts.Expired++
		case "cancelled":
			j.Counts.Cancelled++
		}
	}
	return j, true
}
func (s *Jobs) List(tenant string) []apiv1.Job {
	rows, err := s.db.Query(`SELECT id FROM jobs WHERE tenant=? ORDER BY id`, tenant)
	failJob(err)
	var ids []string
	for rows.Next() {
		var id string
		failJob(rows.Scan(&id))
		ids = append(ids, id)
	}
	failJob(rows.Err())
	failJob(rows.Close())
	out := []apiv1.Job{}
	for _, id := range ids {
		j, ok := s.Get(tenant, id)
		if ok {
			out = append(out, j)
		}
	}
	return out
}
func transition(from, to string) bool {
	if to == "cancelled" {
		return from == "scheduled" || from == "running" || from == "paused"
	}
	switch from {
	case "scheduled":
		return to == "running"
	case "running":
		return to == "paused" || to == "succeeded" || to == "completed_with_failures"
	case "paused":
		return to == "running"
	}
	return false
}
func (s *Jobs) UpdateStatus(tenant, id, status string) bool {
	tx, err := s.db.Begin()
	failJob(err)
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRow(`SELECT payload FROM jobs WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return false
	}
	failJob(err)
	j := decodeJob(b)
	if !transition(j.Status, status) {
		return false
	}
	now := time.Now().UTC()
	if status == "running" && j.StartedAt == nil {
		j.StartedAt = &now
	}
	if status == "cancelled" || status == "succeeded" || status == "completed_with_failures" {
		j.CompletedAt = &now
	}
	j.Status = status
	b, err = encodeJob(j)
	failJob(err)
	_, err = tx.Exec(`UPDATE jobs SET payload=? WHERE tenant=? AND id=?`, b, tenant, id)
	failJob(err)
	failJob(tx.Commit())
	return true
}
func (s *Jobs) PutTask(tenant, id string, t apiv1.JobTask) bool {
	if t.Device == "" {
		return false
	}
	switch t.Status {
	case "pending", "running", "succeeded", "failed", "expired", "cancelled":
	default:
		return false
	}
	var b bytes.Buffer
	failJob(gob.NewEncoder(&b).Encode(t))
	result, err := s.db.Exec(`INSERT INTO job_tasks(tenant,job_id,device,payload) SELECT ?,?,?,? WHERE EXISTS (SELECT 1 FROM jobs WHERE tenant=? AND id=?) ON CONFLICT(tenant,job_id,device) DO UPDATE SET payload=excluded.payload`, tenant, id, t.Device, b.Bytes(), tenant, id)
	failJob(err)
	n, err := result.RowsAffected()
	failJob(err)
	return n > 0
}
func (s *Jobs) ListTasks(tenant, id, status string) []apiv1.JobTask {
	rows, err := s.db.Query(`SELECT payload FROM job_tasks WHERE tenant=? AND job_id=? ORDER BY device`, tenant, id)
	failJob(err)
	defer rows.Close()
	out := []apiv1.JobTask{}
	for rows.Next() {
		var b []byte
		failJob(rows.Scan(&b))
		var t apiv1.JobTask
		failJob(gob.NewDecoder(bytes.NewReader(b)).Decode(&t))
		if status == "" || t.Status == status {
			out = append(out, t)
		}
	}
	failJob(rows.Err())
	return out
}
