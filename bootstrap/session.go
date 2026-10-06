package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

var (
	ErrCancelled      = errors.New("bootstrap: session replaced by a newer one")
	ErrSessionTimeout = errors.New("bootstrap: session exceeded its time bound")
	ErrFinishRejected = errors.New("bootstrap: client rejected Bootstrap-Finish")
	ErrPackRefused    = errors.New("bootstrap: Bootstrap-Pack refused")
)

// Step is one downlink request of a session and the client's answer.
type Step struct {
	Code   codes.Code // method
	Path   lwm2m.Path
	Format *lwm2m.ContentFormat // request Content-Format or Accept
	Result codes.Code           // response code; 0 if none came
}

// Result is the outcome of a bootstrap session or Pack request.
type Result struct {
	Endpoint string
	Identity server.Identity
	Pack     bool                // a Bootstrap-Pack-Request was answered
	Format   lwm2m.ContentFormat // format of the Writes (or of the Pack)
	Query    []string            // the request's query, unknown parameters included
	Steps    []Step
	Discover []link.Entry            // Bootstrap-Discover result
	Reads    map[string][]lwm2m.Node // Bootstrap-Read results by path
	Err      error                   // nil: Finish answered 2.04 (or Pack sent)
}

type session struct {
	s      *Server
	ep     string
	peer   server.Peer
	cfg    *BootstrapConfig
	ctx    context.Context
	cancel func(error)
	res    Result
}

// newSession registers a session for ep, cancelling a running one: the
// newest Bootstrap-Request wins (Leshan two_bootstrap_at_the_same_time).
func (s *Server) newSession(ep string, id server.Identity, peer server.Peer, cfg *BootstrapConfig, f lwm2m.ContentFormat, q []string) *session {
	base, cancelBase := context.WithCancelCause(context.Background())
	ctx, stop := context.WithTimeoutCause(base, s.cfg.SessionTimeout, ErrSessionTimeout) // BS-08
	cancel := func(err error) { cancelBase(err); stop() }
	ss := &session{s: s, ep: ep, peer: peer, cfg: cfg, ctx: ctx, cancel: cancel,
		res: Result{Endpoint: ep, Identity: id, Format: f, Query: q}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		cancel(ErrCancelled)
		return nil
	}
	if old := s.sessions[ep]; old != nil {
		old.cancel(ErrCancelled)
	}
	s.sessions[ep] = ss
	s.wg.Add(1)
	return ss
}

// schema types Bootstrap-Read replies (/1, /2) by the 1.2 object model.
var schema = model.Default().Schema(map[uint16]model.Version{
	0: model.DefaultVersion("1.2", 0), 1: model.DefaultVersion("1.2", 1), 2: model.DefaultVersion("1.2", 2),
})

// run drives the state machine: [Discover] → Delete* → Write* → Read* →
// Finish (C §6.1.3.3, BS-03..07). A Delete, Write or Read error does not
// end the session: the client decides on Finish whether the result is
// consistent (BS-07). A transport failure, cancellation or the session
// bound ends it without Finish.
func (ss *session) run() {
	defer ss.s.wg.Done()
	defer ss.finish()
	var bsIDs []uint16
	if ss.cfg.Discover || ss.cfg.AutoIDForSecurityObject {
		lf := lwm2m.FormatLinkFormat
		r, err := ss.do(codes.GET, lwm2m.Root, nil, &lf, nil)
		if err != nil {
			return
		}
		if r.Code == codes.Content {
			if links, err := link.Parse(string(r.Payload)); err == nil {
				ss.res.Discover, _ = link.ParseDiscover(links, "")
			}
		}
		// The BS account is the /0 instance without ssid (Leshan's rule).
		for _, e := range ss.res.Discover {
			if e.Path.IsInstance() && e.Path.Object() == 0 && !hasAttr(e, "ssid") {
				bsIDs = append(bsIDs, e.Path.Instance())
			}
		}
	}
	deletes, writes := ss.cfg.plan(bsIDs, false)
	for _, p := range deletes {
		if _, err := ss.do(codes.DELETE, p, nil, nil, nil); err != nil {
			return
		}
	}
	for _, w := range writes {
		if err := ss.write(w); err != nil {
			return
		}
	}
	for _, rp := range ss.cfg.Read {
		if err := ss.read(lwm2m.MustParsePath(rp)); err != nil {
			return
		}
	}
	// Bootstrap-Finish: POST /bs, empty payload (BS-07).
	r, err := ss.do(codes.POST, lwm2m.Root, nil, nil, nil)
	if err == nil && r.Code != codes.Changed {
		ss.res.Err = fmt.Errorf("%w: %s", ErrFinishRejected, server.CodeString(r.Code))
	}
}

func hasAttr(e link.Entry, name string) bool {
	for _, a := range e.Attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// candidates is the format order for a write or read: the session's
// format first, then the others the client may support (4.15 / 4.06
// fallback).
func (ss *session) candidates() []lwm2m.ContentFormat {
	out := []lwm2m.ContentFormat{ss.res.Format}
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatTLV, lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatLwM2MCBOR} {
		if f != ss.res.Format {
			out = append(out, f)
		}
	}
	return out
}

// write sends one Bootstrap-Write (PUT, BS-03). A 4.15 retries in the next
// format and the one that works becomes the session format.
func (ss *session) write(w Write) error {
	for _, f := range ss.candidates() {
		cd, err := codec.For(f)
		if err != nil {
			continue
		}
		body, err := cd.Encode(w.Path, w.Nodes)
		if err != nil {
			continue
		}
		r, err := ss.do(codes.PUT, w.Path, &f, nil, body)
		if err != nil {
			return err
		}
		if r.Code != codes.UnsupportedMediaType {
			if r.Code == codes.Changed {
				ss.res.Format = f
			}
			return nil
		}
	}
	return nil
}

// read sends one Bootstrap-Read (GET with Accept, BS-06); 4.06 retries in
// the next format. Unknown resources in the reply are kept, not errors.
func (ss *session) read(p lwm2m.Path) error {
	for _, f := range ss.candidates() {
		r, err := ss.do(codes.GET, p, nil, &f, nil)
		if err != nil {
			return err
		}
		if r.Code == codes.NotAcceptable {
			continue
		}
		if r.Code == codes.Content {
			cf := f
			if r.Format != nil {
				cf = *r.Format
			}
			if cd, err := codec.For(cf); err == nil {
				if nodes, err := cd.Decode(p, r.Payload, schema); err == nil {
					if ss.res.Reads == nil {
						ss.res.Reads = map[string][]lwm2m.Node{}
					}
					ss.res.Reads[p.String()] = nodes
				}
			}
		}
		return nil
	}
	return nil
}

// do sends one request, re-sending it as a new message after a timeout
// (T49), and records the step. Paths are plain /o/i/r: the BS never uses
// the alternate path (BS-09). Finish is POST on "/bs", sent as Root here.
func (ss *session) do(method codes.Code, p lwm2m.Path, cf, accept *lwm2m.ContentFormat, body []byte) (*server.Message, error) {
	path := p.String()
	if method == codes.POST {
		path = "/bs"
	}
	req := &server.Message{Code: method, Path: path, Format: cf, Accept: accept, Payload: body}
	step := Step{Code: method, Path: p, Format: cf}
	if cf == nil {
		step.Format = accept
	}
	var r *server.Message
	var err error
	for i := 0; ; i++ {
		actx, cancel := context.WithTimeout(ss.ctx, ss.s.cfg.RequestTimeout)
		r, err = ss.peer.Exchange(actx, req)
		retry := err != nil && actx.Err() != nil && ss.ctx.Err() == nil && i < ss.s.cfg.Retries
		cancel()
		if !retry {
			break
		}
	}
	if err == nil {
		step.Result = r.Code
	}
	ss.res.Steps = append(ss.res.Steps, step)
	if err != nil {
		if cause := context.Cause(ss.ctx); cause != nil {
			err = cause
		}
		ss.res.Err = err
	}
	return r, err
}

func (ss *session) finish() {
	ss.s.mu.Lock()
	if ss.s.sessions[ss.ep] == ss {
		delete(ss.s.sessions, ss.ep)
	}
	ss.s.mu.Unlock()
	if ss.res.Err == nil {
		if cause := context.Cause(ss.ctx); cause != nil {
			ss.res.Err = cause
		}
	}
	ss.cancel(nil)
	ss.s.report(ss.res)
}

// TriggerBootstrap starts Server-Initiated Bootstrap (C §6.1.3.4, BS-11):
// the LwM2M Server executes /1/{instance}/9 Bootstrap-Request Trigger on a
// registered client, which then sends Bootstrap-Request to its
// Bootstrap-Server.
func TriggerBootstrap(ctx context.Context, srv *server.Server, ep string, instance uint16) (*server.Response, error) {
	return srv.Execute(ctx, ep, lwm2m.NewPath(1, instance, 9), "")
}
