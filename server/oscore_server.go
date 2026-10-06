package server

import (
	"bytes"
	"context"
	"encoding/hex"
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
	*oscore.Responder // contexts, Echo freshness, Appendix B.2 (shared with the Bootstrap-Server)
	s                 *Server

	mu    sync.Mutex
	obs   map[string]*oscoreObs // token of an Observe registration
	peers sync.Map              // oscorePeerKey -> *oscorePeer
}

type oscoreObs struct {
	e *oscore.Entry
	x *oscore.Exchange
}

// oscoreAddrPrefix marks the Identity of an OSCORE-only peer (see
// OSCOREIdentity).
const oscoreAddrPrefix = "oscore:"

// OSCOREIdentity is the Identity of a peer authenticated only by OSCORE:
// a NoSec identity that is stable across addresses and unique per context
// (ID Context and Recipient ID of the provisioned parameters p).
// ponytail: carried as NoSec + Addr until Identity has an OSCORE mode.
func OSCOREIdentity(p oscore.Params) Identity {
	return Identity{Mode: ModeNoSec, Addr: oscoreAddrPrefix + hex.EncodeToString(p.IDContext) + "/" + hex.EncodeToString(p.RecipientID)}
}

// EnableOSCORE turns the OSCORE layer on and returns it. Calling it again
// returns the same layer.
func (s *Server) EnableOSCORE() *OSCORE {
	o := &OSCORE{Responder: &oscore.Responder{EchoLifetime: time.Minute, Now: s.cfg.Now}, s: s, obs: map[string]*oscoreObs{}}
	if !s.coap.oscore.CompareAndSwap(nil, o) {
		return s.coap.oscore.Load()
	}
	return o
}

// ErrDuplicateOSCORERecipient is returned when another endpoint already
// uses the same Recipient ID and ID Context (RFC 8613 §3.3).
var ErrDuplicateOSCORERecipient = oscore.ErrDuplicateRecipient

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
	return o.Responder.Put(ep, p)
}

// Context returns the security context of ep.
func (o *OSCORE) Context(ep string) (*oscore.Context, bool) {
	e, ok := o.Get(ep)
	if !ok {
		return nil, false
	}
	return e.Context(), true
}

func (o *OSCORE) bound(ep string) bool {
	_, ok := o.Get(ep)
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
	peerOf := func(e *oscore.Entry) *oscorePeer { return o.peer(cc, e) }
	if isResponseCode(m.Code()) {
		o.s.coap.defer_(cc, o.notification(in, peerOf))
		return true
	}
	out, after := o.serve(in, peerOf)
	o.s.coap.defer_(cc, after)
	writeCoAP(w, out)
	return true
}

// CoAPWire is a binding that moves whole CoAP messages outside go-coap,
// e.g. SMS (T §5.3.1: SMS NoSec plus /0/x/17 = SMS protected by OSCORE).
// Its Peer methods describe the transport session; ExchangeCoAP sends one
// request, options included, and returns the response as received.
type CoAPWire interface {
	Peer
	ExchangeCoAP(ctx context.Context, req message.Message) (message.Message, error)
}

// HandleCoAP is the OSCORE layer for a CoAPWire binding, as for UDP: a
// protected request is verified (Echo freshness on first use, OSC-04),
// served by the core with a Peer whose downlinks are protected, and reply
// is the protected response; a protected notification is verified and
// delivered (reply nil); an unprotected Register, Update or De-register
// for an OSCORE endpoint gets an unprotected 4.01, and an unprotected
// notification for an OSCORE observation is dropped. handled false: an
// unprotected message OSCORE does not concern, for the binding's normal
// path. The binding sends reply, then calls after (never nil).
func (o *OSCORE) HandleCoAP(w CoAPWire, in message.Message) (reply *message.Message, after func(), handled bool) {
	peerOf := func(e *oscore.Entry) *oscorePeer { return o.wirePeer(w, e) }
	resp := isResponseCode(in.Code)
	switch {
	case in.Options.HasOption(oscore.OptionOSCORE):
	case !o.guard(resp, in.Token, in.Options):
		return nil, func() {}, false
	case resp:
		return nil, func() {}, true
	default:
		m := oscore.PlainError(codes.Unauthorized, "")
		return &m, func() {}, true
	}
	if resp {
		return nil, o.notification(in, peerOf), true
	}
	out, after := o.serve(in, peerOf)
	return &out, after, true
}

// guardPlain answers 4.01 to an unprotected Register, Update or
// De-register for an endpoint that has an OSCORE context, and drops
// unprotected notifications for OSCORE observations (see guard).
func (o *OSCORE) guardPlain(w mux.ResponseWriter, m *mux.Message) bool {
	resp := isResponseCode(m.Code())
	if !o.guard(resp, m.Token(), m.Options()) {
		return false
	}
	if !resp {
		plainError(w, codes.Unauthorized, "")
	}
	return true
}

// guard reports an unprotected message to refuse: a Register, Update or
// De-register for an endpoint that has an OSCORE context, or a
// notification for an OSCORE observation. Such a client must use OSCORE
// (OSC-08), so the plain message is not from it (T Tbl 6.7-2).
func (o *OSCORE) guard(resp bool, token []byte, opts message.Options) bool {
	if resp {
		o.mu.Lock()
		_, protected := o.obs[string(token)]
		o.mu.Unlock()
		return protected
	}
	p, _ := opts.Path()
	seg := strings.Split(strings.Trim(p, "/"), "/")
	ep := ""
	switch {
	case len(seg) == 1 && seg[0] == "rd":
		qs, _ := opts.Queries()
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
	return ep != "" && o.bound(ep)
}

// plainError sends an unprotected OSCORE error (RFC 8613 §8.2).
func plainError(w mux.ResponseWriter, code codes.Code, diag string) {
	writeCoAP(w, oscore.PlainError(code, diag))
}

// writeCoAP sends m as the response, its options replacing go-coap's.
func writeCoAP(w mux.ResponseWriter, m message.Message) {
	var body io.ReadSeeker
	if len(m.Payload) > 0 {
		body = bytes.NewReader(m.Payload)
	}
	_ = w.SetResponse(m.Code, message.TextPlain, body)
	w.Message().ResetOptionsTo(m.Options)
}

// serve verifies an OSCORE request (RFC 8613 §8.2) with freshness
// (OSC-04), applies the endpoint binding, runs it through the LwM2M core
// with the peer of its context and returns the protected response (§8.3)
// and the core's after.
func (o *OSCORE) serve(in message.Message, peerOf func(*oscore.Entry) *oscorePeer) (message.Message, func()) {
	nop := func() {}
	q, reply := o.Verify(in)
	if q == nil {
		return reply, nop
	}
	msg, err := MessageFromCoAP(q.Inner)
	if err != nil {
		return oscore.PlainError(codes.BadRequest, ""), nop
	}
	peer := peerOf(q.Entry)
	if code := o.bind(msg, q.Entry, peer); code != 0 {
		return protect(q, message.Message{Code: code}), nop
	}
	resp, after := o.s.HandleUplink(peer, msg)
	if resp == nil {
		resp = status(codes.Changed)
	}
	return protect(q, toCoAPMessage(resp)), after
}

// bind enforces the endpoint ↔ Sender ID binding (OSC-05, SEC-15): a
// Register's ep must be the context's endpoint (4.00 otherwise) and is
// filled in when omitted; requests on a registration must come from the
// context that registered it.
func (o *OSCORE) bind(msg *Message, e *oscore.Entry, peer Peer) codes.Code {
	seg := msg.segments()
	switch {
	case len(seg) == 1 && seg[0] == "rd" && msg.Code == codes.POST:
		found := false
		for _, q := range msg.Query {
			if v, ok := strings.CutPrefix(q, "ep="); ok {
				if v != e.Name {
					return codes.BadRequest
				}
				found = true
			}
		}
		if !found {
			msg.Query = append(msg.Query, "ep="+e.Name)
		}
	case len(seg) == 2 && seg[0] == "rd":
		if reg, ok := o.s.store.ByID(seg[1]); ok && !reg.Identity.Equal(peer.Identity()) {
			return codes.BadRequest
		}
	}
	return 0
}

func protect(q *oscore.Request, rm message.Message) message.Message {
	prot, err := q.Protect(rm)
	if err != nil {
		return oscore.PlainError(codes.InternalServerError, "")
	}
	return prot
}

// notification verifies a protected notification (RFC 8613 §8.4, §7.4.1)
// and hands it to the core. A notification that fails verification is
// dropped; it does not cancel the observation (§8.4.2). It returns the
// core's after.
func (o *OSCORE) notification(in message.Message, peerOf func(*oscore.Entry) *oscorePeer) func() {
	nop := func() {}
	tok := string(in.Token)
	o.mu.Lock()
	b := o.obs[tok]
	o.mu.Unlock()
	if b == nil {
		return nop
	}
	if !o.s.KnownObservation(in.Token) {
		o.mu.Lock()
		delete(o.obs, tok)
		o.mu.Unlock()
		return nop
	}
	inner, err := b.e.Context().UnprotectResponse(in, b.x)
	if err != nil {
		return nop
	}
	msg, err := MessageFromCoAP(inner)
	if err != nil {
		return nop
	}
	_, after := o.s.HandleUplink(peerOf(b.e), msg)
	return after
}

// bindObservation keeps the request binding of an Observe registration
// for its notifications (RFC 8613 §8) and forgets ended observations.
func (o *OSCORE) bindObservation(tok []byte, e *oscore.Entry, x *oscore.Exchange) {
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
	conn any // coapConn or CoAPWire
	e    *oscore.Entry
}

func (o *OSCORE) peer(cc coapConn, e *oscore.Entry) *oscorePeer {
	k := oscorePeerKey{cc, e}
	if p, ok := o.peers.Load(k); ok {
		return p.(*oscorePeer)
	}
	do := func(ctx context.Context, prot message.Message) (message.Message, error) {
		pm := cc.AcquireMessage(ctx)
		defer cc.ReleaseMessage(pm)
		prot.MessageID, prot.Type = -1, message.Confirmable
		pm.SetMessage(prot)
		res, err := cc.Do(pm)
		if err != nil {
			return message.Message{}, err
		}
		defer cc.ReleaseMessage(res)
		return poolToMessage(res)
	}
	p, loaded := o.peers.LoadOrStore(k, &oscorePeer{base: o.s.peerOf(cc), do: do, o: o, e: e})
	if !loaded {
		go func() {
			<-cc.Context().Done()
			o.peers.Delete(k)
		}()
	}
	return p.(*oscorePeer)
}

// wirePeer is peer for a CoAPWire.
// ponytail: wire peers live as long as the server; a replaced context leaves its old peer behind (one per Put).
func (o *OSCORE) wirePeer(w CoAPWire, e *oscore.Entry) *oscorePeer {
	p, _ := o.peers.LoadOrStore(oscorePeerKey{w, e}, &oscorePeer{base: w, do: w.ExchangeCoAP, o: o, e: e})
	return p.(*oscorePeer)
}

// oscorePeer is a session whose LwM2M traffic is OSCORE-protected with
// one security context.
type oscorePeer struct {
	base Peer
	do   func(ctx context.Context, prot message.Message) (message.Message, error) // one protected exchange
	o    *OSCORE
	e    *oscore.Entry
}

// Identity is the (D)TLS identity when the transport authenticated one
// (OSCORE over DTLS, OSC-08), else the OSCORE context's (OSCOREIdentity).
func (p *oscorePeer) Identity() Identity {
	if id := p.base.Identity(); id.Secure() {
		return id
	}
	return OSCOREIdentity(p.e.Params())
}

func (p *oscorePeer) RemoteAddr() net.Addr { return p.base.RemoteAddr() }
func (p *oscorePeer) Binding() string      { return p.base.Binding() }

// Exchange protects a request to the client (RFC 8613 §8.1) and verifies
// its response (§8.4), with one Echo retry (oscore.RoundTrip, OSC-04).
func (p *oscorePeer) Exchange(ctx context.Context, req *Message) (*Message, error) {
	plain, err := CoAPMessage(req)
	if err != nil {
		return nil, err
	}
	inner, err := oscore.RoundTrip(p.e.Context(), plain, func(prot message.Message, x *oscore.Exchange) (message.Message, error) {
		if x.Observe() {
			p.o.bindObservation(prot.Token, p.e, x)
		}
		return p.do(ctx, prot)
	})
	if err != nil {
		return nil, fmt.Errorf("server: OSCORE: %w", err)
	}
	return MessageFromCoAP(inner)
}

// CoAPMessage renders a Message as a plain CoAP message (a token is
// generated when it has none), e.g. to protect it with OSCORE on another
// binding.
func CoAPMessage(m *Message) (message.Message, error) {
	pm := pool.NewMessage(context.Background())
	if err := toPool(pm, m); err != nil {
		return message.Message{}, err
	}
	if len(m.Token) == 0 {
		tok, err := message.GetToken()
		if err != nil {
			return message.Message{}, err
		}
		pm.SetToken(tok)
	}
	return poolToMessage(pm)
}

// MessageFromCoAP is the inverse of CoAPMessage.
func MessageFromCoAP(m message.Message) (*Message, error) {
	pm := pool.NewMessage(context.Background())
	pm.SetToken(m.Token)
	return poolFromMessage(pm, m)
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
