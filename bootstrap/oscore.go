package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fiumaralabs/lwm2m/security/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
)

// OSCORE is the Bootstrap-Server's OSCORE layer (T §5.4.3, RFC 8613),
// built on the oscore.Responder the LwM2M Server uses too. A protected
// Bootstrap-Request or Bootstrap-Pack-Request on a context used for the
// first time is answered with a protected 4.01 carrying Echo, and only
// the repeated request with that Echo is served (BS-13, OSC-04). A client
// may instead derive a fresh context with RFC 8613 Appendix B.2 (OSC-03).
// The bootstrap session's downlink requests are then protected with the
// same context: the Bootstrap-Server is the OSCORE client for them.
type OSCORE struct {
	*oscore.Responder
	s *Server
}

// EnableOSCORE turns the OSCORE layer on and returns it. Calling it again
// returns the same layer.
func (s *Server) EnableOSCORE() *OSCORE {
	o := &OSCORE{Responder: &oscore.Responder{}, s: s}
	if !s.oscore.CompareAndSwap(nil, o) {
		return s.oscore.Load()
	}
	return o
}

// MinOSCORESecret is the shortest accepted Master Secret: 128 bits, the
// key size of the mandatory AEAD (T §5.4.3: "strong (high-entropy)
// pre-shared key").
const MinOSCORESecret = 16

var (
	ErrWeakOSCORESecret   = errors.New("bootstrap: OSCORE Master Secret shorter than 128 bits")
	ErrSharedOSCORESecret = errors.New("bootstrap: OSCORE Master Secret not unique to the device and protocol")
)

// Put adds or replaces the OSCORE context of endpoint ep (p is the BS's
// view: Params.Reverse of the client's /21 values; an empty ep is the
// client's Sender ID). The pre-shared key must be high-entropy and unique
// per device and per protocol (OSC-03): at least MinOSCORESecret bytes,
// used by no other endpoint's context, and not the endpoint's DTLS/TLS
// PSK. Unique (ID Context, Recipient ID) pairs follow RFC 8613 §3.3.
func (o *OSCORE) Put(ep string, p oscore.Params) error {
	if ep == "" {
		ep = string(p.RecipientID)
	}
	if len(p.MasterSecret) < MinOSCORESecret {
		return ErrWeakOSCORESecret
	}
	if si, ok := o.s.cfg.Security.ByEndpoint(ep); ok && bytes.Equal(si.PSKKey, p.MasterSecret) {
		return fmt.Errorf("%w: it is the endpoint's (D)TLS PSK", ErrSharedOSCORESecret)
	}
	for _, e := range o.Entries() {
		if e.Name != ep && bytes.Equal(e.Params().MasterSecret, p.MasterSecret) {
			return fmt.Errorf("%w: %q uses it", ErrSharedOSCORESecret, e.Name)
		}
	}
	return o.Responder.Put(ep, p)
}

func isBootstrapPath(m *pool.Message) bool {
	p, _ := m.Options().Path()
	p = strings.Trim(p, "/")
	return p == "bs" || p == "bspack"
}

func epQuery(qs []string) (string, bool) {
	for _, q := range qs {
		if v, ok := strings.CutPrefix(q, "ep="); ok {
			return v, true
		}
	}
	return "", false
}

// interceptOSCORE handles a protected request, and refuses an unprotected
// Bootstrap-Request or -Pack-Request for an endpoint that has an OSCORE
// context (4.01, T Tbl 6.7-2). false hands the message to the plain path.
func (s *Server) interceptOSCORE(o *OSCORE, w mux.ResponseWriter, m *mux.Message, peer coapPeer) bool {
	if !m.HasOption(oscore.OptionOSCORE) {
		if !isBootstrapPath(m.Message) {
			return false
		}
		qs, _ := m.Options().Queries()
		if ep, ok := epQuery(qs); ok {
			if o.Bound(ep) {
				writeCoAP(w, oscore.PlainError(codes.Unauthorized, ""))
				return true
			}
		}
		return false
	}
	if m.Code() >= 64 {
		return true // a stray protected response: dropped
	}
	in, err := toMessage(m.Message)
	if err != nil {
		return true
	}
	q, reply := o.Verify(in)
	if q == nil {
		writeCoAP(w, reply)
		return true
	}
	op := oscorePeer{coapPeer: peer, e: q.Entry}
	msg, err := server.MessageFromCoAP(q.Inner)
	if err != nil {
		writeCoAP(w, oscore.PlainError(codes.BadRequest, ""))
		return true
	}
	resp, after := o.HandleUplink(q.Entry, op, msg)
	s.afters.Store(peer.cc, after)
	prot, err := q.Protect(responseMessage(resp))
	if err != nil {
		writeCoAP(w, oscore.PlainError(codes.InternalServerError, ""))
		return true
	}
	writeCoAP(w, prot)
	return true
}

// HandleUplink serves a request verified with entry e (Responder.Verify)
// that arrived on peer, the binding's OSCORE-protected peer. It binds the
// endpoint to the context: ep must be the context's endpoint (4.00
// otherwise) and is filled in when omitted.
func (o *OSCORE) HandleUplink(e *oscore.Entry, peer server.Peer, msg *server.Message) (*server.Message, func()) {
	if msg.Path == "/bs" || msg.Path == "/bspack" {
		if ep, ok := epQuery(msg.Query); !ok {
			msg.Query = append(msg.Query, "ep="+e.Name)
		} else if ep != e.Name {
			return status(codes.BadRequest), noop
		}
	}
	return o.s.HandleUplink(peer, msg)
}

// Bound reports whether ep has an OSCORE context: its unprotected
// Bootstrap-Requests are refused.
func (o *OSCORE) Bound(ep string) bool {
	_, ok := o.Get(ep)
	return ok
}

// responseMessage renders a response as a plain CoAP message.
func responseMessage(r *server.Message) message.Message {
	m := message.Message{Code: r.Code, Payload: r.Payload}
	if r.Format != nil {
		var v []byte
		for f := uint32(*r.Format); f > 0; f >>= 8 {
			v = append([]byte{byte(f)}, v...)
		}
		m.Options = message.Options{{ID: message.ContentFormat, Value: v}}
	}
	return m
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

func toMessage(pm *pool.Message) (message.Message, error) {
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

// oscorePeer is a CoAP session whose bootstrap traffic is protected with
// one OSCORE context.
type oscorePeer struct {
	coapPeer
	e *oscore.Entry
}

// Identity is the (D)TLS identity when the transport authenticated one,
// else the OSCORE context's (server.OSCOREIdentity).
func (p oscorePeer) Identity() server.Identity {
	if id := p.coapPeer.Identity(); id.Secure() {
		return id
	}
	return server.OSCOREIdentity(p.e.Params())
}

// Exchange protects a downlink request (RFC 8613 §8.1) and verifies the
// response, with one Echo retry (oscore.RoundTrip).
func (p oscorePeer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	plain, err := server.CoAPMessage(req)
	if err != nil {
		return nil, err
	}
	inner, err := oscore.RoundTrip(p.e.Context(), plain, func(prot message.Message, _ *oscore.Exchange) (message.Message, error) {
		pm := p.cc.AcquireMessage(ctx)
		defer p.cc.ReleaseMessage(pm)
		prot.MessageID, prot.Type = -1, message.Confirmable
		pm.SetMessage(prot)
		res, err := p.cc.Do(pm)
		if err != nil {
			return message.Message{}, err
		}
		defer p.cc.ReleaseMessage(res)
		return toMessage(res)
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: OSCORE: %w", err)
	}
	return server.MessageFromCoAP(inner)
}

// OSCORE returns the OSCORE layer, nil until EnableOSCORE.
func (s *Server) OSCORE() *OSCORE { return s.oscore.Load() }
