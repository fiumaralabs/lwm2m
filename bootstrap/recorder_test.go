package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestRecorderCapturesSuccessfulAndFailedSessions(t *testing.T) {
	store := NewMemoryConfigStore()
	if err := store.Put("ep", c1("coap://example.com", "identity", "secret")); err != nil {
		t.Fatal(err)
	}
	records := make(chan Result, 2)
	s, results := fakeBS(t, Config{Configs: store, Recorder: func(r Result) error { records <- r; return nil }})
	peer := &fakePeer{id: nosec(), fn: func(ctx context.Context, n int, m *server.Message) (*server.Message, error) { return answer(m), nil }}
	start(t, s, peer, "ep")
	wait(t, results)
	success := <-records
	if success.Err != nil || len(success.Steps) == 0 || success.StartedAt.IsZero() || success.EndedAt.Before(success.StartedAt) {
		t.Fatalf("invalid success: %+v", success)
	}
	failedPeer := &fakePeer{id: nosec(), fn: func(ctx context.Context, n int, m *server.Message) (*server.Message, error) {
		return nil, errors.New("exchange failed")
	}}
	start(t, s, failedPeer, "ep")
	wait(t, results)
	failed := <-records
	if failed.Err == nil || len(failed.Steps) != 1 || failed.EndedAt.Before(failed.StartedAt) {
		t.Fatalf("invalid failure: %+v", failed)
	}
	select {
	case r := <-records:
		t.Fatalf("extra record: %+v", r)
	case <-time.After(20 * time.Millisecond):
	}
}
