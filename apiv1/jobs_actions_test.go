package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestJobsRouterCreate(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",jobs:write,devices:read,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	w := profileWriteRequest(h, "POST", "/v1/jobs", validJobBody, "lwm_test_secret", "same")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	replay := profileWriteRequest(h, "POST", "/v1/jobs", "{}", "lwm_test_secret", "same")
	if replay.Code != w.Code || replay.Body.String() != w.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
}

func TestJobsRouterConcurrentReplayAndActions(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"test":  {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",jobs:write,devices:read,", tenant: "default"},
		"other": {hash: sha256.Sum256([]byte("lwm_other_secret")), scopes: ",jobs:write,devices:read,", tenant: "other"},
		"read":  {hash: sha256.Sum256([]byte("lwm_read_secret")), scopes: ",devices:read,", tenant: "default"},
		"write": {hash: sha256.Sum256([]byte("lwm_write_secret")), scopes: ",jobs:write,", tenant: "default"},
	}}
	store := NewMemoryJobStore()
	h := New(Options{Keys: keys, Jobs: store})
	const n = 24
	results := make(chan *httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		go func() {
			results <- profileWriteRequest(h, "POST", "/v1/jobs", validJobBody, "lwm_test_secret", "concurrent")
		}()
	}
	var first string
	for i := 0; i < n; i++ {
		w := <-results
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		if i == 0 {
			first = w.Body.String()
		}
		if first != w.Body.String() {
			t.Fatal("different replay bodies")
		}
	}
	if len(store.List("default")) != 1 {
		t.Fatalf("created %d jobs", len(store.List("default")))
	}
	var job Job
	if err := json.Unmarshal([]byte(first), &job); err != nil {
		t.Fatal(err)
	}
	other := profileWriteRequest(h, "POST", "/v1/jobs", validJobBody, "lwm_other_secret", "concurrent")
	if other.Code != 201 || other.Body.String() == first || len(store.List("other")) != 1 {
		t.Fatalf("tenant: %d %s", other.Code, other.Body.String())
	}
	path := "/v1/jobs/" + job.ID
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{"POST", path + "/pause", "lwm_test_secret", 409},
		{"POST", path + "/pause", "lwm_other_secret", 404},
		{"POST", "/v1/jobs/missing/cancel", "lwm_test_secret", 404},
		{"POST", path + "/resume", "lwm_test_secret", 200},
		{"POST", path + "/pause", "lwm_test_secret", 200},
		{"POST", path + "/resume", "lwm_test_secret", 200},
		{"POST", path + "/cancel", "lwm_test_secret", 200},
		{"POST", path + "/cancel", "lwm_test_secret", 409},
		{"POST", path + "/cancel", "lwm_read_secret", 403},
		{"GET", "/v1/jobs", "lwm_write_secret", 403},
	} {
		w := profileWriteRequest(h, tc.method, tc.path, "", tc.token, "")
		if w.Code != tc.want {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.want == 404 || tc.want == 409 {
			profileProblem(t, w, tc.want)
		}
	}
	invalid := profileWriteRequest(h, "POST", "/v1/jobs", "{}", "lwm_test_secret", "bad")
	again := profileWriteRequest(h, "POST", "/v1/jobs", validJobBody, "lwm_test_secret", "bad")
	if invalid.Code != 400 || invalid.Code != again.Code || invalid.Body.String() != again.Body.String() {
		t.Fatalf("problem replay: %d %s / %d %s", invalid.Code, invalid.Body.String(), again.Code, again.Body.String())
	}
}
