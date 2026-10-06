package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fiumaralabs/lwm2m"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options/config"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// coapConn is what the CoAP binding needs from a go-coap connection; the
// UDP/DTLS and TCP/TLS connection types both provide it.
type coapConn interface {
	AcquireMessage(ctx context.Context) *pool.Message
	ReleaseMessage(*pool.Message)
	Do(*pool.Message) (*pool.Message, error)
	RemoteAddr() net.Addr
	NetConn() net.Conn
	Context() context.Context
}

var errEmptyResponse = errors.New("server: client answered with an empty message")

// coapPeer is the Peer of one CoAP session (binding U, or T for TCP).
type coapPeer struct {
	cc      coapConn
	binding string
}

func (p *coapPeer) Identity() Identity   { return identityOf(p.cc.NetConn(), p.cc.RemoteAddr()) }
func (p *coapPeer) RemoteAddr() net.Addr { return p.cc.RemoteAddr() }
func (p *coapPeer) Binding() string      { return p.binding }

func (p *coapPeer) Exchange(ctx context.Context, req *Message) (*Message, error) {
	m := p.cc.AcquireMessage(ctx)
	defer p.cc.ReleaseMessage(m)
	if err := toPool(m, req); err != nil {
		return nil, err
	}
	m.SetType(message.Confirmable) // GEN-03; Anjay Lite drops NON requests (C12)
	res, err := p.cc.Do(m)
	if err != nil {
		return nil, err
	}
	defer p.cc.ReleaseMessage(res)
	if res.Code() == codes.Empty {
		// An empty message carries no response; one with our token is
		// malformed (RFC 7252 §4.1).
		return nil, errEmptyResponse
	}
	return fromPool(res)
}

// toPool copies a Message into a go-coap message.
func toPool(m *pool.Message, req *Message) error {
	m.SetCode(req.Code)
	if err := m.SetPath(req.Path); err != nil {
		return err
	}
	for _, q := range req.Query {
		m.AddQuery(q)
	}
	if req.Token != nil {
		m.SetToken(req.Token)
	}
	if req.Observe != nil {
		m.SetObserve(*req.Observe)
	}
	if req.Accept != nil {
		m.SetAccept(message.MediaType(*req.Accept))
	}
	if req.Format != nil {
		m.SetContentFormat(message.MediaType(*req.Format))
	}
	for _, l := range req.Location {
		m.AddOptionString(message.LocationPath, l)
	}
	if req.Payload != nil {
		m.SetBody(bytes.NewReader(req.Payload))
	}
	return nil
}

// fromPool copies a go-coap message into a Message.
func fromPool(m *pool.Message) (*Message, error) {
	out := &Message{Code: m.Code(), Token: append([]byte(nil), m.Token()...)}
	if p, err := m.Options().Path(); err == nil {
		out.Path = "/" + strings.TrimPrefix(p, "/")
	} else {
		out.Path = "/"
	}
	out.Query, _ = m.Options().Queries()
	if cf, err := m.ContentFormat(); err == nil {
		f := lwm2m.ContentFormat(cf)
		out.Format = &f
	}
	if a, err := m.Options().Accept(); err == nil {
		f := lwm2m.ContentFormat(a)
		out.Accept = &f
	}
	if o, err := m.Observe(); err == nil {
		out.Observe = &o
	}
	if lp, err := m.Options().LocationPath(); err == nil && lp != "" {
		out.Location = strings.Split(strings.Trim(lp, "/"), "/")
	}
	if m.Body() != nil {
		b, err := io.ReadAll(m.Body())
		if err != nil {
			return nil, err
		}
		out.Payload = b
	}
	return out, nil
}

// coapBinding holds per-connection state of the CoAP adapter.
type coapBinding struct {
	peers  sync.Map // coapConn -> *coapPeer
	mu     sync.Mutex
	afters map[coapConn][]func()
	oscore atomic.Pointer[OSCORE] // set by EnableOSCORE (oscore_server.go)
}

func (b *coapBinding) peer(cc coapConn, binding string) *coapPeer {
	if p, ok := b.peers.Load(cc); ok {
		return p.(*coapPeer)
	}
	p, _ := b.peers.LoadOrStore(cc, &coapPeer{cc: cc, binding: binding})
	go func() {
		<-cc.Context().Done()
		b.peers.Delete(cc)
	}()
	return p.(*coapPeer)
}

func (b *coapBinding) defer_(cc coapConn, f func()) {
	b.mu.Lock()
	if b.afters == nil {
		b.afters = map[coapConn][]func(){}
	}
	b.afters[cc] = append(b.afters[cc], f)
	b.mu.Unlock()
}

func (b *coapBinding) runAfters(cc coapConn) {
	b.mu.Lock()
	fs := b.afters[cc]
	delete(b.afters, cc)
	b.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

// serveCoAP is the router's only handler: every incoming CoAP request or
// notification goes through HandleUplink.
func (s *Server) serveCoAP(w mux.ResponseWriter, m *mux.Message) {
	cc, ok := w.Conn().(coapConn)
	if !ok {
		return
	}
	if o := s.coap.oscore.Load(); o != nil && o.intercept(w, m, cc) {
		return // OSCORE layer (T §5.4) handled it
	}
	msg, err := fromPool(m.Message)
	if err != nil {
		return
	}
	resp, after := s.HandleUplink(s.peerOf(cc), msg)
	s.coap.defer_(cc, after)
	if resp == nil {
		return // notification: go-coap sends the empty ACK
	}
	var body io.ReadSeeker
	if resp.Payload != nil {
		body = bytes.NewReader(resp.Payload)
	}
	_ = w.SetResponse(resp.Code, message.TextPlain, body)
	if resp.Format == nil {
		w.Message().Remove(message.ContentFormat)
	} else {
		w.Message().SetContentFormat(message.MediaType(*resp.Format))
	}
	for _, l := range resp.Location {
		w.Message().AddOptionString(message.LocationPath, l)
	}
}

// isResponseCode reports a 2.xx-5.xx code (a response, not a request).
func isResponseCode(c codes.Code) bool { return c >= 64 && c < 192 }

// processUDP is go-coap's received-message hook for UDP and DTLS. It
// answers notifications for unknown observations with Reset (OBS-02):
// go-coap's own reply path would turn a Reset to a CON into an ACK. After
// the message is handled (and its response written) it runs the
// handler's deferred actions (GEN-10).
func (s *Server) processUDP(req *pool.Message, cc *client.Conn, handler config.HandlerFunc[*client.Conn]) {
	if isResponseCode(req.Code()) && req.HasOption(message.Observe) && len(req.Token()) > 0 &&
		!s.KnownObservation(req.Token()) && (req.Type() == message.Confirmable || req.Type() == message.NonConfirmable) {
		rst := cc.AcquireMessage(cc.Context())
		rst.SetType(message.Reset)
		rst.SetCode(codes.Empty)
		rst.SetMessageID(req.MessageID())
		_ = cc.Session().WriteMessage(rst)
		cc.ReleaseMessage(rst)
		cc.ReleaseMessage(req)
		return
	}
	cc.ProcessReceivedMessageWithHandler(req, handler)
	s.coap.runAfters(cc)
}
