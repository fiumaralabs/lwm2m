package memory

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestDeviceCreateReplayExpiryAndCopies(t *testing.T) {
	var s Devices
	calls := 0
	run := func() (int, http.Header, []byte) {
		calls++
		return 201, http.Header{"Location": []string{"/original"}}, []byte("original")
	}
	_, h, b, _ := s.ReplayDeviceCreate(context.Background(), "tenant", "key", run)
	h.Set("Location", "/changed")
	b[0] = 'X'
	status, h, b, _ := s.ReplayDeviceCreate(context.Background(), "tenant", "key", run)
	if status != 201 || h.Get("Location") != "/original" || string(b) != "original" || calls != 1 {
		t.Fatalf("replay: %d %v %q calls=%d", status, h, b, calls)
	}
	s.replayMu.Lock()
	s.replays[deviceReplayKey{"tenant", "key"}].at = time.Now().Add(-24 * time.Hour)
	s.replayMu.Unlock()
	s.ReplayDeviceCreate(context.Background(), "tenant", "key", run)
	if calls != 2 {
		t.Fatalf("expired replay ran %d times", calls)
	}
}
