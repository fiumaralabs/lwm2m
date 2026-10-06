// Package httpbinding is the LwM2M-over-HTTP transport binding "H" of the
// Server (T §7). The Binding is an http.Handler for client-initiated
// requests (Register, Update, De-register on /rd, Send on /dp) and sends
// Device Management requests to the client's own HTTP server.
//
// T §7 does not say how the Server reaches the client (A-6); Config.ClientURL
// supplies the client's base URL. Content-Type and Accept carry the IANA
// media types of the Content-Formats (RFC 8075). BootstrapBinding serves
// the Bootstrap interface of a bootstrap.Server the same way (T §7.1.2).
package httpbinding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Config configures the binding.
type Config struct {
	// ClientURL returns the base URL ("https://host:port") of the HTTP
	// server of a registered endpoint, or "" if unknown.
	ClientURL func(ep string) string
	// Client sends the downlink requests; default http.DefaultClient.
	Client *http.Client
}

// Binding serves LwM2M over HTTP for a Server.
type Binding struct {
	srv *server.Server
	cfg Config
}

// New returns the binding of srv. Serve it with TLS (T §7).
func New(srv *server.Server, cfg Config) *Binding {
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &Binding{srv: srv, cfg: cfg}
}

// maxBody bounds an uplink body; Register object lists are the largest.
const maxBody = 1 << 20

var methods = map[string]codes.Code{
	http.MethodGet: codes.GET, http.MethodPost: codes.POST, http.MethodPut: codes.PUT, http.MethodDelete: codes.DELETE,
}

// ServeHTTP handles one client request (T §7.1.3, §7.1.5).
func (b *Binding) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	code, ok := methods[r.Method]
	if !ok {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	msg := &server.Message{Code: code, Path: r.URL.Path, Query: query(r.URL.RawQuery)}
	if len(body) > 0 {
		msg.Payload = body
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		f, ok := formatOf(ct)
		if !ok {
			w.WriteHeader(http.StatusBadRequest) // Register, Update and Send list no 415
			return
		}
		msg.Format = &f
	}
	p := &peer{cfg: &b.cfg, id: identity(r), addr: tcpAddr(r.RemoteAddr)}
	seg := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(seg) == 1 && seg[0] == "rd":
		for _, q := range msg.Query {
			if v, ok := strings.CutPrefix(q, "ep="); ok {
				p.setEP(v)
			}
		}
	case len(seg) == 2 && seg[0] == "rd":
		if reg, ok := b.srv.Store().ByID(seg[1]); ok {
			p.setEP(reg.Endpoint)
		}
	}
	resp, after := b.srv.HandleUplink(p, msg)
	if resp.Code == codes.Created && len(resp.Location) == 2 {
		if reg, ok := b.srv.Store().ByID(resp.Location[1]); ok {
			p.setEP(reg.Endpoint) // ep derived from the certificate (REG-02)
		}
		w.Header().Set("Location", "/"+strings.Join(resp.Location, "/")) // T §7.1.3: under /rd
	}
	if resp.Format != nil {
		w.Header().Set("Content-Type", resp.Format.String())
	}
	w.WriteHeader(Status(resp.Code))
	_, _ = w.Write(resp.Payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	after()
}

// query splits a raw query into "k=v" / "k" items, unescaped.
func query(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, "&") {
		if item == "" {
			continue
		}
		if u, err := url.QueryUnescape(item); err == nil {
			item = u
		}
		out = append(out, item)
	}
	return out
}

// Status maps a CoAP-numbered response code to an HTTP status (T Tbls
// 7.3-1..4): 2.01 -> 201, 2.02 and 2.04 -> 204, 2.05 -> 200, x.yy -> xyy.
func Status(c codes.Code) int {
	switch c {
	case codes.Created:
		return http.StatusCreated
	case codes.Deleted, codes.Changed, codes.Valid:
		return http.StatusNoContent
	case codes.Content:
		return http.StatusOK
	}
	return int(c>>5)*100 + int(c&0x1f)
}

// knownFormats are the Content-Formats with an IANA media type.
var knownFormats = []lwm2m.ContentFormat{
	lwm2m.FormatText, lwm2m.FormatLinkFormat, lwm2m.FormatOpaque, lwm2m.FormatCBOR,
	lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLETCHJSON, lwm2m.FormatSenMLETCHCBOR,
	lwm2m.FormatTLV, lwm2m.FormatOMAJSON, lwm2m.FormatLwM2MCBOR,
}

func formatOf(ct string) (lwm2m.ContentFormat, bool) {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return 0, false
	}
	for _, f := range knownFormats {
		if f.String() == mt {
			return f, true
		}
	}
	return 0, false
}

func mediaType(f lwm2m.ContentFormat) (string, error) {
	for _, k := range knownFormats {
		if k == f {
			return f.String(), nil
		}
	}
	return "", fmt.Errorf("%w: %v has no media type", ErrUnsupported, f)
}

// identity is what TLS authenticated: the client certificate (X.509) when
// the handshake verified it (ClientAuth with ClientCAs), or, without one,
// the client's IP address. An unverified certificate (RequireAnyClientCert)
// authenticates nothing. HTTP opens new connections at will, so the port
// is not part of a NoSec identity.
func identity(r *http.Request) server.Identity {
	if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
		c := r.TLS.PeerCertificates[0]
		return server.Identity{Mode: server.ModeX509, CertCN: c.Subject.CommonName, Cert: c}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return server.Identity{Mode: server.ModeNoSec, Addr: "http:" + host}
}

func tcpAddr(s string) net.Addr {
	if a, err := net.ResolveTCPAddr("tcp", s); err == nil {
		return a
	}
	return &net.TCPAddr{}
}

// ErrUnsupported reports a request the HTTP binding cannot carry.
var ErrUnsupported = errors.New("httpbinding: not supported by the HTTP binding")

// peer is the Peer of one HTTP client.
type peer struct {
	cfg  *Config
	id   server.Identity
	addr net.Addr
	mu   sync.Mutex
	ep   string
}

func (p *peer) setEP(ep string) { p.mu.Lock(); p.ep = ep; p.mu.Unlock() }

func (p *peer) Identity() server.Identity { return p.id }
func (p *peer) RemoteAddr() net.Addr      { return p.addr }
func (p *peer) Binding() string           { return "H" }

// Exchange sends a Device Management request to the client's HTTP server
// (T §7.1.4). Composite operations and Observe have no HTTP mapping (T
// §7.1.4 note, §7.1.5).
func (p *peer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	var method string
	switch req.Code {
	case codes.GET:
		method = http.MethodGet
	case codes.PUT:
		method = http.MethodPut
	case codes.POST:
		method = http.MethodPost
	case codes.DELETE:
		method = http.MethodDelete
	default:
		return nil, fmt.Errorf("%w: method %v (Read-/Write-Composite)", ErrUnsupported, req.Code)
	}
	if req.Observe != nil {
		return nil, fmt.Errorf("%w: Observe (only Send reports information)", ErrUnsupported)
	}
	p.mu.Lock()
	ep := p.ep
	p.mu.Unlock()
	base := ""
	if p.cfg.ClientURL != nil {
		base = p.cfg.ClientURL(ep)
	}
	if base == "" {
		return nil, fmt.Errorf("httpbinding: no HTTP URL for endpoint %q", ep)
	}
	u := strings.TrimSuffix(base, "/") + req.Path
	if len(req.Query) > 0 {
		qs := make([]string, len(req.Query))
		for i, q := range req.Query {
			k, v, ok := strings.Cut(q, "=")
			qs[i] = url.QueryEscape(k)
			if ok {
				qs[i] += "=" + url.QueryEscape(v)
			}
		}
		u += "?" + strings.Join(qs, "&")
	}
	hr, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(req.Payload))
	if err != nil {
		return nil, err
	}
	if req.Format != nil {
		mt, err := mediaType(*req.Format)
		if err != nil {
			return nil, err
		}
		hr.Header.Set("Content-Type", mt)
	}
	if req.Accept != nil {
		mt, err := mediaType(*req.Accept)
		if err != nil {
			return nil, err
		}
		hr.Header.Set("Accept", mt)
	}
	res, err := p.cfg.Client.Do(hr)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, err
	}
	out := &server.Message{Code: Code(req, res.StatusCode), Token: req.Token}
	if len(body) > 0 {
		out.Payload = body
	}
	if f, ok := formatOf(res.Header.Get("Content-Type")); ok {
		out.Format = &f
	}
	if loc := strings.Trim(res.Header.Get("Location"), "/"); loc != "" {
		out.Location = strings.Split(loc, "/")
	}
	return out, nil
}

// Code maps the client's HTTP status to a CoAP-numbered code (T Tbl
// 7.3-3). A success is the operation's 2.xx: GET 2.05, PUT 2.04, POST 2.01
// on an Object (Create) else 2.04, DELETE 2.02. Errors map xyy -> x.yy,
// or x.00 when yy does not fit.
func Code(req *server.Message, status int) codes.Code {
	if status >= 200 && status < 300 {
		switch req.Code {
		case codes.GET:
			return codes.Content
		case codes.DELETE:
			return codes.Deleted
		case codes.POST:
			if numericSegments(req.Path) == 1 {
				return codes.Created
			}
		}
		return codes.Changed
	}
	class, detail := status/100, status%100
	if class != 4 && class != 5 {
		return codes.InternalServerError
	}
	if detail > 31 {
		detail = 0
	}
	return codes.Code(class<<5 | detail)
}

// numericSegments counts LwM2M path segments after any alternate path,
// which has no numeric segment (T §7.1.1).
func numericSegments(p string) int {
	n := 0
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if _, err := strconv.ParseUint(s, 10, 16); err == nil {
			n++
		}
	}
	return n
}
