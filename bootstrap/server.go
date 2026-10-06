// Package bootstrap implements the LwM2M Bootstrap-Server (Core §6.1,
// Transport §6.4.2): Client-Initiated Bootstrap (Bootstrap-Request, then
// Bootstrap-Delete, -Write, -Discover, -Read and -Finish), Bootstrap-Pack-
// Request, and the server side of Server-Initiated Bootstrap, over
// CoAP/UDP and CoAP/DTLS (PSK, X.509, and RPK through the DTLS config).
package bootstrap

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	piondtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/fiumaralabs/lwm2m/link"
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
	tcpClient "github.com/plgd-dev/go-coap/v3/tcp/client"
	"github.com/plgd-dev/go-coap/v3/udp"
	"github.com/plgd-dev/go-coap/v3/udp/client"
	udpServer "github.com/plgd-dev/go-coap/v3/udp/server"
)

// ExchangeLifetime is EXCHANGE_LIFETIME (RFC 7252 §4.8.2): a client that
// gets no Bootstrap-Finish within it considers bootstrap failed (BS-08).
const ExchangeLifetime = 247 * time.Second

// Config configures a Server. Zero values select spec defaults.
type Config struct {
	Configs  ConfigStore          // per-endpoint bootstrap configs; default in-memory
	Security server.SecurityStore // credentials for the bootstrap connection; default in-memory
	// OnSession is called when a session ends, from the session goroutine.
	OnSession func(Result)
	// SessionTimeout bounds a session from Bootstrap-Request to the
	// Finish response. Default ExchangeLifetime (BS-08).
	SessionTimeout time.Duration
	// RequestTimeout bounds one attempt of a downlink request, CoAP
	// retransmissions included. Default 45 s.
	RequestTimeout time.Duration
	// Retries is how often a timed-out request is re-sent as a new message
	// (new MID): Anjay Lite ignores retransmitted bootstrap requests (T49).
	// Default 2; negative disables.
	Retries int
	// DisablePack answers Bootstrap-Pack-Request with 5.01 (BS-12).
	DisablePack bool
}

// Server is an LwM2M Bootstrap-Server.
type Server struct {
	cfg    Config
	router *mux.Router

	mu       sync.Mutex
	sessions map[string]*session // by endpoint
	oscore   atomic.Pointer[OSCORE]
	udp      []*udpServer.Server
	dtls     []*dtlsServer.Server
	closers  []func() error
	afters   sync.Map // coapConn -> func(), started once the response is out
	wg       sync.WaitGroup
	closed   bool
}

// New returns a Server. Call Listen* to start serving.
func New(cfg Config) *Server {
	if cfg.Configs == nil {
		cfg.Configs = NewMemoryConfigStore()
	}
	if cfg.Security == nil {
		cfg.Security = server.NewMemorySecurityStore()
	}
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = ExchangeLifetime
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 45 * time.Second
	}
	if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	s := &Server{cfg: cfg, sessions: map[string]*session{}}
	s.router = mux.NewRouter()
	s.router.DefaultHandle(mux.HandlerFunc(s.serveCoAP))
	return s
}

// Configs returns the config store.
func (s *Server) Configs() ConfigStore { return s.cfg.Configs }

// Security returns the security store.
func (s *Server) Security() server.SecurityStore { return s.cfg.Security }

// ListenUDP serves CoAP over plain UDP (NoSec) on addr.
func (s *Server) ListenUDP(addr string) (net.Addr, error) {
	l, err := coapnet.NewListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	srv := udp.NewServer(options.WithMux(s.router), options.WithBlockwise(true, server.BlockSZX, s.cfg.RequestTimeout),
		options.WithProcessReceivedMessageFunc(s.process))
	s.mu.Lock()
	s.udp = append(s.udp, srv)
	s.closers = append(s.closers, l.Close)
	s.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
	return l.LocalAddr(), nil
}

// MinPSKKey is the shortest (D)TLS PSK the default PSK lookup accepts:
// PSK suites MUST NOT be used with low-entropy secrets (T §5.2.4, BS-10).
const MinPSKKey = 16

// DTLSConfig configures a DTLS listener. Config is passed to pion/dtls
// as is: PSK defaults to a lookup in the security store; X.509 needs
// Certificates, ClientCAs and ClientAuth; RPK and other credential
// schemes plug in through its certificate callbacks
// (VerifyPeerCertificate, GetCertificate). A peer certificate that is not
// X.509 is taken as a raw public key (SubjectPublicKeyInfo).
type DTLSConfig struct {
	Config     *piondtls.Config
	CIDLength  int  // server-assigned Connection ID length (RFC 9146); default 8
	DisableCID bool // turn Connection ID support off
}

// ListenDTLS serves CoAP over DTLS 1.2 on addr. BS-10: the listener
// accepts PSK, certificate and (with a callback) RPK clients.
func (s *Server) ListenDTLS(addr string, dc DTLSConfig) (net.Addr, error) {
	cfg := &piondtls.Config{}
	if dc.Config != nil {
		c := *dc.Config // a shallow copy: defaults below must not leak into the caller's config
		cfg = &c
	}
	if cfg.PSK == nil {
		cfg.PSK = func(id []byte) ([]byte, error) {
			si, ok := s.cfg.Security.ByPSKIdentity(string(id))
			if !ok || len(si.PSKKey) == 0 {
				return nil, errors.New("bootstrap: unknown PSK identity")
			}
			if len(si.PSKKey) < MinPSKKey {
				return nil, errors.New("bootstrap: PSK shorter than 128 bits (BS-10: no low-entropy secrets)")
			}
			return si.PSKKey, nil
		}
	}
	if len(cfg.CipherSuites) == 0 {
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
	srv := coapdtls.NewServer(options.WithMux(s.router), options.WithBlockwise(true, server.BlockSZX, s.cfg.RequestTimeout),
		options.WithProcessReceivedMessageFunc(s.process))
	s.mu.Lock()
	s.dtls = append(s.dtls, srv)
	s.closers = append(s.closers, l.Close)
	s.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
	return l.Addr(), nil
}

// Close stops the listeners and cancels running sessions.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for _, ss := range s.sessions {
		ss.cancel(ErrCancelled)
	}
	udps, dtlss, closers := s.udp, s.dtls, s.closers
	s.mu.Unlock()
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
	s.wg.Wait()
	return errors.Join(errs...)
}

// --- CoAP binding -------------------------------------------------------

// coapConn is a go-coap UDP, DTLS, TCP or TLS connection.
type coapConn interface {
	AcquireMessage(ctx context.Context) *pool.Message
	ReleaseMessage(*pool.Message)
	Do(*pool.Message) (*pool.Message, error)
	RemoteAddr() net.Addr
	NetConn() net.Conn
}

// coapPeer is the server.Peer of one CoAP session: binding U (UDP, DTLS)
// or T (TCP, TLS).
type coapPeer struct {
	cc      coapConn
	binding string
}

func (p coapPeer) Identity() server.Identity { return identityOf(p.cc.NetConn(), p.cc.RemoteAddr()) }
func (p coapPeer) RemoteAddr() net.Addr      { return p.cc.RemoteAddr() }
func (p coapPeer) Binding() string           { return p.binding }

// Exchange sends one CON request (GEN-03); each call is a new message ID.
func (p coapPeer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	m := p.cc.AcquireMessage(ctx)
	defer p.cc.ReleaseMessage(m)
	m.SetCode(req.Code)
	m.SetType(message.Confirmable)
	if err := m.SetPath(req.Path); err != nil {
		return nil, err
	}
	tok, err := message.GetToken()
	if err != nil {
		return nil, err
	}
	m.SetToken(tok)
	if req.Accept != nil {
		m.SetAccept(message.MediaType(*req.Accept))
	}
	if req.Format != nil {
		m.SetContentFormat(message.MediaType(*req.Format))
	}
	if req.Payload != nil {
		m.SetBody(bytes.NewReader(req.Payload))
	}
	res, err := p.cc.Do(m)
	if err != nil {
		return nil, err
	}
	defer p.cc.ReleaseMessage(res)
	return fromPool(res)
}

func fromPool(m *pool.Message) (*server.Message, error) {
	out := &server.Message{Code: m.Code(), Path: "/"}
	if p, err := m.Options().Path(); err == nil {
		out.Path = "/" + strings.TrimPrefix(p, "/")
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
	if m.Body() != nil {
		b, err := io.ReadAll(m.Body())
		if err != nil {
			return nil, err
		}
		out.Payload = b
	}
	return out, nil
}

func (s *Server) serveCoAP(w mux.ResponseWriter, m *mux.Message) {
	cc, ok := w.Conn().(coapConn)
	if !ok {
		return
	}
	peer := coapPeer{cc, "U"}
	if _, tcp := cc.(*tcpClient.Conn); tcp {
		peer.binding = "T"
	}
	if o := s.oscore.Load(); o != nil && s.interceptOSCORE(o, w, m, peer) {
		return // OSCORE layer (T §5.4.3)
	}
	msg, err := fromPool(m.Message)
	if err != nil {
		return
	}
	resp, after := s.HandleUplink(peer, msg)
	s.afters.Store(cc, after)
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
}

// process runs the handler, then starts what it deferred, so the session's
// first request follows the 2.04 to Bootstrap-Request.
func (s *Server) process(req *pool.Message, cc *client.Conn, handler config.HandlerFunc[*client.Conn]) {
	cc.ProcessReceivedMessageWithHandler(req, handler)
	if f, ok := s.afters.LoadAndDelete(cc); ok {
		f.(func())()
	}
}

// identityOf extracts the authenticated identity of a connection: the
// LwM2M Server's rules (server.IdentityOf), plus a verified TLS client
// certificate. Go's crypto/tls has neither PSK nor raw public keys, so a
// TLS session is X.509 or NoSec.
func identityOf(nc net.Conn, remote net.Addr) server.Identity {
	if tc, ok := nc.(*tls.Conn); ok {
		if st := tc.ConnectionState(); len(st.VerifiedChains) > 0 {
			leaf := st.PeerCertificates[0]
			return server.Identity{Mode: server.ModeX509, CertCN: leaf.Subject.CommonName, Cert: leaf}
		}
	}
	return server.IdentityOf(nc, remote)
}

// --- uplink -------------------------------------------------------------

func status(c codes.Code) *server.Message { return &server.Message{Code: c} }

func noop() {}

// HandleUplink processes a request the client sent on peer:
// Bootstrap-Request (POST /bs) or Bootstrap-Pack-Request (GET /bspack).
// It returns the response and a function the binding must call once the
// response is sent; it starts the bootstrap session.
func (s *Server) HandleUplink(peer server.Peer, m *server.Message) (resp *server.Message, after func()) {
	switch m.Path {
	case "/bs":
		if m.Code != codes.POST {
			return status(codes.MethodNotAllowed), noop
		}
		return s.bootstrapRequest(peer, m)
	case "/bspack":
		if s.cfg.DisablePack {
			return status(codes.NotImplemented), noop // BS-12
		}
		if m.Code != codes.GET {
			return status(codes.MethodNotAllowed), noop
		}
		return s.packRequest(peer, m), noop
	}
	return status(codes.NotFound), noop
}

func queryParams(qs []string) map[string]string {
	out := map[string]string{}
	for _, q := range qs {
		k, v, _ := strings.Cut(q, "=")
		if _, dup := out[k]; !dup {
			out[k] = v
		}
	}
	return out
}

// endpoint resolves and authorises the endpoint of a Bootstrap-Request or
// -Pack-Request. ok=false means 4.00: no ep and none derivable (BS-20),
// ep not bound to the authenticated identity (BS-01, SEC-06, C1), a NoSec
// request for an endpoint that has credentials (SEC-07), or no config.
func (s *Server) endpoint(id server.Identity, ep string) (string, *BootstrapConfig, bool) {
	if ep == "" {
		switch id.Mode {
		case server.ModePSK:
			if si, ok := s.cfg.Security.ByPSKIdentity(id.PSKIdentity); ok {
				ep = si.Endpoint
			}
		case server.ModeX509:
			ep = id.CertCN
		}
		if ep == "" {
			return "", nil, false
		}
	}
	si, has := s.cfg.Security.ByEndpoint(ep)
	if id.Secure() != has || has && !si.Matches(id) {
		return "", nil, false
	}
	c, ok := s.cfg.Configs.Get(ep)
	return ep, c, ok
}

func (s *Server) bootstrapRequest(peer server.Peer, m *server.Message) (*server.Message, func()) {
	q := queryParams(m.Query)
	id := peer.Identity()
	ep, cfg, ok := s.endpoint(id, q["ep"])
	if !ok {
		return status(codes.BadRequest), noop
	}
	if err := cfg.Validate(); err != nil {
		return status(codes.InternalServerError), noop
	}
	// BS-02: pct is the client's preferred format; one we cannot write
	// with is 4.15 (BS-01). Without pct: the config's, else TLV.
	format := lwm2m.FormatTLV
	if v, has := q["pct"]; has {
		n, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			return status(codes.BadRequest), noop
		}
		if !multiFormat(lwm2m.ContentFormat(n)) {
			return status(codes.UnsupportedMediaType), noop
		}
		format = lwm2m.ContentFormat(n)
	}
	if cfg.ContentFormat != nil {
		format = *cfg.ContentFormat
	}
	ss := s.newSession(ep, id, peer, cfg, format, m.Query)
	if ss == nil {
		return status(codes.ServiceUnavailable), noop // closing
	}
	return status(codes.Changed), func() { go ss.run() }
}

// packFormat picks the Bootstrap-Pack format from Accept (BS-12).
func packFormat(accept *lwm2m.ContentFormat) (lwm2m.ContentFormat, bool) {
	if accept == nil {
		return lwm2m.FormatSenMLCBOR, true // what Anjay asks for (T58)
	}
	switch f := accept.Canonical(); f {
	case lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatLwM2MCBOR:
		return f, true
	}
	return 0, false
}

// parseAcc reads acc (BS-14): BS-account instances in CoRE link format,
// instance paths without parameters, of /0, /21, /23 or /24.
func parseAcc(v string) (sec, osc []uint16, err error) {
	links, err := link.Parse(v)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range links {
		p, err := lwm2m.ParsePath(l.URI)
		if err != nil || !p.IsInstance() || len(l.Params) > 0 {
			return nil, nil, fmt.Errorf("bootstrap: acc link %q", l.URI)
		}
		switch p.Object() {
		case 0:
			sec = append(sec, p.Instance())
		case 21:
			osc = append(osc, p.Instance())
		case 23, 24:
		default:
			return nil, nil, fmt.Errorf("bootstrap: acc link %q is not a BS-account object", l.URI)
		}
	}
	return sec, osc, nil
}

// packRequest answers Bootstrap-Pack-Request (C §6.1.7.7, BS-12, BS-15).
func (s *Server) packRequest(peer server.Peer, m *server.Message) *server.Message {
	q := queryParams(m.Query)
	id := peer.Identity()
	ep, cfg, ok := s.endpoint(id, q["ep"])
	if !ok {
		return status(codes.BadRequest)
	}
	if err := cfg.Validate(); err != nil {
		return status(codes.InternalServerError)
	}
	format, ok := packFormat(m.Accept)
	if !ok {
		return status(codes.NotAcceptable)
	}
	bsIDs, oscIDs, err := parseAcc(q["acc"])
	if err != nil {
		return status(codes.BadRequest)
	}
	_, writes := cfg.plan(bsIDs, true) // packable checks the Pack covers the deletes
	res := Result{Endpoint: ep, Identity: id, Pack: true, Format: format, Query: m.Query}
	why := cfg.packable(writes)
	// AutoIDForSecurityObject promises that no /0 write lands on the client's
	// BS account. A Pack has no Bootstrap-Discover, so only acc (BS-14, 1.2.1)
	// names that instance; pre-1.2.1 clients (Anjay 3.15) omit it. Refuse
	// (4.05, BS-12) so the client falls back to Bootstrap-Request, whose
	// Discover finds the account (found by interop/peers with Anjay: a Pack
	// /0/1 replaced its BS account at /0/1).
	if _, hasAcc := q["acc"]; why == "" && cfg.AutoIDForSecurityObject && !hasAcc {
		why = "no acc: AutoIDForSecurityObject cannot locate the BS account"
	}
	// The client keeps its BS account's /21 instance (BS-15): a Pack
	// instance on that ID would clash with it.
	// ponytail: refused, so the client falls back to Bootstrap-Request;
	// renumber /21 and rewrite /0/x/17 if a deployment needs the Pack.
	for _, w := range writes {
		if w.Path.Object() == 21 && w.Path.Len() >= 2 && slices.Contains(oscIDs, w.Path.Instance()) {
			why = "/21/" + strconv.Itoa(int(w.Path.Instance())) + " is the client's BS-account OSCORE instance (acc)"
		}
	}
	if why != "" {
		res.Err = fmt.Errorf("%w: %s", ErrPackRefused, why)
		s.report(res)
		return status(codes.MethodNotAllowed) // supported but refused (BS-12)
	}
	var nodes []lwm2m.Node
	for _, w := range writes {
		nodes = append(nodes, w.Nodes...)
	}
	lwm2m.SortNodes(nodes)
	cd, err := codec.For(format)
	if err != nil {
		return status(codes.InternalServerError)
	}
	body, err := cd.Encode(lwm2m.Root, nodes)
	if err != nil {
		res.Err = err
		s.report(res)
		return status(codes.InternalServerError)
	}
	// A Pack supersedes a classic session still running for ep.
	s.mu.Lock()
	if old := s.sessions[ep]; old != nil {
		old.cancel(ErrCancelled)
		delete(s.sessions, ep)
	}
	s.mu.Unlock()
	s.report(res)
	return &server.Message{Code: codes.Content, Format: &format, Payload: body}
}

func (s *Server) report(r Result) {
	if s.cfg.OnSession != nil {
		s.cfg.OnSession(r)
	}
}
