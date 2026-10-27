package operations_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestRejectSessionCleanup(t *testing.T) {
	for _, status := range []operations.Status{operations.Succeeded, operations.Failed, operations.Timeout, operations.Cancelled, operations.Expired} {
		t.Run(string(status), func(t *testing.T) {
			var nanos atomic.Int64
			nanos.Store(time.Now().UnixNano())
			now := func() time.Time { return time.Unix(0, nanos.Load()) }
			h := setup(t, operations.ExecutorOptions{Now: now}, func(c *server.Config) { c.QueueAwake = time.Second; c.RequestTimeout = time.Millisecond * 200 })
			c := h.register(t, testclient.Config{Queue: true})
			c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
			body := `{"paths":["/3/0/0"]}`
			if status == operations.Failed {
				body = `{"paths":["/3/0/999"]}`
			}
			if status == operations.Timeout {
				release := make(chan struct{})
				t.Cleanup(func() { close(release) })
				c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
					<-release
					return 0, nil, nil, false
				})
			}
			if status == operations.Cancelled || status == operations.Expired {
				nanos.Add(int64(2 * time.Second))
			}
			ttl := 5
			o := operations.New("t", "dev", "read", json.RawMessage(body), &ttl, now())
			o.WhenOffline = "reject"
			if _, err := h.e.Submit(h.ctx, "t", o); err != nil {
				t.Fatal(err)
			}
			if status == operations.Cancelled {
				if _, err := h.e.Cancel(h.ctx, "t", o.ID); err != nil {
					t.Fatal(err)
				}
			}
			if status == operations.Expired {
				if operations.SessionCount(h.e) != 1 {
					t.Fatal("queued operation lost ownership")
				}
				nanos.Add(int64(6 * time.Second))
			}
			h.wait(t, o, status)
			// A terminal operation must no longer retain registration ownership.
			if got := operations.SessionCount(h.e); got != 0 {
				t.Fatalf("retained %d terminal sessions", got)
			}
		})
	}
}

func TestRejectZeroTTLCleanup(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{}, func(c *server.Config) { c.QueueAwake = time.Nanosecond })
	h.register(t, testclient.Config{Queue: true})
	time.Sleep(time.Millisecond)
	zero := 0
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &zero, time.Now())
	o.WhenOffline = "reject"
	if got, err := h.e.Submit(h.ctx, "t", o); err != nil || got.Status != operations.Expired {
		t.Fatalf("submit %+v %v", got, err)
	}
	if operations.SessionCount(h.e) != 0 {
		t.Fatal("zero TTL retained ownership")
	}
}

type replaceOnClaim struct {
	operations.Operations
	replace func()
}

func (s *replaceOnClaim) Update(ctx context.Context, tenant, id string, from operations.Status, c operations.Change) error {
	err := s.Operations.Update(ctx, tenant, id, from, c)
	if err == nil && c.Status == operations.Sent {
		s.replace()
	}
	return err
}
func TestRejectReplacementAfterClaim(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{}, func(c *server.Config) { c.OnEvent = nil })
	h.register(t)
	replacement := testclient.New(testclient.Config{Endpoint: "other"})
	if err := replacement.Dial(h.addr); err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if _, err := replacement.Register(h.ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := h.s.Store().ByEndpoint("other")
	r.Endpoint = "dev"
	e := operations.NewExecutor(h.s, &replaceOnClaim{Operations: h.st, replace: func() { h.s.Store().Add(r) }}, operations.ExecutorOptions{})
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
	o.WhenOffline = "reject"
	if _, err := e.Submit(h.ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Wait(h.ctx, "t", o.ID); err != nil || got.Status != operations.Dropped {
		t.Fatalf("wait %+v %v", got, err)
	}
	if len(replacement.Requests()) != 0 {
		t.Fatal("old operation reached replacement")
	}
}

func TestRejectReplacementBeforeEvent(t *testing.T) {
	paused, release := make(chan struct{}), make(chan struct{})
	h := setup(t, operations.ExecutorOptions{}, func(c *server.Config) {
		c.QueueAwake = time.Nanosecond
		forward := c.OnEvent
		c.OnEvent = func(ev server.Event) {
			if r, ok := ev.(server.Registered); ok && r.Replaced != nil {
				close(paused)
				<-release
			}
			forward(ev)
		}
	})
	h.register(t, testclient.Config{Queue: true})
	time.Sleep(time.Millisecond)
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
	o.WhenOffline = "reject"
	if _, err := h.e.Submit(h.ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	c := testclient.New(testclient.Config{Endpoint: "dev"})
	if err := c.Dial(h.addr); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.Register(h.ctx); done <- err }()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-paused:
	case <-h.ctx.Done():
		t.Fatal(h.ctx.Err())
	}
	queued := h.submit(t, "read", `{"paths":["/3/0/0"]}`)
	h.wait(t, o, operations.Dropped)
	if operations.SessionCount(h.e) != 0 {
		t.Fatal("dropped operation retained ownership")
	}
	h.wait(t, queued, operations.Failed) // replacement has no resource; queue policy still delivers
	if len(c.Requests()) != 1 {
		t.Fatalf("replacement received %d requests, want only unrelated read", len(c.Requests()))
	}
}
