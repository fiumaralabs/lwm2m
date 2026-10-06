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
	cq := q.get(reg.ID)
	cq.mu.Lock()
	wasAsleep := q.s.cfg.Now().After(cq.awakeUntil)
	cq.awakeUntil = q.s.cfg.Now().Add(q.s.cfg.QueueAwake)
	close(cq.wakeCh)
	cq.wakeCh = make(chan struct{})
	cq.mu.Unlock()
	if wasAsleep && reg.QueueMode {
		q.s.emit(Awake{Registration: reg})
	}
}

// drop releases waiters of an ended registration.
func (q *queues) drop(id string) {
	q.mu.Lock()
	cq, ok := q.m[id]
	delete(q.m, id)
	q.mu.Unlock()
	if ok {
		cq.mu.Lock()
		cq.dropped = true
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
func (q *queues) run(ctx context.Context, reg *Registration, fn func(*Registration) error) error {
	if !reg.QueueMode {
		return fn(reg)
	}
	cq := q.get(reg.ID)
	cq.serial.Lock()
	defer cq.serial.Unlock()
	notified := false
	for {
		cq.mu.Lock()
		dropped, awake, ch := cq.dropped, !q.s.cfg.Now().After(cq.awakeUntil), cq.wakeCh
		cq.mu.Unlock()
		if dropped {
			return ErrQueueDropped
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
			return ctx.Err()
		}
	}
	cur, ok := q.s.store.ByID(reg.ID)
	if !ok {
		return ErrQueueDropped
	}
	err := fn(cur)
	if err == nil {
		q.wake(cur) // a response is a message from the client
	}
	return err
}
