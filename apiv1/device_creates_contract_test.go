package apiv1

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceCreateContractRouter(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		var devices store.Devices = &memory.Devices{}
		keys := Keys{entries: map[string]credential{
			"a": {hash: sha256.Sum256([]byte("lwm_a_secret")), scopes: ",devices:write,", tenant: "a"},
			"b": {hash: sha256.Sum256([]byte("lwm_b_secret")), scopes: ",devices:write,", tenant: "b"},
		}}
		audit := &MemoryAuditStore{}
		events := &memory.Events{}
		h := New(Options{Keys: keys, Devices: devices, AuditOptions: AuditOptions{Audit: audit}, EventOptions: EventOptions{Events: events}})
		call := func(token, method, id, body string, status int, contains string) string {
			t.Helper()
			req := httptest.NewRequest(method, "/v1/device-creates/"+id, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer lwm_"+token+"_secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			result := w.Body.String()
			if w.Code != status || !strings.Contains(result, contains) {
				t.Fatalf("%s %s %s: %d %s", token, method, id, w.Code, result)
			}
			return result
		}
		first := call("a", "PUT", "one", `{"endpoint":"pump","name":"P"}`, 201, `"status":"committed"`)
		if want := fmt.Sprintf(`"fingerprint":"sha256:%x"`, sha256.Sum256([]byte(`{"endpoint":"pump","name":"P"}`))); !strings.Contains(first, want) {
			t.Fatalf("fingerprint: %s", first)
		}
		replay := call("a", "PUT", "one", `{"name":"P","endpoint":"pump"}`, 200, `"status":"committed"`)
		if first != replay {
			t.Fatal("reordered JSON replay changed outcome")
		}
		items, _, err := events.List("a", store.EventFilter{Type: "lwm2m.device.created", Limit: 100})
		if err != nil || len(items) != 1 {
			t.Fatalf("events %d %v", len(items), err)
		}
		entries, err := audit.List("a")
		if err != nil || len(entries) != 1 {
			t.Fatalf("audit %d %v", len(entries), err)
		}
		call("a", "PUT", "one", `{"endpoint":"other"}`, 409, `operation-id-reused`)
		call("b", "GET", "one", "", 200, `"reason":"fenced"`)
		call("b", "PUT", "one", `{"endpoint":"b"}`, 200, `"reason":"fenced"`)
		call("b", "PUT", "conflict", `{"endpoint":"pump"}`, 200, `"reason":"endpoint-conflict"`)
		call("b", "PUT", "independent", `{"endpoint":"second"}`, 201, `"status":"committed"`)
		call("a", "PUT", "independent", `{"endpoint":"third"}`, 201, `"status":"committed"`)
		invalid := call("a", "PUT", "invalid", `{"endpoint":"","secret":"synthetic-psk-123"}`, 200, `"reason":"invalid"`)
		if strings.Contains(invalid, "synthetic-psk-123") {
			t.Fatal("credential leaked")
		}
		before, _ := audit.List("a")
		call("a", "PUT", "invalid", `{"endpoint":"","secret":"synthetic-psk-123"}`, 200, `"reason":"invalid"`)
		after, _ := audit.List("a")
		if len(after) != len(before) {
			t.Fatal("invalid replay created a second audit")
		}
		call("a", "GET", "unknown", "", 200, `"fingerprint":null`)
		call("a", "PUT", "unknown", `{"endpoint":"nope"}`, 200, `"reason":"fenced"`)
		call("a", "DELETE", "unknown", "", 204, "")
		call("a", "DELETE", "unknown", "", 404, `not_found`)
		call("a", "GET", "bad%20id", "", 400, `invalid-argument`)
		call("a", "GET", strings.Repeat("x", 129), "", 400, `invalid-argument`)
		call("a", http.MethodPost, "other", "", 405, `method_not_allowed`)
	})
}
