package dtls

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// CertificateUsage is /0/x/15 (RFC 6698 §2.1.1 semantics, E.1 res 15).
type CertificateUsage uint8

const (
	UsageCAConstraint          CertificateUsage = 0 // PKIX path that contains /0/x/4
	UsageServiceCertConstraint CertificateUsage = 1 // PKIX path whose end entity is /0/x/4
	UsageTrustAnchorAssertion  CertificateUsage = 2 // /0/x/4 is the only trust anchor
	UsageDomainIssued          CertificateUsage = 3 // end entity is /0/x/4, no PKIX (default)
)

// MatchingType is /0/x/13: how /0/x/4 is compared with a certificate.
type MatchingType uint8

const (
	MatchExact  MatchingType = 0 // /0/x/4 is the certificate DER (default)
	MatchSHA256 MatchingType = 1
	MatchSHA384 MatchingType = 2
	MatchSHA512 MatchingType = 3
)

// ErrCertificateUsage reports a server certificate that does not satisfy
// the provisioned /0/x/4, /0/x/13 and /0/x/15.
var ErrCertificateUsage = errors.New("dtls: server certificate does not satisfy the certificate usage")

// Digest is the /0/x/4 value for cert under matching type m (nil for an
// unknown type).
func Digest(m MatchingType, cert []byte) []byte {
	switch m {
	case MatchExact:
		return cert
	case MatchSHA256:
		s := sha256.Sum256(cert)
		return s[:]
	case MatchSHA384:
		s := sha512.Sum384(cert)
		return s[:]
	case MatchSHA512:
		s := sha512.Sum512(cert)
		return s[:]
	}
	return nil
}

func (m MatchingType) matches(cert, assoc []byte) bool {
	d := Digest(m, cert)
	return d != nil && bytes.Equal(d, assoc)
}

// VerifyServerCertificate checks the chain a server presents (leaf first)
// against what the Bootstrap-Server provisioned: assoc is /0/x/4, m is
// /0/x/13, usage is /0/x/15 (SEC-19). roots is the PKIX trust store for
// usages 0 and 1. dnsName, when not empty, must be in the certificate
// (RFC 6125 via RFC 7925 §4.4.1, SEC-18); usage 3 skips it because the
// certificate itself is pinned. Clients and the Bootstrap-Server use it to
// confirm a server credential matches its provisioning.
func VerifyServerCertificate(usage CertificateUsage, m MatchingType, assoc []byte, rawChain [][]byte,
	roots *x509.CertPool, dnsName string, now time.Time) error {
	if len(rawChain) == 0 {
		return ErrCertificateUsage
	}
	certs := make([]*x509.Certificate, len(rawChain))
	inter := x509.NewCertPool()
	for i, raw := range rawChain {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return err
		}
		certs[i] = c
		if i > 0 {
			inter.AddCert(c)
		}
	}
	leaf := certs[0]
	verify := func(r *x509.CertPool) ([][]*x509.Certificate, error) {
		return leaf.Verify(x509.VerifyOptions{Roots: r, Intermediates: inter, DNSName: dnsName, CurrentTime: now})
	}
	switch usage {
	case UsageDomainIssued:
		if m.matches(leaf.Raw, assoc) {
			return nil
		}
	case UsageServiceCertConstraint:
		if _, err := verify(roots); err != nil {
			return err
		}
		if m.matches(leaf.Raw, assoc) {
			return nil
		}
	case UsageCAConstraint:
		chains, err := verify(roots)
		if err != nil {
			return err
		}
		for _, ch := range chains {
			for _, c := range ch[1:] {
				if m.matches(c.Raw, assoc) {
					return nil
				}
			}
		}
	case UsageTrustAnchorAssertion:
		ta := x509.NewCertPool()
		if m == MatchExact {
			c, err := x509.ParseCertificate(assoc)
			if err != nil {
				return err
			}
			ta.AddCert(c)
		} else {
			for _, c := range certs[1:] {
				if m.matches(c.Raw, assoc) {
					ta.AddCert(c)
				}
			}
		}
		if _, err := verify(ta); err != nil {
			return fmt.Errorf("%w: %w", ErrCertificateUsage, err)
		}
		return nil
	}
	return ErrCertificateUsage
}
