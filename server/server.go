// Package server implements the LwM2M Server: the Registration interface
// (C §6.2), Device Management (C §6.3), Information Reporting (C §6.4) and
// queue mode (T §6.5) over CoAP/UDP and CoAP/DTLS.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	dtlsServer "github.com/plgd-dev/go-coap/v3/dtls/server"
	"github.com/plgd-dev/go-coap/v3/mux"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
	udpServer "github.com/plgd-dev/go-coap/v3/udp/server"
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
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// Server is an LwM2M Server.
type Server struct {
	cfg      Config
	store    Store
	security SecurityStore
	router   *mux.Router

	mu       sync.Mutex
	udp      []*udpServer.Server
	dtls     []*dtlsServer.Server
	closers  []func() error
	stop     chan struct{}
	wg       sync.WaitGroup
	obs      *observations
	queues   *queues
	profiles *profileCache
	formats  sync.Map // registration ID -> learned multi-value format
	coap     coapBinding
	closed   bool
}

// New returns a Server. Call Listen* to start serving.
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
	s.router = mux.NewRouter()
	s.router.DefaultHandle(mux.HandlerFunc(s.serveCoAP))
	s.wg.Add(1)
	go s.expiryLoop()
	return s
}

// Store returns the registration store.
func (s *Server) Store() Store { return s.store }

// Security returns the security store.
func (s *Server) Security() SecurityStore { return s.security }

func (s *Server) emit(e Event) {
	if s.cfg.OnEvent != nil {
		s.cfg.OnEvent(e)
	}
}

// ListenUDP serves CoAP over plain UDP (NoSec, T §5.3) on addr and returns
// the bound address.
func (s *Server) ListenUDP(addr string) (net.Addr, error) {
	l, err := coapnet.NewListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	srv := udp.NewServer(
		options.WithMux(s.router),
		options.WithBlockwise(true, 0x6, s.cfg.RequestTimeout),
		options.WithProcessReceivedMessageFunc(s.processUDP),
	)
	s.mu.Lock()
	s.udp = append(s.udp, srv)
	s.closers = append(s.closers, l.Close)
	s.mu.Unlock()
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
func (s *Server) ListenDTLS(addr string, dc DTLSConfig) (net.Addr, error) {
	cfg := dc.Config
	if cfg == nil {
		cfg = &piondtls.Config{}
	}
	if cfg.PSK == nil {
		cfg.PSK = s.pskLookup
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
	l, err := coapnet.NewDTLSListener("udp", addr, cfg)
	if err != nil {
		return nil, err
	}
	srv := coapdtls.NewServer(
		options.WithMux(s.router),
		options.WithBlockwise(true, 0x6, s.cfg.RequestTimeout),
		options.WithProcessReceivedMessageFunc(s.processUDP),
	)
	s.mu.Lock()
	s.dtls = append(s.dtls, srv)
	s.closers = append(s.closers, l.Close)
	s.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
	return l.Addr(), nil
}

// pskLookup resolves the pre-shared key for a PSK identity (SEC-05).
func (s *Server) pskLookup(identity []byte) ([]byte, error) {
	si, ok := s.security.ByPSKIdentity(string(identity))
	if !ok || len(si.PSKKey) == 0 {
		return nil, fmt.Errorf("server: unknown PSK identity")
	}
	return si.PSKKey, nil
}

// Close stops all listeners and background work.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.stop)
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

// ctx returns a context bounded by the request timeout.
func (s *Server) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, s.cfg.RequestTimeout)
}

// Validator checks downlink payloads against a client's object model.
type Validator interface {
	CheckWrite(reg *Registration, nodes []lwm2m.Node) error
	CheckCreate(reg *Registration, object uint16, nodes []lwm2m.Node) error
}
