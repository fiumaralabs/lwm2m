package apiv1

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/fiumaralabs/lwm2m/operations"
)

type jobCanceller interface {
	Cancel(context.Context, string, string) (operations.Operation, error)
}

func serveJobAction(store JobStore, executor jobCanceller, runner *JobRunner, w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		WriteProblem(w, 404, "not_found", "Unknown job")
		return
	}
	statuses := map[string]string{"pause": "paused", "resume": "running", "cancel": "cancelled"}
	status, ok := statuses[parts[1]]
	if !ok {
		WriteProblem(w, 404, "not_found", "Unknown job")
		return
	}
	tenant := TenantFrom(r.Context())
	if runner != nil && runner.cancelMu != nil {
		runner.cancelMu.Lock()
		defer runner.cancelMu.Unlock()
	}
	existing, ok := store.Get(tenant, parts[0])
	if !ok {
		WriteProblem(w, 404, "not_found", "Unknown job")
		return
	}
	if status == "cancelled" && validJobTransition(existing.Status, status) {
		for _, task := range store.ListTasks(tenant, parts[0], "running") {
			if task.Operation == "" || executor == nil {
				WriteProblem(w, 500, "internal_error", "Cannot cancel job operation")
				return
			}
			op, err := executor.Cancel(r.Context(), tenant, task.Operation)
			if errors.Is(err, operations.ErrConflict) && op.Status != operations.Cancelled {
				continue
			}
			if err != nil && !errors.Is(err, operations.ErrConflict) {
				WriteProblem(w, 500, "internal_error", "Cannot cancel job operation")
				return
			}
			task.Status = "cancelled"
			if !store.PutTask(tenant, parts[0], task) {
				WriteProblem(w, 500, "internal_error", "Cannot update job task")
				return
			}
		}
	}
	if !store.UpdateStatus(tenant, parts[0], status) {
		WriteProblem(w, 409, "conflict", "Illegal job transition")
		return
	}
	job, ok := store.Get(tenant, parts[0])
	if !ok {
		WriteProblem(w, 404, "not_found", "Unknown job")
		return
	}
	deviceJSON(w, 200, job)
}
