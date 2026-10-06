// Package dtlssuite holds the DTLS 1.2 pieces LwM2M needs that pion/dtls
// does not ship: the TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 cipher suite
// (T §5.2.9.2-3, SEC-09/10), raw public key credentials (RFC 7250, SEC-09)
// and the /0/x/13 + /0/x/15 server certificate checks (SEC-19).
package dtlssuite

import (
	"crypto/sha256"
	"errors"
	"hash"
	"sync/atomic"

	dtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/dtls/v3/pkg/crypto/ciphersuite"
	"github.com/fiumaralabs/dtls/v3/pkg/crypto/clientcertificate"
	"github.com/fiumaralabs/dtls/v3/pkg/crypto/prf"
	"github.com/fiumaralabs/dtls/v3/pkg/protocol/recordlayer"
)

// TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 is 0xC023 (RFC 5289 §3.1).
const TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 dtls.CipherSuiteID = 0xC023 //nolint:revive // IANA name

// Custom returns fresh instances of the suites pion lacks, for
// dtls.Config.CustomCipherSuites (pion calls it once per connection).
func Custom() []dtls.CipherSuite { return []dtls.CipherSuite{&ecdheECDSAAES128CBCSHA256{}} }

// Resource16 lists the suites cfg accepts as /0/x/16 values (E.1 res 16:
// the two IANA bytes as one unsigned integer), so the Bootstrap-Server only
// provisions suites the server accepts (TLS13-02).
func Resource16(cfg *dtls.Config) []uint32 {
	var out []uint32
	if cfg.CustomCipherSuites != nil {
		for _, c := range cfg.CustomCipherSuites() {
			out = append(out, uint32(c.ID()))
		}
	}
	for _, id := range cfg.CipherSuites {
		out = append(out, uint32(id))
	}
	return out
}

// ecdheECDSAAES128CBCSHA256 follows RFC 5246 §6.2.3.2 (MAC-then-encrypt,
// explicit IV) with HMAC-SHA256 and the SHA-256 PRF (RFC 5289 §3.2).
type ecdheECDSAAES128CBCSHA256 struct {
	cbc atomic.Pointer[ciphersuite.CBC]
}

func (c *ecdheECDSAAES128CBCSHA256) String() string { return "TLS-ECDHE-ECDSA-WITH-AES-128-CBC-SHA256" }
func (c *ecdheECDSAAES128CBCSHA256) ID() dtls.CipherSuiteID {
	return TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256
}
func (c *ecdheECDSAAES128CBCSHA256) CertificateType() clientcertificate.Type {
	return clientcertificate.ECDSASign
}
func (c *ecdheECDSAAES128CBCSHA256) HashFunc() func() hash.Hash { return sha256.New }
func (c *ecdheECDSAAES128CBCSHA256) AuthenticationType() dtls.CipherSuiteAuthenticationType {
	return dtls.CipherSuiteAuthenticationTypeCertificate
}
func (c *ecdheECDSAAES128CBCSHA256) KeyExchangeAlgorithm() dtls.CipherSuiteKeyExchangeAlgorithm {
	return dtls.CipherSuiteKeyExchangeAlgorithmEcdhe
}
func (c *ecdheECDSAAES128CBCSHA256) ECC() bool           { return true }
func (c *ecdheECDSAAES128CBCSHA256) IsInitialized() bool { return c.cbc.Load() != nil }

func (c *ecdheECDSAAES128CBCSHA256) Init(masterSecret, clientRandom, serverRandom []byte, isClient bool) error {
	const macLen, keyLen, ivLen = 32, 16, 16
	k, err := prf.GenerateEncryptionKeys(masterSecret, clientRandom, serverRandom, macLen, keyLen, ivLen, sha256.New)
	if err != nil {
		return err
	}
	var cbc *ciphersuite.CBC
	if isClient {
		cbc, err = ciphersuite.NewCBC(k.ClientWriteKey, k.ClientWriteIV, k.ClientMACKey,
			k.ServerWriteKey, k.ServerWriteIV, k.ServerMACKey, sha256.New)
	} else {
		cbc, err = ciphersuite.NewCBC(k.ServerWriteKey, k.ServerWriteIV, k.ServerMACKey,
			k.ClientWriteKey, k.ClientWriteIV, k.ClientMACKey, sha256.New)
	}
	if err != nil {
		return err
	}
	c.cbc.Store(cbc)
	return nil
}

var errNotInit = errors.New("dtlssuite: cipher suite not initialized")

func (c *ecdheECDSAAES128CBCSHA256) Encrypt(pkt *recordlayer.RecordLayer, raw []byte) ([]byte, error) {
	cbc := c.cbc.Load()
	if cbc == nil {
		return nil, errNotInit
	}
	return cbc.Encrypt(pkt, raw)
}

func (c *ecdheECDSAAES128CBCSHA256) Decrypt(h recordlayer.Header, in []byte) ([]byte, error) {
	cbc := c.cbc.Load()
	if cbc == nil {
		return nil, errNotInit
	}
	return cbc.Decrypt(h, in)
}
