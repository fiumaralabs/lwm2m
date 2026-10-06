package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/transport/coap"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// fakeBS is a Bootstrap-Server driven through HandleUplink with scripted
// peers.
func fakeBS(t *testing.T, cfg Config) (*Server, chan Result) {
	results := make(chan Result, 8)
	cfg.OnSession = func(r Result) { results <- r }
	s := New(cfg)
	t.Cleanup(func() { _ = s.Close() })
	return s, results
}

func start(t *testing.T, s *Server, peer server.Peer, ep string) {
	t.Helper()
	resp, after := s.HandleUplink(peer, &server.Message{Code: codes.POST, Path: "/bs", Query: []string{"ep=" + ep}})
	if resp.Code != codes.Changed {
		t.Fatalf("Bootstrap-Request: %s", server.CodeString(resp.Code))
	}
	after()
}

func wait(t *testing.T, ch chan Result) Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
		return Result{}
	}
}

func nosec() server.Identity { return server.Identity{Mode: server.ModeNoSec, Addr: "fake"} }

// The session is bounded by EXCHANGE_LIFETIME (247 s by default): a client
// gives up on bootstrap when no Finish arrives in that time, so the BS
// abandons the session too, without sending Finish late.
// Proves: BS-08
func TestSessionBound(t *testing.T) {
	if New(Config{}).cfg.SessionTimeout != 247*time.Second {
		t.Fatal("default session bound is not EXCHANGE_LIFETIME")
	}
	store := NewMemoryConfigStore()
	_ = store.Put("ep", c1("coap://s.example.com", "id", "key"))
	s, results := fakeBS(t, Config{Configs: store, SessionTimeout: 300 * time.Millisecond, RequestTimeout: time.Minute})
	var sent []string
	peer := &fakePeer{id: nosec(), fn: func(ctx context.Context, n int, m *server.Message) (*server.Message, error) {
		sent = append(sent, m.Path)
		if n == 2 {
			return hang(ctx)
		}
		return answer(m), nil
	}}
	start(t, s, peer, "ep")
	res := wait(t, results)
	if !errors.Is(res.Err, ErrSessionTimeout) {
		t.Fatalf("err %v", res.Err)
	}
	if strings.Join(sent, ",") != "/0/0,/1/0" {
		t.Fatalf("sent %v", sent)
	}

	// The real client: a stalled Write ends the session the same way.
	h := newHarness(t, func(c *Config) { c.SessionTimeout = 300 * time.Millisecond })
	h.put("ep", c1("coap://s.example.com", "id", "key"))
	c := h.client("ep", testclient.Config{})
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if r.Code == codes.PUT {
			time.Sleep(time.Second)
		}
		return 0, nil, nil, false
	})
	r, err := c.BootstrapRequest(h.ctx, c.BootstrapQuery(nil))
	mustCode(t, r, err, codes.Changed)
	if res := h.result(); !errors.Is(res.Err, ErrSessionTimeout) {
		t.Fatalf("err %v", res.Err)
	}
	time.Sleep(time.Second)
	for _, r := range c.Requests() {
		if r.Code == codes.POST {
			t.Fatal("Finish sent after the session bound")
		}
	}
}

// Tolerance T49: a request that times out is re-sent as a new message
// (Anjay Lite ignores retransmissions of bootstrap requests); a transport
// failure after the retries ends the session without Finish.
func TestRetryAsNewMessage(t *testing.T) {
	store := NewMemoryConfigStore()
	_ = store.Put("ep", c1("coap://s.example.com", "id", "key"))
	s, results := fakeBS(t, Config{Configs: store, RequestTimeout: 50 * time.Millisecond, Retries: 1})
	var paths []string
	peer := &fakePeer{id: nosec(), fn: func(ctx context.Context, n int, m *server.Message) (*server.Message, error) {
		paths = append(paths, m.Path)
		if n == 1 {
			return hang(ctx) // first attempt of /0/0 lost
		}
		return answer(m), nil
	}}
	start(t, s, peer, "ep")
	if res := wait(t, results); res.Err != nil {
		t.Fatalf("err %v", res.Err)
	}
	if strings.Join(paths, ",") != "/0/0,/0/0,/1/0,/bs" {
		t.Fatalf("paths %v", paths)
	}

	dead := &fakePeer{id: nosec(), fn: func(ctx context.Context, _ int, _ *server.Message) (*server.Message, error) { return hang(ctx) }}
	start(t, s, dead, "ep")
	res := wait(t, results)
	if res.Err == nil || len(res.Steps) != 1 || dead.n != 2 {
		t.Fatalf("err %v steps %d attempts %d", res.Err, len(res.Steps), dead.n)
	}
}

// A new Bootstrap-Request for an endpoint cancels its running session
// (Leshan two_bootstrap_at_the_same_time_not_allowed): the old one ends
// with ErrCancelled, the new one completes.
func TestNewSessionCancelsOld(t *testing.T) {
	store := NewMemoryConfigStore()
	_ = store.Put("ep", c1("coap://s.example.com", "id", "key"))
	s, results := fakeBS(t, Config{Configs: store})
	stuck := &fakePeer{id: nosec(), fn: func(ctx context.Context, _ int, _ *server.Message) (*server.Message, error) { return hang(ctx) }}
	start(t, s, stuck, "ep")
	time.Sleep(50 * time.Millisecond)
	start(t, s, &fakePeer{id: nosec(), fn: func(_ context.Context, _ int, m *server.Message) (*server.Message, error) { return answer(m), nil }}, "ep")
	got := map[bool]Result{}
	for range 2 {
		r := wait(t, results)
		got[r.Err == nil] = r
	}
	if !errors.Is(got[false].Err, ErrCancelled) {
		t.Fatalf("old session: %v", got[false].Err)
	}
	if _, ok := got[true]; !ok {
		t.Fatal("new session failed")
	}
}

// ETS 1.1-int-5 Server Initiated Bootstrap: the LwM2M Server executes
// /1/0/9 on a registered client (2.04); the client sends Bootstrap-Request
// to the BS, is bootstrapped (Finish 2.04) and registers again with the
// new configuration.
// Proves: BS-11
func TestInt5ServerInitiatedBootstrap(t *testing.T) {
	h := newHarness(t)
	dm := server.New(server.Config{RequestTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = dm.Close() })
	dmCoAP := coap.New(dm)
	t.Cleanup(func() { _ = dmCoAP.Close() })
	dmAddr, err := dmCoAP.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &BootstrapConfig{
		Security: map[uint16]SecurityConfig{0: {URI: "coap://" + dmAddr.String(), SecurityMode: ModeNoSec, ServerID: u16(1)}},
		Servers:  map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 600, Binding: "U"}},
		ToDelete: []string{"/0", "/1"},
	}
	h.put("ep5", cfg)
	c := h.client("ep5", testclient.Config{Lifetime: 600})
	if fin, res := h.bootstrap(c, c.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("initial bootstrap: %s %v", server.CodeString(fin), res.Err)
	}
	triggered := make(chan struct{}, 1)
	c.OnTrigger = func() { triggered <- struct{}{} }
	_ = c.Close()
	if err := c.Dial(dmAddr.String()); err != nil {
		t.Fatal(err)
	}
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, codes.Created)

	// New config: a longer lifetime, provisioned by the re-bootstrap.
	cfg2 := *cfg
	cfg2.Servers = map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 86400, Binding: "U"}}
	h.put("ep5", &cfg2)
	resp, err := TriggerBootstrap(h.ctx, dm, "ep5", 0)
	if err != nil || resp.Code != codes.Changed {
		t.Fatalf("execute /1/0/9: %v %v", resp, err)
	}
	if ex := c.Executed(); len(ex) != 1 || ex[0].Path != "/1/0/9" {
		t.Fatalf("executed %v", ex)
	}
	<-triggered
	_ = c.Close()
	if err := c.Dial(h.udp); err != nil {
		t.Fatal(err)
	}
	if fin, res := h.bootstrap(c, c.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("re-bootstrap: %s %v", server.CodeString(fin), res.Err)
	}
	if v, _ := c.Get(p("/1/0/1")); v.Int != 86400 {
		t.Fatalf("lifetime %v", v)
	}
	_ = c.Close()
	if err := c.Dial(dmAddr.String()); err != nil {
		t.Fatal(err)
	}
	q := c.RegisterQuery()
	for i := range q {
		if strings.HasPrefix(q[i], "lt=") {
			q[i] = "lt=86400" // from the re-provisioned /1/0/1
		}
	}
	r, err = c.RegisterRaw(h.ctx, q, []byte(c.ObjectLinks()), true)
	mustCode(t, r, err, codes.Created)
	if reg, ok := dm.Store().ByEndpoint("ep5"); !ok || reg.Lifetime != 86400*time.Second {
		t.Fatalf("registration %+v", reg)
	}
}
