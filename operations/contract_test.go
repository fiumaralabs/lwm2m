package operations_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestContractEncoding(t *testing.T) {
	for _, tc := range []struct{ name, format, body string }{
		{"kind", "", `{"path":"/1/0/1","values":[{"path":"/1/0/1","type":"bogus","value":42}]}`},
		{"automatic", "", `{"path":"/1/0/1","values":[{"path":"/3/0/0","type":"string","value":"outside target"}]}`},
		{"value", "", `{"path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":"bad"}]}`},
		{"codec", "opaque", `{"path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":42}]}`},
		{"unknown", "bogus", `{"path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":42}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t, operations.ExecutorOptions{})
			c := h.register(t)
			o := operations.New("t", "dev", "write", json.RawMessage(tc.body), nil, time.Now())
			o.Format = tc.format
			if _, err := h.e.Submit(h.ctx, "t", o); err != nil {
				t.Fatal(err)
			}
			got := h.wait(t, o, operations.Failed)
			if got.Error == nil || got.Error.Code != "encode" || len(got.Result) != 0 {
				t.Fatalf("encoding: %+v", got)
			}
			if len(c.Requests()) != 0 {
				t.Fatal("encoding failure reached client")
			}
		})
	}
}

func TestContractPrefix(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	c.Set(lwm2m.MustParsePath("/25/0/0"), lwm2m.String("gateway"))
	if _, err := c.Register(h.ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, body string }{
		{"write", `{"path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":42}]}`},
		{"execute", `{"path":"/3/0/4"}`},
		{"read", `{"paths":["/3/0/0","/1/0/1"]}`},
	} {
		o := operations.New("t", "dev", tc.kind, json.RawMessage(tc.body), nil, time.Now())
		o.Prefix = "gw"
		_, err := h.e.Submit(h.ctx, "t", o)
		var unsupported *operations.ErrPrefixUnsupported
		if !errors.As(err, &unsupported) {
			t.Fatalf("prefix: %v", err)
		}
	}
	stored, err := h.st.List(h.ctx, "t", "dev")
	if err != nil || len(stored) != 0 {
		t.Fatalf("stored rejected prefixes: %+v %v", stored, err)
	}
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		f := lwm2m.FormatText
		return codes.Content, &f, []byte("Acme"), true
	})
	o := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
	o.Prefix = "gw"
	if _, err := h.e.Submit(h.ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	h.wait(t, o, operations.Succeeded)
	reqs := c.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/gw/3/0/0" {
		t.Fatalf("prefix forwarding: %+v", reqs)
	}
}

func TestContractTransport(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	h.s.Store().Add(&server.Registration{ID: "peerless", Endpoint: "dev"})
	got := h.wait(t, h.submit(t, "read", `{"paths":["/3/0/0"]}`), operations.Failed)
	if got.Error == nil || got.Error.Code != "transport" || len(got.Result) != 0 {
		t.Fatalf("transport: %+v", got)
	}
}

func TestContractSessionEnd(t *testing.T) {
	for _, reason := range []string{"deregister", "replace", "expired", "removed", "first"} {
		t.Run(reason, func(t *testing.T) {
			h := setup(t, operations.ExecutorOptions{}, func(c *server.Config) { c.QueueAwake = time.Nanosecond })
			c := h.register(t, testclient.Config{Queue: true})
			reg, _ := h.s.Store().ByEndpoint("dev")
			h.e.HandleEvent(server.Registered{Registration: reg})
			time.Sleep(time.Millisecond)
			reject := operations.New("t", "dev", "read", json.RawMessage(`{"paths":["/3/0/0"]}`), nil, time.Now())
			reject.WhenOffline = "reject"
			if _, err := h.e.Submit(h.ctx, "t", reject); err != nil {
				t.Fatal(err)
			}
			queued := h.submit(t, "read", `{"paths":["/3/0/0"]}`)
			switch reason {
			case "deregister":
				if _, err := c.Deregister(h.ctx); err != nil {
					t.Fatal(err)
				}
			case "replace":
				h.register(t)
			case "expired", "removed":
				why := server.ReasonExpired
				if reason == "removed" {
					why = server.ReasonRemoved
				}
				h.e.HandleEvent(server.Deregistered{Registration: reg, Reason: why})
			case "first":
				h.e.HandleEvent(server.Registered{Registration: reg})
				got, err := h.st.Get(h.ctx, "t", reject.ID)
				if err != nil || got.Status != operations.Queued {
					t.Fatalf("first registration dropped: %+v %v", got, err)
				}
				return
			}
			got := h.wait(t, reject, operations.Dropped)
			if got.Error == nil || got.Error.Message != server.ErrQueueDropped.Error() {
				t.Fatalf("drop error: %+v", got.Error)
			}
			if reason != "replace" {
				h.register(t)
			}
			h.wait(t, queued, operations.Succeeded)
		})
	}
}
