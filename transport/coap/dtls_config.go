package coap

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	piondtls "github.com/pion/dtls/v4"
)

// CertificateModes are the certificate-based security modes of a DTLS
// listener (T §5.2.9.3). PSK clients keep working on the same port, so
// one listener serves security modes 0 and 2. Mode 1 (RPK, RFC 7250)
// needs pion/dtls support that is pending upstream (branch
// rfc7250-raw-public-keys), so it is neither offered nor accepted.
type CertificateModes struct {
	// Certificates are the server's X.509 chains, chosen by the client's
	// SNI when there are several (SEC-14). A credential that is not an
	// X.509 chain (a raw key) is refused with server.ErrRPKUnsupported.
	Certificates []tls.Certificate
	// ClientCAs is the trust store for X.509 clients (SEC-10). nil
	// accepts no X.509 client.
	ClientCAs *x509.CertPool
	// SessionTTL bounds how long a session can be resumed (SEC-11);
	// default 24 h.
	SessionTTL time.Duration
}

// DTLSExtensions is the /0/x/23 bitmap of the TLS extensions this server
// supports (E.1 res 23): bit 0 SNI (SEC-14) and bit 12 Connection ID
// (CID-02). pion has no Max Fragment Length, Record Size Limit or the
// other listed extensions.
const DTLSExtensions uint32 = 1<<0 | 1<<12

// DTLSConfig returns the ListenDTLS configuration that serves PSK and
// X.509 clients with the suites LwM2M mandates (SEC-04, SEC-10), session
// resumption (SEC-11) and server-assigned Connection IDs (CID-01).
//
// Certificate clients must authenticate (ClientAuth RequireAndVerify): an
// X.509 client needs a chain to ClientCAs and a stored SecurityInfo with
// X509 for its CN. Anything else fails the handshake with
// bad_certificate (42), a "Fail" alert for the client (T Tbl 5.2.10-1).
func (b *Binding) DTLSConfig(m CertificateModes) (DTLSConfig, error) {
	cas := m.ClientCAs
	if cas == nil {
		cas = x509.NewCertPool()
	}
	ttl := m.SessionTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	var own [][]byte
	for _, c := range m.Certificates {
		if len(c.Certificate) == 0 {
			return DTLSConfig{}, server.ErrRPKUnsupported
		}
		if _, err := x509.ParseCertificate(c.Certificate[0]); err != nil {
			return DTLSConfig{}, server.ErrRPKUnsupported
		}
		if signer, ok := c.PrivateKey.(crypto.Signer); ok {
			if spki, err := x509.MarshalPKIXPublicKey(signer.Public()); err == nil {
				own = append(own, spki)
			}
		}
	}
	opts := []piondtls.ServerOption{
		piondtls.WithPSK(b.pskLookup),
		piondtls.WithClientAuth(piondtls.RequireAndVerifyClientCert),
		piondtls.WithClientCAs(cas),
		piondtls.WithVerifyPeerCertificate(func(raw [][]byte, chains [][]*x509.Certificate) error {
			return b.verifyPeer(raw, chains, own)
		}),
	}
	if len(m.Certificates) > 0 {
		opts = append(opts, piondtls.WithCertificates(m.Certificates...))
	}
	return DTLSConfig{Options: opts, SessionTTL: ttl}, nil
}

var errPeerCredential = errors.New("coap: client credential not accepted")

// verifyPeer authenticates a certificate-mode client during the handshake
// (SEC-01). pion has already verified X.509 chains against ClientCAs and
// the CertificateVerify signature for both kinds.
func (b *Binding) verifyPeer(raw [][]byte, chains [][]*x509.Certificate, own [][]byte) error {
	if len(raw) == 0 {
		return errPeerCredential
	}
	if len(chains) == 0 { // no chain to ClientCAs (pion verified it)
		return errPeerCredential
	}
	leaf := chains[0][0] // SEC-10
	if !strongKey(leaf.PublicKey) || isOwn(leaf.RawSubjectPublicKeyInfo, own) {
		return errPeerCredential
	}
	if si, ok := b.srv.Security().ByEndpoint(leaf.Subject.CommonName); !ok || !si.X509 {
		return errPeerCredential
	}
	return nil
}

// isOwn reports whether a client presents one of the server's own key
// pairs (SEC-11: Server, Bootstrap-Server and client keys differ).
func isOwn(spki []byte, own [][]byte) bool {
	for _, k := range own {
		if bytes.Equal(k, spki) {
			return true
		}
	}
	return false
}

// strongKey rejects ECDSA keys on curves under 255 bits (SEC-16).
func strongKey(pub any) bool {
	if k, ok := pub.(*ecdsa.PublicKey); ok {
		return k.Curve.Params().BitSize >= 255
	}
	return true
}
