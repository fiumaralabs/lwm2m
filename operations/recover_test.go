package operations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestClose(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		close(entered)
		<-release
		return 0, nil, nil, false
	})
	h.submit(t, "read", `{"paths":["/3/0/0"]}`)
	<-entered
	queued := h.submit(t, "read", `{"paths":["/3/0/0"]}`)
	done := make(chan struct{})
	go func() {
		var closeExecutor func() error = h.e.Close
		if err := closeExecutor(); err != nil {
			t.Error(err)
		}
		if err := closeExecutor(); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel delivery")
	}
	if _, err := h.e.Submit(h.ctx, "t", operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())); !errors.Is(err, operations.ErrClosed) {
		t.Fatalf("submit: %v", err)
	}
	reg, _ := h.s.Store().ByEndpoint("dev")
	h.e.HandleQueued(reg)
	got, err := h.st.Get(h.ctx, "t", queued.ID)
	if err != nil || got.Status != operations.Queued {
		t.Fatalf("closed work: %+v %v", got, err)
	}
	if len(c.Requests()) != 1 {
		t.Fatalf("extra requests: %+v", c.Requests())
	}
}

type failingStore struct {
	operations.Operations
	mu              sync.Mutex
	pendingFailures int
	updateFailure   operations.Status
	failed          bool
}

func (s *failingStore) Pending(ctx context.Context, tenant, device string) ([]operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingFailures > 0 {
		s.pendingFailures--
		return nil, errors.New("pending failed")
	}
	return s.Operations.Pending(ctx, tenant, device)
}
func (s *failingStore) Update(ctx context.Context, tenant, id string, from operations.Status, c operations.Change) error {
	s.mu.Lock()
	if c.Status == s.updateFailure && !s.failed {
		s.failed = true
		s.mu.Unlock()
		return errors.New("update failed")
	}
	s.mu.Unlock()
	return s.Operations.Update(ctx, tenant, id, from, c)
}
func TestFIFOStoreErrors(t *testing.T) {
	for _, failure := range []operations.Status{"pending", operations.Sent, operations.Succeeded, operations.Expired, operations.Dropped} {
		t.Run(string(failure), func(t *testing.T) {
			var logs bytes.Buffer
			h := setup(t, operations.ExecutorOptions{})
			st := &failingStore{Operations: h.st, updateFailure: failure}
			if failure == "pending" {
				st.pendingFailures = 1
			}
			h.e = operations.NewExecutor(h.s, st, operations.ExecutorOptions{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			defer h.e.Close()
			first := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
			second := operations.New("t", "dev", "read", first.Request, nil, time.Now())
			if failure == operations.Expired {
				first.ExpiresAt = time.Now().Add(-time.Second)
			}
			if failure == operations.Dropped {
				first.WhenOffline = "reject"
			}
			for _, o := range []operations.Operation{first, second} {
				if err := h.st.Create(h.ctx, "t", o); err != nil {
					t.Fatal(err)
				}
			}
			recovery := &recoveryStore{Operations: st, rows: []operations.Operation{first, second}}
			e := operations.NewExecutor(h.s, recovery, operations.ExecutorOptions{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			h.e.Close()
			h.e = e
			defer e.Close()
			if failure == "pending" || failure == operations.Sent || failure == operations.Succeeded {
				h.register(t)
			}
			err := e.Start(h.ctx)
			if (failure == operations.Expired || failure == operations.Dropped) && err == nil {
				t.Fatal("missing recovery update error")
			}
			if failure == operations.Expired || failure == operations.Dropped {
				h.register(t)
			}
			h.wait(t, second, operations.Succeeded)
			e.Close()
			if !strings.Contains(logs.String(), "failed") {
				t.Fatalf("missing store error log: %s", logs.String())
			}
		})
	}
}

func TestStoreErrorZeroTTLDoesNotSendLate(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.e.Close()
	st := &failingStore{Operations: h.st, updateFailure: operations.Expired}
	h.e = operations.NewExecutor(h.s, st, operations.ExecutorOptions{})
	defer h.e.Close()
	zero := 0
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &zero, time.Now())
	if _, err := h.e.Submit(h.ctx, "t", o); err == nil {
		t.Fatal("missing expiry update error")
	}
	c := h.register(t)
	h.wait(t, o, operations.Expired)
	if got := len(c.Requests()); got != 0 {
		t.Fatalf("expired operation sent late: %d requests", got)
	}
	next := h.submit(t, "read", `{"paths":["/1/0/1"]}`)
	h.wait(t, next, operations.Succeeded)
	if got := len(c.Requests()); got != 1 {
		t.Fatalf("FIFO did not resume with only the later operation: %d requests", got)
	}
}

func TestSubmitZeroExpiryStoreErrorDoesNotSend(t *testing.T) {
	for _, followup := range []bool{false, true} {
		name := "alone"
		if followup {
			name = "with_queued_work"
		}
		t.Run(name, func(t *testing.T) {
			h := setup(t, operations.ExecutorOptions{})
			h.e.Close()
			st := &failingStore{Operations: h.st, updateFailure: operations.Expired}
			h.e = operations.NewExecutor(h.s, st, operations.ExecutorOptions{})
			defer h.e.Close()
			zero := 0
			o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &zero, time.Now())
			got, err := h.e.Submit(h.ctx, "t", o)
			if err == nil || !strings.Contains(err.Error(), "update failed") || got.Status != operations.Queued {
				t.Fatalf("submit: %+v %v", got, err)
			}
			var next operations.Operation
			if followup {
				next = h.submit(t, "read", `{"paths":["/1/0/1"]}`)
				if next.Status != operations.Queued {
					t.Fatalf("followup was not queued: %+v", next)
				}
			}
			c := h.register(t)
			h.wait(t, o, operations.Expired)
			wantRequests := 0
			if followup {
				h.wait(t, next, operations.Succeeded)
				wantRequests = 1
			}
			if requests := c.Requests(); len(requests) != wantRequests {
				t.Fatalf("requests: %+v, want %d", requests, wantRequests)
			}
		})
	}
}

func TestRecoveryZeroExpiryFailureDoesNotSend(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	zero := 0
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &zero, time.Now())
	if err := h.st.Create(h.ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	st := &failingStore{Operations: h.st, updateFailure: operations.Expired}
	e := operations.NewExecutor(h.s, &recoveryStore{Operations: st, rows: []operations.Operation{o}}, operations.ExecutorOptions{})
	defer e.Close()
	if err := e.Start(h.ctx); err == nil {
		t.Fatal("missing expiry error")
	}
	got, err := e.Wait(h.ctx, "t", o.ID)
	if err != nil || got.Status != operations.Expired {
		t.Fatalf("expiry: %+v %v", got, err)
	}
	if len(c.Requests()) != 0 {
		t.Fatal("recovered zero TTL delivered")
	}
}

type recoveryStore struct {
	operations.Operations
	rows []operations.Operation
	err  error
}

func (s *recoveryStore) Unfinished(context.Context) ([]operations.Operation, error) {
	return s.rows, s.err
}

func TestRecovery(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	unsupported := operations.NewExecutor(h.s, struct{ operations.Operations }{h.st}, operations.ExecutorOptions{})
	if err := unsupported.Start(h.ctx); !errors.Is(err, operations.ErrRecoveryUnsupported) {
		t.Fatalf("unsupported: %v", err)
	}
	boom := errors.New("recovery failed")
	st := &recoveryStore{Operations: h.st, err: boom}
	e := operations.NewExecutor(h.s, st, operations.ExecutorOptions{})
	if err := e.Start(h.ctx); !errors.Is(err, boom) {
		t.Fatalf("recovery error: %v", err)
	}
	st.err = nil
	for _, kind := range []string{"sent", "expired", "zero", "reject", "live"} {
		ttl := 60
		if kind == "zero" {
			ttl = 0
		}
		o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &ttl, time.Now())
		if kind == "expired" {
			o.ExpiresAt = time.Now()
		}
		if kind == "reject" {
			o.WhenOffline = "reject"
		}
		if err := h.st.Create(h.ctx, "t", o); err != nil {
			t.Fatal(err)
		}
		if kind == "sent" {
			if err := h.st.Update(h.ctx, "t", o.ID, operations.Queued, operations.Change{Status: operations.Sent, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			o.Status = operations.Sent
		}
		st.rows = append(st.rows, o)
	}
	h.e = e
	if err := e.Start(h.ctx); err != nil {
		t.Fatal(err)
	}
	for i, want := range []operations.Status{operations.Timeout, operations.Expired, operations.Expired, operations.Dropped, operations.Queued} {
		got, err := h.st.Get(h.ctx, "t", st.rows[i].ID)
		if err != nil || got.Status != want {
			t.Fatalf("row %d: %+v %v", i, got, err)
		}
		if want == operations.Timeout && (got.Error == nil || got.Error.Code != "timeout") {
			t.Fatalf("timeout error: %+v", got.Error)
		}
	}
	c := h.register(t)
	h.wait(t, st.rows[4], operations.Succeeded)
	if len(c.Requests()) != 1 {
		t.Fatalf("requests: %+v", c.Requests())
	}
}
