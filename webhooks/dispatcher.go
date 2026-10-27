// Package webhooks delivers signed CloudEvents. Callers own polling/retry timing;
// there are no background timers. Queues and cursors are in memory: a fresh
// dispatcher replays retained history, without durable receipts or recovery.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/store"
)

var ErrClosed = errors.New("webhook dispatcher closed")

// ErrReset reports a retention gap; available history is still dispatched.
var ErrReset = errors.New("webhook event history reset")

// Options must be configured before use. Now must be concurrency-safe.
type Options struct {
	Now    func() time.Time
	Client *http.Client
}
type Dispatcher struct {
	tenant  string
	events  store.Events
	hooks   store.Webhooks
	devices store.Devices
	now     func() time.Time
	client  *http.Client
	after   string
	lanes   map[[2]string][]*delivery
	serial  sync.Mutex
	life    context.Context
	cancel  context.CancelFunc
}
type delivery struct {
	event       store.Event
	first, next time.Time
	delay       time.Duration
}

// New creates one tenant's dispatcher. It copies the client and disables all
// redirects to prevent forwarding signing material to another destination.
func New(tenant string, events store.Events, hooks store.Webhooks, devices store.Devices, o Options) *Dispatcher {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Client == nil {
		o.Client = http.DefaultClient
	}
	client := *o.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	life, cancel := context.WithCancel(context.Background())
	return &Dispatcher{tenant: tenant, events: events, hooks: hooks, devices: devices, now: o.Now, client: &client, lanes: make(map[[2]string][]*delivery), life: life, cancel: cancel}
}

// Close is idempotent, cancels active requests and waits for Dispatch to exit.
func (d *Dispatcher) Close() error {
	d.cancel()
	d.serial.Lock()
	defer d.serial.Unlock()
	return nil
}

// Dispatch serializes polls, scans paginated history, then runs independent
// webhook/device lanes concurrently. Delivery failures are queued, not returned;
// polling errors, retention gaps and cancellation are returned to the caller.
func (d *Dispatcher) Dispatch(parent context.Context) error {
	d.serial.Lock()
	defer d.serial.Unlock()
	if d.life.Err() != nil {
		return ErrClosed
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(d.life, cancel)
	defer stop()
	var hooks []store.Webhook
	for cursor := ""; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, next, err := d.hooks.List(d.tenant, cursor, 500)
		if err != nil {
			return err
		}
		hooks = append(hooks, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	var result error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, reset, err := d.events.List(d.tenant, store.EventFilter{After: d.after, Limit: 500})
		if err != nil {
			return err
		}
		if reset {
			result = ErrReset
		}
		for _, e := range page {
			for _, h := range hooks {
				if d.matches(h, e) {
					key := [2]string{h.ID, e.Subject}
					d.lanes[key] = append(d.lanes[key], &delivery{event: e})
				}
			}
			d.after = e.Sequence
		}
		if len(page) < 500 {
			break
		}
	}
	var wg sync.WaitGroup
	// Workers own disjoint queue elements; only this goroutine writes the map.
	for key, q := range d.lanes {
		wg.Go(func() { d.run(ctx, key, q) })
	}
	wg.Wait()
	for key, q := range d.lanes {
		for len(q) > 0 && q[0] == nil {
			q = q[1:]
		}
		if len(q) == 0 {
			delete(d.lanes, key)
		} else {
			d.lanes[key] = q
		}
	}
	return errors.Join(result, ctx.Err())
}
func (d *Dispatcher) matches(h store.Webhook, e store.Event) bool {
	if h.Status != "active" {
		return false
	}
	matched := false
	for _, p := range h.Types {
		if e.Type == p || strings.HasSuffix(p, "*") && strings.HasPrefix(e.Type, strings.TrimSuffix(p, "*")) {
			matched = true
			break
		}
	}
	if !matched || h.Selector == "" {
		return matched
	}
	s, err := selector.Parse(h.Selector)
	if err != nil {
		return false
	}
	device, err := d.devices.Get(d.tenant, e.Subject)
	return err == nil && s.Matches(device.Labels)
}
func (d *Dispatcher) run(ctx context.Context, key [2]string, q []*delivery) {
	for i, item := range q {
		if ctx.Err() != nil {
			return
		}
		now := d.now()
		if item.delay != 0 && !now.Before(item.first.Add(24*time.Hour)) {
			q[i] = nil
			continue
		}
		if now.Before(item.next) {
			return
		}
		h, err := d.hooks.Get(d.tenant, key[0])
		if errors.Is(err, store.ErrWebhookNotFound) {
			q[i] = nil
			continue
		}
		if err != nil || h.Status != "active" {
			return
		}
		if item.delay == 0 {
			item.first = now
		}
		if d.send(ctx, h, item.event) {
			q[i] = nil
			continue
		}
		if item.delay == 0 {
			item.delay = time.Second
		} else {
			item.delay = min(item.delay*2, time.Hour)
		}
		item.next = d.now().Add(item.delay)
		return
	}
}
func (d *Dispatcher) send(parent context.Context, h store.Webhook, e store.Event) bool {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	body, err := json.Marshal(e)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	now := d.now()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	secrets := []string{h.Secret}
	for _, s := range h.PreviousSecrets {
		if s.ExpiresAt.After(now) {
			secrets = append(secrets, s.Secret)
		}
	}
	var signatures []string
	for _, secret := range secrets {
		if !strings.HasPrefix(secret, "whsec_") {
			return false
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		if err != nil || len(key) == 0 {
			return false
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(e.ID + "." + timestamp + "."))
		mac.Write(body)
		signatures = append(signatures, "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	req.Header.Set("Content-Type", "application/cloudevents+json")
	req.Header.Set("Webhook-Id", e.ID)
	req.Header.Set("Webhook-Timestamp", timestamp)
	req.Header.Set("Webhook-Signature", strings.Join(signatures, " "))
	res, err := d.client.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 300
}
