package sqlite

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func (s *Jobs) ReplayJob(tenant, key string) (apiv1.JobCreateResult, bool, error) {
	var r apiv1.JobCreateResult
	if key == "" {
		return r, false, nil
	}
	var b []byte
	err := s.db.QueryRow(`SELECT body FROM job_replays WHERE tenant=? AND key=? AND expires>?`, tenant, key, time.Now().UnixNano()).Scan(&b)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	err = json.Unmarshal(b, &r)
	return r, err == nil, err
}
func (s *Jobs) CreateJobResult(tenant, key string, r apiv1.JobCreateResult) (apiv1.JobCreateResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return apiv1.JobCreateResult{}, err
	}
	defer tx.Rollback()
	now := time.Now()
	if key != "" {
		// Acquire the SQLite write lock before reading, including across DB handles.
		if _, err = tx.Exec(`DELETE FROM job_replays WHERE tenant=? AND key=? AND expires<=?`, tenant, key, now.UnixNano()); err != nil {
			return apiv1.JobCreateResult{}, err
		}
		now = time.Now()
		if _, err = tx.Exec(`DELETE FROM job_replays WHERE tenant=? AND key=? AND expires<=?`, tenant, key, now.UnixNano()); err != nil {
			return apiv1.JobCreateResult{}, err
		}
		var b []byte
		err = tx.QueryRow(`SELECT body FROM job_replays WHERE tenant=? AND key=? AND expires>?`, tenant, key, now.UnixNano()).Scan(&b)
		if err == nil {
			err = json.Unmarshal(b, &r)
			return r, err
		}
		if err != sql.ErrNoRows {
			return apiv1.JobCreateResult{}, err
		}
	}
	if r.Job != nil {
		j := *r.Job
		if err = apiv1.ValidateJob(j); err != nil {
			return apiv1.JobCreateResult{}, err
		}
		j.Status = "scheduled"
		j.Counts = apiv1.JobCounts{}
		b, e := encodeJob(j)
		if e != nil {
			return apiv1.JobCreateResult{}, e
		}
		if _, err = tx.Exec(`INSERT INTO jobs(tenant,id,payload) VALUES(?,?,?)`, tenant, j.ID, b); err != nil {
			return apiv1.JobCreateResult{}, err
		}
		r.Job = &j
	}
	if key != "" {
		b, e := json.Marshal(r)
		if e != nil {
			return apiv1.JobCreateResult{}, e
		}
		if _, err = tx.Exec(`INSERT INTO job_replays(tenant,key,body,expires) VALUES(?,?,?,?)`, tenant, key, b, now.Add(24*time.Hour).UnixNano()); err != nil {
			return apiv1.JobCreateResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return apiv1.JobCreateResult{}, err
	}
	return r, nil
}
