package httpbinding

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// BootstrapBinding serves the Bootstrap interface of a Bootstrap-Server
// over HTTP (T §7.1.2, Tbl 7.1.2-1): the client sends Bootstrap-Request
// (POST /bs?ep=&pct=) and Bootstrap-Pack-Request (GET /bspack?ep=&acc=);
// the session's Bootstrap-Discover, -Delete, -Write, -Read and -Finish go
// to the client's HTTP server (Config.ClientURL). Status codes follow
// Tbl 7.3-1. With OSCORE (Bootstrap.EnableOSCORE), requests carrying the
// HTTP OSCORE header field are verified and the session's requests are
// protected the same way (RFC 8613 §11, T §5.4.1).
type BootstrapBinding struct {
	bs  *bootstrap.Server
	cfg Config
}

// NewBootstrap returns the HTTP binding of bs. Serve it with TLS (T §7).
func NewBootstrap(bs *bootstrap.Server, cfg Config) *BootstrapBinding {
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &BootstrapBinding{bs: bs, cfg: cfg}
}

// bootstrapStatus is Tbl 7.3-1 for the client-initiated operations:
// success is 200, an error the table lists is passed on, any other error
// is 400 (the table's "Undetermined error" / bad request).
func bootstrapStatus(path string, c codes.Code) int {
	allowed := []int{http.StatusBadRequest, http.StatusUnsupportedMediaType} // Bootstrap-Request
	if path == "/bspack" {
		allowed = []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound,
			http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusNotImplemented}
	}
	switch st := Status(c); {
	case c < 96: // 2.xx
		return http.StatusOK
	case slices.Contains(allowed, st):
		return st
	}
	return http.StatusBadRequest
}

// ServeHTTP handles Bootstrap-Request and Bootstrap-Pack-Request.
func (b *BootstrapBinding) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	p := &peer{cfg: &b.cfg, id: identity(r), addr: tcpAddr(r.RemoteAddr)}
	if r.Header.Get("OSCORE") != "" {
		b.serveOSCORE(w, r, p, body) // the URI path is inside the ciphertext
		return
	}
	if r.URL.Path != "/bs" && r.URL.Path != "/bspack" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	code, ok := methods[r.Method]
	if !ok {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	msg := &server.Message{Code: code, Path: r.URL.Path, Query: query(r.URL.RawQuery)}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		f, ok := formatOf(ct)
		if !ok {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		msg.Format = &f
	}
	if a := r.Header.Get("Accept"); a != "" && a != "*/*" {
		f, ok := formatOf(a)
		if !ok {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		msg.Accept = &f
	}
	ep, ok := epOf(msg.Query)
	if !ok && p.id.Mode == server.ModeX509 {
		ep = p.id.CertCN // ep derived from the certificate (BS-20)
	}
	if o := b.bs.OSCORE(); o != nil && o.Bound(ep) {
		w.WriteHeader(http.StatusUnauthorized) // this endpoint must use OSCORE
		return
	}
	p.setEP(ep)
	bp := &bootstrapPeer{peer: p}
	resp, after := b.bs.HandleUplink(bp, msg)
	if resp.Format != nil {
		w.Header().Set("Content-Type", resp.Format.String())
	}
	w.WriteHeader(bootstrapStatus(msg.Path, resp.Code))
	_, _ = w.Write(resp.Payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	after()
}

func epOf(qs []string) (string, bool) {
	for _, q := range qs {
		if v, ok := strings.CutPrefix(q, "ep="); ok {
			return v, true
		}
	}
	return "", false
}

// bootstrapPeer restricts the session's requests to the HTTP mapping of
// Tbl 7.1.2-1: Bootstrap-Read only on Object 2.
type bootstrapPeer struct{ *peer }

func (p *bootstrapPeer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	if err := checkBootstrapRequest(req); err != nil {
		return nil, err
	}
	return p.peer.Exchange(ctx, req)
}

func checkBootstrapRequest(req *server.Message) error {
	if req.Code == codes.GET && (req.Accept == nil || *req.Accept != lwm2m.FormatLinkFormat) {
		if pth, err := lwm2m.ParsePath(req.Path); err != nil || pth.Len() < 1 || pth.Object() != 2 {
			return fmt.Errorf("%w: Bootstrap-Read of %s (Object ID MUST be 2, T Tbl 7.1.2-1)", ErrUnsupported, req.Path)
		}
	}
	return nil
}

// --- OSCORE over HTTP (RFC 8613 §11, OSC-09) ------------------------------

const oscoreMediaType = "application/oscore"

// encodeOSCOREHeader is §11.2: AA for an empty option, else base64url
// without padding.
func encodeOSCOREHeader(v []byte) string {
	if len(v) == 0 {
		return "AA"
	}
	return base64.RawURLEncoding.EncodeToString(v)
}

// decodeOSCOREHeader is §11.3.
func decodeOSCOREHeader(s string) ([]byte, error) {
	if s == "AA" {
		return []byte{}, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// toHTTP writes an OSCORE-layer CoAP response: a protected one as 200 with
// the OSCORE header and application/oscore body (§11.1), an unprotected
// error with its status.
func toHTTP(w http.ResponseWriter, m message.Message) {
	if hasOption(m, oscore.OptionOSCORE) {
		v, _ := m.Options.GetBytes(oscore.OptionOSCORE)
		w.Header().Set("OSCORE", encodeOSCOREHeader(v))
		w.Header().Set("Content-Type", oscoreMediaType)
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(m.Payload)
		return
	}
	w.WriteHeader(Status(m.Code))
	_, _ = w.Write(m.Payload)
}

func hasOption(m message.Message, id message.OptionID) bool {
	for _, o := range m.Options {
		if o.ID == id {
			return true
		}
	}
	return false
}

// fromHTTP is the receiving side of §11.3: a POST with the OSCORE header
// and an application/oscore body becomes a CoAP OSCORE message.
func fromHTTP(header, ct string, code codes.Code, body []byte) (message.Message, error) {
	v, err := decodeOSCOREHeader(header)
	if err != nil {
		return message.Message{}, err
	}
	if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != oscoreMediaType {
		return message.Message{}, fmt.Errorf("httpbinding: OSCORE message with Content-Type %q", ct)
	}
	return message.Message{Code: code, Options: message.Options{{ID: oscore.OptionOSCORE, Value: v}}, Payload: body}, nil
}

func (b *BootstrapBinding) serveOSCORE(w http.ResponseWriter, r *http.Request, p *peer, body []byte) {
	o := b.bs.OSCORE()
	if o == nil || r.Method != http.MethodPost { // §11.1: only POST carries OSCORE
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	in, err := fromHTTP(r.Header.Get("OSCORE"), r.Header.Get("Content-Type"), codes.POST, body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	q, reply := o.Verify(in)
	if q == nil {
		toHTTP(w, reply)
		return
	}
	msg, err := server.MessageFromCoAP(q.Inner)
	if err != nil {
		toHTTP(w, oscore.PlainError(codes.BadRequest, ""))
		return
	}
	p.setEP(q.Entry.Name)
	resp, after := o.HandleUplink(q.Entry, &oscorePeer{peer: p, e: q.Entry}, msg)
	rm := message.Message{Code: resp.Code, Payload: resp.Payload}
	if resp.Format != nil {
		rm.Options = message.Options{{ID: message.ContentFormat, Value: uintBytes(uint32(*resp.Format))}}
	}
	prot, err := q.Protect(rm)
	if err != nil {
		toHTTP(w, oscore.PlainError(codes.InternalServerError, ""))
		return
	}
	toHTTP(w, prot)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	after()
}

func uintBytes(v uint32) []byte {
	var b []byte
	for ; v > 0; v >>= 8 {
		b = append([]byte{byte(v)}, b...)
	}
	return b
}

// oscorePeer sends the session's requests OSCORE-protected over HTTP: a
// POST to the client's base URL with the OSCORE header field (the method,
// path and options are inside the ciphertext, RFC 8613 §11.2).
type oscorePeer struct {
	*peer
	e *oscore.Entry
}

// Identity is the TLS client certificate when there is one, else the
// OSCORE context's identity.
func (p *oscorePeer) Identity() server.Identity {
	if p.id.Secure() {
		return p.id
	}
	return server.OSCOREIdentity(p.e.Params())
}

func (p *oscorePeer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	if err := checkBootstrapRequest(req); err != nil {
		return nil, err
	}
	base := ""
	if p.cfg.ClientURL != nil {
		base = p.cfg.ClientURL(p.e.Name)
	}
	if base == "" {
		return nil, fmt.Errorf("httpbinding: no HTTP URL for endpoint %q", p.e.Name)
	}
	plain, err := server.CoAPMessage(req)
	if err != nil {
		return nil, err
	}
	inner, err := oscore.RoundTrip(p.e.Context(), plain, func(prot message.Message, _ *oscore.Exchange) (message.Message, error) {
		v, _ := prot.Options.GetBytes(oscore.OptionOSCORE)
		hr, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/", bytes.NewReader(prot.Payload))
		if err != nil {
			return message.Message{}, err
		}
		hr.Header.Set("OSCORE", encodeOSCOREHeader(v))
		hr.Header.Set("Content-Type", oscoreMediaType)
		res, err := p.cfg.Client.Do(hr)
		if err != nil {
			return message.Message{}, err
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
		if err != nil {
			return message.Message{}, err
		}
		if h := res.Header.Get("OSCORE"); h != "" && res.StatusCode == http.StatusOK {
			return fromHTTP(h, res.Header.Get("Content-Type"), codes.Changed, body)
		}
		return message.Message{Code: Code(req, res.StatusCode), Payload: body}, nil // unprotected error
	})
	if err != nil {
		return nil, fmt.Errorf("httpbinding: OSCORE: %w", err)
	}
	return server.MessageFromCoAP(inner)
}
