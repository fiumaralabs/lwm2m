package apiv1

import (
	"errors"
	"time"
)

// JobCreateResult is the immutable first response for a create request.
type JobCreateResult struct {
	Job     *Job     `json:"job,omitempty"`
	Problem *Problem `json:"problem,omitempty"`
}

// JobReplayStore atomically records and replays the first create result.
type JobReplayStore interface {
	ReplayCreate(tenant, key string) (JobCreateResult, bool)
	CreateResult(tenant, key string, r JobCreateResult) (JobCreateResult, error)
}

type jobReplay struct {
	result  JobCreateResult
	expires time.Time
}

func cloneJobResult(r JobCreateResult) JobCreateResult {
	if r.Job != nil {
		j := cloneJob(*r.Job)
		r.Job = &j
	}
	if r.Problem != nil {
		p := *r.Problem
		p.Errors = append([]FieldError(nil), p.Errors...)
		r.Problem = &p
	}
	return r
}
func (s *memoryJobStore) ReplayJob(tenant, key string) (JobCreateResult, bool, error) {
	if key == "" {
		return JobCreateResult{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.replays[tenant][key]
	if !ok || !time.Now().Before(r.expires) {
		return JobCreateResult{}, false, nil
	}
	return cloneJobResult(r.result), true, nil
}
func (s *memoryJobStore) CreateJobResult(tenant, key string, result JobCreateResult) (JobCreateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if key != "" {
		if r, ok := s.replays[tenant][key]; ok && now.Before(r.expires) {
			return cloneJobResult(r.result), nil
		}
	}
	if result.Job != nil {
		j := cloneJob(*result.Job)
		if err := ValidateJob(j); err != nil {
			return JobCreateResult{}, err
		}
		if s.jobs[tenant] == nil {
			s.jobs[tenant] = map[string]Job{}
		}
		if _, ok := s.jobs[tenant][j.ID]; ok {
			return JobCreateResult{}, errors.New("job already exists")
		}
		j.Status = "scheduled"
		j.Counts = JobCounts{}
		s.jobs[tenant][j.ID] = cloneJob(j)
		result.Job = &j
	}
	if key != "" {
		if s.replays[tenant] == nil {
			s.replays[tenant] = map[string]jobReplay{}
		}
		s.replays[tenant][key] = jobReplay{cloneJobResult(result), now.Add(24 * time.Hour)}
	}
	return cloneJobResult(result), nil
}
