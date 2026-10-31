package apiv1

import (
	"strings"
	"testing"
)

func TestJobsCreateRawScope(t *testing.T) {
	body := strings.Replace(validJobBody, `"type":"read","paths":["/3/0/1"]`, `"type":"raw","method":"GET","path":"/3/0/1"`, 1)
	for _, tc := range []struct {
		name, scopes   string
		status, stored int
	}{
		{"write only", "jobs:write", 403, 0},
		{"raw only", "operations:raw", 403, 0},
		{"write and raw", "jobs:write,operations:raw", 201, 1},
		{"admin", "admin", 201, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryJobStore()
			w := jobRequest(NewJobsHandler(store), "POST", "/v1/jobs", body, "a", tc.scopes)
			if w.Code != tc.status || len(store.List("a")) != tc.stored {
				t.Fatalf("status=%d body=%s stored=%d", w.Code, w.Body.String(), len(store.List("a")))
			}
		})
	}
}
