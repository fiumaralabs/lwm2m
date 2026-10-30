package operations

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/server"
)

// Operations is the durable operation store contract. store.Operations aliases
// this interface to keep the executor independent of its implementations.
type Operations interface {
	Create(context.Context, string, Operation) error
	Get(context.Context, string, string) (Operation, error)
	List(context.Context, string, ListFilter, string, int) ([]Operation, string, error)
	Pending(context.Context, string, string) ([]Operation, error)
	Update(context.Context, string, string, Status, Change) error
}

// ExecutorOptions injects the lifecycle clock; nil uses time.Now. Scheduling
// uses real timers, while events and Wait also check the injected clock.
type ExecutorOptions struct {
	// State records successful results before they become visible to Wait callers.
	State  interface{ Record(Operation) error }
	Now    func() time.Time
	Logger *slog.Logger
}
type deviceKey struct{ tenant, device string }

// Executor claims queued operations before asynchronously delivering them.
// Wire HandleEvent to Config.OnEvent and HandleQueued to Config.OnQueued.
type Executor struct {
	state      interface{ Record(Operation) error }
	expiryOnly map[[2]string]bool // zero-TTL operations whose immediate delivery chance has ended
	logger     *slog.Logger
	retries    map[deviceKey]*time.Timer
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	wg         sync.WaitGroup
	srv        *server.Server
	st         Operations
	now        func() time.Time
	mu         sync.Mutex
	devices    map[deviceKey]bool
	timers     map[[2]string]*time.Timer
	sessions   map[[2]string]string // reject operations belong to one registration
}
type request struct {
	Arguments string   `json:"arguments"`
	Paths     []string `json:"paths"`
	Path      string   `json:"path"`
	Mode      string   `json:"mode"`
	Values    []struct {
		Path  string          `json:"path"`
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	} `json:"values"`
}
type valueJSON struct {
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Prefix    string     `json:"prefix,omitempty"`
	Path      string     `json:"path"`
	Type      string     `json:"type,omitempty"`
	Value     any        `json:"value,omitempty"`
}
type resultJSON struct {
	decodeErr error
	Payload   []byte      `json:"payload,omitempty"`
	Format    string      `json:"format,omitempty"`
	Code      string      `json:"code"`
	Values    []valueJSON `json:"values,omitempty"`
}

// Store returns the operation store used by the executor.
func (e *Executor) Store() Operations { return e.st }

func NewExecutor(srv *server.Server, st Operations, opts ExecutorOptions) *Executor {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Executor{state: opts.State, expiryOnly: make(map[[2]string]bool), logger: opts.Logger, retries: make(map[deviceKey]*time.Timer), ctx: ctx, cancel: cancel, srv: srv, st: st, now: opts.Now, devices: make(map[deviceKey]bool), timers: make(map[[2]string]*time.Timer), sessions: make(map[[2]string]string)}
}

var ErrNotRegistered = server.ErrNotRegistered
var ErrInvalid = errors.New("operations: invalid operation")
var ErrConflict = errors.New("operations: operation is not queued")

type encodeError struct{ error }

// ErrPrefixUnsupported identifies an operation the server cannot route with a prefix.
type ErrPrefixUnsupported struct{ Type string }

func (e *ErrPrefixUnsupported) Error() string { return "operations: prefix unsupported for " + e.Type }

// Cancel atomically cancels only a queued operation; sent operations cannot be recalled.
func (e *Executor) Cancel(ctx context.Context, tenant, id string) (Operation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, err := e.st.Get(ctx, tenant, id)
	if err != nil {
		return o, err
	}
	if o.Status != Queued {
		return o, ErrConflict
	}
	err = e.update(ctx, tenant, id, Queued, Change{Status: Cancelled, At: e.now()})
	if errors.Is(err, ErrTransition) {
		err = ErrConflict
	}
	if err != nil {
		return o, err
	}
	e.stopTimer(tenant, id)
	return e.st.Get(ctx, tenant, id)
}

// Submit persists an operation initialized by New. Delivery outlives ctx once
// accepted; TTL zero is claimed or expired before Submit returns.
func (e *Executor) Submit(ctx context.Context, tenant string, o Operation) (Operation, error) {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return Operation{}, ErrClosed
	}
	if o.Type != "read" && o.Type != "write" && o.Type != "execute" && o.Type != "delete" {
		return Operation{}, ErrInvalid
	}
	var q request
	if json.Unmarshal(o.Request, &q) != nil {
		return Operation{}, ErrInvalid
	}
	paths := q.Paths
	if o.Prefix != "" && (o.Type != "read" || len(paths) > 1) {
		return Operation{}, &ErrPrefixUnsupported{Type: o.Type}
	}
	if o.Type != "read" {
		paths = []string{q.Path}
		if o.Type == "write" && q.Path == "" {
			paths = []string{"/"}
		}
	}
	if len(paths) == 0 {
		return Operation{}, ErrInvalid
	}
	for _, s := range paths {
		if p, err := lwm2m.ParsePath(s); err != nil || o.Type == "delete" && !p.IsInstance() {
			return Operation{}, ErrInvalid
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var session string
	if e.closed {
		return Operation{}, ErrClosed
	}
	if o.WhenOffline == "reject" {
		r, ok := e.srv.Store().ByEndpoint(o.Device)
		if !ok {
			return Operation{}, ErrNotRegistered
		}
		session = r.ID
	}
	if err := e.st.Create(ctx, tenant, o); err != nil {
		return Operation{}, err
	}
	k := deviceKey{tenant, o.Device}
	if o.WhenOffline == "reject" {
		e.sessions[[2]string{tenant, o.ID}] = session
	}
	if _, ok := e.devices[k]; !ok {
		e.devices[k] = false
	}
	if o.TTLSeconds > 0 {
		e.timers[[2]string{tenant, o.ID}] = time.AfterFunc(max(0, o.ExpiresAt.Sub(e.now())), func() { e.kick(k) })
	}
	err := e.kickLocked(k)
	if o.TTLSeconds == 0 {
		// The immediate-send chance has ended. Guard before any fallible store
		// call, and use the conditional update rather than a separate lookup.
		e.expiryOnly[[2]string{tenant, o.ID}] = true
		updateErr := e.st.Update(ctx, tenant, o.ID, Queued, Change{Status: Expired, At: e.now()})
		if updateErr == nil || errors.Is(updateErr, ErrTransition) {
			// A transition conflict means the operation was already claimed or
			// terminal. Neither case needs queued-only bookkeeping or expiry.
			e.stopTimer(tenant, o.ID)
		} else {
			e.logger.Error("operation expiry failed", "tenant", tenant, "operation", o.ID, "error", updateErr)
			err = errors.Join(err, updateErr)
			e.retryLocked(k)
		}
	}
	got, getErr := e.st.Get(ctx, tenant, o.ID)
	return got, errors.Join(err, getErr)
}
func (e *Executor) kick(k deviceKey) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kickLocked(k)
}
func (e *Executor) kickLocked(k deviceKey) (result error) {
	if e.closed {
		return
	}
	pending, err := e.st.Pending(context.Background(), k.tenant, k.device)
	if err != nil {
		e.logger.Error("pending operations failed", "tenant", k.tenant, "device", k.device, "error", err)
		e.retryLocked(k)
		return err
	}
	if len(pending) == 0 {
		return
	}
	var live []Operation
	for _, o := range pending {
		expired, expireErr := e.expireLocked(k.tenant, o.ID)
		result = errors.Join(result, expireErr)
		if expireErr != nil {
			e.retryLocked(k)
			continue
		}
		if expired {
			continue
		}
		if o.WhenOffline == "reject" {
			r, ok := e.srv.Store().ByEndpoint(o.Device)
			if !ok || r.ID != e.sessions[[2]string{k.tenant, o.ID}] {
				if err := e.update(context.Background(), k.tenant, o.ID, Queued, Change{Status: Dropped, At: e.now(), Error: &Error{Code: "queue_dropped", Message: server.ErrQueueDropped.Error()}}); err == nil {
					e.stopTimer(k.tenant, o.ID)
				} else {
					result = errors.Join(result, err)
					e.retryLocked(k)
				}
				continue
			}
		}
		live = append(live, o)
	}
	if e.devices[k] || !e.srv.Awake(k.device) || len(live) == 0 {
		return
	}
	for _, o := range live {
		session := e.sessions[[2]string{k.tenant, o.ID}]
		if err := e.update(context.Background(), k.tenant, o.ID, Queued, Change{Status: Sent, At: e.now()}); err != nil {
			result = errors.Join(result, err)
			e.retryLocked(k)
			continue
		}
		e.stopTimer(k.tenant, o.ID)
		e.devices[k] = true
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			r, err := e.deliver(o, session)
			c := Change{Status: Succeeded, At: e.now()}
			if err != nil {
				c.Status = Failed
				c.Error = &Error{Code: "transport", Message: err.Error()}
				var enc *encodeError
				if errors.As(err, &enc) || o.Type == "write" && errors.Is(err, server.ErrBadRequest) {
					c.Error.Code = "encode"
				}
				if errors.Is(err, context.DeadlineExceeded) {
					c.Status = Timeout
					c.Error.Code = "timeout"
				}
				if errors.Is(err, server.ErrQueueDropped) || session != "" && errors.Is(err, server.ErrNotRegistered) {
					c.Status = Dropped
					c.Error = &Error{Code: "queue_dropped", Message: server.ErrQueueDropped.Error()}
				}
			} else {
				c.Result, _ = json.Marshal(r)
				if r.decodeErr != nil {
					c.Error = &Error{Code: "decode", Message: r.decodeErr.Error()}
				}
				if len(r.Code) > 0 && r.Code[0] != '2' {
					c.Status = Failed
					c.Error = &Error{Code: deviceErrorCodes[r.Code], Message: "device returned " + r.Code}
					if c.Error.Code == "" {
						c.Error.Code = r.Code
					}
				}
			}
			e.mu.Lock()
			if !e.closed {
				if c.Status == Succeeded && e.state != nil {
					snapshot := o
					snapshot.Status, snapshot.Result, snapshot.CompletedAt = c.Status, c.Result, &c.At
					if err := e.state.Record(snapshot); err != nil {
						e.logger.Error("record device state", "error", err)
					}
				}
				e.update(context.Background(), k.tenant, o.ID, Sent, c)
			}
			e.devices[k] = false
			e.mu.Unlock()
			e.kick(k)
		}()
		return
	}
	return
}
func (e *Executor) deliver(o Operation, session string) (outResult resultJSON, deliveryErr error) {
	ctx := e.ctx
	if session != "" {
		ctx = server.WithRegistration(ctx, session)
	}
	var q request
	if err := json.Unmarshal(o.Request, &q); err != nil {
		return resultJSON{}, err
	}
	var r *server.Response
	var err error
	if o.Type == "write" {
		preparing := true
		defer func() {
			if preparing && deliveryErr != nil {
				deliveryErr = &encodeError{deliveryErr}
			}
		}()
		if o.Format != "" && format(o.Format) == nil {
			return resultJSON{}, codec.ErrUnsupportedFormat
		}
		path := q.Path
		if path == "" {
			path = "/"
		}
		p, pe := lwm2m.ParsePath(path)
		if pe != nil {
			return resultJSON{}, pe
		}
		var nodes []lwm2m.Node
		for _, v := range q.Values {
			vp, pe := lwm2m.ParsePath(v.Path)
			if pe != nil {
				return resultJSON{}, pe
			}
			if v.Type == "" && len(v.Value) == 0 {
				kind := lwm2m.KindEmptyMultiple
				if vp.IsInstance() {
					kind = lwm2m.KindEmptyInstance
				} else if !vp.IsResource() {
					return resultJSON{}, ErrInvalid
				}
				nodes = append(nodes, lwm2m.Node{Path: vp, Kind: kind})
				continue
			}
			kind := v.Type
			if kind == "" {
				if reg, ok := e.srv.Store().ByEndpoint(o.Device); ok && e.srv.Config().Schema != nil {
					if schema := e.srv.Config().Schema(reg); schema != nil {
						if def, ok := schema.Resource(vp); ok {
							kind = def.Type.String()
						}
					}
				}
			}
			n, err := decodeValue(kind, v.Value)
			if err != nil {
				return resultJSON{}, err
			}
			nodes = append(nodes, lwm2m.ValueNode(vp, n))
		}
		mode := server.PartialUpdate
		cf := format(o.Format)
		if cf == nil && q.Path == "" {
			f := lwm2m.FormatSenMLCBOR
			cf = &f
		}
		if cf != nil {
			c, err := codec.For(*cf)
			if err != nil {
				return resultJSON{}, err
			}
			if reg, ok := e.srv.Store().ByEndpoint(o.Device); ok {
				c = codec.WithRoot(c, reg.RootPath)
			}
			if _, err := c.Encode(p, nodes); err != nil {
				return resultJSON{}, err
			}
		}
		preparing = false
		if q.Mode == "replace" {
			mode = server.Replace
		}
		if q.Path == "" {
			r, err = e.srv.WriteComposite(ctx, o.Device, nodes, format(o.Format))
		} else {
			r, err = e.srv.Write(ctx, o.Device, p, nodes, server.WriteOptions{Mode: mode, Format: format(o.Format)})
		}
		if err == nil && r != nil {
			r.Nodes = nodes
		}
	} else if o.Type == "delete" {
		p, pe := lwm2m.ParsePath(q.Path)
		if pe != nil {
			return resultJSON{}, pe
		}
		r, err = e.srv.Delete(ctx, o.Device, p)
	} else if o.Type == "execute" {
		p, pe := lwm2m.ParsePath(q.Path)
		if pe != nil {
			return resultJSON{}, pe
		}
		r, err = e.srv.Execute(ctx, o.Device, p, q.Arguments)
	} else {
		p, pe := lwm2m.ParsePath(q.Paths[0])
		if pe != nil {
			return resultJSON{}, pe
		}
		if len(q.Paths) > 1 {
			var paths []lwm2m.Path
			for _, s := range q.Paths {
				p, _ := lwm2m.ParsePath(s)
				paths = append(paths, p)
			}
			r, err = e.srv.ReadComposite(ctx, o.Device, paths, server.CompositeOptions{Accept: format(o.Accept)})
		} else {
			r, err = e.srv.Read(ctx, o.Device, p, server.ReadOptions{Accept: format(o.Accept), Prefix: o.Prefix})
		}
	}
	if err != nil {
		return resultJSON{}, err
	}
	out := resultJSON{Code: server.CodeString(r.Code), decodeErr: r.DecodeErr}
	if r.DecodeErr != nil || len(r.Nodes) == 0 {
		out.Payload = r.Payload
	}
	if r.HasFormat {
		for name, f := range formats {
			if r.ContentFormat.Canonical() == f {
				out.Format = name
				break
			}
		}
	}
	out.Values = ObservationValues(r.Nodes)
	return out, nil
}

// ObservationValues uses the operation value projection for notification payloads.
func ObservationValues(nodes []lwm2m.Node) []valueJSON {
	values := make([]valueJSON, 0, len(nodes))
	for _, n := range nodes {
		v := valueJSON{Path: n.Path.String(), Type: n.Value.Type.String(), Value: encodeValue(n.Value)}
		v.Prefix = n.Prefix
		if n.HasTime {
			at := time.Unix(0, int64(n.Time*1e9)).UTC()
			v.Timestamp = &at
		}
		if n.Kind != lwm2m.KindValue {
			v.Type = ""
			v.Value = nil
		}
		values = append(values, v)
	}
	return values
}
func (e *Executor) HandleEvent(ev server.Event) {
	switch v := ev.(type) {
	case server.Registered:
		e.dropRegistration(v.Replaced)
		e.HandleQueued(v.Registration)
	case server.Deregistered:
		e.dropRegistration(v.Registration)
	case server.Updated:
		e.HandleQueued(v.Registration)
	case server.Awake:
		e.HandleQueued(v.Registration)
	}
}
func (e *Executor) HandleQueued(r *server.Registration) {
	if r == nil {
		return
	}
	e.mu.Lock()
	var keys []deviceKey
	for k := range e.devices {
		if k.device == r.Endpoint {
			keys = append(keys, k)
		}
	}
	e.mu.Unlock()
	for _, k := range keys {
		e.kick(k)
	}
}

func (e *Executor) dropRegistration(r *server.Registration) {
	if r == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	for k := range e.devices {
		if k.device != r.Endpoint {
			continue
		}
		pending, err := e.st.Pending(context.Background(), k.tenant, k.device)
		if err != nil {
			e.logger.Error("pending operations failed", "error", err)
			e.retryLocked(k)
			continue
		}
		for _, o := range pending {
			key := [2]string{k.tenant, o.ID}
			if o.WhenOffline == "reject" && e.sessions[key] == r.ID {
				if e.update(context.Background(), k.tenant, o.ID, Queued, Change{Status: Dropped, At: e.now(), Error: &Error{Code: "queue_dropped", Message: server.ErrQueueDropped.Error()}}) == nil {
					e.stopTimer(k.tenant, o.ID)
				}
			}
		}
	}
}

// Wait returns a terminal operation or its current stored state with ctx.Err().
// Cancelling a wait never cancels the operation or its delivery.
func (e *Executor) Wait(ctx context.Context, tenant, id string) (Operation, error) {
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := e.expire(tenant, id); err != nil {
			return Operation{}, err
		}
		o, err := e.st.Get(context.Background(), tenant, id)
		if err != nil || o.Status != Queued && o.Status != Sent {
			return o, err
		}
		select {
		case <-ctx.Done():
			o, err = e.st.Get(context.Background(), tenant, id)
			if err != nil {
				return o, err
			}
			return o, ctx.Err()
		case <-tick.C:
		}
	}
}

var deviceErrorCodes = map[string]string{
	"4.00": "bad_request", "4.01": "unauthorized", "4.02": "bad_option",
	"4.03": "forbidden", "4.04": "not_found", "4.05": "method_not_allowed",
	"4.06": "not_acceptable", "4.08": "request_entity_incomplete",
	"4.12": "precondition_failed", "4.13": "request_entity_too_large",
	"4.15": "unsupported_content_format", "5.00": "internal_server_error",
	"5.01": "not_implemented", "5.03": "service_unavailable",
}

var formats = map[string]lwm2m.ContentFormat{"text": 0, "link-format": 40, "opaque": 42, "cbor": 60, "senml-json": 110, "senml-cbor": 112, "senml-etch-json": 320, "senml-etch-cbor": 322, "tlv": 11542, "oma-json": 11543, "lwm2m-cbor": 11544}

func format(s string) *lwm2m.ContentFormat {
	f, ok := formats[s]
	if !ok {
		return nil
	}
	return &f
}
func decodeValue(kind string, raw json.RawMessage) (lwm2m.Value, error) {
	typ, err := lwm2m.ParseType(kind)
	v := lwm2m.Value{Type: typ}
	if err != nil {
		return v, err
	}
	s := strings.Trim(string(raw), `"`)
	switch typ {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		err = json.Unmarshal(raw, &v.Str)
	case lwm2m.TypeInteger:
		v.Int, err = strconv.ParseInt(s, 10, 64)
	case lwm2m.TypeUnsigned:
		v.Uint, err = strconv.ParseUint(s, 10, 64)
	case lwm2m.TypeFloat:
		switch s {
		case "Infinity":
			v.Float = math.Inf(1)
		case "-Infinity":
			v.Float = math.Inf(-1)
		default:
			v.Float, err = strconv.ParseFloat(s, 64)
		}
	case lwm2m.TypeBoolean:
		err = json.Unmarshal(raw, &v.Bool)
	case lwm2m.TypeOpaque:
		err = json.Unmarshal(raw, &v.Bytes)
	case lwm2m.TypeTime:
		var t time.Time
		t, err = time.Parse(time.RFC3339, s)
		v.Int = t.Unix()
	case lwm2m.TypeObjlnk:
		v.Link, err = lwm2m.ParseObjLink(s)
	default:
		err = ErrInvalid
	}
	return v, err
}
func encodeValue(v lwm2m.Value) any {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return v.Str
	case lwm2m.TypeInteger:
		if v.Int > 9007199254740991 || v.Int < -9007199254740991 {
			return strconv.FormatInt(v.Int, 10)
		}
		return v.Int
	case lwm2m.TypeUnsigned:
		if v.Uint > 9007199254740991 {
			return strconv.FormatUint(v.Uint, 10)
		}
		return v.Uint
	case lwm2m.TypeFloat:
		if math.IsNaN(v.Float) {
			return "NaN"
		}
		if math.IsInf(v.Float, 1) {
			return "Infinity"
		}
		if math.IsInf(v.Float, -1) {
			return "-Infinity"
		}
		return v.Float
	case lwm2m.TypeBoolean:
		return v.Bool
	case lwm2m.TypeOpaque:
		return v.Bytes
	case lwm2m.TypeTime:
		return time.Unix(v.Int, 0).UTC().Format(time.RFC3339)
	case lwm2m.TypeObjlnk:
		return v.Link.String()
	}
	return nil
}
func (e *Executor) expire(tenant, id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expireLocked(tenant, id)
}

// stopTimer releases queued-only bookkeeping on claim or terminal transition.
func (e *Executor) stopTimer(tenant, id string) {
	k := [2]string{tenant, id}
	delete(e.expiryOnly, k)
	delete(e.sessions, k)
	if t := e.timers[k]; t != nil {
		t.Stop()
		delete(e.timers, k)
	}
}
func (e *Executor) expireLocked(tenant, id string) (bool, error) {
	if e.closed {
		return false, nil
	}
	o, err := e.st.Get(context.Background(), tenant, id)
	if err != nil {
		e.logger.Error("operation lookup failed", "error", err)
		return false, err
	}
	if o.Status != Queued || o.TTLSeconds == 0 && !e.expiryOnly[[2]string{tenant, id}] || e.now().Before(o.ExpiresAt) {
		return false, nil
	}
	if err := e.update(context.Background(), tenant, id, Queued, Change{Status: Expired, At: e.now()}); err != nil {
		return false, err
	}
	e.stopTimer(tenant, id)
	return true, nil
}
