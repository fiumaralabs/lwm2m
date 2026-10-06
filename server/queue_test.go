package server

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/link"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// slowPeer answers every request after a delay and records how many
// requests were in flight at once.
type slowPeer struct {
	inFlight, max atomic.Int32
}

func (p *slowPeer) Exchange(ctx context.Context, req *Message) (*Message, error) {
	n := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		m := p.max.Load()
		if n <= m || p.max.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(30 * time.Millisecond)
	return &Message{Code: codes.Content}, nil
}
func (p *slowPeer) Identity() Identity   { return Identity{Mode: ModeNoSec, Addr: "fake"} }
func (p *slowPeer) RemoteAddr() net.Addr { return &net.UDPAddr{} }
func (p *slowPeer) Binding() string      { return "U" }

// Proves: QM-02
// Requests held for a queue-mode client are sent one at a time once it
// wakes (NSTART=1, T §6.5): the transport never sees two in flight. A
// non-queue client is not serialised by the queue.
func TestQueueSerialisesRequests(t *testing.T) {
	for _, queue := range []bool{true, false} {
		h := newHarness(t)
		peer := &slowPeer{}
		reg := &Registration{ID: "q1", Endpoint: "ser", Version: "1.1", QueueMode: queue,
			Objects: []link.Object{{ID: 3, Instances: []uint16{0}}}, peer: peer,
			RegisteredAt: h.clock.Now(), LastUpdate: h.clock.Now()}
		h.srv.Store().Add(reg)
		if queue {
			h.clock.Add(time.Hour) // asleep: requests are held
		}
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := h.srv.Read(h.ctx, "ser", p("/3/0/0"), ReadOptions{}); err != nil {
					t.Error(err)
				}
			}()
		}
		if queue {
			time.Sleep(50 * time.Millisecond)
			if peer.max.Load() != 0 {
				t.Fatal("request sent to a sleeping client")
			}
			h.srv.Wake("ser")
		}
		wg.Wait()
		if queue && peer.max.Load() != 1 {
			t.Fatalf("queue mode: %d requests in flight, want 1", peer.max.Load())
		}
		if !queue && peer.max.Load() < 2 {
			t.Fatalf("non-queue client serialised (max %d)", peer.max.Load())
		}
	}
}
