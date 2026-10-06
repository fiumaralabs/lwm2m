package testclient

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"time"

	"github.com/fiumaralabs/lwm2m/dtlssuite"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options"
)

// DialDTLS connects to addr over DTLS 1.2 with a caller-built pion
// configuration (RPK, X.509, SNI, resumption and CID tests). Config.CID
// adds a send-only Connection ID generator when cfg has none.
func (c *Client) DialDTLS(addr string, cfg *piondtls.Config) error {
	if c.cfg.CID && cfg.ConnectionIDGenerator == nil {
		cfg.ConnectionIDGenerator = piondtls.OnlySendCIDGenerator()
	}
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(c.handle))
	conn, err := coapdtls.Dial(addr, cfg, options.WithMux(r), options.WithBlockwise(true, 0x6, 30*time.Second),
		options.WithProcessReceivedMessageFunc(c.process))
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// RPKConfig is a client in RPK mode (security mode 1): key is /0/x/5,
// serverSPKI is /0/x/4, exact match (SEC-09). suites nil offers CCM_8.
func RPKConfig(key crypto.Signer, serverSPKI []byte, suites ...piondtls.CipherSuiteID) (*piondtls.Config, error) {
	cert, err := dtlssuite.RawKey(key)
	if err != nil {
		return nil, err
	}
	rpk := []piondtls.CertificateType{piondtls.CertificateTypeRawPublicKey}
	return withSuites(&piondtls.Config{
		Certificates:           []tls.Certificate{cert},
		ClientCertificateTypes: rpk,
		ServerCertificateTypes: rpk,
		VerifyPeerCertificate:  dtlssuite.ExpectRawKey(serverSPKI),
	}, suites), nil
}

// X509Config is a client in Certificate mode (security mode 2): cert is
// /0/x/3 + /0/x/5, roots the trust anchor from /0/x/4, serverName the
// expected name and SNI (SEC-14, SEC-18). suites nil offers CCM_8.
func X509Config(cert tls.Certificate, roots *x509.CertPool, serverName string, suites ...piondtls.CipherSuiteID) *piondtls.Config {
	return withSuites(&piondtls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		ServerName:   serverName,
	}, suites)
}

// withSuites makes cfg offer exactly suites (default ECDHE_ECDSA CCM_8).
// 0xC023 comes from dtlssuite.Custom, which pion always offers first; to
// offer it alone, the ID list holds only a PSK suite, which pion drops for
// a config without a PSK callback.
func withSuites(cfg *piondtls.Config, suites []piondtls.CipherSuiteID) *piondtls.Config {
	if suites == nil {
		suites = []piondtls.CipherSuiteID{piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8}
	}
	for _, id := range suites {
		if id == dtlssuite.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 {
			cfg.CustomCipherSuites = dtlssuite.Custom
		} else {
			cfg.CipherSuites = append(cfg.CipherSuites, id)
		}
	}
	if cfg.CipherSuites == nil {
		cfg.CipherSuites = []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8}
	}
	return cfg
}
