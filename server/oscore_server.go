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

// request verifies an OSCORE request (RFC 8613 §8.2) with freshness
// (OSC-04), applies the endpoint binding, runs it through the LwM2M core
// and protects the response (§8.3).
func (o *OSCORE) request(w mux.ResponseWriter, m *mux.Message, cc coapConn, in message.Message) {
	q, reply := o.Verify(in)
	if q == nil {
		writeCoAP(w, reply)
		return
	}
	msg, err := poolFromMessage(m.Message, q.Inner)
	if err != nil {
		plainError(w, codes.BadRequest, "")
		return
	}
	peer := o.peer(cc, q.Entry)
	if code := o.bind(msg, q.Entry, peer); code != 0 {
		o.respond(w, q, message.Message{Code: code})
		return
	}
	resp, after := o.s.HandleUplink(peer, msg)
	o.s.coap.defer_(cc, after)
	if resp == nil {
		resp = status(codes.Changed)
	}
	o.respond(w, q, toCoAPMessage(resp))
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

func (o *OSCORE) respond(w mux.ResponseWriter, q *oscore.Request, rm message.Message) {
	prot, err := q.Protect(rm)
	if err != nil {
		plainError(w, codes.InternalServerError, "")
		return
	}
	writeCoAP(w, prot)
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
	inner, err := b.e.Context().UnprotectResponse(in, b.x)
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
	cc coapConn
	e  *oscore.Entry
}

func (o *OSCORE) peer(cc coapConn, e *oscore.Entry) *oscorePeer {
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
		pm := p.cc.AcquireMessage(ctx)
		defer p.cc.ReleaseMessage(pm)
		prot.MessageID, prot.Type = -1, message.Confirmable
		pm.SetMessage(prot)
		res, err := p.cc.Do(pm)
		if err != nil {
			return message.Message{}, err
		}
		defer p.cc.ReleaseMessage(res)
		return poolToMessage(res)
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
