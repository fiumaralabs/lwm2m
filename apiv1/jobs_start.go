package apiv1

import (
	"context"
	"errors"
	"github.com/fiumaralabs/lwm2m/store"
	"sort"
	"time"
)

func (s *memoryJobStore) JobTenants() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tenants := make([]string, 0, len(s.jobs))
	for tenant := range s.jobs {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	return tenants, nil
}

func (s *memoryJobStore) StartJob(tenant, id string, devices []string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startJobLocked(tenant, id, devices, now)
}

func (s *memoryJobStore) startJobLocked(tenant, id string, devices []string, now time.Time) (bool, error) {
	j, ok := s.jobs[tenant][id]
	if !ok || j.Status != "scheduled" || (j.Rollout.StartAt != nil && j.Rollout.StartAt.After(now)) {
		return false, nil
	}
	tasks := make(map[string]JobTask, len(devices))
	if len(s.tasks[tenant][id]) != 0 {
		return false, errors.New("job already has tasks")
	}
	for _, d := range devices {
		if d == "" {
			return false, errors.New("empty endpoint")
		}
		if _, ok := tasks[d]; ok {
			return false, errors.New("duplicate endpoint")
		}
		tasks[d] = JobTask{Device: d, Status: "pending"}
	}
	if s.tasks[tenant] == nil {
		s.tasks[tenant] = make(map[string]map[string]JobTask)
	}
	s.tasks[tenant][id] = tasks
	j.Status = "running"
	j.StartedAt = &now
	s.jobs[tenant][id] = j
	return true, nil
}

// StartJobSnapshot holds the device view stable through the job transition.
func (s *memoryJobStore) StartJobSnapshot(ctx context.Context, devices store.Devices, tenant, id string, now time.Time) (bool, error) {
	snap, ok := devices.(interface {
		WithDeviceSnapshot(string, func([]store.Device) error) error
	})
	if !ok {
		return false, errors.New("device store does not support atomic snapshots")
	}
	started := false
	err := snap.WithDeviceSnapshot(tenant, func(view []store.Device) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		j, ok := s.jobs[tenant][id]
		if !ok || j.Status != "scheduled" || (j.Rollout.StartAt != nil && j.Rollout.StartAt.After(now)) {
			return nil
		}
		endpoints, err := MatchJobTarget(ctx, view, j.Target)
		if err != nil {
			return err
		}
		started, err = s.startJobLocked(tenant, id, endpoints, now)
		return err
	})
	return started, err
}
