package bootstrap

import (
	"crypto/x509"
	"fmt"

	"github.com/fiumaralabs/lwm2m/security/est"
)

// MountEST serves EST-coaps (RFC 9148) on the Bootstrap-Server's CoAP
// listeners, so a client in Certificate mode with EST (Security Mode 4,
// T §5.2.9.5) enrolls over its bootstrap (D)TLS session. Unless e has its
// own Authorize, enrollment is bound to the bootstrap credential: the
// CSR's Common Name must be the endpoint that the session's PSK identity
// or certificate belongs to in the security store.
func (s *Server) MountEST(e *est.Server) error {
	if e.Authorize == nil {
		e.Authorize = s.authorizeEST
	}
	return e.Mount(s.router)
}

func (s *Server) authorizeEST(id est.Identity, csr *x509.CertificateRequest) error {
	ep := ""
	switch {
	case id.PSKIdentity != "":
		if si, ok := s.cfg.Security.ByPSKIdentity(id.PSKIdentity); ok {
			ep = si.Endpoint
		}
	case id.Cert != nil:
		if si, ok := s.cfg.Security.ByEndpoint(id.Cert.Subject.CommonName); ok && si.X509 {
			ep = si.Endpoint
		}
	}
	if ep == "" || csr.Subject.CommonName != ep {
		return fmt.Errorf("%w: CSR CN %q is not the endpoint of the bootstrap credential", est.ErrForbidden, csr.Subject.CommonName)
	}
	return nil
}
