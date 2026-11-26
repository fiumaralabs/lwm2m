package apiv1_test

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestJobLegacyReplayUpgrade(t *testing.T) {
	const valid = `{"name":"release","target":{"devices":["a"]},"action":{"operation":{"type":"read","paths":["/3/0/1"]}},"rollout":{"maxConcurrent":2,"failureThresholdPercent":5},"ttlSeconds":60}`
	for _, backend := range []string{"memory", "sqlite"} {
		for _, key := range []string{"my-key", "jobs:my-key", "profile-apply:ZHA:k"} {
			for _, failure := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/error=%v", backend, key, failure), func(t *testing.T) {
					jobs := apiv1.NewMemoryJobStore()
					path := filepath.Join(t.TempDir(), "jobs.db")
					var db *sqlite.Devices
					var err error
					if backend == "sqlite" {
						db, err = sqlite.Open(path)
						if err != nil {
							t.Fatal(err)
						}
						jobs = db.Jobs()
					}
					old := apiv1.JobCreateResult{Job: &apiv1.Job{ID: "legacy", Target: apiv1.JobTarget{Devices: []string{"a"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}}
					if failure {
						old = apiv1.JobCreateResult{Problem: &apiv1.Problem{Status: 400, Code: "invalid_job", Detail: "Invalid job configuration"}}
					}
					if _, err := jobs.CreateJobResult("default", key, old); err != nil {
						t.Fatal(err)
					}
					if db != nil {
						if err := db.Close(); err != nil {
							t.Fatal(err)
						}
						db, err = sqlite.Open(path)
						if err != nil {
							t.Fatal(err)
						}
						defer db.Close()
						jobs = db.Jobs()
					}
					profiles := apiv1.NewMemoryDeviceProfileStore()
					if err := profiles.Create("default", apiv1.DeviceProfile{ID: "dp", Name: "pump"}); err != nil {
						t.Fatal(err)
					}
					hash := sha256.Sum256([]byte("lwm_test_secret"))
					keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,jobs:write\n", hash)))
					if err != nil {
						t.Fatal(err)
					}
					h := apiv1.New(apiv1.Options{Keys: keys, DeviceProfiles: profiles, Jobs: jobs})
					request := func(path, key, body string) *httptest.ResponseRecorder {
						r := httptest.NewRequest("POST", path, strings.NewReader(body))
						r.Header.Set("Authorization", "Bearer lwm_test_secret")
						r.Header.Set("Idempotency-Key", key)
						r.Header.Set("Content-Type", "application/json")
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						return w
					}
					w := request("/v1/jobs", key, valid)
					if failure {
						if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_job") {
							t.Fatalf("legacy error lost: %d %s", w.Code, w.Body)
						}
					} else if w.Code != 201 || w.Header().Get("Location") != "/v1/jobs/legacy" {
						t.Fatalf("legacy success lost: %d %s %s", w.Code, w.Header().Get("Location"), w.Body)
					}
					before := len(jobs.List("default"))
					expected := 1
					if failure {
						expected = 0
					}
					if before != expected {
						t.Fatalf("created duplicate job: %d", before)
					}
					if forged := request("/v1/jobs", "\x00profile-apply:ZHA:k", valid); forged.Code != 400 {
						t.Fatalf("internal namespace accepted as HTTP key: %d", forged.Code)
					}
					if key == "profile-apply:ZHA:k" {
						a := request("/v1/device-profiles/dp/apply", "k", "{}")
						if a.Code != 202 {
							t.Fatalf("legacy key collided with apply: %d %s", a.Code, a.Body)
						}
						bad := request("/v1/device-profiles/dp/apply", "other", `{"invalid":true}`)
						if bad.Code != 400 {
							t.Fatalf("distinct apply key: %d %s", bad.Code, bad.Body)
						}
						if db != nil {
							if err := db.Close(); err != nil {
								t.Fatal(err)
							}
							db, err = sqlite.Open(path)
							if err != nil {
								t.Fatal(err)
							}
							defer db.Close()
							jobs = db.Jobs()
							h = apiv1.New(apiv1.Options{Keys: keys, DeviceProfiles: profiles, Jobs: jobs})
						}
						badRetry := request("/v1/device-profiles/dp/apply", "other", "{}")
						if badRetry.Code != bad.Code || badRetry.Body.String() != bad.Body.String() {
							t.Fatalf("apply error replay after restart: %d %s", badRetry.Code, badRetry.Body)
						}
						retry := request("/v1/device-profiles/dp/apply", "k", "{}")
						if retry.Code != a.Code || retry.Body.String() != a.Body.String() {
							t.Fatalf("apply replay after restart: %d %s", retry.Code, retry.Body)
						}
					}
				})
			}
		}
	}
}
