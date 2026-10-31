package apiv1

import "errors"

// ErrESTIssuerOwnershipAmbiguous requires explicit reconciliation of legacy anchors.
var ErrESTIssuerOwnershipAmbiguous = errors.New("legacy EST issuer anchor ownership is ambiguous")

// ErrESTIssuerMismatch indicates that the issuer and its trust anchor disagree.
var ErrESTIssuerMismatch = errors.New("EST issuer certificate and anchor mismatch")

// ValidateESTIssuerAnchor ensures the anchor is exactly the configured issuer.
func ValidateESTIssuerAnchor(c ESTConfig, a TrustAnchor) error {
	certs, ok := parseTrustAnchorCertificates(c.Certificate)
	if !ok || len(certs) != 1 || c.Certificate != a.PEM || len(a.Certificates) != 1 || certs[0].Fingerprint != a.Certificates[0].Fingerprint {
		return ErrESTIssuerMismatch
	}
	return nil
}

// ESTIssuerRotator atomically replaces the config and its managed trust anchor.
type ESTIssuerRotator interface {
	PutConfigRotatingIssuerAnchor(tenant string, config ESTConfig, anchor TrustAnchor) (existed bool, err error)
}

type memoryESTIssuerRotator struct {
	configs *memoryESTConfigStore
	anchors *memoryTrustAnchorStore
}

// NewMemoryESTIssuerRotator binds the memory stores used for config and anchor reads.
// Both arguments must be memory stores created by this package.
func NewMemoryESTIssuerRotator(configs ESTConfigStore, anchors TrustAnchorStore) ESTIssuerRotator {
	return &memoryESTIssuerRotator{configs.(*memoryESTConfigStore), anchors.(*memoryTrustAnchorStore)}
}

func (s *memoryESTIssuerRotator) PutConfigRotatingIssuerAnchor(tenant string, c ESTConfig, anchor TrustAnchor) (bool, error) {
	if err := ValidateESTIssuerAnchor(c, anchor); err != nil {
		return false, err
	}
	s.configs.mu.Lock()
	defer s.configs.mu.Unlock()
	s.anchors.mu.Lock()
	defer s.anchors.mu.Unlock()
	for _, a := range s.anchors.items[tenant] {
		if !a.ESTIssuer && !a.Manual {
			return false, ErrESTIssuerOwnershipAmbiguous
		}
	}
	old := make([]TrustAnchor, 0, 1)
	for id, a := range s.anchors.items[tenant] {
		if a.ESTIssuer {
			old = append(old, a)
			delete(s.anchors.items[tenant], id)
			s.anchors.count--
		}
	}
	anchor.ESTIssuer = true
	anchor.Manual = false
	if err := s.anchors.create(tenant, anchor); err != nil {
		for _, a := range old {
			s.anchors.items[tenant][a.ID] = a
			s.anchors.count++
		}
		return false, err
	}
	_, existed := s.configs.items[tenant]
	s.configs.items[tenant] = c
	return existed, nil
}
