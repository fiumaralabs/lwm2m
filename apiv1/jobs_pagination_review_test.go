package apiv1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestJobTaskPaginationReview(t *testing.T) {
	s := NewMemoryJobStore()
	for _, id := range []string{"job_a", "job_b"} {
		if err := s.Create("a", jobFixture(id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"one", "two", "three"} {
		s.PutTask("a", "job_a", JobTask{Device: d, Status: "failed"})
	}
	h := NewJobsHandler(s)
	w := jobRequest(h, "GET", "/v1/jobs/job_a/tasks?status=failed&limit=1", "", "a", "devices:read")
	var p struct {
		Items []JobTask `json:"items"`
		Next  string    `json:"nextCursor"`
	}
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != 200 || len(p.Items) != 1 || p.Next == "" {
		t.Fatalf("first page: %d %s", w.Code, w.Body.String())
	}
	cursor := p.Next
	w = jobRequest(h, "GET", "/v1/jobs/job_a/tasks?status=failed&limit=1&cursor="+cursor, "", "a", "devices:read")
	p.Items = nil
	p.Next = ""
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != 200 || len(p.Items) != 1 || p.Items[0].Device != "three" {
		t.Fatalf("second page: %d %s", w.Code, w.Body.String())
	}
	w = jobRequest(h, "GET", "/v1/jobs/job_a/tasks?status=failed&limit=1&cursor="+p.Next, "", "a", "devices:read")
	p.Items = nil
	p.Next = ""
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != 200 || len(p.Items) != 1 || p.Items[0].Device != "two" || p.Next != "" {
		t.Fatalf("last page: %d %s", w.Code, w.Body.String())
	}
	if err := s.Create("b", jobFixture("job_a")); err != nil {
		t.Fatal(err)
	}
	w = jobRequest(h, "GET", "/v1/jobs/job_a/tasks?status=failed&cursor="+cursor, "", "b", "devices:read")
	if w.Code != 400 {
		t.Fatalf("tenant cursor: %d", w.Code)
	}
	for _, path := range []string{"/v1/jobs/job_a/tasks?limit=0", "/v1/jobs/job_a/tasks?cursor=bad", "/v1/jobs/job_b/tasks?status=failed&cursor=" + cursor, "/v1/jobs/job_a/tasks?status=pending&cursor=" + cursor} {
		w = jobRequest(h, "GET", path, "", "a", "devices:read")
		if w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestJobsConcurrentIdempotencyReview(t *testing.T) {
	s := NewMemoryJobStore()
	h := NewJobsHandler(s)
	const n = 20
	responses := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(validJobBody))
			r.Header.Set("Idempotency-Key", "key")
			r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: "a", scopes: ",jobs:write,"}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			responses[i] = w
		}(i)
	}
	wg.Wait()
	for _, w := range responses {
		if w.Code != 201 || w.Body.String() != responses[0].Body.String() || w.Header().Get("Location") != responses[0].Header().Get("Location") {
			t.Fatalf("different response %d %s", w.Code, w.Body.String())
		}
	}
	if got := len(s.List("a")); got != 1 {
		t.Fatalf("jobs=%d", got)
	}
}

func TestJobReplayFirstResponseAndTenantReview(t *testing.T) {
	s := NewMemoryJobStore()
	h := NewJobsHandler(s)
	request := func(tenant, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", key)
		r = r.WithContext(context.WithValue(r.Context(), credentialKey{}, credential{tenant: tenant, scopes: ",jobs:write,"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := request("a", "key", validJobBody)
	var j Job
	if err := json.Unmarshal(first.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	s.UpdateStatus("a", j.ID, "running")
	again := request("a", "key", "invalid json")
	if first.Code != 201 || again.Code != 201 || first.Body.String() != again.Body.String() || first.Header().Get("Location") != again.Header().Get("Location") {
		t.Fatalf("replay %d %s", again.Code, again.Body.String())
	}
	other := request("b", "key", validJobBody)
	if other.Code != 201 || other.Header().Get("Location") == first.Header().Get("Location") {
		t.Fatalf("tenant replay %d %s", other.Code, other.Body.String())
	}
	bad := request("a", "bad", "{")
	retry := request("a", "bad", validJobBody)
	if bad.Code != 400 || retry.Code != bad.Code || retry.Body.String() != bad.Body.String() {
		t.Fatalf("error replay %d %s", retry.Code, retry.Body.String())
	}
}
