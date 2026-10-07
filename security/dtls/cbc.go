// Package dtls holds the DTLS 1.2 pieces LwM2M needs that pion/dtls
// does not ship: the TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 cipher suite
// (T §5.2.9.2-3, SEC-09/10) and the /0/x/13 + /0/x/15 server certificate
// checks (SEC-19).
package dtls

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"hash"

	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
	"github.com/pion/dtls/v4/pkg/crypto/clientcertificate"
	"github.com/pion/dtls/v4/pkg/crypto/prf"
)

// TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 is 0xC023 (RFC 5289 §3.1).
const TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 ciphersuite.ID = 0xC023 //nolint:revive // IANA name

// Custom returns the suites pion lacks, for dtls.WithCustomCipherSuites.
// With an explicit dtls.WithCipherSuites list pion only uses the ones
// whose ID is in the list.
func Custom() []ciphersuite.Suite { return []ciphersuite.Suite{ecdheECDSAAES128CBCSHA256{}} }

// Resource16 lists suites as /0/x/16 values (E.1 res 16: the two IANA
// bytes as one unsigned integer), so the Bootstrap-Server only provisions
// suites the server accepts (TLS13-02).
func Resource16(suites []ciphersuite.ID) []uint32 {
	out := make([]uint32, len(suites))
	for i, id := range suites {
		out[i] = uint32(id)
	}
	return out
}

// ecdheECDSAAES128CBCSHA256 follows RFC 5246 §6.2.3.2 (MAC-then-encrypt,
// explicit IV) with HMAC-SHA256 and the SHA-256 PRF (RFC 5289 §3.2). The
// record protection follows pion's built-in CBC suites
// (internal/ciphersuite/record_protection12.go, MIT).
type ecdheECDSAAES128CBCSHA256 struct{}

func (ecdheECDSAAES128CBCSHA256) String() string { return "TLS-ECDHE-ECDSA-WITH-AES-128-CBC-SHA256" }
func (ecdheECDSAAES128CBCSHA256) ID() ciphersuite.ID {
	return TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256
}
func (ecdheECDSAAES128CBCSHA256) CertificateType() clientcertificate.Type {
	return clientcertificate.ECDSASign
}
func (ecdheECDSAAES128CBCSHA256) HashFunc() func() hash.Hash { return sha256.New }
func (ecdheECDSAAES128CBCSHA256) AuthenticationType() ciphersuite.AuthenticationType {
	return ciphersuite.AuthenticationTypeCertificate
}
func (ecdheECDSAAES128CBCSHA256) KeyExchangeAlgorithm() ciphersuite.KeyExchangeAlgorithm {
	return ciphersuite.KeyExchangeAlgorithmEcdhe
}
func (ecdheECDSAAES128CBCSHA256) ECC() bool { return true }

const (
	cbcMACLen   = 32 // HMAC-SHA256
	cbcKeyLen   = 16 // AES-128
	cbcBlockLen = aes.BlockSize
)

func (ecdheECDSAAES128CBCSHA256) Capabilities() ciphersuite.Capabilities {
	c, _ := ciphersuite.NewCBCCapabilities(1<<14, cbcMACLen, cbcBlockLen) // constants: cannot fail
	return c
}

func (ecdheECDSAAES128CBCSHA256) NewConnectionProtection(m ciphersuite.KeyMaterial) (ciphersuite.Protection, error) {
	k, err := prf.GenerateEncryptionKeys(m.MasterSecret(), m.ClientRandom(), m.ServerRandom(), cbcMACLen, cbcKeyLen, cbcBlockLen, sha256.New)
	if err != nil {
		return nil, err
	}
	wk, wmac, rk, rmac := k.ClientWriteKey, k.ClientMACKey, k.ServerWriteKey, k.ServerMACKey
	if m.Role() == ciphersuite.EndpointRoleServer {
		wk, wmac, rk, rmac = rk, rmac, wk, wmac
	}
	w, err := aes.NewCipher(bytes.Clone(wk))
	if err != nil {
		return nil, err
	}
	r, err := aes.NewCipher(bytes.Clone(rk))
	if err != nil {
		return nil, err
	}
	// The key block's IVs are unused: every record carries its own (§6.2.3.2).
	return &cbcProtection{write: w, read: r, writeMAC: bytes.Clone(wmac), readMAC: bytes.Clone(rmac)}, nil
}

type cbcProtection struct {
	write, read       cipher.Block
	writeMAC, readMAC []byte
}

func (c *cbcProtection) Seal(rec ciphersuite.Record, plaintext []byte) ([]byte, error) {
	mac, err := recordMAC(rec, plaintext, nil, c.writeMAC)
	if err != nil {
		return nil, err
	}
	payload := append(bytes.Clone(plaintext), mac...)
	pad := cbcBlockLen - len(payload)%cbcBlockLen
	for range pad {
		payload = append(payload, byte(pad-1))
	}
	out := make([]byte, cbcBlockLen+len(payload))
	if _, err := rand.Read(out[:cbcBlockLen]); err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(c.write, out[:cbcBlockLen]).CryptBlocks(out[cbcBlockLen:], payload)
	return out, nil
}

// Open decrypts and checks MAC and padding in constant time (Lucky 13).
func (c *cbcProtection) Open(rec ciphersuite.Record, protected []byte) ([]byte, error) {
	if len(protected)%cbcBlockLen != 0 || len(protected) < cbcBlockLen+max(cbcMACLen+1, cbcBlockLen) {
		return nil, ciphersuite.ErrAuthenticationFailed
	}
	body := make([]byte, len(protected)-cbcBlockLen)
	cipher.NewCBCDecrypter(c.read, protected[:cbcBlockLen]).CryptBlocks(body, protected[cbcBlockLen:])

	padLen, padGood := examinePadding(body)
	spaceGood := subtle.ConstantTimeLessOrEq(padLen, len(body)-cbcMACLen)
	good := int(padGood&1) & spaceGood
	padLen = subtle.ConstantTimeSelect(good, padLen, 1)
	end := len(body) - cbcMACLen - padLen
	want, err := recordMAC(rec, body[:end], body[end+cbcMACLen:], c.readMAC)
	if err != nil || subtle.ConstantTimeCompare(want, body[end:end+cbcMACLen])&good != 1 {
		return nil, ciphersuite.ErrAuthenticationFailed
	}
	return body[:end], nil
}

// recordMAC is the HMAC over the record's MAC input and payload; extra
// (the padding) is hashed after the digest so the work does not depend on
// the padding length.
func recordMAC(rec ciphersuite.Record, payload, extra, key []byte) ([]byte, error) {
	ad, err := rec.AuthenticationData(len(payload))
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, key)
	m.Write(ad)
	m.Write(payload)
	digest := m.Sum(nil)
	m.Write(extra)
	return digest, nil
}

// examinePadding is crypto/tls's constant-time padding check: the
// padding length + 1 and whether the padding is well formed (0xff).
func examinePadding(payload []byte) (toRemove int, good byte) {
	if len(payload) == 0 {
		return 0, 0
	}
	padLen := payload[len(payload)-1]
	t := uint(len(payload)-1) - uint(padLen)
	good = byte(int32(^t) >> 31)
	for i := range min(256, len(payload)) {
		t = uint(padLen) - uint(i)
		mask := byte(int32(^t) >> 31)
		b := payload[len(payload)-1-i]
		good &^= mask&padLen ^ mask&b
	}
	good &= good << 4
	good &= good << 2
	good &= good << 1
	good = uint8(int8(good) >> 7)
	padLen &= good
	return int(padLen) + 1, good
}
