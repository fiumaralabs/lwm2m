package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/id"
	"github.com/fiumaralabs/lwm2m/selector"
)

// NewJobsHandler serves the fleet jobs API without installing routes. Authentication is provided by the caller.
func NewJobsHandler(store JobStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := "devices:read"
		if r.Method == http.MethodPost {
			scope = "jobs:write"
		}
		RequireScope(scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				serveJobReads(store, w, r)
				return
			}
			if r.URL.Path != "/v1/jobs" || r.Method != http.MethodPost {
				WriteProblem(w, 404, "not_found", "Unknown job")
				return
			}
			tenant, key := TenantFrom(r.Context()), r.Header.Get("Idempotency-Key")
			result, found, err := store.ReplayJob(tenant, key)
			if err != nil {
				WriteProblem(w, 500, "internal_error", "Unable to create job")
				return
			}
			if !found {
				var in struct {
					Name       string     `json:"name"`
					Target     JobTarget  `json:"target"`
					Action     JobAction  `json:"action"`
					Rollout    JobRollout `json:"rollout"`
					TTLSeconds int        `json:"ttlSeconds"`
				}
				capture := &profileProblemCapture{header: make(http.Header)}
				if !DecodeJSON(capture, r, &in) {
					var p Problem
					if json.Unmarshal(capture.body.Bytes(), &p) != nil {
						WriteProblem(w, 500, "internal_error", "Unable to create job")
						return
					}
					result.Problem = &p
				} else {
					j := Job{ID: id.New("job"), Name: in.Name, Target: in.Target, Action: in.Action, Rollout: in.Rollout, TTLSeconds: in.TTLSeconds, Status: "scheduled", CreatedAt: time.Now().UTC()}
					if j.Rollout.StartAt == nil {
						j.Rollout.StartAt = &j.CreatedAt
					}
					if ValidateJob(j) != nil || !validJobRequest(j) {
						result.Problem = &Problem{Status: 400, Code: "invalid_job", Detail: "Invalid job configuration"}
					} else {
						result.Job = &j
					}
				}
				if !authorizeJobResult(w, r, result) {
					return
				}
				result, err = store.CreateJobResult(tenant, key, result)
				if err != nil {
					WriteProblem(w, 500, "internal_error", "Unable to create job")
					return
				}
			}
			if !authorizeJobResult(w, r, result) {
				return
			}
			if result.Problem != nil {
				WriteProblem(w, result.Problem.Status, result.Problem.Code, result.Problem.Detail)
				return
			}
			if result.Job == nil {
				WriteProblem(w, 500, "internal_error", "Unable to create job")
				return
			}
			w.Header().Set("Location", "/v1/jobs/"+result.Job.ID)
			deviceJSON(w, 201, result.Job)
		})).ServeHTTP(w, r)
	})
}

func authorizeJobResult(w http.ResponseWriter, r *http.Request, result JobCreateResult) bool {
	if result.Job == nil {
		return true
	}
	var operation struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(result.Job.Action.Operation, &operation)
	if operation.Type != "raw" {
		return true
	}
	authorized := false
	RequireScope("operations:raw")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { authorized = true })).ServeHTTP(w, r)
	return authorized
}

func validJobRequest(j Job) bool {
	if j.Target.Devices != nil {
		if len(j.Target.Devices) == 0 {
			return false
		}
		for _, d := range j.Target.Devices {
			if strings.TrimSpace(d) == "" {
				return false
			}
		}
	}
	if j.Target.Selector != "" {
		if strings.TrimSpace(j.Target.Selector) == "" {
			return false
		}
		if _, err := selector.Parse(j.Target.Selector); err != nil {
			return false
		}
	}
	for _, raw := range []json.RawMessage{j.Action.Operation, j.Action.Observation, j.Action.FirmwareUpdate} {
		if len(raw) > 0 && string(raw) != "null" {
			var obj map[string]json.RawMessage
			if json.Unmarshal(raw, &obj) != nil || obj == nil {
				return false
			}
		}
	}
	if len(j.Action.Operation) > 0 && string(j.Action.Operation) != "null" && !validProfileOperation(j.Action.Operation) {
		return false
	}
	if len(j.Action.Observation) > 0 && string(j.Action.Observation) != "null" {
		var o observationInput
		if !profileDecode(j.Action.Observation, &o) || validateObservation(o.Paths, o.Attributes) != nil {
			return false
		}
	}
	if len(j.Action.FirmwareUpdate) > 0 && string(j.Action.FirmwareUpdate) != "null" {
		var f struct {
			Firmware           string `json:"firmware"`
			URI                string `json:"uri"`
			Method             string `json:"method"`
			Severity           string `json:"severity"`
			MaxDeferSeconds    *int   `json:"maxDeferSeconds"`
			AutoUpgrade        bool   `json:"autoUpgrade"`
			Resume             bool   `json:"resume"`
			PushTimeoutSeconds int    `json:"pushTimeoutSeconds"`
			PushAttempts       int    `json:"pushAttempts"`
			PollSeconds        int    `json:"pollSeconds"`
		}
		if !profileDecode(j.Action.FirmwareUpdate, &f) {
			return false
		}
		u := FirmwareUpdate{Firmware: f.Firmware, URI: f.URI, Method: f.Method, Severity: f.Severity, MaxDeferSeconds: f.MaxDeferSeconds, PushTimeoutSeconds: f.PushTimeoutSeconds, PushAttempts: f.PushAttempts, PollSeconds: f.PollSeconds}
		if ValidateFirmwareUpdate(&u) != nil {
			return false
		}
	}
	return true
}

func serveJobReads(store JobStore, w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	if r.URL.Path != "/v1/jobs" {
		path := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
		parts := strings.Split(path, "/")
		if path == r.URL.Path || len(parts) > 2 || parts[0] == "" || len(parts) == 2 && parts[1] != "tasks" {
			WriteProblem(w, 404, "not_found", "Unknown job")
			return
		}
		j, ok := store.Get(tenant, parts[0])
		if !ok {
			WriteProblem(w, 404, "not_found", "Unknown job")
			return
		}
		if len(parts) == 1 {
			deviceJSON(w, 200, j)
			return
		}
		serveJobTasks(store, w, r, tenant, parts[0])
		return
	}
	q := r.URL.Query()
	for _, key := range []string{"limit", "cursor"} {
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
	after := ""
	if q.Get("cursor") != "" {
		b, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		var c profileCursor
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.After == "" {
			WriteProblem(w, 400, "invalid_cursor", "Invalid cursor")
			return
		}
		after = c.After
	}
	all := store.List(tenant)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	items := make([]Job, 0, limit)
	next := ""
	for _, j := range all {
		if j.ID <= after {
			continue
		}
		if len(items) == limit {
			b, _ := json.Marshal(profileCursor{Tenant: tenant, After: items[len(items)-1].ID})
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		items = append(items, j)
	}
	deviceJSON(w, 200, struct {
		Items []Job  `json:"items"`
		Next  string `json:"nextCursor,omitempty"`
	}{items, next})
}
