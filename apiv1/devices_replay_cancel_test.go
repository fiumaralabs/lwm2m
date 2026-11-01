package apiv1

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type signallingReader struct {
	io.Reader
	started chan struct{}
}

func (r signallingReader) Read(p []byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	return r.Reader.Read(p)
}

func TestDeviceCreateReplayCancelledRetry(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	started := make(chan struct{})
	request := func(body io.Reader) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices", body)
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", "cancel-retry")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- request(signallingReader{reader, started}) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not start reading")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(`{"endpoint":"other"}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer lwm_test_secret")
	r.Header.Set("Idempotency-Key", "cancel-retry")
	cancelled := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), r); close(cancelled) }()
	select {
	case <-cancelled:
		t.Fatal("retry returned before cancellation or first response")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancelled retry remained blocked on first request")
	}
	live := make(chan *httptest.ResponseRecorder, 1)
	go func() { live <- request(strings.NewReader(`{"endpoint":"other"}`)) }()
	select {
	case <-live:
		t.Fatal("live retry returned before first response")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := io.WriteString(writer, `{"endpoint":"original"}`); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	var original, replay *httptest.ResponseRecorder
	select {
	case original = <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not complete")
	}
	select {
	case replay = <-live:
	case <-time.After(2 * time.Second):
		t.Fatal("live retry did not complete")
	}
	if original.Code != 201 || replay.Code != original.Code || replay.Body.String() != original.Body.String() || replay.Header().Get("Location") != original.Header().Get("Location") {
		t.Fatalf("original: %d %s; replay: %d %s", original.Code, original.Body, replay.Code, replay.Body)
	}
}
