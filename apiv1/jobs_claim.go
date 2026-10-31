package apiv1

import "sort"

// ClaimTasks atomically reserves available rollout slots for a running job.
func (s *memoryJobStore) ClaimTasks(tenant, id string) ([]JobTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[tenant][id]
	if !ok || job.Status != "running" {
		return nil, nil
	}
	tasks := s.tasks[tenant][id]
	available := job.Rollout.MaxConcurrent
	var pending []string
	for device, task := range tasks {
		switch task.Status {
		case "running", "dispatching":
			available--
		case "pending":
			pending = append(pending, device)
		}
	}
	sort.Strings(pending)
	var claimed []JobTask
	for _, device := range pending {
		if available <= 0 {
			break
		}
		task := tasks[device]
		task.Status = "dispatching"
		tasks[device] = task
		claimed = append(claimed, task)
		available--
	}
	return claimed, nil
}
