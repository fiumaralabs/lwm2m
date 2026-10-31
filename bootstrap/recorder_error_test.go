package bootstrap

import (
	"context"
	"errors"
	"github.com/fiumaralabs/lwm2m/server"
	"testing"
	"time"
)

func TestRecorderErrorDoesNotChangeOutcome(t *testing.T) {
	store := NewMemoryConfigStore()
	if err := store.Put("ep", c1("coap://example.com", "id", "key")); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("store failed")
	reports := make(chan error, 1)
	s, results := fakeBS(t, Config{Configs: store, Recorder: func(Result) error { return failure }, RecordError: func(err error) { reports <- err }})
	peer := &fakePeer{id: nosec(), fn: func(_ context.Context, _ int, m *server.Message) (*server.Message, error) { return answer(m), nil }}
	start(t, s, peer, "ep")
	if r := wait(t, results); r.Err != nil {
		t.Fatalf("session changed by recorder failure: %v", r.Err)
	}
	select {
	case err := <-reports:
		if !errors.Is(err, failure) {
			t.Fatalf("wrong error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("missing recorder error")
	}
}
