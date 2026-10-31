package apiv1

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

type JobTarget struct {
	Selector string   `json:"selector,omitempty"`
	Devices  []string `json:"devices,omitempty"`
}
type JobAction struct {
	Operation      json.RawMessage `json:"operation,omitempty"`
	Observation    json.RawMessage `json:"observation,omitempty"`
	FirmwareUpdate json.RawMessage `json:"firmwareUpdate,omitempty"`
}
type JobRollout struct {
	MaxConcurrent           int        `json:"maxConcurrent"`
	FailureThresholdPercent int        `json:"failureThresholdPercent"`
	StartAt                 *time.Time `json:"startAt,omitempty"`
}
type JobCounts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Expired   int `json:"expired"`
	Cancelled int `json:"cancelled"`
}
type Job struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Target      JobTarget  `json:"target"`
	Action      JobAction  `json:"action"`
	Rollout     JobRollout `json:"rollout"`
	TTLSeconds  int        `json:"ttlSeconds"`
	Status      string     `json:"status"`
	Counts      JobCounts  `json:"counts"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
}
type JobTask struct {
	Device         string `json:"device"`
	Status         string `json:"status"`
	Operation      string `json:"operation,omitempty"`
	Observation    string `json:"observation,omitempty"`
	FirmwareUpdate string `json:"firmwareUpdate,omitempty"`
}

// JobStore keeps job and task operations tenant-scoped. IDs are supplied by the caller.
type JobStore interface {
	Create(string, Job) error
	ReplayJob(string, string) (JobCreateResult, bool, error)
	CreateJobResult(string, string, JobCreateResult) (JobCreateResult, error)
	Get(string, string) (Job, bool)
	List(string) []Job
	UpdateStatus(string, string, string) bool
	PutTask(string, string, JobTask) bool
	ListTasks(string, string, string) []JobTask
	JobTenants() ([]string, error)
	StartJob(tenant, id string, devices []string, now time.Time) (bool, error)
}
type memoryJobStore struct {
	mu          sync.RWMutex
	jobs        map[string]map[string]Job
	tasks       map[string]map[string]map[string]JobTask
	replays     map[string]map[string]jobReplay
	replayClock func() time.Time
}

func NewMemoryJobStore() JobStore {
	return NewMemoryJobStoreWithReplayClock(time.Now)
}

// NewMemoryJobStoreWithReplayClock uses clock for replay retention decisions.
// The clock must be safe for concurrent use and must not be nil.
func NewMemoryJobStoreWithReplayClock(clock func() time.Time) JobStore {
	return &memoryJobStore{jobs: make(map[string]map[string]Job), tasks: make(map[string]map[string]map[string]JobTask), replays: make(map[string]map[string]jobReplay), replayClock: clock}
}

// ReplayCreate and CreateResult expose the separate replay contract while the
// existing JobStore methods remain available to current endpoint consumers.
func (s *memoryJobStore) ReplayCreate(tenant, key string) (JobCreateResult, bool) {
	r, ok, _ := s.ReplayJob(tenant, key) // the in-memory lookup cannot fail
	return r, ok
}

func (s *memoryJobStore) CreateResult(tenant, key string, r JobCreateResult) (JobCreateResult, error) {
	return s.CreateJobResult(tenant, key, r)
}

// ValidateJob checks the create-time rollout and exclusive target and action fields.
func ValidateJob(j Job) error {
	targets := 0
	if j.Target.Selector != "" {
		targets++
	}
	if j.Target.Devices != nil {
		targets++
	}
	actions := 0
	for _, v := range []json.RawMessage{j.Action.Operation, j.Action.Observation, j.Action.FirmwareUpdate} {
		if len(v) > 0 && string(v) != "null" {
			actions++
		}
	}
	if targets != 1 || actions != 1 || j.Rollout.MaxConcurrent < 1 || j.Rollout.FailureThresholdPercent < 0 || j.Rollout.FailureThresholdPercent > 100 || j.TTLSeconds <= 0 {
		return errors.New("invalid job configuration")
	}
	return nil
}

// ShouldPauseJob applies the threshold only after ten finished tasks.
func ShouldPauseJob(failed, finished, threshold int) bool {
	return finished >= 10 && float64(failed)*100 >= float64(threshold)*float64(finished)
}
func cloneJob(j Job) Job {
	if j.Target.Devices != nil {
		j.Target.Devices = append([]string{}, j.Target.Devices...)
	}
	j.Action.Operation = append(json.RawMessage(nil), j.Action.Operation...)
	j.Action.Observation = append(json.RawMessage(nil), j.Action.Observation...)
	j.Action.FirmwareUpdate = append(json.RawMessage(nil), j.Action.FirmwareUpdate...)
	if j.Rollout.StartAt != nil {
		v := *j.Rollout.StartAt
		j.Rollout.StartAt = &v
	}
	if j.StartedAt != nil {
		v := *j.StartedAt
		j.StartedAt = &v
	}
	if j.CompletedAt != nil {
		v := *j.CompletedAt
		j.CompletedAt = &v
	}
	return j
}
func (s *memoryJobStore) Create(tenant string, j Job) error {
	if err := ValidateJob(j); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[tenant] == nil {
		s.jobs[tenant] = map[string]Job{}
	}
	if _, ok := s.jobs[tenant][j.ID]; ok {
		return errors.New("job already exists")
	}
	j.Status = "scheduled"
	j.Counts = JobCounts{}
	s.jobs[tenant][j.ID] = cloneJob(j)
	return nil
}
func (s *memoryJobStore) job(tenant, id string) (Job, bool) {
	j, ok := s.jobs[tenant][id]
	if !ok {
		return Job{}, false
	}
	j.Counts = JobCounts{}
	for _, task := range s.tasks[tenant][id] {
		j.Counts.Total++
		switch task.Status {
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
	return cloneJob(j), true
}
func (s *memoryJobStore) Get(tenant, id string) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.job(tenant, id)
}
func (s *memoryJobStore) List(tenant string) []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Job, 0, len(s.jobs[tenant]))
	for id := range s.jobs[tenant] {
		j, _ := s.job(tenant, id)
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func validJobTransition(from, to string) bool {
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
func (s *memoryJobStore) UpdateStatus(tenant, id, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[tenant][id]
	if !ok || !validJobTransition(j.Status, status) {
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
	s.jobs[tenant][id] = j
	return true
}
func (s *memoryJobStore) PutTask(tenant, id string, t JobTask) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[tenant][id]; !ok {
		return false
	}
	switch t.Status {
	case "pending", "running", "succeeded", "failed", "expired", "cancelled":
	default:
		return false
	}
	if t.Device == "" {
		return false
	}
	if s.tasks[tenant] == nil {
		s.tasks[tenant] = map[string]map[string]JobTask{}
	}
	if s.tasks[tenant][id] == nil {
		s.tasks[tenant][id] = map[string]JobTask{}
	}
	s.tasks[tenant][id][t.Device] = t
	return true
}
func (s *memoryJobStore) ListTasks(tenant, id, status string) []JobTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []JobTask{}
	for _, t := range s.tasks[tenant][id] {
		if status == "" || status == t.Status {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}
