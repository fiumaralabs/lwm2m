package server_test

import (
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// Proves: QM-02, REG-12 (client-ecosystem T24)
// A request queued for a sleeping queue-mode client is delivered when the
// client wakes with a new Register instead of an Update, as Anjay does on a
// fresh NoSec socket. The replaced registration's observations still end; its
// queued requests follow the replacement. Found by interop/peers (Anjay).
func TestQueuedRequestFollowsReRegister(t *testing.T) {
	h := newHarness(t)
	c := h.device(testclient.Config{Endpoint: "qr", Queue: true})
	mustCode(mustRegister(h, c))
	old, _ := h.srv.Store().ByEndpoint("qr")
	h.clock.Add(time.Hour) // asleep
	done := make(chan *server.Response, 1)
	errc := make(chan error, 1)
	go func() {
		r, err := h.srv.Read(h.ctx, "qr", p("/3/0/9"), server.ReadOptions{})
		if err != nil {
			errc <- err
			return
		}
		done <- r
	}()
	time.Sleep(100 * time.Millisecond)
	if len(c.Requests()) != 0 {
		t.Fatal("request sent to a sleeping client")
	}
	mustCode(mustRegister(h, c)) // wakes with a re-Register
	if cur, _ := h.srv.Store().ByEndpoint("qr"); cur.ID == old.ID {
		t.Fatal("re-Register kept the registration ID")
	}
	select {
	case r := <-done:
		if !r.Success() {
			t.Fatalf("queued read after re-Register: %s", server.CodeString(r.Code))
		}
	case err := <-errc:
		t.Fatalf("queued read dropped on re-Register: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("queued read not delivered after re-Register")
	}
}
