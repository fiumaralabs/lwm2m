package oscore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Responder is the receiving side of OSCORE for an endpoint that serves
// requests from many peers (the LwM2M Server and the Bootstrap-Server,
// T §5.4.3): it finds the security context of a request, verifies it
// (§8.2), demands Echo freshness on the first use of a context (Appendix
// B.1.2, RFC 9175, LwM2M OSC-04), and re-derives contexts from the
// pre-established input parameters with Appendix B.2 when a peer asks for
// it with a new 'kid context' (OSC-03). It is transport-neutral: CoAP
// messages in, CoAP messages out.
type Responder struct {
	// EchoLifetime is how long an Echo value proves freshness (RFC 9175
	// §2.3). Default 60 s.
	EchoLifetime time.Duration
	// Now is the clock for Echo; default time.Now.
	Now func() time.Time

	mu     sync.Mutex
	byRID  map[string][]*Entry // hex Recipient ID
	byName map[string]*Entry
}

// Entry is the security context of one peer.
type Entry struct {
	Name string
	base Params // as provisioned; Appendix B.2 derives from it

	mu     sync.Mutex
	ctx    *Context
	fresh  bool
	echo   []byte
	echoAt time.Time
	r2     [][]byte // Appendix B.2 nonces sent in responses #1
}

// Context returns the current security context.
func (e *Entry) Context() *Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ctx
}

// Params returns the provisioned input parameters (stable across
// Appendix B.2 re-derivations).
func (e *Entry) Params() Params { return e.base }

// ErrDuplicateRecipient is returned when another peer already uses the
// same Recipient ID and ID Context (§3.3).
var ErrDuplicateRecipient = errors.New("oscore: Recipient ID already used by another peer")

// maxR2 bounds the cached Appendix B.2 R2 values per context (B.2.1).
const maxR2 = 4

// Put adds or replaces the context of the peer name. p is this
// endpoint's view (its Sender ID is the peer's Recipient ID).
func (r *Responder) Put(name string, p Params) error {
	c, err := New(p)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName == nil {
		r.byRID, r.byName = map[string][]*Entry{}, map[string]*Entry{}
	}
	rid := hex.EncodeToString(p.RecipientID)
	for _, e := range r.byRID[rid] {
		if e.Name != name && bytes.Equal(e.base.IDContext, p.IDContext) {
			return ErrDuplicateRecipient
		}
	}
	r.removeLocked(name)
	e := &Entry{Name: name, base: p, ctx: c}
	r.byName[name] = e
	r.byRID[rid] = append(r.byRID[rid], e)
	return nil
}

// Remove deletes the context of name.
func (r *Responder) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removeLocked(name)
}

func (r *Responder) removeLocked(name string) bool {
	old, ok := r.byName[name]
	if !ok {
		return false
	}
	delete(r.byName, name)
	rid := hex.EncodeToString(old.base.RecipientID)
	l := r.byRID[rid][:0]
	for _, e := range r.byRID[rid] {
		if e != old {
			l = append(l, e)
		}
	}
	if len(l) == 0 {
		delete(r.byRID, rid)
	} else {
		r.byRID[rid] = l
	}
	return true
}

// Get returns the entry of name.
func (r *Responder) Get(name string) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byName[name]
	return e, ok
}

func (r *Responder) byKID(kid []byte) []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Entry(nil), r.byRID[hex.EncodeToString(kid)]...)
}

// PlainError is an unprotected OSCORE error response (§8.2) with Outer
// Max-Age 0 so intermediaries do not cache it (§4.1.3.1).
func PlainError(code codes.Code, diag string) message.Message {
	m := message.Message{Code: code, Options: message.Options{{ID: message.MaxAge, Value: []byte{}}}}
	if diag != "" {
		m.Payload = []byte(diag)
	}
	return m
}

// Request is a verified, fresh request.
type Request struct {
	Entry *Entry
	Inner message.Message // the decrypted request
	X     *Exchange
	ctx   *Context
}

// Protect protects the response to q (§8.3).
func (q *Request) Protect(resp message.Message) (message.Message, error) {
	return q.ctx.ProtectResponse(resp, q.X, false)
}

// Verify verifies a protected request. It returns the request to serve,
// or nil and the message to send instead: an unprotected error (§8.2), a
// protected 4.01 carrying Echo (freshness, Appendix B.1.2), or an
// Appendix B.2 response #1.
func (r *Responder) Verify(in message.Message) (*Request, message.Message) {
	v, err := optionValue(in)
	if err != nil {
		return nil, PlainError(codes.BadOption, "Failed to decode COSE")
	}
	h, err := ParseHeader(v)
	if err != nil || h.KID == nil {
		return nil, PlainError(codes.BadOption, "Failed to decode COSE")
	}
	var e *Entry
	var c *Context
	var inner message.Message
	var x *Exchange
	err = ErrNoContext
	for _, cand := range r.byKID(h.KID) {
		cc := cand.Context()
		if h.KIDContext != nil && !bytes.Equal(h.KIDContext, cc.Params().IDContext) {
			continue
		}
		if inner, x, err = cc.UnprotectRequest(in); err == nil {
			e, c = cand, cc
			break
		}
		if !errors.Is(err, ErrDecrypt) {
			break
		}
	}
	if errors.Is(err, ErrNoContext) && h.KIDContext != nil {
		var reply *message.Message
		if e, c, inner, x, reply = r.rederive(h, in); reply != nil {
			return nil, *reply
		}
		if e != nil {
			err = nil
		}
	}
	switch {
	case errors.Is(err, ErrNoContext):
		return nil, PlainError(codes.Unauthorized, "Security context not found")
	case errors.Is(err, ErrReplay):
		return nil, PlainError(codes.Unauthorized, "Replay detected")
	case errors.Is(err, ErrDecrypt):
		return nil, PlainError(codes.BadRequest, "Decryption failed")
	case err != nil:
		return nil, PlainError(codes.BadOption, "Failed to decode COSE")
	}
	if echo, ok := r.fresh(e, c, inner, x); !ok {
		// A protected 4.01 with only Echo, with our own Partial IV.
		prot, err := c.ProtectResponse(message.Message{Code: codes.Unauthorized, Options: message.Options{{ID: OptionEcho, Value: echo}}}, x, true)
		if err != nil {
			return nil, PlainError(codes.InternalServerError, "")
		}
		return nil, prot
	}
	return &Request{Entry: e, Inner: inner, X: x, ctx: c}, message.Message{}
}

func (r *Responder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// fresh reports whether e has proven freshness since it was added or
// re-derived; otherwise it checks the request's Echo against the one
// sent, and returns a new Echo value when that fails. The verified
// Partial IV becomes the lower limit of the replay window.
func (r *Responder) fresh(e *Entry, c *Context, inner message.Message, x *Exchange) ([]byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fresh && e.ctx == c {
		return nil, true
	}
	life := r.EchoLifetime
	if life == 0 {
		life = time.Minute
	}
	now := r.now()
	if got, err := inner.Options.GetBytes(OptionEcho); err == nil && e.echo != nil && e.ctx == c &&
		bytes.Equal(got, e.echo) && !now.After(e.echoAt.Add(life)) {
		e.fresh, e.echo = true, nil
		c.ResetReplayWindow(x.RequestPIV())
		return nil, true
	}
	e.echo, e.echoAt = random(8), now
	return e.echo, false
}

// rederive runs the server side of Appendix B.2 for a request whose
// 'kid context' names no current context:
//
//   - request #2: kid context R2||R3 with an R2 this endpoint sent; on
//     verification the context derived with ID Context R2||R3 replaces the
//     peer's context (still subject to Echo freshness);
//   - request #1: kid context ID1; on verification the request is not
//     served: the reply is a 4.01 protected with the context derived from
//     ID Context R2||ID1, carrying R2 as 'kid context' and an Echo.
func (r *Responder) rederive(h Header, in message.Message) (e *Entry, c *Context, inner message.Message, x *Exchange, reply *message.Message) {
	for _, cand := range r.byKID(h.KID) {
		cand.mu.Lock()
		for _, r2 := range cand.r2 {
			if len(h.KIDContext) <= len(r2) || !bytes.HasPrefix(h.KIDContext, r2) {
				continue
			}
			p := cand.base
			p.IDContext = append([]byte{}, h.KIDContext...)
			nc, err := New(p)
			if err != nil {
				continue
			}
			if inner, x, err = nc.UnprotectRequest(in); err != nil {
				continue
			}
			cand.ctx, cand.r2, cand.fresh = nc, nil, false // the Echo sent in response #1 stays valid
			cand.mu.Unlock()
			return cand, nc, inner, x, nil
		}
		p := cand.base
		id1 := append([]byte{}, h.KIDContext...)
		p.IDContext = id1
		c1, err := New(p)
		if err != nil {
			cand.mu.Unlock()
			continue
		}
		_, x1, err := c1.UnprotectRequest(in)
		if err != nil {
			cand.mu.Unlock()
			continue
		}
		r2 := random(8)
		p.IDContext = append(append([]byte{}, r2...), id1...)
		c2, err := New(p)
		if err != nil {
			cand.mu.Unlock()
			continue
		}
		cand.r2 = append(cand.r2, r2)
		if len(cand.r2) > maxR2 {
			cand.r2 = cand.r2[len(cand.r2)-maxR2:]
		}
		// The Echo is checked on request #2, under the context it installs.
		cand.echo, cand.echoAt = random(8), r.now()
		echo := cand.echo
		cand.mu.Unlock()
		prot, err := c2.protectResponse(message.Message{Code: codes.Unauthorized, Options: message.Options{{ID: OptionEcho, Value: echo}}}, x1, true, r2)
		if err != nil {
			m := PlainError(codes.InternalServerError, "")
			return nil, nil, inner, nil, &m
		}
		return nil, nil, inner, nil, &prot
	}
	return nil, nil, inner, nil, nil
}

// RoundTrip protects a request with c (§8.1), sends it with send and
// verifies the response (§8.4). A protected 4.01 with Echo (the peer
// requires freshness, RFC 9175 §2.3) is retried once with that Echo. An
// unprotected response (the peer's OSCORE layer refused the request) is
// returned as is.
func RoundTrip(c *Context, plain message.Message, send func(prot message.Message, x *Exchange) (message.Message, error)) (message.Message, error) {
	for attempt := 0; ; attempt++ {
		prot, x, err := c.ProtectRequest(plain)
		if err != nil {
			return message.Message{}, err
		}
		res, err := send(prot, x)
		if err != nil {
			return message.Message{}, err
		}
		if _, ok := get(res.Options, OptionOSCORE); !ok {
			return res, nil
		}
		inner, err := c.UnprotectResponse(res, x)
		if err != nil {
			return message.Message{}, err
		}
		if echo, e := inner.Options.GetBytes(OptionEcho); e == nil && inner.Code == codes.Unauthorized && attempt == 0 {
			plain.Options = SetOption(plain.Options, message.Option{ID: OptionEcho, Value: echo})
			continue
		}
		return inner, nil
	}
}

// SetOption replaces or inserts opt, keeping options sorted.
func SetOption(opts message.Options, opt message.Option) message.Options {
	out := message.Options{}
	for _, o := range opts {
		if o.ID != opt.ID {
			out = append(out, o)
		}
	}
	return sorted(append(out, opt))
}

// Entries returns all entries.
func (r *Responder) Entries() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Entry, 0, len(r.byName))
	for _, e := range r.byName {
		out = append(out, e)
	}
	return out
}
