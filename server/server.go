// Package server implements the LwM2M Server core: the Registration
// interface (C §6.2), Device Management (C §6.3), Information Reporting
// (C §6.4), Send (C §6.4.6) and queue mode (T §6.5).
//
// The core is binding-neutral. A transport delivers every uplink message
// with HandleUplink and carries downlinks through its Peer:
// transport/coap serves CoAP over UDP, DTLS, TCP/TLS and WebSockets, and
// the transport/mqtt, http, sms, nidd and lorawan packages the other
// bindings.
//
//	srv := server.New(server.Config{})
//	cb := coap.New(srv) // github.com/fiumaralabs/lwm2m/transport/coap
//	cb.ListenUDP(":5683")
//	defer srv.Close()
//	defer cb.Close()
package server

import (
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
)

// Config configures a Server. Zero values select spec defaults.
type Config struct {
	Store    Store         // registrations; default in-memory
	Security SecurityStore // credentials; default in-memory
	// OnEvent receives registration, notification and send events. It is
	// called synchronously from protocol goroutines; it must not block.
	OnEvent func(Event)
	// ExpiryCheck is the lifetime-expiry sweep interval (REG-13).
	ExpiryCheck time.Duration
	// QueueAwake is how long a queue-mode client stays reachable after its
	// last message: MAX_TRANSMIT_WAIT, 93 s (QM-03).
	QueueAwake time.Duration
	// RequestTimeout bounds one downlink exchange including retransmissions:
	// EXCHANGE_LIFETIME is 247 s, MAX_TRANSMIT_WAIT 93 s (RFC 7252 §4.8.2).
	RequestTimeout time.Duration
	// ShortServerID is this server's SSID on its clients (/1/x/0), used to
	// find its Server object instance. 0 means "the only instance".
	ShortServerID uint16
	// Authorize, if set, decides whether an authenticated endpoint may
	// register; false answers 4.03 (REG-06).
	Authorize func(ep string, id Identity) bool
	// Profiles resolves pre-configured Profile IDs ("oma:..", "v:..") to
	// object lists (PROF-04). Dynamic IDs are learned automatically.
	Profiles map[string][]link.Object
	// Schema returns the model used to type values of a client (e.g.
	// model.Registry.Schema for its object versions). nil leaves untyped
	// values to format defaults.
	Schema func(*Registration) lwm2m.Schema
	// Validator, if set, checks writes and creates against the client's
	// model before they are sent (DM-06, DM-09).
	Validator Validator
	// OnQueued, if set, is called when a request has to wait for a sleeping
	// queue-mode client, so the application can wake it (e.g. an SMS
	// trigger, T §6.6). It must not block.
	OnQueued func(*Registration)
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// Server is an LwM2M Server.
type Server struct {
	cfg      Config
	store    Store
	security SecurityStore

	mu       sync.Mutex
	stop     chan struct{}
	wg       sync.WaitGroup
	obs      *observations
	queues   *queues
	profiles *profileCache
	formats  sync.Map // registration ID -> learned multi-value format
	live     sync.Map // Peer -> struct{}: sessions of live registrations (Registered)
	closed   bool
}

// New returns a Server. Connect transports to it (e.g. coap.New) to serve
// clients.
func New(cfg Config) *Server {
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore()
	}
	if cfg.Security == nil {
		cfg.Security = NewMemorySecurityStore()
	}
	if cfg.ExpiryCheck == 0 {
		cfg.ExpiryCheck = time.Second
	}
	if cfg.QueueAwake == 0 {
		cfg.QueueAwake = 93 * time.Second
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 93 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Server{
		cfg:      cfg,
		store:    cfg.Store,
		security: cfg.Security,
		stop:     make(chan struct{}),
		obs:      newObservations(),
		profiles: newProfileCache(cfg.Profiles),
	}
	s.queues = newQueues(s)
	s.wg.Add(1)
	go s.expiryLoop()
	return s
}

// Store returns the registration store.
func (s *Server) Store() Store { return s.store }

// Security returns the security store.
func (s *Server) Security() SecurityStore { return s.security }

// Config returns the configuration in effect, defaults applied.
// Transports take their clock and request timeout from it.
func (s *Server) Config() Config { return s.cfg }

func (s *Server) emit(e Event) {
	s.trackSession(e)
	if s.cfg.OnEvent != nil {
		s.cfg.OnEvent(e)
	}
}

// Close stops background work. It does not stop transports: close them
// first.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.stop)
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

// Registered reports whether p is the session of a live registration.
// Transports that close idle sessions use it to keep a registered
// client's session open: a DTLS session, and its Connection ID, lasts as
// long as the registration (T §5.2.8).
func (s *Server) Registered(p Peer) bool {
	_, ok := s.live.Load(p)
	return ok
}

// trackSession follows the session of every registration for Registered.
func (s *Server) trackSession(e Event) {
	switch e := e.(type) {
	case Registered:
		s.hold(e.Registration.peer)
		if e.Replaced != nil && e.Replaced.peer != e.Registration.peer {
			s.release(e.Replaced.peer)
		}
	case Updated:
		s.hold(e.Registration.peer)
		if e.Previous.peer != e.Registration.peer {
			s.release(e.Previous.peer)
		}
	case Deregistered: // client, expiry, replacement, operator
		s.release(e.Registration.peer)
	}
}

func (s *Server) hold(p Peer) {
	if p != nil {
		s.live.Store(p, struct{}{})
	}
}

func (s *Server) release(p Peer) {
	if p != nil {
		s.live.Delete(p)
	}
}

// expiryLoop removes registrations whose lifetime elapsed (REG-13).
func (s *Server) expiryLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.ExpiryCheck)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.expireNow()
		}
	}
}

// expireNow runs one expiry sweep.
func (s *Server) expireNow() {
	now := s.cfg.Now()
	for _, r := range s.store.All() {
		if r.Expired(now) {
			if _, ok := s.store.Remove(r.ID); ok {
				s.dropClientState(r)
				s.emit(Deregistered{Registration: r, Reason: ReasonExpired})
			}
		}
	}
}

// dropClientState voids observations and queued requests of a registration
// that ended (REG-12, REG-13, REG-16, OBS-03).
func (s *Server) dropClientState(r *Registration) {
	s.obs.removeRegistration(r.ID)
	s.queues.drop(r.ID)
	s.formats.Delete(r.ID)
}

// Validator checks downlink payloads against a client's object model.
type Validator interface {
	CheckWrite(reg *Registration, nodes []lwm2m.Node) error
	CheckCreate(reg *Registration, object uint16, nodes []lwm2m.Node) error
}
