package dtlssuite

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// RawKey returns a credential that presents key as an RFC 7250 raw public
// key (security mode 1, SEC-09). The patched pion (third_party/pion-dtls)
// sends the SubjectPublicKeyInfo of PrivateKey; Certificate carries the
// same SPKI only because pion requires a non-empty chain.
func RawKey(key crypto.Signer) (tls.Certificate, error) {
	spki, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{spki}, PrivateKey: key}, nil
}

// ErrUnexpectedKey rejects a peer raw public key that is not the
// provisioned one.
var ErrUnexpectedKey = errors.New("dtlssuite: peer raw public key does not match")

// ExpectRawKey returns a VerifyPeerCertificate callback accepting exactly
// the peer key spki (the client side of SEC-09: /0/x/4 holds the server
// SPKI and the match is exact).
func ExpectRawKey(spki []byte) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) != 1 || !bytes.Equal(raw[0], spki) {
			return ErrUnexpectedKey
		}
		return nil
	}
}
