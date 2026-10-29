package apiv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestObservationDelivery(t *testing.T) {
	var delivery *ObservationDelivery
	srv := server.New(server.Config{Schema: server.NewModels(model.Default()).Schema, OnEvent: func(e server.Event) { delivery.HandleEvent(e) }})
	defer srv.Close()
	state, events := &memory.State{}, &memory.Events{}
	delivery = NewObservationDelivery(srv, &memory.Observations{}, state, events, "default")
	keys := deviceKeys()
	k := keys.entries["test"]
	k.scopes += "operations:write,"
	keys.entries["test"] = k
	series := NewMemoryTimeSeriesStore()
	h := New(Options{Keys: keys, Observations: delivery, State: state, TimeSeries: series})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	create := func(paths string, persistent bool) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"paths": json.RawMessage(paths), "persistent": persistent})
		w := deviceRequest(h, "POST", "/v1/devices/pump/observations", string(body))
		var v store.Observation
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return v.ID
	}
	await := func(id, status string) store.Observation {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			v, _ := delivery.Get("default", id)
			if v.Status == status {
				return v
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: want %s got %+v", id, status, v)
			}
			time.Sleep(time.Millisecond)
		}
	}
	id := create(`["/3/0/0"]`, true)
	await(id, "pending")
	b := coap.New(srv)
	defer b.Close()
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := testclient.New(testclient.Config{Endpoint: "pump"})
	defer c.Close()
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
	c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(50))
	if err = c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if v := await(id, "active"); v.EstablishedAt == nil {
		t.Fatal("missing establishedAt")
	}
	req, _ := c.LastRequest()
	if _, err = c.Notify(ctx, req.Token); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		v, _ := delivery.Get("default", id)
		if v.NotificationCount > 0 {
			if v.LastNotificationAt == nil {
				t.Fatal(v)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no notification")
		}
		time.Sleep(time.Millisecond)
	}
	delivery.mu.Lock() // Wait for the notification callback's complete projection.
	delivery.mu.Unlock()
	values, _ := state.List("default", "pump", "", nil)
	if len(values) != 1 || values[0].Source != "notification" {
		t.Fatalf("state %+v", values)
	}
	ev, _, err := events.List("default", store.EventFilter{Type: "lwm2m.notification", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 {
		t.Fatal(ev)
	}
	var payload struct {
		Observation string
		Prefix      *string
		Values      []store.StateValue
	}
	if json.Unmarshal(ev[0].Data, &payload) != nil || payload.Observation != id || payload.Prefix != nil || len(payload.Values) != 1 || string(payload.Values[0].Value) != `"Acme"` {
		t.Fatal(string(ev[0].Data))
	}
	str := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path=/3/0/0", "")
	var empty struct {
		Series []struct{ Points []TimeSeriesPoint }
	}
	if str.Code != 200 || json.Unmarshal(str.Body.Bytes(), &empty) != nil || len(empty.Series) != 1 || len(empty.Series[0].Points) != 0 {
		t.Fatalf("string series: %d %s", str.Code, str.Body.String())
	}
	numeric := create(`["/1/0/1"]`, false)
	await(numeric, "active")
	for i, value := range []int64{51, 52} {
		c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(value))
		deadline := time.Now().Add(2 * time.Second)
		for {
			delivery.mu.Lock()
			v, _ := delivery.Get("default", numeric)
			delivery.mu.Unlock()
			if v.NotificationCount >= uint64(i+1) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("notification timeout")
			}
			time.Sleep(time.Millisecond)
		}
	}
	wSeries := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path=/1/0/1", "")
	var result struct {
		Series []struct{ Points []TimeSeriesPoint }
	}
	if wSeries.Code != 200 || json.Unmarshal(wSeries.Body.Bytes(), &result) != nil || len(result.Series) != 1 || len(result.Series[0].Points) != 2 || result.Series[0].Points[0].V != float64(51) || result.Series[0].Points[1].V != float64(52) {
		t.Fatalf("numeric series: %d %s", wSeries.Code, wSeries.Body.String())
	}
	foreign, err := series.Query("other", "pump", "/1/0/1", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(foreign) != 0 {
		t.Fatalf("tenant isolation: %v %v", foreign, err)
	}
	bad := create(`["/3/0/99"]`, true)
	if v := await(bad, "failed"); len(v.Error) == 0 {
		t.Fatal("missing refusal")
	}
	if w := deviceRequest(h, "DELETE", "/v1/observations/"+id+"?mode=passive", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if res, err := c.Notify(ctx, req.Token); err != nil || res != testclient.NotifyReset {
		t.Fatal(res, err)
	}
	composite := create(`["/3/0/0","/1/0/1"]`, false)
	await(composite, "active")
	if w := deviceRequest(h, "DELETE", "/v1/observations/"+composite, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	req, _ = c.LastRequest()
	if req.Observe == nil || *req.Observe != 1 || uint32(req.Code) != 5 {
		t.Fatalf("cancel %+v", req)
	}
	persistent := create(`["/3/0/0"]`, true)
	await(persistent, "active")
	ephemeral := create(`["/1/0/1"]`, false)
	await(ephemeral, "active")
	if _, err = c.Deregister(ctx); err != nil {
		t.Fatal(err)
	}
	await(persistent, "pending")
	await(ephemeral, "ended")
	c.Set(lwm2m.MustParsePath("/3/0/99"), lwm2m.String("now available"))
	if _, err = c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	await(persistent, "active")
	await(bad, "active")
	await(ephemeral, "ended")
	await(id, "ended")
	// Replacement without deregistration ends only nonpersistent intent.
	replaced := create(`["/1/0/1"]`, false)
	await(replaced, "active")
	if _, err = c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	await(replaced, "ended")
	await(persistent, "active")
	await(bad, "active")
	if err := delivery.Create("other", store.Observation{Tenant: "other", ID: "foreign", Device: "pump"}); err != store.ErrTenant {
		t.Fatal("cross-tenant dispatch", err)
	}
}
