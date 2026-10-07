package bootstrap

import (
	"crypto/tls"
	"net"

	"github.com/fiumaralabs/lwm2m/transport/coap"

	"github.com/plgd-dev/go-coap/v3/mux"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/tcp"
	tcpClient "github.com/plgd-dev/go-coap/v3/tcp/client"
	tcpServer "github.com/plgd-dev/go-coap/v3/tcp/server"
)

// ListenTLS serves the Bootstrap interface over CoAP over TLS (coaps+tcp,
// RFC 8323, binding T) on addr. For X.509 clients set ClientAuth to
// tls.RequireAndVerifyClientCert with ClientCAs: only a verified chain is
// an authenticated identity. Go's crypto/tls implements neither TLS-PSK
// nor raw public keys (RFC 7250), so over TLS the BS bootstraps
// certificate clients; PSK clients use DTLS (ListenDTLS; RPK is not
// available, BS-10). A nil
// cfg serves plain TCP (coap+tcp, NoSec).
func (s *Server) ListenTLS(addr string, cfg *tls.Config) (net.Addr, error) {
	var l interface {
		tcpServer.Listener
		Addr() net.Addr
	}
	var err error
	if cfg == nil {
		l, err = coapnet.NewTCPListener("tcp", addr)
	} else {
		cfg = cfg.Clone()
		if len(cfg.NextProtos) == 0 {
			cfg.NextProtos = []string{"coap"} // RFC 8323 §11.7
		}
		l, err = coapnet.NewTLSListener("tcp", addr, cfg)
	}
	if err != nil {
		return nil, err
	}
	srv := tcp.NewServer(options.WithMux(mux.HandlerFunc(s.serveTCP)), options.WithBlockwise(true, coap.BlockSZX, s.cfg.RequestTimeout))
	s.mu.Lock()
	s.closers = append(s.closers, func() error { srv.Stop(); return nil })
	s.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
	return l.Addr(), nil
}

// serveTCP routes one message. go-coap's TCP connection ignores
// WithProcessReceivedMessageFunc, so the response is written here before
// the deferred session start runs (the first BS request follows the 2.04).
func (s *Server) serveTCP(w mux.ResponseWriter, m *mux.Message) {
	cc, ok := w.Conn().(*tcpClient.Conn)
	if !ok {
		return
	}
	s.router.ServeCOAP(w, m)
	if w.Message().IsModified() {
		if err := cc.Session().WriteMessage(w.Message()); err != nil {
			_ = cc.Close()
		}
		w.Message().SetModified(false)
	}
	if f, ok := s.afters.LoadAndDelete(cc); ok {
		f.(func())()
	}
}
