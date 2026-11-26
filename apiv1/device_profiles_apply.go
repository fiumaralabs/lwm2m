package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/id"
	"github.com/fiumaralabs/lwm2m/store"
)

// applyDeviceProfile snapshots registered, tenant-owned devices into a fleet job.
func (o Options) applyDeviceProfile(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/device-profiles/"), "/apply")
	if key == "" || strings.Contains(key, "/") {
		WriteProblem(w, 404, "not_found", "Unknown profile")
		return
	}
	tenant := TenantFrom(r.Context())
	replayKey := r.Header.Get("Idempotency-Key")
	if replayKey != "" {
		replayKey = "\x00profile-apply:" + base64.RawURLEncoding.EncodeToString([]byte(key)) + ":" + replayKey
	}
	respond := func(result JobCreateResult) {
		if result.Problem != nil {
			WriteProblem(w, result.Problem.Status, result.Problem.Code, result.Problem.Detail)
			return
		}
		if result.Job == nil {
			WriteProblem(w, 500, "internal_error", "Unable to create job")
			return
		}
		if result.Job.Action.ProfileApply == nil || result.Job.Action.ProfileApply.Profile != key {
			WriteProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency key is already in use")
			return
		}
		w.Header().Set("Location", "/v1/jobs/"+result.Job.ID)
		deviceJSON(w, http.StatusAccepted, struct {
			ID string `json:"id"`
		}{result.Job.ID})
	}
	persist := func(result JobCreateResult) {
		result, err := o.Jobs.CreateJobResult(tenant, replayKey, result)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Unable to create job")
			return
		}
		respond(result)
	}
	if result, found, err := o.Jobs.ReplayJob(tenant, replayKey); err != nil {
		WriteProblem(w, 500, "internal_error", "Unable to create job")
		return
	} else if found {
		respond(result)
		return
	}
	if _, ok := o.DeviceProfiles.Get(tenant, key); !ok {
		persist(JobCreateResult{Problem: &Problem{Status: 404, Code: "not_found", Detail: "Unknown profile"}})
		return
	}
	if r.ContentLength != 0 {
		var body *struct{}
		capture := &profileProblemCapture{header: make(http.Header)}
		if !DecodeJSON(capture, r, &body) {
			var problem Problem
			if json.Unmarshal(capture.body.Bytes(), &problem) != nil {
				WriteProblem(w, 500, "internal_error", "Unable to create job")
				return
			}
			persist(JobCreateResult{Problem: &problem})
			return
		}
		if body == nil {
			persist(JobCreateResult{Problem: &Problem{Status: 400, Code: "invalid_request", Detail: "Expected an object"}})
			return
		}
	}
	endpoints := []string{}
	cursor := ""
	for {
		batch, next, err := o.Devices.List(tenant, store.DeviceFilter{}, cursor, 500)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Unable to list devices")
			return
		}
		for _, d := range batch {
			if d.Profile == key && o.Server != nil && tenant == o.ServerTenant {
				reg, ok := o.Server.Store().ByEndpoint(d.Endpoint)
				if ok && !reg.Expired(time.Now()) {
					endpoints = append(endpoints, d.Endpoint)
				}
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	now := time.Now().UTC()
	job := Job{ID: id.New("job"), Name: "Apply profile " + key, Target: JobTarget{Devices: endpoints}, Action: JobAction{ProfileApply: &ProfileApplyAction{Profile: key}}, Rollout: JobRollout{MaxConcurrent: 50, FailureThresholdPercent: 100}, TTLSeconds: 86400, Status: "scheduled", CreatedAt: now}
	persist(JobCreateResult{Job: &job})
}
