package apiv1

import (
	"crypto/sha256"
	"testing"

	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceCreateOutcomeReplay(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	audit := &MemoryAuditStore{}
	devices := &memory.Devices{}
	h := New(Options{Keys: keys, Devices: devices, AuditOptions: AuditOptions{Audit: audit}})
	first := deviceRequest(h, "PUT", "/v1/device-creates/op1", `{"endpoint":"pump"}`)
	if first.Code != 201 {
		t.Fatalf("first PUT: %d %s", first.Code, first.Body)
	}
	replay := deviceRequest(h, "PUT", "/v1/device-creates/op1", `{"endpoint":"pump"}`)
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	entries, err := audit.List("default")
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries: %d, %v", len(entries), err)
	}
}
