package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestCustomExecutorQuotaAndUsage(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaulted", true: "separate"}[separate], func(t *testing.T) {
			keys := deviceKeys()
			e := keys.entries["test"]
			e.scopes += "operations:write,"
			keys.entries["test"] = e
			keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
			srv := server.New(server.Config{})
			defer srv.Close()
			st := memory.NewOperations()
			executor := operations.NewExecutor(srv, st, operations.ExecutorOptions{})
			defer executor.Close()
			opts := Options{Keys: keys, Server: srv, Executor: executor}
			if separate {
				opts.Operations = memory.NewOperations()
			}
			h := New(opts)
			if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":1}}`); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
				t.Fatal(w.Code, w.Body)
			}
			path := "/v1/devices/pump/operations"
			body := `{"type":"read","paths":["/3/0/0"]}`
			first := deviceRequest(h, "POST", path, body)
			if first.Code != 202 {
				t.Fatal(first.Code, first.Body)
			}
			var op operations.Operation
			if err := json.Unmarshal(first.Body.Bytes(), &op); err != nil {
				t.Fatal(err)
			}
			if w := deviceRequest(h, "GET", first.Header().Get("Location"), ""); w.Code != 200 {
				t.Errorf("get: %d %s", w.Code, w.Body)
			}
			if w := deviceRequest(h, "POST", path, body); w.Code != 429 {
				t.Errorf("quota: got %d %s, want 429", w.Code, w.Body)
			}
			w := keyRequest(h, "GET", "/v1/tenants/default/usage", "lwm_platform_test", "")
			var v struct {
				Daily map[string]struct {
					Operations int `json:"operations"`
				} `json:"daily"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || v.Daily[op.CreatedAt.UTC().Format(time.DateOnly)].Operations != 1 {
				t.Errorf("usage: %d %s", w.Code, w.Body)
			}
		})
	}
}
