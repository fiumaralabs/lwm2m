package testclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"

	piondtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
)

// OSCOREClient is a test client whose LwM2M traffic is protected with
// OSCORE (RFC 8613, T §5.4). Uplink requests are protected with Ctx's
// Sender Context; Device Management requests from the server are verified
// with its Recipient Context and answered protected (OSC-06).
type OSCOREClient struct {
	*Client
	Ctx *oscore.Context
	// RequireEcho makes the client demand Echo freshness (RFC 9175) once
	// before it accepts a Write, Execute, Create or Delete (OSC-04).
	RequireEcho bool
	// Base, when set, makes the client derive its contexts with RFC 8613
	// Appendix B.2 from these pre-established parameters: call Rederive
	// to start with a random ID Context R1; a response carrying 'kid
	// context' R2 switches to ID Context R2||R3.
	Base *oscore.Params

	omu      sync.Mutex
	obs      map[string]*oscore.Exchange
	echo     []byte
	echoDone bool
	last     message.Message // last protected request sent
	lastX    *oscore.Exchange
}

// OSCOREResponse is a server reply to an OSCORE request.
type OSCOREResponse struct {
	Response
	Protected bool   // carried the OSCORE option
	Echo      []byte // inner Echo option, if any
	// KIDContext is the response's 'kid context' (Appendix B.2 R2).
	KIDContext []byte
}

// NewOSCORE returns an OSCORE client with an empty store.
func NewOSCORE(cfg Config, ctx *oscore.Context) *OSCOREClient {
	return NewOSCOREOn(New(cfg), ctx)
}

// NewOSCOREOn returns an OSCORE client sharing c's store and override
// (e.g. a BootstrapClient's Client, for bootstrap over OSCORE).
func NewOSCOREOn(c *Client, ctx *oscore.Context) *OSCOREClient {
	return &OSCOREClient{Client: c, Ctx: ctx, obs: map[string]*oscore.Exchange{}}
}

func (o *OSCOREClient) context() *oscore.Context {
	o.omu.Lock()
	defer o.omu.Unlock()
	return o.Ctx
}

// Rederive starts Appendix B.2 (step 1): the next request is protected
// with a context derived from Base with a random ID Context R1, sent as
// 'kid context'.
func (o *OSCOREClient) Rederive() error {
	r1 := make([]byte, 8)
	_, _ = rand.Read(r1)
	return o.useIDContext(r1)
}

func (o *OSCOREClient) useIDContext(id []byte) error {
	p := *o.Base
	p.IDContext, p.SendKIDContext = id, true
	c, err := oscore.New(p)
	if err != nil {
		return err
	}
	o.omu.Lock()
	o.Ctx = c
	o.omu.Unlock()
	return nil
}

// Dial connects over UDP, or DTLS when PSKIdentity is set (OSCORE over
// DTLS, OSC-08).
func (o *OSCOREClient) Dial(addr string) error {
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(o.handle))
	hook := options.WithProcessReceivedMessageFunc(o.process)
	if o.cfg.PSKIdentity != "" {
		cfg := &piondtls.Config{
			PSK:             func([]byte) ([]byte, error) { return o.cfg.PSKKey, nil },
			PSKIdentityHint: []byte(o.cfg.PSKIdentity),
			CipherSuites:    []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8},
		}
		conn, err := dtlscoap.Dial(addr, cfg, options.WithMux(r), options.WithBlockwise(true, 0x6, 30*time.Second), hook)
		if err != nil {
			return err
		}
		o.conn = conn
		return nil
	}
	conn, err := udp.Dial(addr, options.WithMux(r), options.WithBlockwise(true, 0x6, 30*time.Second), hook)
	if err != nil {
		return err
	}
	o.conn = conn
	return nil
}

func uintOpt(id message.OptionID, v uint32) message.Option {
	var b []byte
	for v > 0 {
		b = append([]byte{byte(v)}, b...)
		v >>= 8
	}
	return message.Option{ID: id, Value: b}
}

func sortOpts(opts message.Options) message.Options {
	for i := 1; i < len(opts); i++ { // insertion sort keeps repeated options in order
		for j := i; j > 0 && opts[j].ID < opts[j-1].ID; j-- {
			opts[j], opts[j-1] = opts[j-1], opts[j]
		}
	}
	return opts
}

// Raw sends one OSCORE-protected request.
func (o *OSCOREClient) Raw(ctx context.Context, code codes.Code, path string, query []string, cf *lwm2m.ContentFormat, body []byte, extra ...message.Option) (*OSCOREResponse, error) {
	tok, err := message.GetToken()
	if err != nil {
		return nil, err
	}
	m := message.Message{Code: code, Token: tok, Payload: body, MessageID: -1, Type: message.Confirmable}
	for _, s := range strings.Split(strings.Trim(path, "/"), "/") {
		if s != "" {
			m.Options = append(m.Options, message.Option{ID: message.URIPath, Value: []byte(s)})
		}
	}
	for _, q := range query {
		m.Options = append(m.Options, message.Option{ID: message.URIQuery, Value: []byte(q)})
	}
	if cf != nil {
		m.Options = append(m.Options, uintOpt(message.ContentFormat, uint32(*cf)))
	}
	m.Options = sortOpts(append(m.Options, extra...))
	prot, x, err := o.context().ProtectRequest(m)
	if err != nil {
		return nil, err
	}
	o.omu.Lock()
	last := prot // go-coap may edit the options it sends: keep a copy
	last.Options = append(message.Options(nil), prot.Options...)
	last.Payload = append([]byte(nil), prot.Payload...)
	o.last, o.lastX = last, x
	o.omu.Unlock()
	return o.send(ctx, prot, x)
}

func (o *OSCOREClient) send(ctx context.Context, prot message.Message, x *oscore.Exchange) (*OSCOREResponse, error) {
	pm := o.conn.AcquireMessage(ctx)
	defer o.conn.ReleaseMessage(pm)
	pm.SetMessage(prot)
	res, err := o.conn.Do(pm)
	if err != nil {
		return nil, err
	}
	defer o.conn.ReleaseMessage(res)
	in, err := toMessage(res)
	if err != nil {
		return nil, err
	}
	out := &OSCOREResponse{}
	if res.HasOption(oscore.OptionOSCORE) {
		v, _ := in.Options.GetBytes(oscore.OptionOSCORE)
		h, err := oscore.ParseHeader(v)
		if err != nil {
			return nil, err
		}
		c := o.context()
		if h.KIDContext != nil && o.Base != nil {
			// Appendix B.2 step 3: verify response #1 with ID Context
			// R2||ID1, then switch to R2||R3 for request #2.
			p := *o.Base
			p.IDContext = append(append([]byte{}, h.KIDContext...), c.Params().IDContext...)
			if c, err = oscore.New(p); err != nil {
				return nil, err
			}
			defer func() {
				r3 := make([]byte, 8)
				_, _ = rand.Read(r3)
				_ = o.useIDContext(append(append([]byte{}, h.KIDContext...), r3...))
			}()
		}
		if in, err = c.UnprotectResponse(in, x); err != nil {
			return nil, err
		}
		out.Protected = true
		out.KIDContext = h.KIDContext
	}
	out.Code = in.Code
	for _, op := range in.Options {
		switch op.ID {
		case message.LocationPath:
			out.Location = append(out.Location, string(op.Value))
		case oscore.OptionEcho:
			out.Echo = op.Value
		}
	}
	out.Body = in.Payload
	return out, nil
}

// Replay resends the last protected request byte for byte.
func (o *OSCOREClient) Replay(ctx context.Context) (*OSCOREResponse, error) {
	o.omu.Lock()
	m, x := o.last, o.lastX
	o.omu.Unlock()
	m.MessageID = -1
	return o.send(ctx, m, x)
}

// Do sends a request and, when the server answers 4.01 with Echo
// (freshness, RFC 9175 §2.4), repeats it once with that Echo.
func (o *OSCOREClient) Do(ctx context.Context, code codes.Code, path string, query []string, cf *lwm2m.ContentFormat, body []byte) (*OSCOREResponse, error) {
	r, err := o.Raw(ctx, code, path, query, cf, body)
	if err == nil && r.Code == codes.Unauthorized && r.Echo != nil {
		r, err = o.Raw(ctx, code, path, query, cf, body, message.Option{ID: oscore.OptionEcho, Value: r.Echo})
	}
	return r, err
}

// Register registers over OSCORE, answering the server's Echo challenge.
func (o *OSCOREClient) Register(ctx context.Context) (*OSCOREResponse, error) {
	cf := lwm2m.FormatLinkFormat
	r, err := o.Do(ctx, codes.POST, "/rd", o.RegisterQuery(), &cf, []byte(o.ObjectLinks()))
	if err == nil && r.Code == codes.Created && len(r.Location) == 2 {
		o.mu.Lock()
		o.location = r.Location[1]
		o.mu.Unlock()
	}
	return r, err
}

// SetLocation sets the registration location segment.
func (o *OSCOREClient) SetLocation(id string) {
	o.mu.Lock()
	o.location = id
	o.mu.Unlock()
}

// Update sends POST /rd/<location> over OSCORE.
func (o *OSCOREClient) Update(ctx context.Context, query []string) (*OSCOREResponse, error) {
	return o.Do(ctx, codes.POST, "/rd/"+o.Location(), query, nil, nil)
}

// Deregister sends DELETE /rd/<location> over OSCORE.
func (o *OSCOREClient) Deregister(ctx context.Context) (*OSCOREResponse, error) {
	return o.Do(ctx, codes.DELETE, "/rd/"+o.Location(), nil, nil, nil)
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

func requestOf(m message.Message, typ message.Type) Request {
	r := Request{Code: m.Code, Type: typ, Token: append(message.Token(nil), m.Token...)}
	p, _ := m.Options.Path()
	r.Path = "/" + strings.TrimPrefix(p, "/")
	r.Queries, _ = m.Options.Queries()
	if cf, err := m.Options.ContentFormat(); err == nil {
		f := lwm2m.ContentFormat(cf)
		r.Format = &f
	}
	if a, err := m.Options.Accept(); err == nil {
		f := lwm2m.ContentFormat(a)
		r.Accept = &f
	}
	if v, err := m.Options.Observe(); err == nil {
		r.Observe = &v
	}
	r.Body = m.Payload
	return r
}

// handle verifies a request from the server (RFC 8613 §8.2), serves it and
// protects the answer (§8.3). Unprotected requests get 4.01.
func (o *OSCOREClient) handle(w mux.ResponseWriter, m *mux.Message) {
	if !m.HasOption(oscore.OptionOSCORE) {
		respond(w, codes.Unauthorized, nil, nil)
		return
	}
	in, err := toMessage(m.Message)
	if err != nil {
		return
	}
	inner, x, err := o.context().UnprotectRequest(in)
	switch {
	case errors.Is(err, oscore.ErrReplay), errors.Is(err, oscore.ErrNoContext):
		respond(w, codes.Unauthorized, nil, nil)
		return
	case errors.Is(err, oscore.ErrDecrypt):
		respond(w, codes.BadRequest, nil, nil)
		return
	case err != nil:
		respond(w, codes.BadOption, nil, nil)
		return
	}
	r := requestOf(inner, m.Type())
	o.mu.Lock()
	o.requests = append(o.requests, r)
	ov := o.override
	o.mu.Unlock()
	if o.RequireEcho && r.Code != codes.GET && r.Code != 5 {
		if echo := o.checkEcho(inner); echo != nil {
			o.reply(w, x, message.Message{Code: codes.Unauthorized, Options: message.Options{{ID: oscore.OptionEcho, Value: echo}}})
			return
		}
	}
	var code codes.Code
	var cf *lwm2m.ContentFormat
	var body []byte
	var extra []message.Option
	handled := false
	if ov != nil {
		code, cf, body, handled = ov(r)
	}
	if !handled {
		code, cf, body, extra = o.serve(r)
	}
	if r.Observe != nil && *r.Observe == 0 && code < 128 {
		o.omu.Lock()
		o.obs[string(r.Token)] = x
		o.omu.Unlock()
	}
	resp := message.Message{Code: code, Payload: body, Options: append(message.Options(nil), extra...)}
	if cf != nil {
		resp.Options = append(resp.Options, uintOpt(message.ContentFormat, uint32(*cf)))
	}
	resp.Options = sortOpts(resp.Options)
	o.reply(w, x, resp)
}

// checkEcho returns a new Echo value to challenge with, nil once the
// server echoed the last one.
func (o *OSCOREClient) checkEcho(inner message.Message) []byte {
	o.omu.Lock()
	defer o.omu.Unlock()
	if o.echoDone {
		return nil
	}
	if got, err := inner.Options.GetBytes(oscore.OptionEcho); err == nil && o.echo != nil && bytes.Equal(got, o.echo) {
		o.echoDone = true
		return nil
	}
	o.echo = make([]byte, 6)
	_, _ = rand.Read(o.echo)
	return o.echo
}

func (o *OSCOREClient) reply(w mux.ResponseWriter, x *oscore.Exchange, resp message.Message) {
	prot, err := o.context().ProtectResponse(resp, x, false)
	if err != nil {
		respond(w, codes.InternalServerError, nil, nil)
		return
	}
	var rd *bytes.Reader
	if len(prot.Payload) > 0 {
		rd = bytes.NewReader(prot.Payload)
	}
	if rd != nil {
		_ = w.SetResponse(prot.Code, message.TextPlain, rd)
	} else {
		_ = w.SetResponse(prot.Code, message.TextPlain, nil)
	}
	w.Message().ResetOptionsTo(prot.Options)
}

// Notify sends a protected CON notification with a fresh Partial IV for
// the observation with token tok (RFC 8613 §4.1.3.5.2).
func (o *OSCOREClient) Notify(ctx context.Context, tok message.Token) error {
	o.omu.Lock()
	x := o.obs[string(tok)]
	o.omu.Unlock()
	o.mu.Lock()
	ob, ok := o.observers[string(tok)]
	o.seq++
	seq := o.seq
	o.mu.Unlock()
	if x == nil || !ok {
		return errors.New("testclient: unknown observation")
	}
	base := ob.paths[0]
	nodes := o.Nodes(base)
	_, cf, body, _ := o.encode(base, o.responseFormat(base, ob.accept, nodes), nodes)
	if cf == nil {
		return errors.New("testclient: cannot encode notification")
	}
	m := message.Message{Code: codes.Content, Token: tok, Payload: body, MessageID: -1, Type: message.Confirmable,
		Options: message.Options{observeOpt(seq), uintOpt(message.ContentFormat, uint32(*cf))}}
	prot, err := o.context().ProtectResponse(m, x, true)
	if err != nil {
		return err
	}
	pm := o.conn.AcquireMessage(ctx)
	defer o.conn.ReleaseMessage(pm)
	pm.SetMessage(prot)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pm.SetContext(ctx)
	return o.conn.WriteMessage(pm)
}
