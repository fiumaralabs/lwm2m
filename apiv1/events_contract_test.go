package apiv1

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func eventStream(t *testing.T, s *httptest.Server, query, last string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	r, _ := http.NewRequestWithContext(ctx, "GET", s.URL+"/v1/events"+query, nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	r.Header.Set("Last-Event-ID", last)
	res, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	return bufio.NewReader(res.Body)
}
func eventFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(line)
		if line == "\n" {
			return b.String()
		}
	}
}
func eventAppend(t *testing.T, s store.Events, tenant, kind, device string) store.Event {
	t.Helper()
	e, err := s.Append(tenant, kind, device, json.RawMessage(`{ "value": 1 }`))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestEventsReplay(t *testing.T) {
	log := &memory.Events{}
	eventAppend(t, log, "other", "secret", "a")
	a := eventAppend(t, log, "default", "device.created", "a")
	b := eventAppend(t, log, "default", "device.updated", "b")
	c := eventAppend(t, log, "default", "operation.done", "a")
	s := httptest.NewServer(New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: log}}))
	defer s.Close()
	for _, tc := range []struct {
		query, last string
		want        store.Event
	}{
		{"", "", a}, {"?type=device.updated", "", b}, {"?type=missing,operation.done", "", c},
		{"?type=device.*&device=b", "", b}, {"?device=b", "", b},
		{"?since=" + a.Sequence, "", b}, {"", b.Sequence, c}, {"?since=" + b.Sequence, a.Sequence, b},
	} {
		t.Run(tc.query+tc.last, func(t *testing.T) {
			frame := eventFrame(t, eventStream(t, s, tc.query, tc.last))
			raw, _ := json.Marshal(tc.want)
			want := "id: " + tc.want.Sequence + "\nevent: " + tc.want.Type + "\ndata: " + string(raw) + "\n\n"
			if frame != want {
				t.Fatalf("got %q want %q", frame, want)
			}
		})
	}
}
func TestEventsLiveTenant(t *testing.T) {
	log := &memory.Events{}
	s := httptest.NewServer(New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: log}}))
	t.Cleanup(s.Close)
	r := eventStream(t, s, "", "")
	eventAppend(t, log, "other", "secret", "a")
	e := eventAppend(t, log, "default", "device.created", "a")
	if f := eventFrame(t, r); !strings.Contains(f, "id: "+e.Sequence+"\nevent: device.created\n") || strings.Contains(f, "secret") {
		t.Fatal(f)
	}
}

func TestEventsValidation(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	for _, q := range []string{"since=1", "since=-00000000000000001", "type=foo**", "type=a,,b", "type=", "type=*", "type=a*b", "type=a%0Ab", "since=00000000000000000x"} {
		if w := deviceRequest(h, "GET", "/v1/events?"+q, ""); w.Code != 400 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
	if w := deviceRequest(h, "POST", "/v1/events", ""); w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatal(w)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/events?since=000000000000000001", nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	r.Header.Set("Last-Event-ID", "bad")
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/events", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes = ",devices:write,"
	keys.entries["test"] = e
	if w := deviceRequest(New(Options{Keys: keys}), "GET", "/v1/events", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestEventsResetKeepAlive(t *testing.T) {
	now := time.Now()
	log := &memory.Events{Clock: func() time.Time { return now }, Retention: time.Second}
	old := eventAppend(t, log, "default", "old", "a")
	eventAppend(t, log, "default", "old", "a")
	now = now.Add(2 * time.Second)
	s := httptest.NewServer(New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: log, EventKeepAlive: 20 * time.Millisecond}}))
	t.Cleanup(s.Close)
	r := eventStream(t, s, "?since="+old.Sequence, "")
	if f := eventFrame(t, r); f != "event: reset\ndata: {}\n\n" {
		t.Fatal(f)
	}
	for i := 0; i < 4; i++ {
		if f := eventFrame(t, r); !strings.HasPrefix(f, ":") {
			t.Fatal(f)
		}
	}
	e := eventAppend(t, log, "default", "new", "a")
	for {
		f := eventFrame(t, r)
		if strings.HasPrefix(f, ":") {
			continue
		}
		if !strings.Contains(f, "id: "+e.Sequence) {
			t.Fatal(f)
		}
		break
	}
}
func TestEventsResetRetained(t *testing.T) {
	now := time.Now()
	log := &memory.Events{Clock: func() time.Time { return now }, Retention: time.Second}
	old := eventAppend(t, log, "default", "old", "a")
	eventAppend(t, log, "default", "old", "a")
	now = now.Add(2 * time.Second)
	kept := eventAppend(t, log, "default", "kept", "a")
	s := httptest.NewServer(New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: log}}))
	t.Cleanup(s.Close)
	r := eventStream(t, s, "", old.Sequence)
	if f := eventFrame(t, r); f != "event: reset\ndata: {}\n\n" {
		t.Fatal(f)
	}
	if f := eventFrame(t, r); !strings.Contains(f, "id: "+kept.Sequence) {
		t.Fatal(f)
	}
}
func TestEventsCancel(t *testing.T) {
	done := make(chan struct{})
	h := New(Options{Keys: deviceKeys()})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); h.ServeHTTP(w, r) }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := http.NewRequestWithContext(ctx, "GET", s.URL+"/v1/events", nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	res, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	res.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not exit")
	}
}

type failingEvents struct {
	store.Events
	calls, failAt int
}

func (s *failingEvents) List(string, store.EventFilter) ([]store.Event, bool, error) {
	s.calls++
	if s.calls >= s.failAt {
		return nil, false, errors.New("list failed")
	}
	return nil, false, nil
}
func TestEventsListErrors(t *testing.T) {
	h := New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: &failingEvents{failAt: 1}}})
	if w := deviceRequest(h, "GET", "/v1/events", ""); w.Code != 500 {
		t.Fatal(w.Code)
	}
	s := httptest.NewServer(New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: &failingEvents{failAt: 2}}}))
	t.Cleanup(s.Close)
	if _, err := eventStream(t, s, "", "").ReadByte(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}
func TestEventsPages(t *testing.T) {
	log := &memory.Events{}
	var want []store.Event
	for i := 0; i < 105; i++ {
		want = append(want, eventAppend(t, log, "default", "device.created", "a"))
	}
	s := httptest.NewServer(New(Options{Keys: deviceKeys(), EventOptions: EventOptions{Events: log}}))
	t.Cleanup(s.Close)
	r := eventStream(t, s, "", "")
	for _, e := range want {
		if f := eventFrame(t, r); !strings.HasPrefix(f, "id: "+e.Sequence+"\n") {
			t.Fatal(f)
		}
	}
}

func TestEventsConnect(t *testing.T) {
	s := httptest.NewServer(New(Options{Keys: deviceKeys()}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", s.URL+"/v1/events", nil)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	res, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d headers=%v", res.StatusCode, res.Header)
	}
}
