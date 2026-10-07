package testclient

import (
	"crypto/tls"
	"crypto/x509"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/fiumaralabs/lwm2m/security/dtls"
	piondtls "github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options"
)

// DialDTLS connects to addr over DTLS with caller-built pion options
// (X.509, SNI, resumption, CID and DTLS 1.3 tests). Config.CID adds a
// send-only Connection ID generator, which opts may override.
func (c *Client) DialDTLS(addr string, opts ...piondtls.ClientOption) error {
	if c.cfg.CID {
		opts = append([]piondtls.ClientOption{cidOption()}, opts...)
	}
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(c.handle))
	conn, err := dtlscoap.Dial(addr, opts, options.WithMux(r), options.WithBlockwise(true, 0x6, 30*time.Second),
		options.WithProcessReceivedMessageFunc(c.process))
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// cidOption asks for the server's CID without assigning one (RFC 9146).
func cidOption() piondtls.Option {
	return piondtls.WithConnectionID(piondtls.OnlySendCIDGenerator(), piondtls.CIDPathMigrationReject)
}

// PSKConfig is a client in PSK mode (security mode 0): identity is
// /0/x/3, key /0/x/5. It offers exactly suites (nil: TLS_PSK_WITH_AES_128_CCM_8,
// SEC-04).
func PSKConfig(identity string, key []byte, suites ...ciphersuite.ID) []piondtls.ClientOption {
	if suites == nil {
		suites = []ciphersuite.ID{ciphersuite.TLS_PSK_WITH_AES_128_CCM_8}
	}
	return []piondtls.ClientOption{
		piondtls.WithPSK(func([]byte) ([]byte, error) { return key, nil }),
		piondtls.WithPSKIdentityHint([]byte(identity)),
		piondtls.WithCipherSuites(suites...),
	}
}

func pskOptions(identity string, key []byte, cid bool) []piondtls.ClientOption {
	opts := PSKConfig(identity, key)
	if cid {
		opts = append(opts, cidOption())
	}
	return opts
}

// X509Config is a client in Certificate mode (security mode 2): cert is
// /0/x/3 + /0/x/5, roots the trust anchor from /0/x/4, serverName the
// expected name and SNI (SEC-14, SEC-18). It offers exactly suites (nil:
// ECDHE_ECDSA CCM_8); a DTLS 1.3 suite also needs
// piondtls.WithMaxVersion(protocol.Version1_3).
func X509Config(cert tls.Certificate, roots *x509.CertPool, serverName string, suites ...ciphersuite.ID) []piondtls.ClientOption {
	if suites == nil {
		suites = []ciphersuite.ID{ciphersuite.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8}
	}
	opts := []piondtls.ClientOption{
		piondtls.WithCertificates(cert),
		piondtls.WithServerName(serverName),
		piondtls.WithCipherSuites(suites...),
		piondtls.WithCustomCipherSuites(dtls.Custom), // 0xC023, used only if listed
	}
	if roots != nil {
		opts = append(opts, piondtls.WithRootCAs(roots))
	}
	return opts
}
