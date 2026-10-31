package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
)

type jobTaskCursor struct {
	Tenant *string `json:"tenant"`
	Job    *string `json:"job"`
	Status *string `json:"status"`
	After  string  `json:"after"`
}

func serveJobTasks(store JobStore, w http.ResponseWriter, r *http.Request, tenant, job string) {
	q := r.URL.Query()
	for _, key := range []string{"limit", "cursor", "status"} {
		if v, ok := q[key]; ok && (len(v) != 1 || v[0] == "") {
			WriteProblem(w, 400, "invalid_"+key, "Expected one nonempty value")
			return
		}
	}
	limit := 50
	if q.Get("limit") != "" {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 500 {
			WriteProblem(w, 400, "invalid_limit", "Expected limit from 1 to 500")
			return
		}
		limit = n
	}
	status := q.Get("status")
	after := ""
	if q.Get("cursor") != "" {
		b, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		var c jobTaskCursor
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant == nil || *c.Tenant != tenant || c.Job == nil || *c.Job != job || c.Status == nil || *c.Status != status || c.After == "" {
			WriteProblem(w, 400, "invalid_cursor", "Invalid cursor")
			return
		}
		after = c.After
	}
	tasks := store.ListTasks(tenant, job, status)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Device < tasks[j].Device })
	items := make([]JobTask, 0, limit)
	next := ""
	for _, task := range tasks {
		if task.Device <= after {
			continue
		}
		if len(items) == limit {
			b, _ := json.Marshal(jobTaskCursor{Tenant: &tenant, Job: &job, Status: &status, After: items[len(items)-1].Device})
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		items = append(items, task)
	}
	deviceJSON(w, 200, struct {
		Items []JobTask `json:"items"`
		Next  string    `json:"nextCursor,omitempty"`
	}{items, next})
}
