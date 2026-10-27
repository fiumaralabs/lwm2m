package apiv1

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEventsHTTP2IdleKeepAlive(t *testing.T) {
	s := httptest.NewUnstartedServer(New(Options{Keys: deviceKeys()}))
	s.EnableHTTP2 = true
	s.StartTLS()
	t.Cleanup(s.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer lwm_test_secret")
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != http.StatusOK || res.ProtoMajor != 2 {
		t.Fatalf("status=%d protocol=%s", res.StatusCode, res.Proto)
	}

	r := bufio.NewReader(res.Body)
	// Use the default 15-second interval, longer than the five-second write
	// deadline. A second frame also checks that keepalive writes disarm it.
	for i := 0; i < 2; i++ {
		if frame := eventFrame(t, r); frame != ": keepalive\n\n" {
			t.Fatalf("keepalive %d: got %q", i+1, frame)
		}
	}
}
