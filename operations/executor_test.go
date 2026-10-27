package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type harness struct {
	e    *operations.Executor
	s    *server.Server
	st   *memory.Operations
	addr string
	ctx  context.Context
}

func setup(t *testing.T, opts operations.ExecutorOptions, mods ...func(*server.Config)) *harness {
	t.Helper()
	h := &harness{st: memory.NewOperations()}
	models := server.NewModels(model.Default())
	cfg := server.Config{Now: opts.Now, QueueAwake: time.Second, Schema: models.Schema, Validator: models, RequestTimeout: time.Second, OnEvent: func(ev server.Event) {
		if h.e != nil {
			h.e.HandleEvent(ev)
		}
	}}
	for _, mod := range mods {
		mod(&cfg)
	}
	h.s = server.New(cfg)
	h.e = operations.NewExecutor(h.s, h.st, opts)
	b := coap.New(h.s)
	a, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.addr = a.String()
	var cancel context.CancelFunc
	h.ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(func() { cancel(); b.Close(); h.s.Close() })
	return h
}
func (h *harness) register(t *testing.T, configs ...testclient.Config) *testclient.Client {
	t.Helper()
	cfg := testclient.Config{Endpoint: "dev"}
	if len(configs) > 0 {
		cfg = configs[0]
		cfg.Endpoint = "dev"
	}
	c := testclient.New(cfg)
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
	c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(100))
	if err := c.Dial(h.addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if r, err := c.Register(h.ctx); err != nil || r.Code != 65 {
		t.Fatalf("register %v %v", r, err)
	}
	return c
}
func (h *harness) submit(t *testing.T, kind, body string) operations.Operation {
	t.Helper()
	o, err := h.e.Submit(h.ctx, "t", operations.New("t", "dev", kind, json.RawMessage(body), nil, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func (h *harness) wait(t *testing.T, o operations.Operation, status operations.Status) operations.Operation {
	t.Helper()
	got, err := h.e.Wait(h.ctx, "t", o.ID)
	if err != nil || got.Status != status {
		t.Fatalf("wait: %+v %v, want %s", got, err, status)
	}
	return got
}
func TestExecutorWrite(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.register(t)
	h.wait(t, h.submit(t, "write", `{"path":"/1/0/1","mode":"replace","values":[{"path":"/1/0/1","type":"integer","value":42}]}`), operations.Succeeded)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/1/0/1"]}`), operations.Succeeded)
	var r struct {
		Values []struct {
			Type  string
			Value int
		}
	}
	json.Unmarshal(o.Result, &r)
	if len(r.Values) != 1 || r.Values[0].Value != 42 || r.Values[0].Type != "integer" {
		t.Fatalf("result %s", o.Result)
	}
}
func TestExecutorExecute(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.register(t)
	o := h.wait(t, h.submit(t, "execute", `{"path":"/3/0/4"}`), operations.Succeeded)
	if string(o.Result) != `{"code":"2.04"}` {
		t.Fatal(string(o.Result))
	}
}
func TestExecutorCoAPFailure(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.register(t)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/999"]}`), operations.Failed)
	if o.Error == nil || o.Error.Code != "not_found" {
		t.Fatalf("error %+v", o.Error)
	}
}
func TestExecutorOfflineZero(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	zero := 0
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &zero, time.Now())
	got, err := h.e.Submit(h.ctx, "t", o)
	if err != nil || got.Status != operations.Expired {
		t.Fatalf("status %s err %v", got.Status, err)
	}
	h.register(t)
	h.wait(t, got, operations.Expired)
}
func TestExecutorExpiry(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h := setup(t, operations.ExecutorOptions{Now: clock})
	one := 1
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &one, clock())
	got, err := h.e.Submit(h.ctx, "t", o)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = now.Add(2 * time.Second)
	mu.Unlock()
	h.wait(t, got, operations.Expired)
	h.register(t)
	h.wait(t, got, operations.Expired)
}
func TestExecutorRejectOffline(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
	o.WhenOffline = "reject"
	if _, err := h.e.Submit(h.ctx, "t", o); !errors.Is(err, operations.ErrNotRegistered) {
		t.Fatalf("err %v", err)
	}
}
func TestExecutorCancel(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	o := h.submit(t, "read", `{"paths":["/3/0/0"]}`)
	got, err := h.e.Cancel(h.ctx, "t", o.ID)
	if err != nil || got.Status != operations.Cancelled {
		t.Fatalf("cancel %s %v", got.Status, err)
	}
	if _, err = h.e.Cancel(h.ctx, "t", o.ID); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("repeat cancel: %v", err)
	}
	c := h.register(t)
	h.wait(t, got, operations.Cancelled)
	if len(c.Requests()) != 0 {
		t.Fatal("cancelled delivered")
	}
}
func TestExecutorInvalid(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	for _, kind := range []string{"unknown", "create", ""} {
		o := operations.New("t", "dev", kind, json.RawMessage(`{}`), nil, time.Now())
		if _, err := h.e.Submit(h.ctx, "t", o); !errors.Is(err, operations.ErrInvalid) {
			t.Fatalf("%q: %v", kind, err)
		}
	}
}
func TestExecutorFIFO(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	var ops []operations.Operation
	for _, arg := range []string{"0='a'", "0='b'", "0='c'"} {
		ops = append(ops, h.submit(t, "execute", `{"path":"/3/0/4","arguments":"`+arg+`"}`))
	}
	c := h.register(t)
	for _, o := range ops {
		h.wait(t, o, operations.Succeeded)
	}
	reqs := c.Executed()
	if len(reqs) != 3 {
		t.Fatalf("executed %d", len(reqs))
	}
	for i, want := range []string{"0='a'", "0='b'", "0='c'"} {
		if string(reqs[i].Body) != want {
			t.Fatalf("request %d: %q want %q", i, reqs[i].Body, want)
		}
	}
}
func TestExecutorMalformedRequest(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	for _, tc := range []struct{ kind, body string }{{"read", `{}`}, {"read", `{"paths":["bad"]}`}, {"write", `{"path":"bad"}`}, {"execute", `{"path":"bad"}`}, {"read", `not json`}} {
		o := operations.New("t", "dev", tc.kind, json.RawMessage(tc.body), nil, time.Now())
		if _, err := h.e.Submit(h.ctx, "t", o); !errors.Is(err, operations.ErrInvalid) {
			t.Fatalf("%s %s: %v", tc.kind, tc.body, err)
		}
	}
}
func TestExecutorComposite(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.register(t)
	h.wait(t, h.submit(t, "write", `{"values":[{"path":"/1/0/1","type":"integer","value":23}]}`), operations.Succeeded)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/0","/1/0/1"]}`), operations.Succeeded)
	var r struct{ Values []json.RawMessage }
	json.Unmarshal(o.Result, &r)
	if len(r.Values) != 2 {
		t.Fatalf("result %s", o.Result)
	}
}
func TestExecutorTypedValues(t *testing.T) {
	for _, tc := range []struct{ typ, raw string }{{"string", `""`}, {"integer", `"9223372036854775807"`}, {"unsigned", `"18446744073709551615"`}, {"float", `21.5`}, {"float", `"NaN"`}, {"boolean", `false`}, {"opaque", `"AAEC"`}, {"time", `"2026-10-08T10:00:00Z"`}, {"objlnk", `"3:0"`}, {"corelnk", `"</3/0>"`}} {
		t.Run(tc.typ+tc.raw, func(t *testing.T) {
			typ, _ := lwm2m.ParseType(tc.typ)
			h := setup(t, operations.ExecutorOptions{}, func(cfg *server.Config) {
				cfg.Validator = nil
				cfg.Schema = func(*server.Registration) lwm2m.Schema {
					return lwm2m.SchemaFunc(func(lwm2m.Path) (lwm2m.ResourceDef, bool) { return lwm2m.ResourceDef{Type: typ}, true })
				}
			})
			c := h.register(t, testclient.Config{Format: lwm2m.FormatTLV})
			c.Set(lwm2m.MustParsePath("/999/0/1"), lwm2m.Value{Type: typ})
			body := `{"path":"/999/0/1","mode":"replace","values":[{"path":"/999/0/1","type":"` + tc.typ + `","value":` + tc.raw + `}]}`
			h.wait(t, h.submit(t, "write", body), operations.Succeeded)
			o := h.wait(t, h.submit(t, "read", `{"paths":["/999/0"]}`), operations.Succeeded)
			var r struct {
				Values []struct {
					Type  string
					Value json.RawMessage
				}
			}
			if err := json.Unmarshal(o.Result, &r); err != nil {
				t.Fatal(err)
			}
			if len(r.Values) != 1 {
				t.Fatalf("result %s", o.Result)
			}
			var got, want any
			json.Unmarshal(r.Values[0].Value, &got)
			json.Unmarshal([]byte(tc.raw), &want)
			if r.Values[0].Type != tc.typ || got != want {
				t.Fatalf("result %s want %s %s", o.Result, tc.typ, tc.raw)
			}
		})
	}
}
func TestExecutorWaitContextTenant(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	o := h.submit(t, "read", `{"paths":["/3/0/0"]}`)
	ctx, cancel := context.WithCancel(h.ctx)
	cancel()
	got, err := h.e.Wait(ctx, "t", o.ID)
	if !errors.Is(err, context.Canceled) || got.ID != o.ID || got.Status != operations.Queued {
		t.Fatalf("wait %+v %v", got, err)
	}
	if _, err = h.e.Wait(h.ctx, "other", o.ID); err == nil {
		t.Fatal("cross-tenant wait")
	}
	if _, err = h.e.Cancel(h.ctx, "other", o.ID); err == nil {
		t.Fatal("cross-tenant cancel")
	}
	h.register(t)
	h.wait(t, o, operations.Succeeded)
}
func TestExecutorZeroClaimAndInflight(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		close(started)
		<-release
		return 0, nil, nil, false
	})
	zero := 0
	newOp := func() operations.Operation {
		return operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &zero, time.Now())
	}
	first, err := h.e.Submit(h.ctx, "t", newOp())
	if err != nil || first.Status != operations.Sent {
		t.Fatalf("claim %s %v", first.Status, err)
	}
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("not delivered")
	}
	if _, err = h.e.Cancel(h.ctx, "t", first.ID); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("sent cancel %v", err)
	}
	next, err := h.e.Submit(h.ctx, "t", newOp())
	if err != nil || next.Status != operations.Expired {
		t.Fatalf("busy zero %s %v", next.Status, err)
	}
	c.SetOverride(nil)
	unblock()
	h.wait(t, first, operations.Succeeded)
	h.wait(t, next, operations.Expired)
	if len(c.Requests()) != 1 {
		t.Fatal("expired delivered")
	}
}
func TestExecutorWake(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h := setup(t, operations.ExecutorOptions{Now: clock})
	c := h.register(t, testclient.Config{Version: "1.1", Queue: true})
	mu.Lock()
	now = now.Add(2 * time.Second)
	mu.Unlock()
	if h.s.Awake("dev") {
		t.Fatal("not asleep")
	}
	o := h.submit(t, "read", `{"paths":["/3/0/0"]}`)
	if o.Status != operations.Queued {
		t.Fatalf("sleeping %s", o.Status)
	}
	reg, _ := h.s.Store().ByEndpoint("dev")
	h.e.HandleQueued(reg)
	if len(c.Requests()) != 0 {
		t.Fatal("sleeping delivery")
	}
	h.s.Wake("dev")
	h.wait(t, o, operations.Succeeded)
}
func TestExecutorRealExpiry(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	one := 1
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), &one, time.Now())
	if _, err := h.e.Submit(h.ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	got, err := h.st.Get(h.ctx, "t", o.ID)
	if err != nil || got.Status != operations.Expired {
		t.Fatalf("timer %s %v", got.Status, err)
	}
}
func TestExecutorReadFormat(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
	o.Accept = "tlv"
	got, err := h.e.Submit(h.ctx, "t", o)
	if err != nil {
		t.Fatal(err)
	}
	got = h.wait(t, got, operations.Succeeded)
	var r struct{ Format string }
	json.Unmarshal(got.Result, &r)
	if r.Format != "tlv" {
		t.Fatalf("format %s", got.Result)
	}
	reqs := c.Requests()
	if len(reqs) != 1 || reqs[0].Accept == nil || *reqs[0].Accept != lwm2m.FormatTLV {
		t.Fatalf("accept %+v", reqs)
	}
}
func TestExecutorInferWriteType(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.register(t)
	h.wait(t, h.submit(t, "write", `{"path":"/1/0/1","mode":"replace","values":[{"path":"/1/0/1","value":17}]}`), operations.Succeeded)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/1/0/1"]}`), operations.Succeeded)
	var r struct{ Values []struct{ Value int } }
	json.Unmarshal(o.Result, &r)
	if len(r.Values) != 1 || r.Values[0].Value != 17 {
		t.Fatal(string(o.Result))
	}
}
func TestExecutorUndecodablePayload(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	cf := lwm2m.FormatText
	c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.Content, &cf, []byte("oops"), true
	})
	o := h.wait(t, h.submit(t, "read", `{"paths":["/1/0/1"]}`), operations.Succeeded)
	var r struct {
		Payload []byte
		Code    string
		Format  string
	}
	if err := json.Unmarshal(o.Result, &r); err != nil {
		t.Fatal(err)
	}
	if string(r.Payload) != "oops" || r.Code != "2.05" || r.Format != "text" {
		t.Fatalf("undecodable result %s", o.Result)
	}
	if o.Error == nil || o.Error.Code != "decode" || o.Error.Message == "" {
		t.Fatalf("expected decode error with message, got %+v", o.Error)
	}
}
func TestExecutorTimeout(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{}, func(cfg *server.Config) { cfg.RequestTimeout = 20 * time.Millisecond })
	c := h.register(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		<-release
		return 0, nil, nil, false
	})
	o := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/0"]}`), operations.Timeout)
	if o.Error == nil {
		t.Fatal("missing timeout error")
	}
}
func TestExecutorReadNodeMetadata(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	for _, tc := range []struct {
		format lwm2m.ContentFormat
		base   string
		node   lwm2m.Node
	}{
		{lwm2m.FormatSenMLJSON, "/3/0", lwm2m.Node{Path: lwm2m.MustParsePath("/3/0/0"), Value: lwm2m.String("Acme"), HasTime: true, Time: 1700000000}},
		{lwm2m.FormatTLV, "/3/0", lwm2m.Node{Path: lwm2m.MustParsePath("/3/0/11"), Kind: lwm2m.KindEmptyMultiple}},
	} {
		enc, err := codec.For(tc.format)
		if err != nil {
			t.Fatal(err)
		}
		body, err := enc.Encode(lwm2m.MustParsePath(tc.base), []lwm2m.Node{tc.node})
		if err != nil {
			t.Fatal(err)
		}
		c.SetOverride(func(testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
			return codes.Content, &tc.format, body, true
		})
		o := h.wait(t, h.submit(t, "read", `{"paths":["`+tc.base+`"]}`), operations.Succeeded)
		var r struct {
			Values []struct {
				Type      string
				Value     any
				Timestamp *time.Time
			}
		}
		if err = json.Unmarshal(o.Result, &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Values) != 1 {
			t.Fatal(string(o.Result))
		}
		v := r.Values[0]
		if tc.node.HasTime && (v.Timestamp == nil || !v.Timestamp.Equal(time.Unix(1700000000, 0))) {
			t.Fatalf("timestamp %s", o.Result)
		}
		if tc.node.Kind != lwm2m.KindValue && (v.Type != "" || v.Value != nil) {
			t.Fatalf("marker %s", o.Result)
		}
	}
}
func TestExecutorWriteEmptyMultiple(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{}, func(cfg *server.Config) { cfg.Validator = nil })
	c := h.register(t)
	c.Set(lwm2m.MustParsePath("/3/0/11/0"), lwm2m.Integer(1))
	h.wait(t, h.submit(t, "write", `{"path":"/3/0/11","mode":"replace","values":[{"path":"/3/0/11"}]}`), operations.Succeeded)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/11"]}`), operations.Succeeded)
	var r struct {
		Values []struct {
			Type  string
			Value any
		}
	}
	json.Unmarshal(o.Result, &r)
	if len(r.Values) != 1 || r.Values[0].Type != "" || r.Values[0].Value != nil {
		t.Fatalf("empty %s", o.Result)
	}
}
func TestExecutorRead(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.register(t)
	o := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/0"]}`), operations.Succeeded)
	var result struct {
		Code   string
		Values []struct {
			Path, Type string
			Value      string
		}
	}
	if err := json.Unmarshal(o.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != "2.05" || len(result.Values) != 1 || result.Values[0].Value != "Acme" || result.Values[0].Type != "string" {
		t.Fatalf("result %s", o.Result)
	}
}
