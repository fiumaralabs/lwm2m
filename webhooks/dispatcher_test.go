package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestSignature(t *testing.T) {
	now := time.Unix(1700000000, 0)
	events, hooks, devices := &memory.Events{}, &memory.Webhooks{}, &memory.Devices{}
	e, _ := events.Append("a", "device.updated", "d", json.RawMessage(`{"x":1}`))
	received := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("key"))
		mac.Write([]byte(e.ID + ".1700000000."))
		mac.Write(b)
		var got store.Event
		received <- json.Unmarshal(b, &got) == nil && got.ID == e.ID && r.Header.Get("Webhook-Id") == e.ID && r.Header.Get("Webhook-Timestamp") == "1700000000" && r.Header.Get("Webhook-Signature") == "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)) && r.Header.Get("Content-Type") == "application/cloudevents+json"
	}))
	defer srv.Close()
	hooks.Create("a", store.Webhook{ID: "h", URL: srv.URL, Status: "active", Types: []string{"device.*"}, Secret: "whsec_a2V5"}, "")
	d := New("a", events, hooks, devices, Options{Now: func() time.Time { return now }})
	defer d.Close()
	if err := d.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-received:
		if !ok {
			t.Fatal("invalid signed CloudEvent")
		}
	default:
		t.Fatal("no delivery")
	}
}

func TestRetriesAndExpiry(t *testing.T) {
	now := time.Time{}
	events, hooks := &memory.Events{}, &memory.Webhooks{}
	var ids []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("Webhook-Id"))
		w.WriteHeader(503)
	}))
	defer srv.Close()
	hooks.Create("a", store.Webhook{ID: "h", URL: srv.URL, Status: "active", Types: []string{"*"}, Secret: "whsec_a2V5"}, "")
	first, _ := events.Append("a", "x", "d", nil)
	second, _ := events.Append("a", "x", "d", nil)
	d := New("a", events, hooks, &memory.Devices{}, Options{Now: func() time.Time { return now }})
	defer d.Close()
	check := func(n int) {
		t.Helper()
		if err := d.Dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(ids) != n {
			t.Fatalf("attempts=%d want %d: %v", len(ids), n, ids)
		}
	}
	check(1)
	check(1)
	now = now.Add(time.Second)
	check(2)
	now = now.Add(time.Second)
	check(2)
	now = now.Add(time.Second)
	check(3)
	// Exponential delay caps at an hour.
	for i := 0; i < 15; i++ {
		now = now.Add(time.Hour)
		check(4 + i)
	}
	now = now.Add(30 * time.Minute)
	check(18)
	now = now.Add(30 * time.Minute)
	check(19)
	if ids[18] != first.ID {
		t.Fatal("later event overtook failure")
	}
	now = (time.Time{}).Add(24 * time.Hour)
	check(20)
	if ids[19] != second.ID {
		t.Fatal("expiry did not unblock lane")
	}
}

func TestIndependentLanes(t *testing.T) {
	events, hooks := &memory.Events{}, &memory.Webhooks{}
	slow := make(chan struct{})
	fast := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e store.Event
		json.NewDecoder(r.Body).Decode(&e)
		if e.Subject == "slow" {
			<-slow
			w.WriteHeader(503)
		} else {
			fast <- e.ID
		}
	}))
	defer srv.Close()
	defer close(slow)
	hooks.Create("a", store.Webhook{ID: "h", URL: srv.URL, Status: "active", Types: []string{"*"}, Secret: "whsec_a2V5"}, "")
	events.Append("a", "x", "slow", nil)
	a, _ := events.Append("a", "x", "fast", nil)
	b, _ := events.Append("a", "x", "fast", nil)
	d := New("a", events, hooks, &memory.Devices{}, Options{})
	done := make(chan error, 1)
	go func() { done <- d.Dispatch(context.Background()) }()
	for _, want := range []string{a.ID, b.ID} {
		select {
		case got := <-fast:
			if got != want {
				t.Fatal("lane reordered")
			}
		case <-time.After(time.Second):
			t.Fatal("slow lane blocked fast lane")
		}
	}
	// Close cancellation must also stop the HTTP request even while the handler waits.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait/cancel")
	}
	if d.Dispatch(context.Background()) == nil {
		t.Fatal("Dispatch accepted after Close")
	}
}

func TestRotationFilteringAndPagination(t *testing.T) {
	now := time.Now()
	events, hooks, devices := &memory.Events{}, &memory.Webhooks{}, &memory.Devices{}
	received := make(chan string, 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var want []string
		for _, key := range []string{"key", "old"} {
			mac := hmac.New(sha256.New, []byte(key))
			mac.Write([]byte(r.Header.Get("Webhook-Id") + "." + r.Header.Get("Webhook-Timestamp") + "."))
			mac.Write(body)
			want = append(want, "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		}
		signature := r.Header.Get("Webhook-Signature")
		if signature != want[0] && signature != strings.Join(want, " ") {
			t.Error("rotation signature failed verification")
		}
		received <- signature
	}))
	defer srv.Close()
	devices.Create("a", store.Device{Endpoint: "d", Labels: map[string]string{"site": "lab"}})
	for i := 0; i < 501; i++ {
		hooks.Create("a", store.Webhook{ID: "a" + strconv.Itoa(i), URL: srv.URL, Status: "paused", Types: []string{"*"}, Secret: "whsec_a2V5"}, "")
	}
	devices.Create("a", store.Device{Endpoint: "wrong", Labels: map[string]string{"site": "field"}})
	events.Append("a", "device.updated", "wrong", nil)
	for _, tenant := range []string{"a", "other"} {
		hooks.Create(tenant, store.Webhook{ID: "h", URL: srv.URL, Status: "active", Types: []string{"device.updated"}, Selector: "site=lab", Secret: "whsec_a2V5", PreviousSecrets: []store.WebhookSecret{{Secret: "whsec_b2xk", ExpiresAt: now.Add(time.Second)}}}, "")
	}
	// First page contains no matching events; the match is on the second page.
	for i := 0; i < 501; i++ {
		events.Append("a", "ignored", "d", nil)
	}
	events.Append("other", "device.updated", "d", nil)
	events.Append("a", "device.updated", "missing", nil)
	events.Append("a", "device.updated", "d", nil)
	d := New("a", events, hooks, devices, Options{Now: func() time.Time { return now }})
	defer d.Close()
	if err := d.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 {
		t.Fatalf("deliveries=%d want 1", len(received))
	}
	if len(strings.Fields(<-received)) != 2 {
		t.Fatal("missing overlapping key")
	}
	now = now.Add(time.Second)
	events.Append("a", "device.updated", "d", nil)
	d.Dispatch(context.Background())
	if len(received) != 1 || len(strings.Fields(<-received)) != 1 {
		t.Fatal("expired key still signed")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDeadlineAndRedirect(t *testing.T) {
	events, hooks := &memory.Events{}, &memory.Webhooks{}
	events.Append("a", "x", "d", nil)
	hooks.Create("a", store.Webhook{ID: "h", URL: "http://example.invalid", Status: "active", Types: []string{"*"}, Secret: "whsec_a2V5"}, "")
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
			t.Error("missing 10 second request deadline")
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	d := New("a", events, hooks, &memory.Devices{}, Options{Client: client})
	d.Dispatch(context.Background())
	d.Close()
	reached := make(chan bool, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached <- true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	hooks.Patch("a", "h", store.WebhookPatch{URL: &redirect.URL}, store.WebhookMatch{})
	d = New("a", events, hooks, &memory.Devices{}, Options{})
	d.Dispatch(context.Background())
	d.Close()
	if len(reached) != 0 {
		t.Fatal("redirect leaked signed request")
	}
	// Parent cancellation is exercised against a real HTTP server, not a fake transport.
	entered := make(chan struct{}, 1)
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer hanging.Close()
	hooks.Patch("a", "h", store.WebhookPatch{URL: &hanging.URL}, store.WebhookMatch{})
	d = New("a", events, hooks, &memory.Devices{}, Options{})
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if d.Dispatch(ctx) == nil {
		t.Fatal("missing parent cancellation error")
	}
	select {
	case <-entered:
	default:
		t.Fatal("request never started")
	}
}
