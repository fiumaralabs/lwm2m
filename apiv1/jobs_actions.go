package apiv1

import (
	"net/http"
	"strings"
)

func serveJobAction(store JobStore, w http.ResponseWriter, r *http.Request) {
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
	if _, ok := store.Get(tenant, parts[0]); !ok {
		WriteProblem(w, 404, "not_found", "Unknown job")
		return
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
