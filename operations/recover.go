package operations

import (
	"context"
	"errors"
	"github.com/fiumaralabs/lwm2m/server"
	"time"
)

// Recoverer optionally enumerates durable work left by a previous executor.
type Recoverer interface {
	Unfinished(context.Context) ([]Operation, error)
}

var ErrRecoveryUnsupported = errors.New("operations: store does not support recovery")
var ErrClosed = errors.New("operations: executor closed")

// Close cancels delivery, stops scheduling and waits for all delivery workers.
func (e *Executor) Close() {
	e.mu.Lock()
	e.closed = true
	e.cancel()
	for k, t := range e.retries {
		t.Stop()
		delete(e.retries, k)
	}
	for key, t := range e.timers {
		t.Stop()
		delete(e.timers, key)
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// Start reconciles unfinished work before enabling recovered FIFO delivery.
// Sent work is never resent: its outcome is unknowable after a restart.
func (e *Executor) Start(ctx context.Context) error {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return ErrClosed
	}
	r, ok := e.st.(Recoverer)
	if !ok {
		return ErrRecoveryUnsupported
	}
	rows, err := r.Unfinished(ctx)
	if err != nil {
		e.logger.Error("operation recovery failed", "error", err)
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	keys := map[deviceKey]bool{}
	if e.recoveredZero == nil {
		e.recoveredZero = make(map[[2]string]bool)
	}
	var result error
	for _, o := range rows {
		if o.Status == Queued && o.TTLSeconds == 0 {
			e.recoveredZero[[2]string{o.Tenant, o.ID}] = true
		}
		k := deviceKey{o.Tenant, o.Device}
		keys[k] = true
		if _, ok := e.devices[k]; !ok {
			e.devices[k] = false
		}
		c := Change{At: e.now()}
		switch {
		case o.Status == Sent:
			c.Status = Timeout
			c.Error = &Error{Code: "timeout", Message: "executor restarted before delivery completed"}
		case o.Status != Queued:
			continue
		case !e.now().Before(o.ExpiresAt):
			c.Status = Expired
		case o.WhenOffline == "reject":
			reg, ok := e.srv.Store().ByEndpoint(o.Device)
			if ok {
				e.sessions[[2]string{o.Tenant, o.ID}] = reg.ID
			} else {
				c.Status = Dropped
				c.Error = &Error{Code: "queue_dropped", Message: server.ErrQueueDropped.Error()}
			}
		}
		if c.Status != "" {
			if err := e.update(ctx, o.Tenant, o.ID, o.Status, c); err != nil {
				result = errors.Join(result, err)
				e.retryLocked(k)
			} else {
				e.stopTimer(o.Tenant, o.ID)
			}
			continue
		}
		e.timers[[2]string{o.Tenant, o.ID}] = time.AfterFunc(max(0, o.ExpiresAt.Sub(e.now())), func() { e.kick(k) })
	}
	for k := range keys {
		result = errors.Join(result, e.kickLocked(k))
	}
	return result
}

func (e *Executor) update(ctx context.Context, tenant, id string, from Status, c Change) error {
	err := e.st.Update(ctx, tenant, id, from, c)
	if err != nil {
		e.logger.Error("operation update failed", "tenant", tenant, "operation", id, "status", c.Status, "error", err)
	}
	return err
}

// One delayed retry per device prevents hot loops during store outages.
func (e *Executor) retryLocked(k deviceKey) {
	if e.closed || e.retries[k] != nil {
		return
	}
	e.retries[k] = time.AfterFunc(50*time.Millisecond, func() { e.mu.Lock(); defer e.mu.Unlock(); delete(e.retries, k); e.kickLocked(k) })
}
