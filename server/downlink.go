package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/codec/senml"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// CoAP method and response codes go-coap does not name.
const (
	codeFETCH    codes.Code = 5   // RFC 8132
	codeIPATCH   codes.Code = 7   // RFC 8132
	codeConflict codes.Code = 137 // 4.09
)

var (
	ErrNotRegistered = errors.New("server: endpoint not registered")
	ErrBadRequest    = errors.New("server: invalid request")
)

// Response is the outcome of a downlink operation.
type Response struct {
	Code          codes.Code
	ContentFormat lwm2m.ContentFormat
	HasFormat     bool
	Payload       []byte
	Nodes         []lwm2m.Node // decoded payload for data formats
	DecodeErr     error        // payload present but undecodable
	Location      []string     // Location-Path of a Create response
}

// Success reports a 2.xx response.
func (r *Response) Success() bool { return r.Code >= 64 && r.Code < 96 }

// CodeString renders the code as "2.05".
func CodeString(c codes.Code) string { return fmt.Sprintf("%d.%02d", c>>5, c&0x1f) }

// request is a prepared downlink exchange.
type request struct {
	method   codes.Code
	path     lwm2m.Path
	root     bool // send to the alternate path root even when path is "/"
	query    []string
	cf       *lwm2m.ContentFormat
	accept   *lwm2m.ContentFormat
	body     []byte
	observe  *uint32
	token    message.Token
	schemaOf lwm2m.Path // base path used to decode the response
}

// lookup returns the registration of ep.
func (s *Server) lookup(ep string) (*Registration, error) {
	r, ok := s.store.ByEndpoint(ep)
	if !ok {
		return nil, ErrNotRegistered
	}
	return r, nil
}

// uriPath builds the request path, prefixing the alternate path (GEN-08).
func uriPath(reg *Registration, p lwm2m.Path) string {
	if reg.RootPath == "" {
		return p.String()
	}
	if p.IsRoot() {
		return reg.RootPath
	}
	return strings.TrimSuffix(reg.RootPath, "/") + p.String()
}

// exchange sends one request to reg, honouring queue mode (QM-02), and
// decodes the response.
func (s *Server) exchange(ctx context.Context, reg *Registration, rq request) (*Response, error) {
	if err := validateTarget(rq); err != nil {
		return nil, err
	}
	// Waiting for a sleeping client is bounded by the caller's ctx (QM-02);
	// each transmission by RequestTimeout (RFC 7252 §4.8.2).
	var resp *Response
	err := s.queues.run(ctx, reg, func(cur *Registration) error {
		tctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
		defer cancel()
		r, err := s.transmit(tctx, cur, rq)
		resp = r
		return err
	})
	return resp, err
}

// validateTarget enforces path rules the client would answer with 4.05 or
// that the spec forbids the server to send (GEN-09, DM-11).
func validateTarget(rq request) error {
	if rq.path.Len() > 0 && (rq.path.Object() == 0 || rq.path.Object() == 21 || rq.path.Object() == 23) {
		return fmt.Errorf("%w: %s is reserved for bootstrap (DM-11)", ErrBadRequest, rq.path)
	}
	return nil
}

func (s *Server) transmit(ctx context.Context, reg *Registration, rq request) (*Response, error) {
	if reg.peer == nil {
		return nil, ErrNotRegistered
	}
	tok := rq.token
	if tok == nil {
		var err error
		if tok, err = message.GetToken(); err != nil {
			return nil, err
		}
	}
	res, err := reg.peer.Exchange(ctx, &Message{
		Code:    rq.method,
		Path:    uriPath(reg, rq.path),
		Query:   rq.query,
		Format:  rq.cf,
		Accept:  rq.accept,
		Observe: rq.observe,
		Token:   tok,
		Payload: rq.body,
	})
	if err != nil {
		return nil, err
	}
	return s.decodeResponse(reg, res, rq.schemaOf), nil
}

// decodeResponse decodes data formats with the registration's schema.
func (s *Server) decodeResponse(reg *Registration, res *Message, base lwm2m.Path) *Response {
	out := &Response{Code: res.Code, Payload: res.Payload, Location: res.Location}
	if res.Format != nil {
		out.ContentFormat, out.HasFormat = *res.Format, true
	}
	if out.HasFormat && len(out.Payload) > 0 && out.ContentFormat != lwm2m.FormatLinkFormat {
		if c, err := codec.For(out.ContentFormat); err == nil {
			out.Nodes, out.DecodeErr = c.Decode(base, out.Payload, s.schema(reg))
		} else {
			out.DecodeErr = err
		}
	}
	return out
}

// schema returns the per-client schema used to type values.
func (s *Server) schema(reg *Registration) lwm2m.Schema {
	if s.cfg.Schema == nil {
		return nil
	}
	return s.cfg.Schema(reg)
}

// ReadOptions selects the response format. A zero value sends no Accept:
// the server decodes every format and the client uses its preferred one
// (C §7.5).
type ReadOptions struct {
	Accept *lwm2m.ContentFormat
}

func fmtPtr(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// Read reads an object, instance, resource or resource instance (DM-02).
func (s *Server) Read(ctx context.Context, ep string, p lwm2m.Path, o ReadOptions) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	if p.IsRoot() {
		return nil, fmt.Errorf("%w: Read on / (use ReadComposite)", ErrBadRequest)
	}
	return s.exchange(ctx, reg, request{method: codes.GET, path: p, accept: o.Accept, schemaOf: p})
}

// Discover returns the link-format description of p (DM-04). depth, when
// non-nil, is the 1.2 depth modifier (C 1.2.2 Tbl 6.3.2-2).
func (s *Server) Discover(ctx context.Context, ep string, p lwm2m.Path, depth *int) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	rq := request{method: codes.GET, path: p, accept: fmtPtr(lwm2m.FormatLinkFormat), schemaOf: p}
	if depth != nil {
		if *depth < 0 || *depth > 3 {
			return nil, fmt.Errorf("%w: depth must be 0..3", ErrBadRequest)
		}
		rq.query = []string{fmt.Sprintf("depth=%d", *depth)}
	}
	return s.exchange(ctx, reg, rq)
}

// WriteMode selects Replace (PUT) or Partial Update (POST) (DM-05).
type WriteMode uint8

const (
	Replace WriteMode = iota
	PartialUpdate
)

// WriteOptions: Format forces the payload Content-Format; zero picks one.
type WriteOptions struct {
	Mode   WriteMode
	Format *lwm2m.ContentFormat
}

// Write writes nodes under p (DM-05). Values are validated against the
// client's schema first when Config.Validate is set (DM-06).
func (s *Server) Write(ctx context.Context, ep string, p lwm2m.Path, nodes []lwm2m.Node, o WriteOptions) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	if p.IsRoot() || p.IsObject() {
		return nil, fmt.Errorf("%w: Write targets /o/i, /o/i/r or /o/i/r/ri", ErrBadRequest)
	}
	if o.Mode == PartialUpdate && !(p.IsInstance() || p.IsResource()) {
		return nil, fmt.Errorf("%w: Partial Update targets /o/i or a multi-instance /o/i/r", ErrBadRequest)
	}
	if err := s.validateWrite(reg, nodes); err != nil {
		return nil, err
	}
	method := codes.PUT
	if o.Mode == PartialUpdate {
		method = codes.POST
	}
	return s.writeWithFormat(ctx, reg, method, p, nodes, o.Format, o.Mode == PartialUpdate)
}

// writeWithFormat encodes nodes and sends them. Without an explicit format
// it picks one the client can take, retrying on 4.15/4.06 (C §7.5).
func (s *Server) writeWithFormat(ctx context.Context, reg *Registration, method codes.Code, p lwm2m.Path, nodes []lwm2m.Node, forced *lwm2m.ContentFormat, multiOnly bool) (*Response, error) {
	cands := s.writeFormats(reg, p, nodes, multiOnly)
	if forced != nil {
		cands = []lwm2m.ContentFormat{*forced}
	}
	var last *Response
	for _, cf := range cands {
		c, err := codec.For(cf)
		if err != nil {
			return nil, err
		}
		body, err := c.Encode(p, nodes)
		if err != nil {
			if forced != nil {
				return nil, err
			}
			continue
		}
		r, err := s.exchange(ctx, reg, request{method: method, path: p, cf: fmtPtr(cf), body: body, schemaOf: p})
		if err != nil {
			return nil, err
		}
		last = r
		if r.Code != codes.UnsupportedMediaType && r.Code != codes.NotAcceptable {
			s.rememberFormat(reg, cf, singleValue(p, nodes))
			return r, nil
		}
	}
	if last == nil {
		return nil, fmt.Errorf("%w: no content format can encode this payload", ErrBadRequest)
	}
	return last, nil
}

// singleValue reports a payload that plain text/opaque can carry.
func singleValue(p lwm2m.Path, nodes []lwm2m.Node) bool {
	return len(nodes) == 1 && nodes[0].Kind == lwm2m.KindValue && nodes[0].Path == p &&
		(p.IsResource() || p.IsResourceInstance())
}

// writeFormats lists candidate formats in preference order.
func (s *Server) writeFormats(reg *Registration, p lwm2m.Path, nodes []lwm2m.Node, multiOnly bool) []lwm2m.ContentFormat {
	var out []lwm2m.ContentFormat
	if !multiOnly && singleValue(p, nodes) {
		if nodes[0].Value.Type == lwm2m.TypeOpaque {
			out = append(out, lwm2m.FormatOpaque)
		}
		out = append(out, lwm2m.FormatText) // mandatory for clients (C §7.5)
	}
	out = append(out, s.multiFormats(reg)...)
	return out
}

// multiFormats: the client's advertised formats (REG-10), then the learned
// one, then by version. 1.0 clients must support TLV.
func (s *Server) multiFormats(reg *Registration) []lwm2m.ContentFormat {
	var out []lwm2m.ContentFormat
	add := func(f lwm2m.ContentFormat) {
		for _, x := range out {
			if x == f {
				return
			}
		}
		out = append(out, f)
	}
	if f, ok := s.learnedFormat(reg); ok {
		add(f)
	}
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatLwM2MCBOR, lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatTLV} {
		for _, adv := range reg.ContentFormats {
			if adv == f {
				add(f)
			}
		}
	}
	switch reg.Version {
	case "1.0":
		add(lwm2m.FormatTLV)
	case "1.1":
		add(lwm2m.FormatSenMLCBOR)
		add(lwm2m.FormatSenMLJSON)
		add(lwm2m.FormatTLV)
	default:
		add(lwm2m.FormatLwM2MCBOR)
		add(lwm2m.FormatSenMLCBOR)
		add(lwm2m.FormatSenMLJSON)
		add(lwm2m.FormatTLV)
	}
	return out
}

// WriteAttributes sets or clears <NOTIFICATION> attributes on p (DM-07).
// query items are "pmin=10" or a bare "pmin" to unset.
func (s *Server) WriteAttributes(ctx context.Context, ep string, p lwm2m.Path, query []string) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	if p.IsRoot() {
		return nil, fmt.Errorf("%w: Write-Attributes on /", ErrBadRequest)
	}
	return s.exchange(ctx, reg, request{method: codes.PUT, path: p, query: query, schemaOf: p})
}

// Execute executes resource p with optional plain-text arguments (DM-08).
func (s *Server) Execute(ctx context.Context, ep string, p lwm2m.Path, args string) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	if !p.IsResource() {
		return nil, fmt.Errorf("%w: Execute targets /o/i/r", ErrBadRequest)
	}
	rq := request{method: codes.POST, path: p, schemaOf: p}
	if args != "" {
		rq.cf, rq.body = fmtPtr(lwm2m.FormatText), []byte(args)
	}
	return s.exchange(ctx, reg, rq)
}

// Create creates an instance of object p (DM-09). nodes carry absolute
// paths; include the instance ID so the result is known without a
// Location-Path (Zephyr omits it, C9).
func (s *Server) Create(ctx context.Context, ep string, p lwm2m.Path, nodes []lwm2m.Node, format *lwm2m.ContentFormat) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	if !p.IsObject() {
		return nil, fmt.Errorf("%w: Create targets /o", ErrBadRequest)
	}
	if !reg.HasObject(p.Object()) {
		return nil, fmt.Errorf("%w: object %d not registered (DM-09)", ErrBadRequest, p.Object())
	}
	if err := s.validateCreate(reg, p, nodes); err != nil {
		return nil, err
	}
	return s.writeWithFormat(ctx, reg, codes.POST, p, nodes, format, true)
}

// Delete deletes object instance p (DM-10).
func (s *Server) Delete(ctx context.Context, ep string, p lwm2m.Path) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	if !p.IsInstance() {
		return nil, fmt.Errorf("%w: Delete targets /o/i", ErrBadRequest)
	}
	if p.Object() == 3 && p.Instance() == 0 {
		return nil, fmt.Errorf("%w: /3/0 cannot be deleted (DM-10)", ErrBadRequest)
	}
	return s.exchange(ctx, reg, request{method: codes.DELETE, path: p, schemaOf: p})
}

// CompositeOptions picks request and response formats for composite
// operations. Zero values pick SenML CBOR for both (DM-12).
type CompositeOptions struct {
	Format *lwm2m.ContentFormat // request body format
	Accept *lwm2m.ContentFormat // response format
}

func (o CompositeOptions) formats(reg *Registration) (lwm2m.ContentFormat, lwm2m.ContentFormat) {
	req := lwm2m.FormatSenMLCBOR
	if o.Format != nil {
		req = *o.Format
	}
	acc := req
	switch req {
	case lwm2m.FormatSenMLETCHCBOR:
		acc = lwm2m.FormatSenMLCBOR
	case lwm2m.FormatSenMLETCHJSON:
		acc = lwm2m.FormatSenMLJSON
	}
	if o.Accept != nil {
		acc = *o.Accept
	}
	return req, acc
}

// encodePaths encodes a composite path list in a SenML or ETCH format.
func encodePaths(cf lwm2m.ContentFormat, paths []lwm2m.Path) ([]byte, error) {
	c, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	sc, ok := c.(senml.Codec)
	if !ok {
		return nil, fmt.Errorf("%w: composite path lists need SenML or SenML-ETCH, not %v", ErrBadRequest, cf)
	}
	return sc.EncodePaths(paths)
}

// ReadComposite reads several paths with one FETCH on / (DM-12). The
// request goes to the root with no Uri-Path or Uri-Query (C12).
func (s *Server) ReadComposite(ctx context.Context, ep string, paths []lwm2m.Path, o CompositeOptions) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := validateTarget(request{path: p}); err != nil {
			return nil, err
		}
	}
	reqCF, acc := o.formats(reg)
	body, err := encodePaths(reqCF, paths)
	if err != nil {
		return nil, err
	}
	return s.exchange(ctx, reg, request{method: codeFETCH, path: lwm2m.Root, cf: &reqCF, accept: &acc, body: body, schemaOf: lwm2m.Root})
}

// WriteComposite writes nodes at several paths atomically with iPATCH on /
// (DM-12).
func (s *Server) WriteComposite(ctx context.Context, ep string, nodes []lwm2m.Node, format *lwm2m.ContentFormat) (*Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if err := validateTarget(request{path: n.Path}); err != nil {
			return nil, err
		}
	}
	if err := s.validateWrite(reg, nodes); err != nil {
		return nil, err
	}
	cf := lwm2m.FormatSenMLCBOR
	if format != nil {
		cf = *format
	}
	c, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	body, err := c.Encode(lwm2m.Root, nodes)
	if err != nil {
		return nil, err
	}
	return s.exchange(ctx, reg, request{method: codeIPATCH, path: lwm2m.Root, cf: &cf, body: body, schemaOf: lwm2m.Root})
}

// validateWrite checks values against the client's model before sending
// (DM-06), when Config.Validator is set.
func (s *Server) validateWrite(reg *Registration, nodes []lwm2m.Node) error {
	if s.cfg.Validator == nil {
		return nil
	}
	if err := s.cfg.Validator.CheckWrite(reg, nodes); err != nil {
		return fmt.Errorf("%w: %w", ErrBadRequest, err) // keep the model error for errors.Is
	}
	return nil
}

func (s *Server) validateCreate(reg *Registration, p lwm2m.Path, nodes []lwm2m.Node) error {
	if s.cfg.Validator == nil {
		return nil
	}
	if err := s.cfg.Validator.CheckCreate(reg, p.Object(), nodes); err != nil {
		return fmt.Errorf("%w: %w", ErrBadRequest, err)
	}
	return nil
}
