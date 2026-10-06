package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
)

// OSCORE is the server's OSCORE layer (T §5.4, RFC 8613) over the CoAP
// bindings. It verifies protected requests from clients, protects the
// responses, protects Device Management requests to clients (the Server is
// then the OSCORE client, OSC-06) and verifies their responses and
// notifications. It sits in serveCoAP, under the LwM2M core, so a request
// reaches HandleUplink only after OSCORE verified it.
//
// Freshness (OSC-04, RFC 8613 Appendix B.1.2, RFC 9175): the first request
// on a context after it was added, or after a server restart, is answered
// with a protected 4.01 carrying an Echo option; only the repeated request
// with that Echo is processed, and its Partial IV becomes the lower limit
// of the replay window. A Register is always the first request of a new
// context, so it always goes through Echo.
type OSCORE struct {
	s *Server
	// EchoLifetime is how long an Echo value proves freshness (RFC 9175
	// §2.3). Default 60 s.
	EchoLifetime time.Duration

	mu    sync.Mutex
	byRID map[string][]*oscoreEntry // hex Recipient ID
	byEP  map[string]*oscoreEntry
	obs   map[string]*oscoreObs // token of an Observe registration
	peers sync.Map              // oscorePeerKey -> *oscorePeer
}

type oscoreEntry struct {
	ep  string
	ctx *oscore.Context

	mu     sync.Mutex
	fresh  bool
	echo   []byte
	echoAt time.Time
}

type oscoreObs struct {
	e *oscoreEntry
	x *oscore.Exchange
}

// oscoreAddrPrefix marks the Identity of an OSCORE-only peer (see
// oscorePeer.Identity).
const oscoreAddrPrefix = "oscore:"

// EnableOSCORE turns the OSCORE layer on and returns it. Calling it again
// returns the same layer.
func (s *Server) EnableOSCORE() *OSCORE {
	o := &OSCORE{s: s, EchoLifetime: time.Minute, byRID: map[string][]*oscoreEntry{}, byEP: map[string]*oscoreEntry{}, obs: map[string]*oscoreObs{}}
	if !s.coap.oscore.CompareAndSwap(nil, o) {
		return s.coap.oscore.Load()
	}
	return o
}

// ErrDuplicateOSCORERecipient is returned when another endpoint already
// uses the same Recipient ID and ID Context (RFC 8613 §3.3).
var ErrDuplicateOSCORERecipient = errors.New("server: OSCORE Recipient ID already used by another endpoint")

// Put adds or replaces the OSCORE context of endpoint ep. p is the server's
// view: its Sender ID is the client's Recipient ID (/21/x/2) and its
// Recipient ID the client's Sender ID (/21/x/1); use Params.Reverse on the
// client's /21 values. An empty ep binds the endpoint name to the client's
// Sender ID ("ep authenticated by Sender ID", OSC-05); otherwise the
// Register ep must equal ep.
func (o *OSCORE) Put(ep string, p oscore.Params) error {
	if ep == "" {
		ep = string(p.RecipientID)
	}
	c, err := oscore.New(p)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rid := hex.EncodeToString(p.RecipientID)
	for _, e := range o.byRID[rid] {
		if e.ep != ep && bytes.Equal(e.ctx.Params().IDContext, p.IDContext) {
			return ErrDuplicateOSCORERecipient
		}
	}
	o.removeLocked(ep)
	e := &oscoreEntry{ep: ep, ctx: c}
	o.byEP[ep] = e
	o.byRID[rid] = append(o.byRID[rid], e)
	return nil
}

// Remove deletes the context of ep.
func (o *OSCORE) Remove(ep string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.removeLocked(ep)
}

func (o *OSCORE) removeLocked(ep string) bool {
	old, ok := o.byEP[ep]
	if !ok {
		return false
	}
	delete(o.byEP, ep)
	rid := hex.EncodeToString(old.ctx.Params().RecipientID)
	l := o.byRID[rid][:0]
	for _, e := range o.byRID[rid] {
		if e != old {
			l = append(l, e)
		}
	}
	if len(l) == 0 {
		delete(o.byRID, rid)
	} else {
		o.byRID[rid] = l
	}
	return true
}

// Context returns the security context of ep.
func (o *OSCORE) Context(ep string) (*oscore.Context, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.byEP[ep]
	if !ok {
		return nil, false
	}
	return e.ctx, true
}

// candidates are the contexts matching a request's kid (and kid context).
func (o *OSCORE) candidates(h oscore.Header) []*oscoreEntry {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []*oscoreEntry
	for _, e := range o.byRID[hex.EncodeToString(h.KID)] {
		if h.KIDContext == nil || bytes.Equal(h.KIDContext, e.ctx.Params().IDContext) {
			out = append(out, e)
		}
	}
	return out
}

func (o *OSCORE) bound(ep string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.byEP[ep]
	return ok
}

// intercept handles a message carrying the OSCORE option, and refuses
// unprotected requests that try to act for an OSCORE endpoint. false hands
// the message to the normal path.
func (o *OSCORE) intercept(w mux.ResponseWriter, m *mux.Message, cc coapConn) bool {
	if !m.HasOption(oscore.OptionOSCORE) {
		return o.guardPlain(w, m)
	}
	in, err := poolToMessage(m.Message)
	if err != nil {
		return true
	}
	if isResponseCode(m.Code()) {
		o.notification(m, cc, in)
		return true
	}
	o.request(w, m, cc, in)
	return true
}

// guardPlain answers 4.01 to an unprotected Register, Update or
// De-register for an endpoint that has an OSCORE context, and drops
// unprotected notifications for OSCORE observations: such a client must
// use OSCORE (OSC-08), so the plain message is not from it (T Tbl 6.7-2).
func (o *OSCORE) guardPlain(w mux.ResponseWriter, m *mux.Message) bool {
	if isResponseCode(m.Code()) {
		o.mu.Lock()
		_, protected := o.obs[string(m.Token())]
		o.mu.Unlock()
		return protected // an unprotected notification for an OSCORE observation is dropped
	}
	p, _ := m.Options().Path()
	seg := strings.Split(strings.Trim(p, "/"), "/")
	ep := ""
	switch {
	case len(seg) == 1 && seg[0] == "rd":
		qs, _ := m.Options().Queries()
		for _, q := range qs {
			if v, ok := strings.CutPrefix(q, "ep="); ok {
				ep = v
			}
		}
	case len(seg) == 2 && seg[0] == "rd":
		if reg, ok := o.s.store.ByID(seg[1]); ok {
			ep = reg.Endpoint
		}
	}
	if ep == "" || !o.bound(ep) {
		return false
	}
	plainError(w, codes.Unauthorized, "")
	return true
}

// plainError sends an unprotected OSCORE error (RFC 8613 §8.2) with Outer
// Max-Age 0 so intermediaries do not cache it (§4.1.3.1).
func plainError(w mux.ResponseWriter, code codes.Code, diag string) {
	var body io.ReadSeeker
	if diag != "" {
		body = strings.NewReader(diag)
	}
	_ = w.SetResponse(code, message.TextPlain, body)
	w.Message().Remove(message.ContentFormat)
	w.Message().SetOptionUint32(message.MaxAge, 0)
}

// request verifies an OSCORE request (RFC 8613 §8.2), applies freshness
// and the endpoint binding, runs it through the LwM2M core and protects
// the response (§8.3).
func (o *OSCORE) request(w mux.ResponseWriter, m *mux.Message, cc coapConn, in message.Message) {
	v, _ := m.Options().GetBytes(oscore.OptionOSCORE)
	h, err := oscore.ParseHeader(v)
	if err != nil || h.KID == nil {
		plainError(w, codes.BadOption, "Failed to decode COSE")
		return
	}
	var e *oscoreEntry
	var inner message.Message
	var x *oscore.Exchange
	err = oscore.ErrNoContext
	for _, c := range o.candidates(h) {
		if inner, x, err = c.ctx.UnprotectRequest(in); err == nil {
			e = c
			break
		}
		if !errors.Is(err, oscore.ErrDecrypt) {
			break
		}
	}
	switch {
	case errors.Is(err, oscore.ErrNoContext):
		plainError(w, codes.Unauthorized, "Security context not found")
		return
	case errors.Is(err, oscore.ErrReplay):
		plainError(w, codes.Unauthorized, "Replay detected")
		return
	case errors.Is(err, oscore.ErrDecrypt):
		plainError(w, codes.BadRequest, "Decryption failed")
		return
	case err != nil:
		plainError(w, codes.BadOption, "Failed to decode COSE")
		return
	}
	if echo, fresh := o.fresh(e, inner, x); !fresh {
		// OSC-04, Appendix B.1.2: protected 4.01 with only Echo, with the
		// server's own Partial IV.
		o.respond(w, e, x, message.Message{Code: codes.Unauthorized, Options: message.Options{{ID: oscore.OptionEcho, Value: echo}}}, true)
		return
	}
	msg, err := poolFromMessage(m.Message, inner)
	if err != nil {
		plainError(w, codes.BadRequest, "")
		return
	}
	peer := o.peer(cc, e)
	if code := o.bind(msg, e, peer); code != 0 {
		o.respond(w, e, x, message.Message{Code: code}, false)
		return
	}
	resp, after := o.s.HandleUplink(peer, msg)
	o.s.coap.defer_(cc, after)
	if resp == nil {
		resp = status(codes.Changed)
	}
	o.respond(w, e, x, toCoAPMessage(resp), false)
}

// fresh reports whether the context has proven freshness since it was
// added; otherwise it checks the request's Echo against the one sent, and
// returns a new Echo value to send when that fails.
func (o *OSCORE) fresh(e *oscoreEntry, inner message.Message, x *oscore.Exchange) ([]byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fresh {
		return nil, true
	}
	now := o.s.cfg.Now()
	if got, err := inner.Options.GetBytes(oscore.OptionEcho); err == nil && e.echo != nil &&
		bytes.Equal(got, e.echo) && !now.After(e.echoAt.Add(o.EchoLifetime)) {
		e.fresh, e.echo = true, nil
		e.ctx.ResetReplayWindow(x.RequestPIV())
		return nil, true
	}
	e.echo = make([]byte, 8)
	_, _ = rand.Read(e.echo)
	e.echoAt = now
	return e.echo, false
}

// bind enforces the endpoint ↔ Sender ID binding (OSC-05, SEC-15): a
// Register's ep must be the context's endpoint (4.00 otherwise) and is
// filled in when omitted; requests on a registration must come from the
// context that registered it.
func (o *OSCORE) bind(msg *Message, e *oscoreEntry, peer Peer) codes.Code {
	seg := msg.segments()
	switch {
	case len(seg) == 1 && seg[0] == "rd" && msg.Code == codes.POST:
		found := false
		for _, q := range msg.Query {
			if v, ok := strings.CutPrefix(q, "ep="); ok {
				if v != e.ep {
					return codes.BadRequest
				}
				found = true
			}
		}
		if !found {
			msg.Query = append(msg.Query, "ep="+e.ep)
		}
	case len(seg) == 2 && seg[0] == "rd":
		if reg, ok := o.s.store.ByID(seg[1]); ok && !reg.Identity.Equal(peer.Identity()) {
			return codes.BadRequest
		}
	}
	return 0
}

func (o *OSCORE) respond(w mux.ResponseWriter, e *oscoreEntry, x *oscore.Exchange, rm message.Message, newPIV bool) {
	prot, err := e.ctx.ProtectResponse(rm, x, newPIV)
	if err != nil {
		plainError(w, codes.InternalServerError, "")
		return
	}
	var body io.ReadSeeker
	if len(prot.Payload) > 0 {
		body = bytes.NewReader(prot.Payload)
	}
	_ = w.SetResponse(prot.Code, message.TextPlain, body)
	w.Message().ResetOptionsTo(prot.Options)
}

// notification verifies a protected notification (RFC 8613 §8.4, §7.4.1)
// and hands it to the core. A notification that fails verification is
// dropped; it does not cancel the observation (§8.4.2).
func (o *OSCORE) notification(m *mux.Message, cc coapConn, in message.Message) {
	tok := string(m.Token())
	o.mu.Lock()
	b := o.obs[tok]
	o.mu.Unlock()
	if b == nil {
		return
	}
	if !o.s.KnownObservation(m.Token()) {
		o.mu.Lock()
		delete(o.obs, tok)
		o.mu.Unlock()
		return
	}
	inner, err := b.e.ctx.UnprotectResponse(in, b.x)
	if err != nil {
		return
	}
	msg, err := poolFromMessage(m.Message, inner)
	if err != nil {
		return
	}
	_, after := o.s.HandleUplink(o.peer(cc, b.e), msg)
	o.s.coap.defer_(cc, after)
}

// bindObservation keeps the request binding of an Observe registration
// for its notifications (RFC 8613 §8) and forgets ended observations.
func (o *OSCORE) bindObservation(tok []byte, e *oscoreEntry, x *oscore.Exchange) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// ponytail: O(n) sweep per Observe registration; index by registration if fleets observe heavily.
	for t := range o.obs {
		if !o.s.KnownObservation([]byte(t)) {
			delete(o.obs, t)
		}
	}
	o.obs[string(tok)] = &oscoreObs{e: e, x: x}
}

type oscorePeerKey struct {
	cc coapConn
	e  *oscoreEntry
}

func (o *OSCORE) peer(cc coapConn, e *oscoreEntry) *oscorePeer {
	k := oscorePeerKey{cc, e}
	if p, ok := o.peers.Load(k); ok {
		return p.(*oscorePeer)
	}
	p, loaded := o.peers.LoadOrStore(k, &oscorePeer{base: o.s.peerOf(cc), cc: cc, o: o, e: e})
	if !loaded {
		go func() {
			<-cc.Context().Done()
			o.peers.Delete(k)
		}()
	}
	return p.(*oscorePeer)
}

// oscorePeer is a CoAP session whose LwM2M traffic is OSCORE-protected
// with one security context.
type oscorePeer struct {
	base Peer
	cc   coapConn
	o    *OSCORE
	e    *oscoreEntry
}

// Identity is the (D)TLS identity when the transport authenticated one
// (OSCORE over DTLS, OSC-08), else the OSCORE context: a NoSec identity
// that is stable across addresses and unique per context.
// ponytail: carried as NoSec + Addr until Identity has an OSCORE mode.
func (p *oscorePeer) Identity() Identity {
	if id := p.base.Identity(); id.Secure() {
		return id
	}
	c := p.e.ctx.Params()
	return Identity{Mode: ModeNoSec, Addr: oscoreAddrPrefix + hex.EncodeToString(c.IDContext) + "/" + hex.EncodeToString(c.RecipientID)}
}

func (p *oscorePeer) RemoteAddr() net.Addr { return p.base.RemoteAddr() }
func (p *oscorePeer) Binding() string      { return p.base.Binding() }

// Exchange protects a request to the client (RFC 8613 §8.1) and verifies
// its response (§8.4). A protected 4.01 with Echo (the client requires
// freshness, RFC 9175 §2.3, OSC-04) is retried once with that Echo. An
// unprotected error (the client's OSCORE layer refused it) is returned
// as is.
func (p *oscorePeer) Exchange(ctx context.Context, req *Message) (*Message, error) {
	pm := p.cc.AcquireMessage(ctx)
	defer p.cc.ReleaseMessage(pm)
	if err := toPool(pm, req); err != nil {
		return nil, err
	}
	pm.SetType(message.Confirmable)
	plain, err := poolToMessage(pm)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		prot, x, err := p.e.ctx.ProtectRequest(plain)
		if err != nil {
			return nil, err
		}
		if x.Observe() {
			p.o.bindObservation(prot.Token, p.e, x)
		}
		pm.SetMessageID(-1)
		pm.ResetOptionsTo(prot.Options)
		pm.SetCode(prot.Code)
		pm.SetBody(bytes.NewReader(prot.Payload))
		res, err := p.cc.Do(pm)
		if err != nil {
			return nil, err
		}
		if !res.HasOption(oscore.OptionOSCORE) {
			out, err := fromPool(res)
			p.cc.ReleaseMessage(res)
			return out, err
		}
		rm, err := poolToMessage(res)
		if err != nil {
			p.cc.ReleaseMessage(res)
			return nil, err
		}
		inner, err := p.e.ctx.UnprotectResponse(rm, x)
		if err != nil {
			p.cc.ReleaseMessage(res)
			return nil, fmt.Errorf("server: OSCORE response: %w", err)
		}
		if echo, e := inner.Options.GetBytes(oscore.OptionEcho); e == nil && inner.Code == codes.Unauthorized && attempt == 0 {
			p.cc.ReleaseMessage(res)
			plain.Options = setOption(plain.Options, message.Option{ID: oscore.OptionEcho, Value: echo})
			continue
		}
		out, err := poolFromMessage(res, inner)
		p.cc.ReleaseMessage(res)
		return out, err
	}
}

// setOption replaces or inserts opt, keeping options sorted.
func setOption(opts message.Options, opt message.Option) message.Options {
	out := message.Options{}
	done := false
	for _, o := range opts {
		if o.ID == opt.ID {
			continue
		}
		if !done && o.ID > opt.ID {
			out = append(out, opt)
			done = true
		}
		out = append(out, o)
	}
	if !done {
		out = append(out, opt)
	}
	return out
}

// poolToMessage copies a go-coap pool message into a plain message.
func poolToMessage(pm *pool.Message) (message.Message, error) {
	opts, err := pm.Options().Clone()
	if err != nil {
		return message.Message{}, err
	}
	body, err := pm.ReadBody()
	if err != nil {
		return message.Message{}, err
	}
	return message.Message{Code: pm.Code(), Token: append([]byte(nil), pm.Token()...), Options: opts,
		Payload: body, MessageID: pm.MessageID(), Type: pm.Type()}, nil
}

// poolFromMessage writes a decrypted message into pm (keeping its header)
// and converts it with fromPool.
func poolFromMessage(pm *pool.Message, in message.Message) (*Message, error) {
	pm.ResetOptionsTo(in.Options)
	pm.SetCode(in.Code)
	if len(in.Payload) > 0 {
		pm.SetBody(bytes.NewReader(in.Payload))
	} else {
		pm.SetBody(nil)
	}
	return fromPool(pm)
}

// toCoAPMessage renders a core response as an unprotected CoAP message.
func toCoAPMessage(r *Message) message.Message {
	m := message.Message{Code: r.Code, Payload: r.Payload}
	for _, l := range r.Location {
		m.Options = append(m.Options, message.Option{ID: message.LocationPath, Value: []byte(l)})
	}
	if r.Format != nil {
		m.Options = append(m.Options, message.Option{ID: message.ContentFormat, Value: uintValue(uint32(*r.Format))})
	}
	return m
}

func uintValue(v uint32) []byte {
	var b []byte
	for v > 0 {
		b = append([]byte{byte(v)}, b...)
		v >>= 8
	}
	return b
}
