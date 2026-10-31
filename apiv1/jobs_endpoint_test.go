package apiv1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validJobBody = `{"name":"release","target":{"devices":["a"]},"action":{"operation":{"type":"read","paths":["/3/0/1"]}},"rollout":{"maxConcurrent":2,"failureThresholdPercent":5},"ttlSeconds":60}`

func jobRequest(h http.Handler, method, path, body, tenant, scopes string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: "," + scopes + ","}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestJobsCreate(t *testing.T) {
	store := NewMemoryJobStore()
	h := NewJobsHandler(store)
	w := jobRequest(h, "POST", "/v1/jobs", validJobBody, "a", "jobs:write")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var j Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(j.ID, "job_") || j.Status != "scheduled" || j.Counts != (JobCounts{}) || j.CreatedAt.IsZero() || j.Rollout.StartAt == nil {
		t.Fatalf("job: %+v", j)
	}
	if got, ok := store.Get("a", j.ID); !ok || got.Name != "release" {
		t.Fatalf("stored: %+v %v", got, ok)
	}
}

func TestJobsValidation(t *testing.T) {
	h := NewJobsHandler(NewMemoryJobStore())
	cases := map[string]string{
		"target absent":       strings.Replace(validJobBody, `"devices":["a"]`, ``, 1),
		"target both":         strings.Replace(validJobBody, `"devices":["a"]`, `"selector":"env=prod","devices":["a"]`, 1),
		"empty devices":       strings.Replace(validJobBody, `["a"]`, `[]`, 1),
		"empty device":        strings.Replace(validJobBody, `["a"]`, `[""]`, 1),
		"action absent":       strings.Replace(validJobBody, `"operation":{"type":"read","paths":["/3/0/1"]}`, ``, 1),
		"action both":         strings.Replace(validJobBody, `"operation":`, `"observation":{},"operation":`, 1),
		"invalid operation":   strings.Replace(validJobBody, `"paths":["/3/0/1"]`, `"paths":[]`, 1),
		"invalid observation": strings.Replace(validJobBody, `"operation":{"type":"read","paths":["/3/0/1"]}`, `"observation":{}`, 1),
		"invalid firmware":    strings.Replace(validJobBody, `"operation":{"type":"read","paths":["/3/0/1"]}`, `"firmwareUpdate":{"method":"wrong"}`, 1),
		"action scalar":       strings.Replace(validJobBody, `{"type":"read","paths":["/3/0/1"]}`, `5`, 1),
		"concurrency":         strings.Replace(validJobBody, `"maxConcurrent":2`, `"maxConcurrent":0`, 1),
		"threshold negative":  strings.Replace(validJobBody, `"failureThresholdPercent":5`, `"failureThresholdPercent":-1`, 1),
		"threshold high":      strings.Replace(validJobBody, `"failureThresholdPercent":5`, `"failureThresholdPercent":101`, 1),
		"ttl":                 strings.Replace(validJobBody, `"ttlSeconds":60`, `"ttlSeconds":0`, 1),
		"unknown":             strings.Replace(validJobBody, `"name":`, `"extra":1,"name":`, 1),
		"malformed":           `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := jobRequest(h, "POST", "/v1/jobs", body, "a", "jobs:write")
			if w.Code != 400 || !strings.Contains(w.Header().Get("Content-Type"), "application/problem+json") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestJobsReadAndScopes(t *testing.T) {
	store := NewMemoryJobStore()
	h := NewJobsHandler(store)
	for _, id := range []string{"job_a", "job_b", "job_c"} {
		j := jobFixture(id)
		if err := store.Create("a", j); err != nil {
			t.Fatal(err)
		}
	}
	store.PutTask("a", "job_a", JobTask{Device: "one", Status: "pending"})
	store.PutTask("a", "job_a", JobTask{Device: "two", Status: "failed"})
	for _, path := range []string{"/v1/jobs/job_a", "/v1/jobs/job_a/tasks"} {
		if w := jobRequest(h, "GET", path, "", "b", "devices:read"); w.Code != 404 {
			t.Fatalf("isolation %s: %d", path, w.Code)
		}
	}
	if w := jobRequest(h, "GET", "/v1/jobs/job_a", "", "a", "devices:read"); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"job_a"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	if w := jobRequest(h, "GET", "/v1/jobs/job_a/tasks?status=failed", "", "a", "devices:read"); w.Code != 200 || !strings.Contains(w.Body.String(), `"device":"two"`) || strings.Contains(w.Body.String(), `"device":"one"`) {
		t.Fatalf("tasks: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path, scope string }{{"GET", "/v1/jobs", "jobs:write"}, {"POST", "/v1/jobs", "devices:read"}} {
		if w := jobRequest(h, tc.method, tc.path, validJobBody, "a", tc.scope); w.Code != 403 {
			t.Fatalf("scope %s: %d", tc.method, w.Code)
		}
	}
}

func TestJobsPagination(t *testing.T) {
	store := NewMemoryJobStore()
	for _, id := range []string{"job_a", "job_b", "job_c"} {
		if err := store.Create("a", jobFixture(id)); err != nil {
			t.Fatal(err)
		}
	}
	h := NewJobsHandler(store)
	w := jobRequest(h, "GET", "/v1/jobs?limit=2", "", "a", "devices:read")
	var page struct {
		Items []Job  `json:"items"`
		Next  string `json:"nextCursor"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 2 || page.Next == "" {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	cursor := page.Next
	w = jobRequest(h, "GET", "/v1/jobs?limit=2&cursor="+cursor, "", "a", "devices:read")
	page.Items = nil
	page.Next = ""
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != "job_c" || page.Next != "" {
		t.Fatalf("next: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/jobs?limit=0", "/v1/jobs?cursor=bad", "/v1/jobs?cursor=" + cursor} {
		tenant := "a"
		if strings.Contains(path, cursor) {
			tenant = "b"
		}
		if w := jobRequest(h, "GET", path, "", tenant, "devices:read"); w.Code != 400 {
			t.Fatalf("bad page %s: %d", path, w.Code)
		}
	}
}

func jobFixture(id string) Job { j := validJob(); j.ID = id; return j }

func TestJobsCreateActionVariants(t *testing.T) {
	h := NewJobsHandler(NewMemoryJobStore())
	for _, action := range []string{`"observation":{"paths":["/3/0/1"]}`, `"firmwareUpdate":{"firmware":"fw_1","method":"auto"}`} {
		body := strings.Replace(validJobBody, `"operation":{"type":"read","paths":["/3/0/1"]}`, action, 1)
		body = strings.Replace(body, `"devices":["a"]`, `"selector":"env=prod"`, 1)
		if w := jobRequest(h, "POST", "/v1/jobs", body, "a", "jobs:write"); w.Code != 201 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
}
