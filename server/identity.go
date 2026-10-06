package server

import (
	"bytes"
	"crypto/x509"
)

// SecurityMode is the transport security a client used (T §5.2, SEC-08).
type SecurityMode uint8

const (
	ModeNoSec SecurityMode = iota
	ModePSK
	ModeRPK
	ModeX509
)

func (m SecurityMode) String() string {
	return [...]string{"nosec", "psk", "rpk", "x509"}[m]
}

// Identity is what the transport authenticated about a peer. It is fixed
// for a DTLS session and compared against the registration (SEC-06).
type Identity struct {
	Mode        SecurityMode
	PSKIdentity string // ModePSK
	PublicKey   []byte // ModeRPK: SubjectPublicKeyInfo DER
	CertCN      string // ModeX509: leaf certificate Common Name
	Cert        *x509.Certificate
	Addr        string // ModeNoSec: the only identity is the source address
}

// Equal reports whether two identities name the same authenticated peer.
// NoSec identities are never equal to secured ones.
func (a Identity) Equal(b Identity) bool {
	if a.Mode != b.Mode {
		return false
	}
	switch a.Mode {
	case ModePSK:
		return a.PSKIdentity == b.PSKIdentity
	case ModeRPK:
		return bytes.Equal(a.PublicKey, b.PublicKey)
	case ModeX509:
		return a.Cert != nil && b.Cert != nil && bytes.Equal(a.Cert.Raw, b.Cert.Raw)
	}
	return a.Addr == b.Addr
}

// Secure reports whether the identity was authenticated by (D)TLS.
func (a Identity) Secure() bool { return a.Mode != ModeNoSec }
