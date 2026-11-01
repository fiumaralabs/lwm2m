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
	if entries[0].Action != "device.create" || entries[0].Resource != "/v1/devices" || entries[0].Status != 201 {
		t.Fatalf("unexpected audit: %+v", entries[0])
	}
}

func TestDeviceCreateFencedMalformedBody(t *testing.T) {
	devices := &memory.Devices{}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:write,", tenant: "default"}}}
	audit := &MemoryAuditStore{}
	h := New(Options{Keys: keys, Devices: devices, AuditOptions: AuditOptions{Audit: audit}})
	fence := deviceRequest(h, "GET", "/v1/device-creates/op1", "")
	if fence.Code != 200 {
		t.Fatalf("GET: %d %s", fence.Code, fence.Body)
	}
	for _, body := range []string{"{", "", "{} {}"} {
		got := deviceRequest(h, "PUT", "/v1/device-creates/op1", body)
		if got.Code != 200 || got.Body.String() != fence.Body.String() {
			t.Fatalf("PUT %q: %d %s", body, got.Code, got.Body)
		}
	}
	entries, err := audit.List("default")
	if err != nil || len(entries) != 0 {
		t.Fatalf("fenced replay audit: %v %v", entries, err)
	}
	bad := deviceRequest(h, "PUT", "/v1/device-creates/new", "{")
	if bad.Code != 400 {
		t.Fatalf("unknown malformed: %d %s", bad.Code, bad.Body)
	}
	valid := deviceRequest(h, "PUT", "/v1/device-creates/new", `{"endpoint":"pump"}`)
	if valid.Code != 201 {
		t.Fatalf("valid after malformed: %d %s", valid.Code, valid.Body)
	}
}
