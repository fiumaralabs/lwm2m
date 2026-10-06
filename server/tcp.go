package server

import (
	"crypto/tls"
	"net"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/mux"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/tcp"
	tcpClient "github.com/plgd-dev/go-coap/v3/tcp/client"
	tcpServer "github.com/plgd-dev/go-coap/v3/tcp/server"
)

// Binding T: CoAP over TCP and TLS (RFC 8323, T §6.8.2, TCP-01). go-coap
// sends the CSM first on every connection and answers Ping with Pong
// (RFC 8323 §5.3, §5.4); the LwM2M core is the same as for binding U,
// reached through serveCoAP and HandleUplink.

// tcpPeerKey stores a connection's *tcpPeer in its context.
type tcpPeerKey struct{}

// tcpPeer is the Peer of one CoAP-over-TCP/TLS connection. Requests to the
// client go back over this connection (GEN-12).
type tcpPeer struct{ coapPeer }

// Identity is the TLS client certificate when the handshake verified it
// (ModeX509, CN as endpoint). Go's crypto/tls has neither PSK nor raw
// public keys (RFC 7250), so a TLS session is X.509 or NoSec; a plain TCP
// or unverified-certificate session is NoSec, bound to the address.
func (p *tcpPeer) Identity() Identity {
	if tc, ok := p.cc.NetConn().(*tls.Conn); ok {
		if st := tc.ConnectionState(); len(st.VerifiedChains) > 0 {
			leaf := st.PeerCertificates[0]
			return Identity{Mode: ModeX509, CertCN: leaf.Subject.CommonName, Cert: leaf}
		}
	}
	return Identity{Mode: ModeNoSec, Addr: p.cc.RemoteAddr().String()}
}

// peerOf returns the Peer of a CoAP connection: its TCP peer, or a U one.
func (s *Server) peerOf(cc coapConn) Peer {
	if p, ok := cc.Context().Value(tcpPeerKey{}).(*tcpPeer); ok {
		return p
	}
	return s.coap.peer(cc, "U")
}

// withPort adds the scheme's default port to an addr that has none.
func withPort(addr, port string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, port)
	}
	return addr
}

// ListenTCP serves CoAP over TCP (coap+tcp, NoSec, TCP-02) on addr, port
// 5683 when addr has none, and returns the bound address.
func (s *Server) ListenTCP(addr string) (net.Addr, error) {
	l, err := coapnet.NewTCPListener("tcp", withPort(addr, "5683"))
	if err != nil {
		return nil, err
	}
	s.serveTCP(l)
	return l.Addr(), nil
}

// ListenTLS serves CoAP over TLS 1.2/1.3 (coaps+tcp, TCP-02) on addr, port
// 5684 when addr has none. For X.509 clients set ClientAuth to
// tls.RequireAndVerifyClientCert with ClientCAs; only a verified
// certificate is an authenticated identity.
// NextProtos defaults to the ALPN "coap" (RFC 8323 §11.7).
func (s *Server) ListenTLS(addr string, cfg *tls.Config) (net.Addr, error) {
	cfg = cfg.Clone()
	if len(cfg.NextProtos) == 0 {
		cfg.NextProtos = []string{"coap"}
	}
	l, err := coapnet.NewTLSListener("tcp", withPort(addr, "5684"), cfg)
	if err != nil {
		return nil, err
	}
	s.serveTCP(l)
	return l.Addr(), nil
}

func (s *Server) serveTCP(l tcpServer.Listener) {
	srv := tcp.NewServer(
		options.WithMux(mux.HandlerFunc(s.serveTCPMessage)),
		options.WithBlockwise(true, BlockSZX, s.cfg.RequestTimeout),
		options.WithOnNewConn(func(cc *tcpClient.Conn) {
			cc.SetContextValue(tcpPeerKey{}, &tcpPeer{coapPeer{cc: cc, binding: "T"}})
		}),
	)
	s.mu.Lock()
	s.closers = append(s.closers, func() error { srv.Stop(); return nil })
	s.mu.Unlock()
	go func() { _ = srv.Serve(l) }()
}

// serveTCPMessage handles every message on a TCP or TLS connection. The
// connection is ordered and reliable, so the Observe value of a
// notification is ignored (RFC 8323 §7.1) and there is no ACK or Reset: a
// notification for an unknown token is dropped, and the server cancels
// with GET/FETCH Observe=1 (RFC 8323 §7.4). go-coap's TCP connection
// ignores WithProcessReceivedMessageFunc, so the response is written here,
// before the handler's deferred actions run (GEN-10).
func (s *Server) serveTCPMessage(w mux.ResponseWriter, m *mux.Message) {
	cc, ok := w.Conn().(*tcpClient.Conn)
	if !ok {
		return
	}
	if isResponseCode(m.Code()) {
		m.Remove(message.Observe)
	}
	s.serveCoAP(w, m)
	if w.Message().IsModified() {
		if err := cc.Session().WriteMessage(w.Message()); err != nil {
			_ = cc.Close()
		}
		w.Message().SetModified(false)
	}
	s.coap.runAfters(cc)
}
