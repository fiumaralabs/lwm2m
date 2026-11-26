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

func TestJobReplayRouteIsolation(t *testing.T) {
	const valid = `{"name":"release","target":{"devices":["a"]},"action":{"operation":{"type":"read","paths":["/3/0/1"]}},"rollout":{"maxConcurrent":2,"failureThresholdPercent":5},"ttlSeconds":60}`
	for _, backend := range []string{"memory", "sqlite"} {
		for _, first := range []string{"apply", "job"} {
			for _, invalid := range []bool{false, true} {
				for _, jobKey := range []string{"profile-apply:dp:k", "profile-apply:ZHA:k"} {
					t.Run(fmt.Sprintf("%s/%s/error=%v/key=%s", backend, first, invalid, jobKey), func(t *testing.T) {
						jobs := apiv1.NewMemoryJobStore()
						if backend == "sqlite" {
							db, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { _ = db.Close() })
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
						request := func(route, body string) *httptest.ResponseRecorder {
							path, key := "/v1/jobs", jobKey
							if route == "apply" {
								path, key = "/v1/device-profiles/dp/apply", "k"
							}
							r := httptest.NewRequest("POST", path, strings.NewReader(body))
							r.Header.Set("Authorization", "Bearer lwm_test_secret")
							r.Header.Set("Idempotency-Key", key)
							r.Header.Set("Content-Type", "application/json")
							w := httptest.NewRecorder()
							h.ServeHTTP(w, r)
							return w
						}
						second := "job"
						if first == "job" {
							second = "apply"
						}
						body := func(route string) string {
							if route == "job" {
								return valid
							}
							return "{}"
						}
						firstBody := body(first)
						if invalid {
							firstBody = `{"bad":true}`
						}
						a := request(first, firstBody)
						expectedFirst := 202
						if first == "job" {
							expectedFirst = 201
						}
						if invalid {
							expectedFirst = 400
						}
						if a.Code != expectedFirst {
							t.Fatalf("first %s: %d %s", first, a.Code, a.Body)
						}
						b := request(second, body(second))
						expectedSecond := 202
						if second == "job" {
							expectedSecond = 201
						}
						if b.Code != expectedSecond {
							t.Fatalf("second %s: %d %s", second, b.Code, b.Body)
						}
						if !invalid && a.Header().Get("Location") == b.Header().Get("Location") {
							t.Fatal("routes returned the same job")
						}
						for _, item := range []struct {
							route    string
							original *httptest.ResponseRecorder
						}{{first, a}, {second, b}} {
							retry := request(item.route, "{}")
							if retry.Code != item.original.Code || retry.Body.String() != item.original.Body.String() {
								t.Fatalf("%s retry: %d %s", item.route, retry.Code, retry.Body)
							}
						}
						count := 2
						if invalid {
							count = 1
						}
						if len(jobs.List("default")) != count {
							t.Fatalf("got %d jobs, want %d", len(jobs.List("default")), count)
						}
					})
				}
			}
		}
	}
}
