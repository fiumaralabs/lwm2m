package operations

import (
	"context"
	"encoding/json"
	"errors"
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
	List(context.Context, string, string) ([]Operation, error)
	Pending(context.Context, string, string) ([]Operation, error)
	Update(context.Context, string, string, Status, Change) error
}

// ExecutorOptions injects the lifecycle clock; nil uses time.Now. Scheduling
// uses real timers, while events and Wait also check the injected clock.
type ExecutorOptions struct{ Now func() time.Time }
type deviceKey struct{ tenant, device string }

// Executor claims queued operations before asynchronously delivering them.
// Wire HandleEvent to Config.OnEvent and HandleQueued to Config.OnQueued.
type Executor struct {
	srv      *server.Server
	st       Operations
	now      func() time.Time
	mu       sync.Mutex
	devices  map[deviceKey]bool
	timers   map[[2]string]*time.Timer
	sessions map[[2]string]string // reject operations belong to one registration
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

func NewExecutor(srv *server.Server, st Operations, opts ExecutorOptions) *Executor {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Executor{srv: srv, st: st, now: opts.Now, devices: make(map[deviceKey]bool), timers: make(map[[2]string]*time.Timer), sessions: make(map[[2]string]string)}
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
	err = e.st.Update(ctx, tenant, id, Queued, Change{Status: Cancelled, At: e.now()})
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
	if o.Type != "read" && o.Type != "write" && o.Type != "execute" {
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
		if _, err := lwm2m.ParsePath(s); err != nil {
			return Operation{}, ErrInvalid
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var session string
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
	e.kickLocked(k)
	if o.TTLSeconds == 0 {
		e.st.Update(context.Background(), tenant, o.ID, Queued, Change{Status: Expired, At: e.now()})
	}
	return e.st.Get(context.Background(), tenant, o.ID)
}
func (e *Executor) kick(k deviceKey) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kickLocked(k)
}
func (e *Executor) kickLocked(k deviceKey) {
	pending, err := e.st.Pending(context.Background(), k.tenant, k.device)
	if err != nil || len(pending) == 0 {
		return
	}
	var live []Operation
	for _, o := range pending {
		if !e.expireLocked(k.tenant, o.ID) {
			live = append(live, o)
		}
	}
	if e.devices[k] || !e.srv.Awake(k.device) || len(live) == 0 {
		return
	}
	o := live[0]
	if e.st.Update(context.Background(), k.tenant, o.ID, Queued, Change{Status: Sent, At: e.now()}) != nil {
		return
	}
	e.stopTimer(k.tenant, o.ID)
	e.devices[k] = true
	go func() {
		r, err := e.deliver(o)
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
		e.st.Update(context.Background(), k.tenant, o.ID, Sent, c)
		e.mu.Lock()
		e.devices[k] = false
		e.mu.Unlock()
		e.kick(k)
	}()
}
func (e *Executor) deliver(o Operation) (outResult resultJSON, deliveryErr error) {
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
			r, err = e.srv.WriteComposite(context.Background(), o.Device, nodes, format(o.Format))
		} else {
			r, err = e.srv.Write(context.Background(), o.Device, p, nodes, server.WriteOptions{Mode: mode, Format: format(o.Format)})
		}
	} else if o.Type == "execute" {
		p, pe := lwm2m.ParsePath(q.Path)
		if pe != nil {
			return resultJSON{}, pe
		}
		r, err = e.srv.Execute(context.Background(), o.Device, p, q.Arguments)
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
			r, err = e.srv.ReadComposite(context.Background(), o.Device, paths, server.CompositeOptions{Accept: format(o.Accept)})
		} else {
			r, err = e.srv.Read(context.Background(), o.Device, p, server.ReadOptions{Accept: format(o.Accept), Prefix: o.Prefix})
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
	for _, n := range r.Nodes {
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
		out.Values = append(out.Values, v)
	}
	return out, nil
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
	for k := range e.devices {
		if k.device != r.Endpoint {
			continue
		}
		pending, err := e.st.Pending(context.Background(), k.tenant, k.device)
		if err != nil {
			continue
		}
		for _, o := range pending {
			key := [2]string{k.tenant, o.ID}
			if o.WhenOffline == "reject" && e.sessions[key] == r.ID {
				e.st.Update(context.Background(), k.tenant, o.ID, Queued, Change{Status: Dropped, At: e.now(), Error: &Error{Code: "queue_dropped", Message: server.ErrQueueDropped.Error()}})
				e.stopTimer(k.tenant, o.ID)
				delete(e.sessions, key)
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
		e.expire(tenant, id)
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
func (e *Executor) expire(tenant, id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expireLocked(tenant, id)
}
func (e *Executor) stopTimer(tenant, id string) {
	k := [2]string{tenant, id}
	if t := e.timers[k]; t != nil {
		t.Stop()
		delete(e.timers, k)
	}
}
func (e *Executor) expireLocked(tenant, id string) bool {
	o, err := e.st.Get(context.Background(), tenant, id)
	if err != nil || o.Status != Queued || o.TTLSeconds == 0 || e.now().Before(o.ExpiresAt) {
		return false
	}
	if e.st.Update(context.Background(), tenant, id, Queued, Change{Status: Expired, At: e.now()}) != nil {
		return false
	}
	e.stopTimer(tenant, id)
	return true
}
