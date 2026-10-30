package memory_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

// Exercise the actual store mutex: an unrelated tenant's old history must not
// make repeated rolling-window queries or concurrent writes scale with history.
func TestRollingWindowDoesNotScanUnrelatedHistory(t *testing.T) {
	ctx := context.Background()
	s := memory.NewOperations()
	now := time.Now().UTC()
	for i := 0; i < 300000; i++ {
		o := operations.Operation{Tenant: "old", ID: fmt.Sprint(i), CreatedAt: now.Add(-time.Hour)}
		if err := s.Create(ctx, "old", o); err != nil {
			t.Fatal(err)
		}
	}
	after := now.Add(-time.Minute)
	done := make(chan error, 2)
	start := make(chan struct{})
	go func() {
		<-start
		for i := 0; i < 300; i++ {
			tenant := "empty"
			if i%2 == 0 {
				tenant = "old"
			}
			rows, _, err := s.List(ctx, tenant, operations.ListFilter{CreatedAfter: &after}, "", 1)
			if err != nil {
				done <- err
				return
			}
			if len(rows) != 0 {
				done <- fmt.Errorf("unexpected rows: %d", len(rows))
				return
			}
		}
		done <- nil
	}()
	go func() {
		<-start
		for i := 0; i < 300; i++ {
			o := operations.Operation{Tenant: "writer", ID: fmt.Sprint(i), CreatedAt: now}
			if err := s.Create(ctx, "writer", o); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	started := time.Now()
	close(start)
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-timer.C:
			t.Fatal("rolling-window queries and writes blocked by unrelated old history")
		}
	}
	t.Logf("300 bounded queries and concurrent writes: %s", time.Since(started))
}
