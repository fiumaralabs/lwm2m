package coap

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// fakeClock is a settable clock for lifetime and queue-mode tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// events collects server events.
type events struct {
	mu sync.Mutex
	ch chan server.Event
	l  []server.Event
}

func newEvents() *events { return &events{ch: make(chan server.Event, 256)} }

func (e *events) on(ev server.Event) {
	e.mu.Lock()
	e.l = append(e.l, ev)
	e.mu.Unlock()
	select {
	case e.ch <- ev:
	default:
	}
}

// wait returns the next event satisfying match.
func (e *events) wait(t *testing.T, match func(server.Event) bool) server.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-e.ch:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for event")
			return nil
		}
	}
}

type harness struct {
	t      *testing.T
	srv    *server.Server
	b      *Binding
	addr   string // UDP
	dtls   string // DTLS
	ev     *events
	clock  *fakeClock
	ctx    context.Context
	cancel context.CancelFunc
}

func newHarness(t *testing.T, mod ...func(*server.Config)) *harness {
	t.Helper()
	h := &harness{t: t, ev: newEvents(), clock: &fakeClock{now: time.Unix(1_700_000_000, 0)}}
	models := server.NewModels(model.Default())
	cfg := server.Config{OnEvent: h.ev.on, Now: h.clock.Now, ExpiryCheck: time.Hour, RequestTimeout: 5 * time.Second,
		Schema: models.Schema, Validator: models}
	for _, m := range mod {
		m(&cfg)
	}
	h.srv = server.New(cfg)
	h.b = New(h.srv)
	a, err := h.b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.addr = a.String()
	d, err := h.b.ListenDTLS("127.0.0.1:0", DTLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	h.dtls = d.String()
	h.ctx, h.cancel = context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(func() {
		h.cancel()
		_ = h.b.Close()
		_ = h.srv.Close()
	})
	return h
}

// device returns a client with a Device object /3/0 and Server object /1/0.
func (h *harness) device(cfg testclient.Config) *testclient.Client {
	h.t.Helper()
	c := testclient.New(cfg)
	c.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(1))
	c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(86400))
	c.Set(lwm2m.MustParsePath("/1/0/7"), lwm2m.String("U"))
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	c.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("Lightweight M2M Client"))
	c.Set(lwm2m.MustParsePath("/3/0/9"), lwm2m.Integer(100))
	c.Set(lwm2m.MustParsePath("/3/0/11/0"), lwm2m.Integer(0))
	c.Set(lwm2m.MustParsePath("/3/0/16"), lwm2m.String("U"))
	addr := h.addr
	if cfg.PSKIdentity != "" {
		addr = h.dtls
	}
	if err := c.Dial(addr); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = c.Close() })
	return c
}

// registered returns a registered NoSec client.
func (h *harness) registered(ep string) *testclient.Client {
	h.t.Helper()
	c := h.device(testclient.Config{Endpoint: ep})
	r, err := c.Register(h.ctx)
	if err != nil || r.Code != 65 {
		h.t.Fatalf("register: %v %v", r, err)
	}
	return c
}

func mustCode(t *testing.T, r *testclient.Response, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := server.CodeString(r.Code); got != want {
		t.Fatalf("code %s, want %s", got, want)
	}
}

func mustResp(t *testing.T, r *server.Response, err error, want string) *server.Response {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := server.CodeString(r.Code); got != want {
		t.Fatalf("code %s, want %s (payload %q)", got, want, r.Payload)
	}
	return r
}

func p(s string) lwm2m.Path { return lwm2m.MustParsePath(s) }

// expect returns a checker for a downlink result with the given code.
func expect(t *testing.T, want string) func(*server.Response, error) *server.Response {
	return func(r *server.Response, err error) *server.Response {
		t.Helper()
		return mustResp(t, r, err, want)
	}
}

func second[A, B any](_ A, b B) B { return b }

func notification(t *testing.T, h *harness, ob *server.Observation) server.Notification {
	t.Helper()
	return h.ev.wait(t, func(e server.Event) bool {
		n, ok := e.(server.Notification)
		return ok && n.Observation.ID == ob.ID
	}).(server.Notification)
}
