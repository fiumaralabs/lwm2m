package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options/config"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// Observation is an active Observe or Observe-Composite (C §6.4.1, §6.4.4).
type Observation struct {
	ID             string // token, hex
	RegistrationID string
	Endpoint       string
	Paths          []lwm2m.Path // one path, or the composite list
	Composite      bool
	Format         lwm2m.ContentFormat // composite request body format
	Accept         *lwm2m.ContentFormat
	Query          []string // 1.2 attributes in the Observe request (OBS-06)
	Created        time.Time

	token   message.Token
	lastSeq uint32
	lastAt  time.Time
	haveSeq bool
}

// observations indexes active observations by token.
type observations struct {
	mu      sync.Mutex
	byToken map[string]*Observation
}

func newObservations() *observations {
	return &observations{byToken: map[string]*Observation{}}
}

func (o *observations) add(ob *Observation) {
	o.mu.Lock()
	o.byToken[string(ob.token)] = ob
	o.mu.Unlock()
}

func (o *observations) get(tok message.Token) (*Observation, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ob, ok := o.byToken[string(tok)]
	return ob, ok
}

func (o *observations) remove(tok message.Token) (*Observation, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ob, ok := o.byToken[string(tok)]
	delete(o.byToken, string(tok))
	return ob, ok
}

func (o *observations) removeRegistration(regID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for k, ob := range o.byToken {
		if ob.RegistrationID == regID {
			delete(o.byToken, k)
		}
	}
}

func (o *observations) forRegistration(regID string) []*Observation {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []*Observation
	for _, ob := range o.byToken {
		if ob.RegistrationID == regID {
			out = append(out, ob)
		}
	}
	return out
}

// rebind is a no-op: notifications are matched by token, not by
// connection, so an address or session change keeps observations (C2).
func (o *observations) rebind(string, *client.Conn) {}

// Observations lists the active observations of an endpoint.
func (s *Server) Observations(ep string) []*Observation {
	reg, ok := s.store.ByEndpoint(ep)
	if !ok {
		return nil
	}
	return s.obs.forRegistration(reg.ID)
}

// ObserveOptions: Accept selects the notification format; Query carries
// 1.2 notification attributes in the request (OBS-06, OBS-05).
type ObserveOptions struct {
	Accept *lwm2m.ContentFormat
	Query  []string
}

// Observe starts observing p (OBS-01). The first response carries the
// current value; later values arrive as Notification events.
func (s *Server) Observe(ctx context.Context, ep string, p lwm2m.Path, o ObserveOptions) (*Observation, *Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, nil, err
	}
	if p.IsRoot() {
		return nil, nil, fmt.Errorf("%w: Observe on / (use ObserveComposite)", ErrBadRequest)
	}
	if len(o.Query) > 0 && reg.Version != "1.2" {
		return nil, nil, fmt.Errorf("%w: attributes in Observe need a 1.2 client (OBS-06)", ErrBadRequest)
	}
	ob := s.newObservation(reg, []lwm2m.Path{p}, false)
	ob.Accept, ob.Query = o.Accept, o.Query
	zero := uint32(0)
	return s.startObservation(ctx, reg, ob, request{method: codes.GET, path: p, query: o.Query, accept: o.Accept, observe: &zero, token: ob.token, schemaOf: p})
}

// ObserveComposite observes several paths with FETCH on / (OBS-05).
func (s *Server) ObserveComposite(ctx context.Context, ep string, paths []lwm2m.Path, o CompositeOptions) (*Observation, *Response, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range paths {
		if err := validateTarget(request{path: p}); err != nil {
			return nil, nil, err
		}
	}
	reqCF, acc := o.formats(reg)
	body, err := encodePaths(reqCF, paths)
	if err != nil {
		return nil, nil, err
	}
	ob := s.newObservation(reg, paths, true)
	ob.Format, ob.Accept = reqCF, &acc
	zero := uint32(0)
	return s.startObservation(ctx, reg, ob, request{method: codeFETCH, path: lwm2m.Root, cf: &reqCF, accept: &acc, body: body, observe: &zero, token: ob.token, schemaOf: lwm2m.Root})
}

func (s *Server) newObservation(reg *Registration, paths []lwm2m.Path, composite bool) *Observation {
	tok, err := message.GetToken()
	if err != nil {
		panic(err)
	}
	return &Observation{
		ID:             fmt.Sprintf("%x", []byte(tok)),
		RegistrationID: reg.ID,
		Endpoint:       reg.Endpoint,
		Paths:          paths,
		Composite:      composite,
		Created:        s.cfg.Now(),
		token:          tok,
	}
}

// startObservation registers the token before sending, so a notification
// racing the first response is still recognised.
func (s *Server) startObservation(ctx context.Context, reg *Registration, ob *Observation, rq request) (*Observation, *Response, error) {
	s.obs.add(ob)
	resp, err := s.exchange(ctx, reg, rq)
	if err != nil {
		s.obs.remove(ob.token)
		return nil, nil, err
	}
	if !resp.Success() {
		s.obs.remove(ob.token) // the client refused: nothing to observe
		return nil, resp, nil
	}
	return ob, resp, nil
}

// CancelObservation ends an observation. active sends a cancel to the
// client (GET/FETCH with Observe=1, OBS-02/OBS-05); passive only forgets
// it, and the next notification is answered with Reset (OBS-02, int-302).
func (s *Server) CancelObservation(ctx context.Context, ob *Observation, active bool) (*Response, error) {
	if _, ok := s.obs.remove(ob.token); !ok {
		return nil, fmt.Errorf("%w: unknown observation", ErrBadRequest)
	}
	if !active {
		return nil, nil
	}
	reg, ok := s.store.ByID(ob.RegistrationID)
	if !ok {
		return nil, ErrNotRegistered
	}
	one := uint32(1)
	if ob.Composite {
		body, err := encodePaths(ob.Format, ob.Paths) // exactly the same list (OBS-05)
		if err != nil {
			return nil, err
		}
		cf := ob.Format
		return s.exchange(ctx, reg, request{method: codeFETCH, path: lwm2m.Root, cf: &cf, accept: ob.Accept, body: body, observe: &one, token: ob.token, schemaOf: lwm2m.Root})
	}
	return s.exchange(ctx, reg, request{method: codes.GET, path: ob.Paths[0], accept: ob.Accept, observe: &one, token: ob.token, schemaOf: ob.Paths[0]})
}

// isResponseCode reports a 2.xx-5.xx code (a response, not a request).
func isResponseCode(c codes.Code) bool { return c >= 64 && c < 192 }

// processUDP is go-coap's received-message hook. It answers notifications
// for unknown observations with Reset (OBS-02): go-coap's own reply path
// would turn a Reset to a CON message into an ACK.
func (s *Server) processUDP(req *pool.Message, cc *client.Conn, handler config.HandlerFunc[*client.Conn]) {
	if isResponseCode(req.Code()) && req.HasOption(message.Observe) && len(req.Token()) > 0 {
		if _, ok := s.obs.get(req.Token()); !ok && (req.Type() == message.Confirmable || req.Type() == message.NonConfirmable) {
			s.sendReset(cc, req)
			cc.ReleaseMessage(req)
			return
		}
	}
	cc.ProcessReceivedMessageWithHandler(req, handler)
}

func (s *Server) sendReset(cc *client.Conn, req *pool.Message) {
	rst := cc.AcquireMessage(cc.Context())
	defer cc.ReleaseMessage(rst)
	rst.SetType(message.Reset)
	rst.SetCode(codes.Empty)
	rst.SetMessageID(req.MessageID())
	_ = cc.Session().WriteMessage(rst)
}

// handleDefault receives everything the router has no route for: the
// notifications of known observations (their token handler is gone after
// the first response).
func (s *Server) handleDefault(w mux.ResponseWriter, m *mux.Message) {
	if !isResponseCode(m.Code()) {
		reply(w, codes.NotFound)
		return
	}
	ob, ok := s.obs.get(m.Token())
	if !ok {
		return // a stray response; the empty ACK is sent automatically
	}
	reg, ok := s.store.ByID(ob.RegistrationID)
	if !ok {
		return
	}
	// Observe reordering (RFC 7641 §3.4): drop a notification older than the
	// last one unless 128 s have passed.
	if seq, err := m.Observe(); err == nil {
		now := s.cfg.Now()
		if ob.haveSeq && !newerNotification(ob.lastSeq, seq, ob.lastAt, now) {
			return
		}
		ob.lastSeq, ob.lastAt, ob.haveSeq = seq, now, true
	}
	base := lwm2m.Root
	if !ob.Composite {
		base = ob.Paths[0]
	}
	resp, err := s.decodeResponse(reg, m.Message, base)
	if err != nil {
		return
	}
	s.queues.wake(reg) // a notification means the client is awake (QM-03)
	s.emit(Notification{Registration: reg, Observation: ob, Response: resp})
	if !resp.Success() {
		s.obs.remove(ob.token) // an error notification ends the observation (RFC 7641 §3.2)
	}
}

// newerNotification implements RFC 7641 §3.4.
func newerNotification(v1, v2 uint32, t1, t2 time.Time) bool {
	const half = 1 << 23
	return (v1 < v2 && v2-v1 < half) || (v1 > v2 && v1-v2 > half) || t2.After(t1.Add(128*time.Second))
}
