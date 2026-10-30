package apiv1

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/fiumaralabs/lwm2m/server"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type countingWindowStore struct {
	operations.Operations
	calls int
}

func (s *countingWindowStore) List(ctx context.Context, tenant string, f operations.ListFilter, cursor string, limit int) ([]operations.Operation, string, error) {
	s.calls++
	return s.Operations.List(ctx, tenant, f, cursor, limit)
}
func TestOperationQuotaDoesNotPageOldHistory(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	st := &countingWindowStore{Operations: memory.NewOperations()}
	for i := 0; i < 1001; i++ {
		op := operations.New("tenant", "dev", "read", []byte(`{}`), nil, now.Add(-time.Hour))
		op.ID = fmt.Sprint(i)
		if err := st.Create(ctx, "tenant", op); err != nil {
			t.Fatal(err)
		}
	}
	reached, err := (Options{Operations: st}).operationLimitReached(ctx, "tenant", 1, now)
	if err != nil || reached || st.calls != 1 {
		t.Fatalf("reached=%v err=%v calls=%d, want false and one bounded query", reached, err, st.calls)
	}
}

type blockingWindowStore struct {
	operations.Operations
	entered chan struct{}
	release chan struct{}
}

func (s *blockingWindowStore) List(ctx context.Context, tenant string, f operations.ListFilter, cursor string, limit int) ([]operations.Operation, string, error) {
	if tenant == "default" {
		close(s.entered)
		<-s.release
	}
	return s.Operations.List(ctx, tenant, f, cursor, limit)
}
func TestOtherTenantDeviceCreateNotBlockedByQuota(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	keys.entries["other"] = credential{hash: sha256.Sum256([]byte("lwm_other_test")), scopes: ",devices:write,", tenant: "other"}
	keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
	st := &blockingWindowStore{Operations: memory.NewOperations(), entered: make(chan struct{}), release: make(chan struct{})}
	srv := server.New(server.Config{})
	defer srv.Close()
	exec := operations.NewExecutor(srv, st, operations.ExecutorOptions{})
	defer exec.Close()
	h := New(Options{Keys: keys, Server: srv, Executor: exec})
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":1}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	done := make(chan int, 1)
	go func() {
		done <- deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"]}`).Code
	}()
	select {
	case <-st.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("quota not entered")
	}
	other := make(chan int, 1)
	go func() { other <- keyRequest(h, "POST", "/v1/devices", "lwm_other_test", `{"endpoint":"other"}`).Code }()
	select {
	case code := <-other:
		if code != 201 {
			t.Errorf("other status: %d", code)
		}
	case <-time.After(time.Second):
		t.Error("other tenant device create blocked")
	}
	close(st.release)
	<-done
}

func TestConcurrentTenantQuotaSubmissions(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	keys.entries["platform"] = credential{hash: sha256.Sum256([]byte("lwm_platform_test")), scopes: ",platform,", tenant: "default"}
	srv := server.New(server.Config{})
	defer srv.Close()
	st := memory.NewOperations()
	exec := operations.NewExecutor(srv, st, operations.ExecutorOptions{})
	defer exec.Close()
	h := New(Options{Keys: keys, Server: srv, Executor: exec})
	if w := keyRequest(h, "PATCH", "/v1/tenants/default", "lwm_platform_test", `{"limits":{"operationsPerMinute":1}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	const n = 20
	start := make(chan struct{})
	results := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			<-start
			results <- deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"]}`).Code
		}()
	}
	close(start)
	accepted, rejected := 0, 0
	for i := 0; i < n; i++ {
		switch code := <-results; code {
		case 202:
			accepted++
		case 429:
			rejected++
		default:
			t.Errorf("unexpected status %d", code)
		}
	}
	if accepted != 1 || rejected != n-1 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
}
