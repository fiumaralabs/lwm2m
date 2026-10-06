// Package coap connects a server.Server to CoAP: binding U over UDP
// (NoSec, RFC 7252) and DTLS 1.2 (PSK, RPK, X.509, Connection ID), and
// binding T over TCP, TLS and WebSockets (RFC 8323). It also holds the
// Server's OSCORE layer (T §5.4, RFC 8613), which protects LwM2M traffic
// on these bindings and, through CoAPWire, on SMS.
//
// A Binding owns the listeners and the per-connection state: peers,
// responses that must precede the core's follow-up actions (GEN-10),
// Block1 reassembly, and the sessions it keeps open for registered
// clients. Everything LwM2M is done by the core through
// Server.HandleUplink and Peer.Exchange.
//
//	srv := server.New(server.Config{})
//	cb := coap.New(srv)
//	cb.ListenUDP(":5683")
//	cb.ListenDTLS(":5684", coap.DTLSConfig{})
//	defer srv.Close()
//	defer cb.Close() // listeners first, then the core
package coap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	piondtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/fiumaralabs/lwm2m/server"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	dtlsServer "github.com/plgd-dev/go-coap/v3/dtls/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/options/config"
	"github.com/plgd-dev/go-coap/v3/udp"
	"github.com/plgd-dev/go-coap/v3/udp/client"
	udpServer "github.com/plgd-dev/go-coap/v3/udp/server"
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
	Close() error
}

const (
	// BlockSZX is the block size of downlink block-wise transfers: 512
	// bytes (SZX 5), Californium's default. A 1024-byte Block1 over DTLS
	// with CID is a 1087-byte datagram, which constrained clients drop
	// before CoAP sees it (Zephyr interop test_blockwise_*: 1 KiB of RX
	// buffers); a client asking for smaller blocks still gets them.
	BlockSZX      = 0x5
	blockSize     = 16 << BlockSZX        // 512
	optRequestTag = message.OptionID(292) // RFC 9175
)

func newRequestTag() []byte {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return b
}

var errEmptyResponse = errors.New("coap: client answered with an empty message")

// coapPeer is the Peer of one CoAP session (binding U, or T for TCP).
type coapPeer struct {
	cc      coapConn
	binding string
}

func (p *coapPeer) Identity() server.Identity { return IdentityOf(p.cc.NetConn(), p.cc.RemoteAddr()) }
func (p *coapPeer) RemoteAddr() net.Addr      { return p.cc.RemoteAddr() }
func (p *coapPeer) Binding() string           { return p.binding }

func (p *coapPeer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	m := p.cc.AcquireMessage(ctx)
	defer p.cc.ReleaseMessage(m)
	if err := toPool(m, req); err != nil {
		return nil, err
	}
	m.SetType(message.Confirmable) // GEN-03; Anjay Lite drops NON requests (C12)
	if len(req.Payload) > blockSize {
		// Request-Tag (RFC 9175 §3) on every block of a block-wise request
		// stops block interchange between transfers (GEN-07). go-coap copies
		// the request's options onto each block.
		m.SetOptionBytes(optRequestTag, newRequestTag())
	}
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
func toPool(m *pool.Message, req *server.Message) error {
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
func fromPool(m *pool.Message) (*server.Message, error) {
	out := &server.Message{Code: m.Code(), Token: append([]byte(nil), m.Token()...)}
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

// Binding serves the CoAP bindings of one Server.
type Binding struct {
	srv    *server.Server
	router *mux.Router

	mu      sync.Mutex
	udp     []*udpServer.Server
	dtls    []*dtlsServer.Server
	closers []func() error
	closed  bool

	peers  sync.Map // coapConn -> *coapPeer
	amu    sync.Mutex
	afters map[coapConn][]func()
	oscore atomic.Pointer[OSCORE] // set by EnableOSCORE (oscore.go)
	held   heldConns              // keeps sessions of registered clients open (keepconn.go)
	block1 block1Assembler        // client Block1 reassembly (block1.go)
}

// New returns a Binding for srv. Call Listen* to start serving.
func New(srv *server.Server) *Binding {
	b := &Binding{srv: srv}
	b.router = mux.NewRouter()
	b.router.DefaultHandle(mux.HandlerFunc(b.serveCoAP))
	return b
}

// requestTimeout bounds block-wise transfers: the core's RequestTimeout.
func (b *Binding) requestTimeout() time.Duration { return b.srv.Config().RequestTimeout }

// ListenUDP serves CoAP over plain UDP (NoSec, T §5.3) on addr and returns
// the bound address.
func (b *Binding) ListenUDP(addr string) (net.Addr, error) {
	l, err := coapnet.NewListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	srv := udp.NewServer(
		options.WithMux(b.router),
		options.WithBlockwise(true, BlockSZX, b.requestTimeout()),
		options.WithProcessReceivedMessageFunc(b.processUDP),
		b.monitor(),
	)
	b.mu.Lock()
	b.udp = append(b.udp, srv)
	b.closers = append(b.closers, l.Close)
	b.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
	return l.LocalAddr(), nil
}

// DTLSConfig configures a DTLS listener. PSK credentials are resolved from
// the security store by identity; a nil Config builds one.
type DTLSConfig struct {
	Config *piondtls.Config
	// CIDLength is the length of the Connection ID the server assigns
	// (RFC 9146, CID-01). 0 disables CID; default 8.
	CIDLength int
	// DisableCID turns Connection ID support off.
	DisableCID bool
}

// ListenDTLS serves CoAP over DTLS 1.2 on addr.
func (b *Binding) ListenDTLS(addr string, dc DTLSConfig) (net.Addr, error) {
	cfg := dc.Config
	if cfg == nil {
		cfg = &piondtls.Config{}
	}
	if cfg.PSK == nil {
		cfg.PSK = b.pskLookup
	}
	if len(cfg.CipherSuites) == 0 {
		// SEC-04: TLS_PSK_WITH_AES_128_CCM_8 and TLS_PSK_WITH_AES_128_CBC_SHA256;
		// SEC-09/10: ECDHE_ECDSA with AES_128_CCM_8 and AES_128_CBC_SHA256.
		cfg.CipherSuites = []piondtls.CipherSuiteID{
			piondtls.TLS_PSK_WITH_AES_128_CCM_8,
			piondtls.TLS_PSK_WITH_AES_128_CBC_SHA256,
			piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8,
			piondtls.TLS_PSK_WITH_AES_128_CCM,
			piondtls.TLS_PSK_WITH_AES_128_GCM_SHA256,
			piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		}
	}
	if !dc.DisableCID && cfg.ConnectionIDGenerator == nil {
		n := dc.CIDLength
		if n == 0 {
			n = 8
		}
		cfg.ConnectionIDGenerator = piondtls.RandomCIDGenerator(n)
	}
	l, err := dtlscoap.Listen("udp", addr, cfg)
	if err != nil {
		return nil, err
	}
	srv := coapdtls.NewServer(
		options.WithMux(b.router),
		options.WithBlockwise(true, BlockSZX, b.requestTimeout()),
		options.WithProcessReceivedMessageFunc(b.processUDP),
		b.monitor(),
	)
	b.mu.Lock()
	b.dtls = append(b.dtls, srv)
	b.closers = append(b.closers, l.Close)
	b.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
	return l.Addr(), nil
}

// pskLookup resolves the pre-shared key for a PSK identity (SEC-05).
func (b *Binding) pskLookup(identity []byte) ([]byte, error) {
	si, ok := b.srv.Security().ByPSKIdentity(string(identity))
	if !ok || len(si.PSKKey) == 0 {
		return nil, fmt.Errorf("coap: unknown PSK identity")
	}
	return si.PSKKey, nil
}

// Close stops all listeners. It does not close the Server.
func (b *Binding) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	udps, dtlss, closers := b.udp, b.dtls, b.closers
	b.mu.Unlock()
	for _, u := range udps {
		u.Stop()
	}
	for _, d := range dtlss {
		d.Stop()
	}
	var errs []error
	for _, c := range closers {
		if err := c(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// IdentityOf extracts the authenticated identity of a (D)TLS or plain UDP
// connection. Bindings and the Bootstrap-Server share it.
func IdentityOf(nc net.Conn, remote net.Addr) server.Identity {
	dc, ok := nc.(*piondtls.Conn)
	if !ok {
		return server.Identity{Mode: server.ModeNoSec, Addr: remote.String()}
	}
	st, ok := dc.ConnectionState()
	if !ok {
		return server.Identity{Mode: server.ModeNoSec, Addr: remote.String()}
	}
	if len(st.IdentityHint) > 0 {
		return server.Identity{Mode: server.ModePSK, PSKIdentity: string(st.IdentityHint)}
	}
	if len(st.PeerCertificates) > 0 {
		if c, err := x509.ParseCertificate(st.PeerCertificates[0]); err == nil {
			return server.Identity{Mode: server.ModeX509, CertCN: c.Subject.CommonName, Cert: c}
		}
		// A raw public key (RFC 7250) arrives as a bare SubjectPublicKeyInfo.
		return server.Identity{Mode: server.ModeRPK, PublicKey: st.PeerCertificates[0]}
	}
	return server.Identity{Mode: server.ModeNoSec, Addr: remote.String()}
}

func (b *Binding) peer(cc coapConn, binding string) *coapPeer {
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

func (b *Binding) defer_(cc coapConn, f func()) {
	b.amu.Lock()
	if b.afters == nil {
		b.afters = map[coapConn][]func(){}
	}
	b.afters[cc] = append(b.afters[cc], f)
	b.amu.Unlock()
}

func (b *Binding) runAfters(cc coapConn) {
	b.amu.Lock()
	fs := b.afters[cc]
	delete(b.afters, cc)
	b.amu.Unlock()
	for _, f := range fs {
		f()
	}
}

// serveCoAP is the router's only handler: every incoming CoAP request or
// notification goes through HandleUplink.
func (b *Binding) serveCoAP(w mux.ResponseWriter, m *mux.Message) {
	cc, ok := w.Conn().(coapConn)
	if !ok {
		return
	}
	if o := b.oscore.Load(); o != nil && o.intercept(w, m, cc) {
		return // OSCORE layer (T §5.4) handled it
	}
	msg, err := fromPool(m.Message)
	if err != nil {
		return
	}
	resp, after := b.srv.HandleUplink(b.peerOf(cc), msg)
	b.defer_(cc, after)
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
	if b, err := m.GetOptionUint32(message.Block1); err == nil {
		w.Message().SetOptionUint32(message.Block1, b) // final block of a Block1 request (RFC 7959 §2.3)
	}
}

// isResponseCode reports a 2.xx-5.xx code (a response, not a request).
func isResponseCode(c codes.Code) bool { return c >= 64 && c < 192 }

// processUDP is go-coap's received-message hook for UDP and DTLS. It
// answers notifications for unknown observations with Reset (OBS-02):
// go-coap's own reply path would turn a Reset to a CON into an ACK. After
// the message is handled (and its response written) it runs the
// handler's deferred actions (GEN-10).
func (b *Binding) processUDP(req *pool.Message, cc *client.Conn, handler config.HandlerFunc[*client.Conn]) {
	if isResponseCode(req.Code()) && req.HasOption(message.Observe) && len(req.Token()) > 0 &&
		!b.srv.KnownObservation(req.Token()) && (req.Type() == message.Confirmable || req.Type() == message.NonConfirmable) {
		rst := cc.AcquireMessage(cc.Context())
		rst.SetType(message.Reset)
		rst.SetCode(codes.Empty)
		rst.SetMessageID(req.MessageID())
		_ = cc.Session().WriteMessage(rst)
		cc.ReleaseMessage(rst)
		cc.ReleaseMessage(req)
		return
	}
	if b.block1.handle(req, cc) {
		return // 2.31 Continue or an error sent; the request is released
	}
	cc.ProcessReceivedMessageWithHandler(req, handler)
	b.runAfters(cc)
}
