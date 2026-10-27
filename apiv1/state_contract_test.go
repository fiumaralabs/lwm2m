package apiv1

import (
	"context"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestDeviceStateContract(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	srv := server.New(server.Config{})
	defer srv.Close()
	h := New(Options{Keys: keys, Server: srv})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	check := func(query string, count int, source string) {
		t.Helper()
		w := deviceRequest(h, "GET", "/v1/devices/pump/state"+query, "")
		var reply struct {
			Values []struct {
				Path, Type, Source string
				Value              any
				ReceivedAt         time.Time
			}
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Values == nil || len(reply.Values) != count {
			t.Fatalf("state %d %s", w.Code, w.Body.String())
		}
		for _, v := range reply.Values {
			if v.Source != source || v.ReceivedAt.IsZero() || v.Type == "" {
				t.Fatalf("value %+v", v)
			}
		}
	}
	check("", 0, "")
	w := deviceRequest(h, "GET", "/v1/devices/missing/state", "")
	if w.Code != 404 || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatal(w.Code, w.Body.String())
	}
	b := coap.New(srv)
	defer b.Close()
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := testclient.New(testclient.Config{Endpoint: "pump"})
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
	c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(50))
	defer c.Close()
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	run := func(body string, status operations.Status) {
		t.Helper()
		w := deviceRequest(h, "POST", "/v1/devices/pump/operations?wait=2", body)
		if w.Code != 200 || !containsStatus(w.Body.Bytes(), status) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	run(`{"type":"read","paths":["/3/0/0"]}`, operations.Succeeded)
	check("", 1, "read")
	check("?paths=/3/0", 1, "read")
	check("?paths=/3/1,/1", 0, "")
	check("?prefix=gateway", 0, "")
	run(`{"type":"write","path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":100}]}`, operations.Succeeded)
	check("?paths=/1", 1, "write")
	if w := deviceRequest(h, "GET", "/v1/devices/pump/state?paths=/1", ""); !strings.Contains(w.Body.String(), `"value":100`) {
		t.Fatal(w.Body.String())
	}
	run(`{"type":"read","paths":["/3/0/999"]}`, operations.Failed)
	check("?paths=/3", 1, "read")
	run(`{"type":"delete","path":"/1/0"}`, operations.Succeeded)
	check("?paths=/1", 0, "")
}

func TestStateFiltersAndIsolation(t *testing.T) {
	state := &memory.State{}
	at := time.Now().UTC().Truncate(time.Second)
	op := operations.New("default", "pump", "read", nil, nil, at)
	op.Status = operations.Succeeded
	op.CompletedAt = &at
	op.Result = json.RawMessage(`{"values":[{"path":"/3/0/9","type":"integer","value":87,"timestamp":"2026-10-08T09:58:12Z","prefix":"gw"},{"path":"/30/0/1","type":"boolean","value":false}]}`)
	if err := state.Record(op); err != nil {
		t.Fatal(err)
	}
	got, err := state.List("default", "pump", "gw", []string{"/3"})
	if err != nil || len(got) != 1 || got[0].Timestamp == nil || got[0].ReceivedAt != at {
		t.Fatal(got, err)
	}
	for _, scope := range [][3]string{{"other", "pump", "gw"}, {"default", "other", "gw"}, {"default", "pump", ""}} {
		got, err := state.List(scope[0], scope[1], scope[2], []string{"/3"})
		if err != nil || len(got) != 0 {
			t.Fatal(scope, got, err)
		}
	}
}
