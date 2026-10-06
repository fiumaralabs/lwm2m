package testclient

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/attr"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// This file implements the client side of the Core spec that a server
// relies on: notification attributes (C §7.3.2), Execute of the Server
// object's triggers (E.2 /1/x/4, /1/x/8, /1/x/9) and notification storing
// while offline (E.2 /1/x/6, hqmax). It makes the test client a reference
// peer for proving server behaviour end to end.

// obsState is the attribute state of one observation.
type obsState struct {
	mu       sync.Mutex
	last     time.Time              // last notification sent
	notified map[lwm2m.Path]float64 // numeric value at the last notification
	boolLast map[lwm2m.Path]bool
	pminT    *time.Timer // deferred notification inside pmin (ATT-03)
	pmaxT    *time.Timer // periodic notification (ATT-04)
	stopped  bool
	stored   []storedNotification // hqmax history while offline (ATT-08)
}

type storedNotification struct {
	at    time.Time
	nodes []lwm2m.Node
}

// applyAttrs merges a Write-Attributes query into the stored attributes of
// p; a valueless attribute unsets it at that level (DM-07).
func (c *Client) applyAttrs(p lwm2m.Path, q []string) {
	w, err := attr.ParseQuery(strings.Join(q, "&"))
	if err != nil {
		return
	}
	stored, _ := attr.ParseQuery(strings.Join(c.attrs[p], "&"))
	if len(c.attrs[p]) == 0 {
		stored = nil
	}
	merged := attr.Apply(stored, w)
	if len(merged) == 0 {
		delete(c.attrs, p)
		return
	}
	c.attrs[p] = strings.Split(merged.Query(), "&")
}

// effectiveAttrs resolves the attributes that govern an observation of p:
// server defaults pmin=/1/x/2, pmax=/1/x/3, then the levels from the object
// down to p (attributes below the observed level are ignored, OBS-07),
// then attributes from the Observe request (1.2, OBS-06).
func (c *Client) effectiveAttrs(p lwm2m.Path, query []string) attr.Attrs {
	c.mu.Lock()
	defer c.mu.Unlock()
	var defaults attr.Attrs
	if v, ok := c.values[lwm2m.MustParsePath("/1/0/2")]; ok && v.Int > 0 {
		defaults = append(defaults, attr.Attr{Name: "pmin", Value: uint64(v.Int)})
	}
	if v, ok := c.values[lwm2m.MustParsePath("/1/0/3")]; ok && v.Int > 0 {
		defaults = append(defaults, attr.Attr{Name: "pmax", Value: uint64(v.Int)})
	}
	levels := []attr.Attrs{defaults}
	for n := 1; n <= p.Len(); n++ {
		if q := c.attrs[p.Truncate(n)]; len(q) > 0 {
			a, _ := attr.ParseQuery(strings.Join(q, "&"))
			levels = append(levels, a)
		}
	}
	if len(query) > 0 {
		a, _ := attr.ParseQuery(strings.Join(query, "&"))
		levels = append(levels, a)
	}
	return attr.Resolve(levels...)
}

func seconds(v any) time.Duration {
	switch x := v.(type) {
	case uint64:
		return time.Duration(x) * time.Second
	case float64:
		return time.Duration(x * float64(time.Second))
	}
	return 0
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func numeric(v lwm2m.Value) (float64, bool) {
	switch v.Type {
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		return float64(v.Int), true
	case lwm2m.TypeUnsigned:
		return float64(v.Uint), true
	case lwm2m.TypeFloat:
		return v.Float, true
	}
	return 0, false
}

// startObservation records the initial state and arms pmax.
func (c *Client) startObservation(o *observer) {
	o.st = &obsState{last: time.Now(), notified: map[lwm2m.Path]float64{}, boolLast: map[lwm2m.Path]bool{}}
	c.snapshot(o)
	c.armPmax(o)
}

func (c *Client) stopObservation(o *observer) {
	if o == nil || o.st == nil {
		return
	}
	o.st.mu.Lock()
	o.st.stopped = true
	if o.st.pminT != nil {
		o.st.pminT.Stop()
	}
	if o.st.pmaxT != nil {
		o.st.pmaxT.Stop()
	}
	o.st.mu.Unlock()
}

// snapshot stores the values that later conditions compare against.
func (c *Client) snapshot(o *observer) {
	for _, op := range o.paths {
		for _, n := range c.Nodes(op) {
			if f, ok := numeric(n.Value); ok {
				o.st.notified[n.Path] = f
			}
			if n.Value.Type == lwm2m.TypeBoolean {
				o.st.boolLast[n.Path] = n.Value.Bool
			}
		}
	}
}

func (c *Client) armPmax(o *observer) {
	if o.composite {
		return
	}
	a := c.effectiveAttrs(o.paths[0], o.query)
	pmax := seconds(a.Get("pmax"))
	pmin := seconds(a.Get("pmin"))
	if pmax <= 0 || pmax < pmin { // pmax < pmin is ignored (ATT-04)
		return
	}
	o.st.mu.Lock()
	defer o.st.mu.Unlock()
	if o.st.pmaxT != nil {
		o.st.pmaxT.Stop()
	}
	if o.st.stopped {
		return
	}
	o.st.pmaxT = time.AfterFunc(pmax, func() { c.fire(o) })
}

// conditionsMet evaluates gt, lt, st and edge for the observed resource
// (ATT-05, ATT-08). Without any of them every change qualifies.
func (c *Client) conditionsMet(o *observer, a attr.Attrs) bool {
	gt, hasGT := number(a.Get("gt"))
	lt, hasLT := number(a.Get("lt"))
	st, hasST := number(a.Get("st"))
	edge, hasEdge := a.Get("edge").(bool)
	if !hasGT && !hasLT && !hasST && !hasEdge {
		return true
	}
	for _, n := range c.Nodes(o.paths[0]) {
		if f, ok := numeric(n.Value); ok {
			prev, seen := o.st.notified[n.Path]
			if !seen {
				return true
			}
			if hasGT && (prev <= gt) != (f <= gt) {
				return true
			}
			if hasLT && (prev < lt) != (f < lt) {
				return true
			}
			if hasST && math.Abs(f-prev) >= st {
				return true
			}
		}
		if n.Value.Type == lwm2m.TypeBoolean && hasEdge {
			prev := o.st.boolLast[n.Path]
			if prev != n.Value.Bool && n.Value.Bool == edge {
				return true
			}
		}
	}
	return false
}

// evaluate handles a change of an observed value: notify now, or when pmin
// expires (ATT-03), if the conditions hold (OBS-07).
func (c *Client) evaluate(o *observer) {
	if o.st == nil {
		_, _ = c.Notify(context.Background(), o.token)
		return
	}
	var a attr.Attrs
	if !o.composite {
		a = c.effectiveAttrs(o.paths[0], o.query)
		if !c.conditionsMet(o, a) {
			return
		}
	}
	pmin := seconds(a.Get("pmin"))
	o.st.mu.Lock()
	wait := time.Until(o.st.last.Add(pmin))
	if wait > 0 {
		if o.st.pminT == nil && !o.st.stopped {
			o.st.pminT = time.AfterFunc(wait, func() {
				o.st.mu.Lock()
				o.st.pminT = nil
				o.st.mu.Unlock()
				c.fire(o)
			})
		}
		o.st.mu.Unlock()
		return
	}
	o.st.mu.Unlock()
	c.fire(o)
}

// fire sends a notification for o and re-arms pmax.
func (c *Client) fire(o *observer) {
	o.st.mu.Lock()
	if o.st.stopped {
		o.st.mu.Unlock()
		return
	}
	o.st.last = time.Now()
	o.st.mu.Unlock()
	c.snapshot(o)
	confirmable := true
	if !o.composite {
		if con, ok := c.effectiveAttrs(o.paths[0], o.query).Get("con").(bool); ok {
			confirmable = con // ATT-08: con decides CON/NON
		} else if mode, ok := c.Get(lwm2m.MustParsePath("/1/0/26")); ok {
			confirmable = mode.Int == 1 // OBS-09: Default Notification Mode
		}
	}
	if c.Offline() {
		c.store(o)
	} else if confirmable {
		_, _ = c.Notify(context.Background(), o.token)
	} else {
		_ = c.NotifyNON(context.Background(), o.token)
	}
	c.armPmax(o)
}

// --- offline storage (hqmax, /1/x/6) -------------------------------------

// SetOffline simulates a client that cannot reach the server. Notifications
// are stored, at most hqmax per observation with the oldest dropped first
// (ATT-08); going online sends them as one time-stamped SenML batch.
func (c *Client) SetOffline(off bool) {
	c.mu.Lock()
	c.offline = off
	obs := make([]*observer, 0, len(c.observers))
	for _, o := range c.observers {
		obs = append(obs, o)
	}
	c.mu.Unlock()
	if off {
		return
	}
	for _, o := range obs {
		c.flushStored(o)
	}
}

// Offline reports SetOffline's state.
func (c *Client) Offline() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offline
}

func (c *Client) store(o *observer) {
	if o.composite {
		return
	}
	hq, _ := c.effectiveAttrs(o.paths[0], o.query).Get("hqmax").(uint64)
	if hq == 0 {
		return // only parts with hqmax > 0 are stored
	}
	nodes := c.Nodes(o.paths[0])
	o.st.mu.Lock()
	o.st.stored = append(o.st.stored, storedNotification{at: time.Now(), nodes: nodes})
	if len(o.st.stored) > int(hq) {
		o.st.stored = o.st.stored[len(o.st.stored)-int(hq):]
	}
	o.st.mu.Unlock()
}

func (c *Client) flushStored(o *observer) {
	if o.st == nil {
		return
	}
	o.st.mu.Lock()
	stored := o.st.stored
	o.st.stored = nil
	o.st.mu.Unlock()
	if len(stored) == 0 {
		return
	}
	var batch []lwm2m.Node
	for _, s := range stored {
		for _, n := range s.nodes {
			n.Time, n.HasTime = float64(s.at.Unix()), true
			batch = append(batch, n)
		}
	}
	cd, _ := codec.For(lwm2m.FormatSenMLCBOR)
	body, err := cd.Encode(o.paths[0], batch)
	if err != nil {
		return
	}
	_, _ = c.NotifyRaw(context.Background(), o.token, lwm2m.FormatSenMLCBOR, body)
}

// NotifyNON sends a non-confirmable notification for token tok.
func (c *Client) NotifyNON(ctx context.Context, tok message.Token) error {
	c.mu.Lock()
	o, ok := c.observers[string(tok)]
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	if !ok {
		return nil
	}
	nodes := c.Nodes(o.paths[0])
	_, cf, body, _ := c.encode(o.paths[0], c.responseFormat(o.paths[0], o.accept, nodes), nodes)
	if cf == nil {
		return nil
	}
	m := c.conn.AcquireMessage(ctx)
	defer c.conn.ReleaseMessage(m)
	m.SetCode(codes.Content)
	m.SetType(message.NonConfirmable)
	m.SetToken(tok)
	m.SetObserve(seq)
	m.SetContentFormat(message.MediaType(*cf))
	m.SetBody(strings.NewReader(string(body)))
	m.SetMessageID(c.conn.GetMessageID())
	return c.conn.Session().WriteMessage(m)
}

// --- Server object triggers ----------------------------------------------

// ExecHook runs when an executable resource is executed.
type ExecHook func(r Request)

// onExecute implements the Server object's executable resources.
func (c *Client) onExecute(p lwm2m.Path, r Request) {
	if p.Object() != 1 {
		return
	}
	ctx := context.Background()
	switch p.Resource() {
	case 8: // Registration Update Trigger (REG-19, GEN-15)
		if arg := string(r.Body); strings.HasPrefix(arg, "0='") && strings.HasSuffix(arg, "'") {
			c.mu.Lock()
			c.bindingOverride = arg[3 : len(arg)-1]
			c.mu.Unlock()
		}
		_, _ = c.Update(ctx, nil, nil)
	case 4: // Disable (REG-24)
		_, _ = c.Deregister(ctx)
		d := 86400 * time.Second
		if v, ok := c.Get(lwm2m.NewPath(1, p.Instance(), 5)); ok {
			d = time.Duration(v.Int) * time.Second
		}
		time.AfterFunc(d, func() { _, _ = c.Register(context.Background()) })
	case 9: // Bootstrap-Request Trigger (REG-19)
		c.mu.Lock()
		h := c.bootstrapHook
		c.mu.Unlock()
		if h != nil {
			h(r)
		}
	}
}

// SetBootstrapTrigger installs the action for Execute /1/x/9.
func (c *Client) SetBootstrapTrigger(h ExecHook) {
	c.mu.Lock()
	c.bootstrapHook = h
	c.mu.Unlock()
}

// BindingOverride returns the binding requested by the last Execute
// /1/x/8 argument, "" if none.
func (c *Client) BindingOverride() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bindingOverride
}
