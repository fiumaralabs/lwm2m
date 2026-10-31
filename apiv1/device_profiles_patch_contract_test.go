package apiv1_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceProfilePatchContract(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var store apiv1.DeviceProfileStore
			if backend == "memory" {
				store = apiv1.NewMemoryDeviceProfileStore()
			} else {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "profiles.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				store = db.DeviceProfiles()
			}
			for _, p := range []struct {
				tenant  string
				profile apiv1.DeviceProfile
			}{
				{"default", apiv1.DeviceProfile{ID: "dp_a", Name: "pump", Timeseries: json.RawMessage(`{"retentionDays":90,"fromReads":false}`), OnRegister: json.RawMessage(`[{"type":"sync-clock"}]`)}},
				{"other", apiv1.DeviceProfile{ID: "dp_foreign", Name: "private"}},
			} {
				if err := store.Create(p.tenant, p.profile); err != nil {
					t.Fatal(err)
				}
			}
			hash := sha256.Sum256([]byte("lwm_test_secret"))
			readHash := sha256.Sum256([]byte("lwm_read_secret"))
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\nkey_read sha256:%x devices:read\n", hash, readHash)))
			if err != nil {
				t.Fatal(err)
			}
			h := apiv1.New(apiv1.Options{Keys: keys, DeviceProfiles: store})
			req := func(method, path, body, key, contentType, match string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				if contentType != "" {
					r.Header.Set("Content-Type", contentType)
				}
				if match != "" {
					r.Header.Set("If-Match", match)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			const path = "/v1/device-profiles/dp_a"
			const writeKey = "lwm_test_secret"
			const media = "application/merge-patch+json"
			get := func() *httptest.ResponseRecorder { return req("GET", path, "", writeKey, "", "") }
			initial := get()
			tag := initial.Header().Get("ETag")
			if initial.Code != 200 || tag == "" {
				t.Fatalf("GET: %d ETag %q %s", initial.Code, tag, initial.Body)
			}
			changed := req("PATCH", path, `{"name":"updated"}`, writeKey, media, tag)
			if changed.Code != 200 || changed.Header().Get("ETag") == "" {
				t.Fatalf("PATCH: %d ETag %q %s", changed.Code, changed.Header().Get("ETag"), changed.Body)
			}
			var got apiv1.DeviceProfile
			if err := json.Unmarshal(changed.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != "dp_a" || got.Name != "updated" || string(got.Timeseries) != `{"retentionDays":90,"fromReads":false}` || string(got.OnRegister) != `[{"type":"sync-clock"}]` {
				t.Fatalf("partial update lost fields: %+v", got)
			}
			after := get()
			if after.Code != 200 || after.Header().Get("ETag") != changed.Header().Get("ETag") {
				t.Fatalf("GET/PATCH ETag mismatch: %d %q vs %q", after.Code, after.Header().Get("ETag"), changed.Header().Get("ETag"))
			}
			preserved := req("PATCH", path, `{"timeseries":{"retentionDays":91}}`, writeKey, media, after.Header().Get("ETag"))
			if preserved.Code != 200 || !strings.Contains(preserved.Body.String(), `"fromReads":false`) {
				t.Fatalf("nested sibling not preserved: %d %s", preserved.Code, preserved.Body)
			}
			nested := req("PATCH", path, `{"timeseries":{"fromReads":null}}`, writeKey, media, preserved.Header().Get("ETag"))
			if nested.Code != 200 {
				t.Fatalf("nested PATCH: %d %s", nested.Code, nested.Body)
			}
			var nestedResult map[string]json.RawMessage
			if err := json.Unmarshal(nested.Body.Bytes(), &nestedResult); err != nil {
				t.Fatal(err)
			}
			var timeseries map[string]json.RawMessage
			if err := json.Unmarshal(nestedResult["timeseries"], &timeseries); err != nil {
				t.Fatal(err)
			}
			if string(timeseries["retentionDays"]) != "91" || len(timeseries) != 1 || string(nestedResult["onRegister"]) != `[{"type":"sync-clock"}]` {
				t.Fatalf("nested merge/null lost or retained fields: %s", nested.Body)
			}
			removed := req("PATCH", path, `{"timeseries":null}`, writeKey, media, nested.Header().Get("ETag"))
			if removed.Code != 200 {
				t.Fatalf("null PATCH: %d %s", removed.Code, removed.Body)
			}
			got = apiv1.DeviceProfile{}
			if err := json.Unmarshal(removed.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Timeseries) != 0 || len(got.OnRegister) == 0 {
				t.Fatalf("null removal: %+v", got)
			}
			before, ok := store.Get("default", "dp_a")
			if !ok {
				t.Fatal("missing profile")
			}
			beforeTag := get().Header().Get("ETag")
			for _, tc := range []struct {
				name, body, key, contentType, match, url string
				codes                                    []int
			}{
				{"required", `{"name":null}`, writeKey, media, "", path, []int{400, 422}},
				{"id", `{"id":"dp_changed"}`, writeKey, media, "", path, []int{400, 422}},
				{"media", `{"name":"bad"}`, writeKey, "application/json", "", path, []int{415}},
				{"stale", `{"name":"bad"}`, writeKey, media, `"not-current"`, path, []int{412}},
				{"foreign", `{"name":"bad"}`, writeKey, media, "", "/v1/device-profiles/dp_foreign", []int{404}},
				{"readonly", `{"name":"bad"}`, "lwm_read_secret", media, "", path, []int{403}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := req("PATCH", tc.url, tc.body, tc.key, tc.contentType, tc.match)
					valid := false
					for _, code := range tc.codes {
						if w.Code == code {
							valid = true
						}
					}
					if !valid {
						t.Errorf("status %d: %s", w.Code, w.Body)
					}
					current, ok := store.Get("default", "dp_a")
					if !ok || !reflect.DeepEqual(current, before) || get().Header().Get("ETag") != beforeTag {
						t.Error("rejected request changed profile or ETag")
					}
				})
			}
			// Release both writers together with the same validator and distinct values.
			start := make(chan struct{})
			type outcome struct {
				name     string
				response *httptest.ResponseRecorder
			}
			results := make(chan outcome, 2)
			for _, name := range []string{"winner-a", "winner-b"} {
				go func(name string) {
					<-start
					w := req("PATCH", path, fmt.Sprintf(`{"name":%q}`, name), writeKey, media, beforeTag)
					results <- outcome{name, w}
				}(name)
			}
			close(start)
			winner, success, precondition := "", 0, 0
			for range 2 {
				result := <-results
				switch result.response.Code {
				case 200:
					success++
					winner = result.name
				case 412:
					precondition++
				default:
					t.Errorf("concurrent PATCH: %d %s", result.response.Code, result.response.Body)
				}
			}
			if success != 1 || precondition != 1 {
				t.Fatalf("concurrent PATCH results: %d successes, %d precondition failures", success, precondition)
			}
			persisted := get()
			var actual apiv1.DeviceProfile
			if err := json.Unmarshal(persisted.Body.Bytes(), &actual); err != nil || persisted.Code != 200 || actual.Name != winner {
				t.Fatalf("persisted winner %q: %d %s (error %v)", winner, persisted.Code, persisted.Body, err)
			}
			method := req("POST", path, "", writeKey, "", "")
			if method.Code != 405 || !strings.Contains(method.Header().Get("Allow"), "PATCH") {
				t.Errorf("item Allow: %d %q", method.Code, method.Header().Get("Allow"))
			}
		})
	}
}
