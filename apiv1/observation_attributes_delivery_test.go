package apiv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestObservationAttributeDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, version, paths, status string
		writes                       int
		reject                       bool
	}{
		{"10_single", "1.0", `["/3/0/0"]`, "active", 1, false},
		{"11_single", "1.1", `["/3/0/0"]`, "active", 1, false},
		{"10_composite", "1.0", `["/3/0/0","/3/0/1"]`, "active", 2, false},
		{"11_composite", "1.1", `["/3/0/0","/3/0/1"]`, "active", 2, false},
		{"12_composite", "1.2", `["/3/0/0","/3/0/1"]`, "active", 2, false},
		{"rejected", "1.1", `["/3/0/0"]`, "failed", 1, true},
		{"rejected_second", "1.1", `["/3/0/0","/3/0/1"]`, "failed", 2, true},
		{"12_single", "1.2", `["/3/0/0"]`, "active", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d *ObservationDelivery
			srv := server.New(server.Config{OnEvent: func(e server.Event) { d.HandleEvent(e) }})
			defer srv.Close()
			state := &memory.State{}
			d = NewObservationDelivery(srv, &memory.Observations{}, state, &memory.Events{}, "default")
			keys := deviceKeys()
			k := keys.entries["test"]
			k.scopes += "operations:write,"
			keys.entries["test"] = k
			h := New(Options{Keys: keys, Observations: d, State: state})
			deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
			b := coap.New(srv)
			defer b.Close()
			addr, err := b.ListenUDP("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			c := testclient.New(testclient.Config{Endpoint: "pump", Version: tc.version})
			defer c.Close()
			c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
			c.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("Model"))
			if tc.reject {
				c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
					if r.Code == codes.PUT && (tc.writes == 1 || r.Path == "/3/0/1") {
						return codes.BadRequest, nil, nil, true
					}
					return 0, nil, nil, false
				})
			}
			if err := c.Dial(addr.String()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.Register(ctx); err != nil {
				t.Fatal(err)
			}
			w := deviceRequest(h, "POST", "/v1/devices/pump/observations", `{"paths":`+tc.paths+`,"attributes":{"pmin":60}}`)
			var v store.Observation
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				v, err = d.Get("default", v.ID)
				if err != nil {
					t.Fatal(err)
				}
				if v.Status == tc.status {
					break
				}
				if v.Status == "failed" || time.Now().After(deadline) {
					t.Fatalf("want %s: %+v error=%s", tc.status, v, v.Error)
				}
				time.Sleep(time.Millisecond)
			}
			requests := c.Requests()
			want := tc.writes
			if !tc.reject {
				want++
			}
			if len(requests) != want {
				t.Fatalf("requests: %+v", requests)
			}
			for i := 0; i < tc.writes; i++ {
				r := requests[i]
				expected := []string{"/3/0/0", "/3/0/1"}[i]
				if r.Code != codes.PUT || r.Path != expected || len(r.Queries) != 1 || r.Queries[0] != "pmin=60" {
					t.Fatalf("write %d: %+v", i, r)
				}
			}
			if tc.reject {
				if string(v.Error) != `{"message":"4.00"}` {
					t.Fatalf("error: %s", v.Error)
				}
				return
			}
			r := requests[len(requests)-1]
			if r.Observe == nil || *r.Observe != 0 {
				t.Fatalf("observe: %+v", r)
			}
			if tc.writes == 0 {
				if len(r.Queries) != 1 || r.Queries[0] != "pmin=60" {
					t.Fatalf("query: %+v", r)
				}
			} else if len(r.Queries) != 0 {
				t.Fatalf("unexpected query: %+v", r)
			}
		})
	}
}
