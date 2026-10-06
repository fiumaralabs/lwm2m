package server

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"

	"github.com/fiumaralabs/lwm2m/dtlssuite"
	piondtls "github.com/pion/dtls/v3"
)

// CertificateModes are the certificate-based security modes of a DTLS
// listener (T §5.2.9.2-3). PSK clients keep working on the same port, so
// one listener serves security modes 0, 1 and 2 (SEC-03).
type CertificateModes struct {
	// Certificates are the server credentials: X.509 chains, chosen by the
	// client's SNI when there are several (SEC-14), or dtlssuite.RawKey
	// keys. An RPK client gets the SubjectPublicKeyInfo of the selected
	// credential's key, the value the Bootstrap-Server writes to /0/x/4
	// (SEC-09).
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

// DTLSConfig returns the pion configuration for ListenDTLS that serves
// PSK, RPK (RFC 7250) and X.509 clients with the suites LwM2M mandates
// (SEC-04, SEC-09, SEC-10), session resumption (SEC-11) and server-
// assigned Connection IDs (CID-01, set by ListenDTLS).
//
// Certificate clients must authenticate (ClientAuth RequireAndVerify): an
// X.509 client needs a chain to ClientCAs and a stored SecurityInfo with
// X509 for its CN; an RPK client needs a key stored for exactly one
// endpoint. Anything else fails the handshake with bad_certificate (42),
// a "Fail" alert for the client (T Tbl 5.2.10-1).
func (s *Server) DTLSConfig(m CertificateModes) *piondtls.Config {
	cas := m.ClientCAs
	if cas == nil {
		cas = x509.NewCertPool()
	}
	ttl := m.SessionTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	var own [][]byte
	serverTypes := []piondtls.CertificateType{piondtls.CertificateTypeRawPublicKey}
	hasX509 := false
	for _, c := range m.Certificates {
		if signer, ok := c.PrivateKey.(crypto.Signer); ok {
			if spki, err := x509.MarshalPKIXPublicKey(signer.Public()); err == nil {
				own = append(own, spki)
			}
		}
		if len(c.Certificate) > 0 {
			if _, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
				hasX509 = true
			}
		}
	}
	if hasX509 {
		serverTypes = append(serverTypes, piondtls.CertificateTypeX509)
	}
	return &piondtls.Config{
		PSK:                    s.pskLookup,
		Certificates:           m.Certificates,
		CustomCipherSuites:     dtlssuite.Custom, // 0xC023
		ClientAuth:             piondtls.RequireAndVerifyClientCert,
		ClientCAs:              cas,
		ClientCertificateTypes: []piondtls.CertificateType{piondtls.CertificateTypeRawPublicKey, piondtls.CertificateTypeX509},
		ServerCertificateTypes: serverTypes,
		VerifyPeerCertificate: func(raw [][]byte, chains [][]*x509.Certificate) error {
			return s.verifyPeer(raw, chains, own)
		},
		SessionStore: newSessionStore(ttl, 100_000, time.Now),
	}
}

var errPeerCredential = errors.New("server: client credential not accepted")

// verifyPeer authenticates a certificate-mode client during the handshake
// (SEC-01). pion has already verified X.509 chains against ClientCAs and
// the CertificateVerify signature for both kinds.
func (s *Server) verifyPeer(raw [][]byte, chains [][]*x509.Certificate, own [][]byte) error {
	if len(raw) == 0 {
		return errPeerCredential
	}
	if len(chains) > 0 { // X.509 (SEC-10)
		leaf := chains[0][0]
		if !strongKey(leaf.PublicKey) {
			return errPeerCredential
		}
		if si, ok := s.security.ByEndpoint(leaf.Subject.CommonName); !ok || !si.X509 {
			return errPeerCredential
		}
		return nil
	}
	// RFC 7250 raw public key (SEC-09).
	pub, err := x509.ParsePKIXPublicKey(raw[0])
	if err != nil || !strongKey(pub) {
		return errPeerCredential
	}
	for _, k := range own {
		if bytes.Equal(k, raw[0]) {
			return errPeerCredential // SEC-11: never the server's own key pair
		}
	}
	if lk, ok := s.security.(PublicKeyLookup); ok {
		if _, ok := lk.ByPublicKey(raw[0]); !ok {
			return errPeerCredential
		}
	}
	return nil // other stores: Register still checks the exact key (SEC-06)
}

// strongKey rejects ECDSA keys on curves under 255 bits (SEC-16).
func strongKey(pub any) bool {
	if k, ok := pub.(*ecdsa.PublicKey); ok {
		return k.Curve.Params().BitSize >= 255
	}
	return true
}
