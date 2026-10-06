package server

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrQueueDropped is returned to requests waiting for a queue-mode client
// whose registration ended.
var ErrQueueDropped = errors.New("server: registration ended while the request was queued")

// queues implements queue mode (T §6.5): requests to a sleeping client wait
// until it next talks to the server, then go out one at a time (QM-02).
type queues struct {
	s  *Server
	mu sync.Mutex
	m  map[string]*clientQueue
}

type clientQueue struct {
	serial     sync.Mutex // NSTART=1: one outstanding request (QM-02)
	mu         sync.Mutex
	awakeUntil time.Time
	wakeCh     chan struct{}
	dropped    bool
	movedTo    string // ID of the registration that replaced this one, if any
}

func newQueues(s *Server) *queues { return &queues{s: s, m: map[string]*clientQueue{}} }

func (q *queues) get(id string) *clientQueue {
	q.mu.Lock()
	defer q.mu.Unlock()
	cq, ok := q.m[id]
	if !ok {
		cq = &clientQueue{wakeCh: make(chan struct{})}
		q.m[id] = cq
	}
	return cq
}

// wake marks a client reachable for QueueAwake from now (QM-03): after
// Register, Update, a notification, Send or any response.
func (q *queues) wake(reg *Registration) {
	q.release(reg, q.markAwake(reg))
}

// release lets requests waiting for reg go out, and emits Awake when the
// client was asleep before.
func (q *queues) release(reg *Registration, wasAsleep bool) {
	cq := q.get(reg.ID)
	cq.mu.Lock()
	close(cq.wakeCh)
	cq.wakeCh = make(chan struct{})
	cq.mu.Unlock()
	if wasAsleep && reg.QueueMode {
		q.s.emit(Awake{Registration: reg})
	}
}

// markAwake records that a client is reachable from now without releasing
// waiters yet: Register and Update call it while handling the request, so
// Awake is right at once, and wake (after the reply is sent, GEN-10) then
// lets queued requests go.
func (q *queues) markAwake(reg *Registration) (wasAsleep bool) {
	cq := q.get(reg.ID)
	cq.mu.Lock()
	defer cq.mu.Unlock()
	wasAsleep = q.s.cfg.Now().After(cq.awakeUntil)
	cq.awakeUntil = q.s.cfg.Now().Add(q.s.cfg.QueueAwake)
	return wasAsleep
}

// drop releases waiters of an ended registration.
func (q *queues) drop(id string) { q.end(id, "") }

// handover ends registration id, which a new Register of the same endpoint
// replaced (REG-12), and moves its waiting requests to the replacement next.
// Clients re-register on wake rather than Update when their transport
// session is new (Anjay NoSec, Anjay Lite on any failure, u-blox after PSM:
// client-ecosystem T24); dropping the queue would starve them of every
// queued request. Observations still end (OBS-03).
func (q *queues) handover(id, next string) { q.end(id, next) }

func (q *queues) end(id, next string) {
	q.mu.Lock()
	cq, ok := q.m[id]
	delete(q.m, id)
	q.mu.Unlock()
	if ok {
		cq.mu.Lock()
		cq.dropped = true
		cq.movedTo = next
		close(cq.wakeCh)
		cq.wakeCh = make(chan struct{})
		cq.mu.Unlock()
	}
}

// Awake reports whether a queue-mode client is currently reachable.
func (s *Server) Awake(ep string) bool {
	reg, ok := s.store.ByEndpoint(ep)
	if !ok {
		return false
	}
	if !reg.QueueMode {
		return true
	}
	cq := s.queues.get(reg.ID)
	cq.mu.Lock()
	defer cq.mu.Unlock()
	return !s.cfg.Now().After(cq.awakeUntil)
}

// run executes fn for reg. For queue-mode clients it waits until the client
// is awake and serialises requests. fn receives the current registration,
// whose connection may have changed while waiting.
// A request queued for a registration that a re-Register replaced follows
// the replacement (handover).
func (q *queues) run(ctx context.Context, reg *Registration, fn func(*Registration) error) error {
	for {
		next, err := q.runOnce(ctx, reg, fn)
		if next == nil {
			return err
		}
		reg = next
	}
}

// runOnce is run for one registration. A non-nil registration means reg was
// replaced before fn ran: retry on it.
func (q *queues) runOnce(ctx context.Context, reg *Registration, fn func(*Registration) error) (*Registration, error) {
	if !reg.QueueMode {
		return nil, fn(reg)
	}
	cq := q.get(reg.ID)
	cq.serial.Lock()
	defer cq.serial.Unlock()
	notified := false
	for {
		cq.mu.Lock()
		dropped, awake, ch, moved := cq.dropped, !q.s.cfg.Now().After(cq.awakeUntil), cq.wakeCh, cq.movedTo
		cq.mu.Unlock()
		if dropped {
			if next, ok := q.s.store.ByID(moved); ok && moved != "" {
				return next, nil
			}
			return nil, ErrQueueDropped
		}
		if awake {
			break
		}
		if q.s.cfg.OnQueued != nil && !notified {
			notified = true
			q.s.cfg.OnQueued(reg) // e.g. send an SMS Registration Update Trigger (T §6.6)
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	cur, ok := q.s.store.ByID(reg.ID)
	if !ok {
		return nil, ErrQueueDropped
	}
	err := fn(cur)
	if err == nil {
		q.wake(cur) // a response is a message from the client
	}
	return nil, err
}
