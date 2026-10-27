package apiv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceEventEmission(t *testing.T) {
	h := New(Options{Keys: deviceKeys()}) // The default log must be shared by all handlers.
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	stream := eventStream(t, s, "?device=a", "")
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a","name":"sensor","labels":{"site":"lab"}}`); w.Code != 201 {
		t.Fatal(w)
	}
	created := history(t, h, "/v1/devices/a/events").Items
	if len(created) != 1 || created[0].Type != "lwm2m.device.created" {
		t.Fatal(created)
	}
	if w := deviceRequest(h, "DELETE", "/v1/devices/a", ""); w.Code != 204 {
		t.Fatal(w)
	}
	// Deleted devices return 404. Reprovision to inspect their retained history.
	if w := deviceRequest(h, "GET", "/v1/devices/a/events", ""); w.Code != 404 {
		t.Fatal(w)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w)
	}
	events := history(t, h, "/v1/devices/a/events").Items
	if len(events) != 3 {
		t.Fatal(events)
	}
	for i, kind := range []string{"lwm2m.device.created", "lwm2m.device.deleted"} {
		e := events[2-i]
		if e.Type != kind || e.Subject != "a" || e.Source != "/tenants/default" {
			t.Fatal(e)
		}
		var data struct{ Device deviceView }
		if err := json.Unmarshal(e.Data, &data); err != nil || data.Device.Endpoint != "a" || data.Device.Name != "sensor" || data.Device.Labels["site"] != "lab" {
			t.Fatalf("snapshot: %s", e.Data)
		}
		raw, _ := json.Marshal(e)
		if f := eventFrame(t, stream); f != "id: "+e.Sequence+"\nevent: "+kind+"\ndata: "+string(raw)+"\n\n" {
			t.Fatal(f)
		}
	}
}

type appendFailure struct{ store.Events }

func (appendFailure) Append(string, string, string, json.RawMessage) (store.Event, error) {
	return store.Event{}, errors.New("append unavailable")
}
func TestDeviceEventEmissionFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	h := New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: appendFailure{&memory.Events{}}}})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/a", ""); w.Code != 200 {
		t.Fatal(w)
	}
	if w := deviceRequest(h, "DELETE", "/v1/devices/a", ""); w.Code != 204 {
		t.Fatal(w)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/a", ""); w.Code != 404 {
		t.Fatal(w)
	}
	if strings.Count(logs.String(), "append unavailable") != 2 {
		t.Fatal(logs.String())
	}
}

type historyPage struct {
	Items      []store.Event
	NextCursor string
}

func history(t *testing.T, h http.Handler, path string) historyPage {
	t.Helper()
	w := deviceRequest(h, "GET", path, "")
	var p historyPage
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != 200 || err != nil {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	return p
}
func TestDeviceEventHistory(t *testing.T) {
	log := &memory.Events{}
	devices := &memory.Devices{}
	devices.Create("default", store.Device{Endpoint: "a"})
	devices.Create("other", store.Device{Endpoint: "a"})
	eventAppend(t, log, "other", "private", "a")
	eventAppend(t, log, "default", "private", "b")
	a := eventAppend(t, log, "default", "lwm2m.device.created", "a")
	b := eventAppend(t, log, "default", "lwm2m.notification", "a")
	c := eventAppend(t, log, "default", "lwm2m.device.deleted", "a")
	h := New(Options{Keys: deviceKeys(), Devices: devices, EventOptions: EventOptions{Events: log}})
	p := history(t, h, "/v1/devices/a/events?limit=2")
	if len(p.Items) != 2 || p.Items[0].ID != c.ID || p.Items[1].ID != b.ID || p.NextCursor == "" {
		t.Fatal(p)
	}
	q := history(t, h, "/v1/devices/a/events?limit=2&cursor="+p.NextCursor)
	if len(q.Items) != 1 || q.Items[0].ID != a.ID || q.NextCursor != "" {
		t.Fatal(q)
	}
	q = history(t, h, "/v1/devices/a/events?type=lwm2m.device.*")
	if len(q.Items) != 2 || q.Items[0].ID != c.ID || q.Items[1].ID != a.ID {
		t.Fatal(q)
	}
	q = history(t, h, "/v1/devices/a/events?type=lwm2m.notification,lwm2m.device.created")
	if len(q.Items) != 2 || q.Items[0].ID != b.ID || q.Items[1].ID != a.ID {
		t.Fatal(q)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/devices/a/events", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/missing/events", ""); w.Code != 404 || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatal(w)
	}
	for _, query := range []string{"limit=0", "limit=501", "limit=x", "cursor=bad", "type=", "type=a**", "type=a,,b", "type=a%0Ab", "type=a&type=b", "cursor=" + p.NextCursor + "&type=x"} {
		if w := deviceRequest(h, "GET", "/v1/devices/a/events?"+query, ""); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	for _, ep := range []string{"events", "a%2Fb"} {
		body := `{"endpoint":"` + strings.ReplaceAll(ep, "%2F", "/") + `"}`
		if w := deviceRequest(h, "POST", "/v1/devices", body); w.Code != 201 {
			t.Fatal(w)
		}
		if w := deviceRequest(h, "GET", "/v1/devices/"+ep, ""); w.Code != 200 || strings.Contains(w.Body.String(), `"items"`) {
			t.Fatal(w.Body.String())
		}
		if p := history(t, h, "/v1/devices/"+ep+"/events"); len(p.Items) != 1 {
			t.Fatal(p)
		}
	}
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.tenant = "other"
	keys.entries["test"] = entry
	other := New(Options{Keys: keys, Devices: devices, EventOptions: EventOptions{Events: log}})
	q = history(t, other, "/v1/devices/a/events")
	if len(q.Items) != 1 || q.Items[0].Type != "private" {
		t.Fatal(q)
	}
	if w := deviceRequest(other, "GET", "/v1/devices/a/events?cursor="+p.NextCursor, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
