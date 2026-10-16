package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags([]string{"-server", "coaps://example.org", "-psk", "0011", "-ep", "dev1", "-lifetime", "86400", "-binding", "uq"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.addr != "example.org:5684" || !cfg.secure || cfg.lifetime != 3600 || !cfg.queue || cfg.endpoint != "dev1" || cfg.version != "1.1" {
		t.Errorf("cfg = %+v", cfg)
	}
	cfg, err = parseFlags(nil, io.Discard)
	if err != nil || cfg.addr != "lwm2m.fiumaralabs.com:5683" || cfg.secure || !strings.HasPrefix(cfg.endpoint, "demo-") || len(cfg.endpoint) != 11 {
		t.Errorf("defaults = %+v, %v", cfg, err)
	}
	if cfg, _ := parseFlags([]string{"-server", "coap://[::1]:6000"}, io.Discard); cfg.addr != "[::1]:6000" {
		t.Errorf("IPv6 addr = %q", cfg.addr)
	}
	for _, args := range [][]string{
		{"-server", "coaps://example.org"},               // no key
		{"-psk", "0011"},                                 // key without coaps
		{"-server", "http://example.org"},                // scheme
		{"-lwm2m", "2.0"},                                // version
		{"-lwm2m", "1.0", "-send", "10s"},                // Send in 1.0
		{"-binding", "T"},                                // binding
		{"-lifetime", "5"},                               // too short
		{"-server", "coaps://example.org", "-psk", "zz"}, // bad hex
		{"-ep", "a b"},                                   // bad name
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

type events struct {
	mu    sync.Mutex
	reg   chan string
	dereg chan server.DeregisterReason
	temps []float64
}

func (e *events) on(ev server.Event) {
	switch x := ev.(type) {
	case server.Registered:
		e.reg <- x.Registration.Endpoint
	case server.Deregistered:
		e.dereg <- x.Reason
	case server.Notification:
		for _, n := range x.Response.Nodes {
			if n.Path.String() == "/3303/0/5700" {
				e.mu.Lock()
				e.temps = append(e.temps, n.Value.Float)
				e.mu.Unlock()
			}
		}
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) logf(format string, a ...any) {
	s.mu.Lock()
	fmt.Fprintf(&s.b, format+"\n", a...)
	s.mu.Unlock()
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestRunAgainstServer registers the client with an in-process server,
// observes a changing temperature and checks that cancelling the context
// deregisters.
func TestRunAgainstServer(t *testing.T) {
	ev := &events{reg: make(chan string, 4), dereg: make(chan server.DeregisterReason, 4)}
	models := server.NewModels(model.Default())
	srv := server.New(server.Config{Store: server.NewMemoryStore(), Schema: models.Schema, Validator: models, OnEvent: ev.on})
	cb := coap.New(srv)
	a, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cb.Close(); srv.Close() }()

	// The simulation clock runs a minute per 50 ms tick, so the temperature
	// visibly moves between notifications.
	var clockMu sync.Mutex
	clock := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock = clock.Add(time.Minute)
		return clock
	}
	cfg := config{server: "coap://" + a.String(), addr: a.String(), endpoint: "demo-test01", version: "1.1",
		lifetime: 60, interval: 50 * time.Millisecond, send: 200 * time.Millisecond, timeout: 5 * time.Second, now: now}
	out := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, out.logf) }()

	select {
	case ep := <-ev.reg:
		if ep != "demo-test01" {
			t.Fatalf("registered %q", ep)
		}
	case err := <-done:
		t.Fatalf("run ended: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no registration")
	}
	octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ocancel()
	cbor := lwm2m.FormatSenMLCBOR
	if _, r, err := srv.Observe(octx, "demo-test01", lwm2m.MustParsePath("/3303/0/5700"), server.ObserveOptions{Accept: &cbor}); err != nil || r.Code.String() != "Content" {
		t.Fatalf("observe: %v %v", r, err)
	}
	r, err := srv.Read(octx, "demo-test01", lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
	if err != nil || string(r.Payload) != "Fiumara Labs" {
		t.Fatalf("read /3/0/0: %v %v", r, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ev.mu.Lock()
		distinct := map[float64]bool{}
		for _, v := range ev.temps {
			distinct[v] = true
		}
		ev.mu.Unlock()
		if len(distinct) >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("temperature did not change: %v\n%s", ev.temps, out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	select {
	case reason := <-ev.dereg:
		if reason != server.ReasonDeregistered {
			t.Errorf("deregister reason %v", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server saw no deregistration")
	}
	log := out.String()
	for _, want := range []string{"registered demo-test01 at /rd/", "observe /3303/0/5700", "notify /3303/0/5700 = ", "read /3/0/0", "send /3303/0/5700=", "deregistered (2.02 Deleted)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestRegisterRefused(t *testing.T) {
	models := server.NewModels(model.Default())
	srv := server.New(server.Config{Schema: models.Schema, Validator: models,
		Authorize: func(string, server.Identity) bool { return false }})
	cb := coap.New(srv)
	a, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cb.Close(); srv.Close() }()
	cfg := config{addr: a.String(), endpoint: "x", version: "1.1", lifetime: 60, interval: time.Second, timeout: 3 * time.Second}
	err = run(context.Background(), cfg, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "4.") {
		t.Errorf("err = %v, want a 4.xx", err)
	}
}

func TestRegisterTimeout(t *testing.T) {
	cfg := config{addr: "127.0.0.1:9", endpoint: "x", version: "1.1", lifetime: 60, interval: time.Second, timeout: 300 * time.Millisecond}
	err := run(context.Background(), cfg, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "register") {
		t.Errorf("err = %v", err)
	}
}
