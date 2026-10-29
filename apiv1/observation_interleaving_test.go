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

func TestObservationEstablishmentInterleavings(t *testing.T) {
	for _, mode := range []string{"notification", "active", "passive"} {
		t.Run(mode, func(t *testing.T) {
			var d *ObservationDelivery
			srv := server.New(server.Config{OnEvent: func(e server.Event) { d.HandleEvent(e) }})
			defer srv.Close()
			state, events := &memory.State{}, &memory.Events{}
			d = NewObservationDelivery(srv, &memory.Observations{}, state, events, "default")
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
			c := testclient.New(testclient.Config{Endpoint: "pump"})
			defer c.Close()
			path := lwm2m.MustParsePath("/3/0/0")
			c.Set(path, lwm2m.String("Acme"))
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			cancelled := make(chan struct{}, 1)
			c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
				if r.Observe != nil && *r.Observe == 0 {
					entered <- struct{}{}
					<-release
				}
				if r.Observe != nil && *r.Observe == 1 {
					cancelled <- struct{}{}
				}
				return 0, nil, nil, false
			})
			if err := c.Dial(addr.String()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.Register(ctx); err != nil {
				t.Fatal(err)
			}
			w := deviceRequest(h, "POST", "/v1/devices/pump/observations", `{"paths":["/3/0/0"]}`)
			var v store.Observation
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("observe not received")
			}
			if mode == "notification" {
				obs := srv.Observations("pump")
				if len(obs) != 1 {
					t.Fatal(obs)
				}
				reg, ok := srv.Store().ByEndpoint("pump")
				if !ok {
					t.Fatal("registration missing")
				}
				d.HandleEvent(server.Notification{Registration: reg, Observation: obs[0], Response: &server.Response{Code: codes.Content, Nodes: []lwm2m.Node{lwm2m.ValueNode(path, lwm2m.String("early"))}}})
			} else {
				suffix := ""
				if mode == "passive" {
					suffix = "?mode=passive"
				}
				if w := deviceRequest(h, "DELETE", "/v1/observations/"+v.ID+suffix, ""); w.Code != 204 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			close(release)
			deadline := time.Now().Add(2 * time.Second)
			if mode == "notification" {
				for {
					got, _ := d.Get("default", v.ID)
					if got.Status == "active" {
						if got.NotificationCount != 1 || got.LastNotificationAt == nil {
							t.Fatalf("lost early notification: %+v", got)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal(got)
					}
					time.Sleep(time.Millisecond)
				}
				values, _ := state.List("default", "pump", "", nil)
				ev, _, _ := events.List("default", store.EventFilter{Type: "lwm2m.notification", Limit: 10})
				if len(values) != 1 || string(values[0].Value) != `"early"` || len(ev) != 1 {
					t.Fatalf("state=%+v events=%+v", values, ev)
				}
			} else if mode == "active" {
				select {
				case <-cancelled:
				case <-ctx.Done():
					t.Fatal("missing active cancellation")
				}
			} else {
				for {
					if len(srv.Observations("pump")) == 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("passive cancellation did not finish")
					}
					time.Sleep(time.Millisecond)
				}
				select {
				case <-cancelled:
					t.Fatal("passive sent active cancellation")
				default:
				}
			}
		})
	}
}
