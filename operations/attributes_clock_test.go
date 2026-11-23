package operations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
)

func TestExecutorWriteAttributesRemoval(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	p := lwm2m.MustParsePath("/3/0/0")
	h.wait(t, h.submit(t, "write-attributes", `{"path":"/3/0/0","attributes":{"pmin":10,"pmax":20}}`), operations.Succeeded)
	if !strings.Contains(strings.Join(c.Attributes(p), ","), "pmin=10") {
		t.Fatalf("attribute not set: %v", c.Attributes(p))
	}
	h.wait(t, h.submit(t, "write-attributes", `{"path":"/3/0/0","attributes":{"pmin":null}}`), operations.Succeeded)
	got := strings.Join(c.Attributes(p), ",")
	if strings.Contains(got, "pmin") || !strings.Contains(got, "pmax=20") {
		t.Fatalf("incorrect attributes after removal: %s", got)
	}
}

func TestExecutorSyncClock(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	h := setup(t, operations.ExecutorOptions{Now: func() time.Time { return now }})
	c := h.register(t)
	p := lwm2m.MustParsePath("/3/0/13")
	c.Set(p, lwm2m.Time(0))
	h.wait(t, h.submit(t, "sync-clock", `{}`), operations.Succeeded)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/13"]}`), operations.Succeeded)
	if !strings.Contains(string(o.Result), now.Format(time.RFC3339)) {
		t.Fatalf("wrong clock result: %s", o.Result)
	}
}
