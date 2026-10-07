// Package est is the server side of EST over secure CoAP (EST-coaps,
// RFC 9148; EST, RFC 7030) for LwM2M Certificate mode with EST (T
// §5.2.9.5, Security Mode 4): CA certificate retrieval (/crts), simple
// enrollment (/sen) and simple re-enrollment (/sren) under the default
// root /.well-known/est. Server-side key generation (/skg, /skc) and CSR
// attributes (/att) are optional and not offered: the private key stays
// on the device (EST-03), and requests for them get 4.04 (RFC 9148 §4.5).
package est

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	piondtls "github.com/pion/dtls/v4"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
)

// Content-Formats (RFC 9148 §8.1).
const (
	FormatPKCS7CertsOnly uint16 = 281 // application/pkcs7-mime; smime-type=certs-only
	FormatCSRAttrs       uint16 = 285
	FormatPKCS10         uint16 = 286 // application/pkcs10
	FormatPKIXCert       uint16 = 287 // application/pkix-cert
)

// Root is the default EST-coaps root resource the server MUST support
// (RFC 9148 §4.1).
const Root = "/.well-known/est"

// CA issues client certificates.
type CA struct {
	Cert     *x509.Certificate
	Key      crypto.Signer
	Validity time.Duration    // default 1 year
	Now      func() time.Time // default time.Now

	mu     sync.Mutex
	serial int64
}

// NewTestCA returns a self-signed ECDSA P-256 CA (RFC 9148 §3: secp256r1
// MUST be supported).
func NewTestCA(cn string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, serial: 1}, nil
}

// Issue signs a client certificate for a verified CSR: the CSR's subject,
// SANs and public key, for TLS client and server authentication.
func (ca *CA) Issue(csr *x509.CertificateRequest) (*x509.Certificate, error) {
	now := time.Now
	if ca.Now != nil {
		now = ca.Now
	}
	validity := ca.Validity
	if validity == 0 {
		validity = 365 * 24 * time.Hour
	}
	ca.mu.Lock()
	ca.serial++
	serial := big.NewInt(ca.serial)
	ca.mu.Unlock()
	t := now()
	tmpl := &x509.Certificate{
		SerialNumber:   serial,
		Subject:        csr.Subject,
		DNSNames:       csr.DNSNames,
		EmailAddresses: csr.EmailAddresses,
		IPAddresses:    csr.IPAddresses,
		URIs:           csr.URIs,
		NotBefore:      t.Add(-time.Minute),
		NotAfter:       t.Add(validity),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// Identity is what the secure transport authenticated about the client.
// RFC 9148 §4 requires an authenticated client for every function.
type Identity struct {
	Cert        *x509.Certificate // DTLS client certificate (leaf)
	PSKIdentity string            // DTLS PSK identity
}

func (id Identity) authenticated() bool { return id.Cert != nil || id.PSKIdentity != "" }

// ErrForbidden is returned by Authorize to refuse with 4.03.
var ErrForbidden = errors.New("est: enrollment not allowed")

// Server serves the EST-coaps functions.
type Server struct {
	CA *CA
	// Authorize decides whether an authenticated client may enroll with
	// csr (/sen), e.g. that the CSR's Common Name is the LwM2M endpoint the
	// bootstrap credential belongs to. nil allows every authenticated
	// client. An error answers 4.03.
	Authorize func(id Identity, csr *x509.CertificateRequest) error
}

// Request is one EST-coaps request, binding-neutral.
type Request struct {
	Code    codes.Code
	Path    string  // e.g. /.well-known/est/sen
	Format  *uint16 // Content-Format
	Accept  *uint16
	Payload []byte
}

// Response is the reply.
type Response struct {
	Code    codes.Code
	Format  *uint16
	Payload []byte
}

func fail(c codes.Code) Response { return Response{Code: c} }

// op returns the short EST-coaps name (crts, sen, ...) of a path under the
// root, with or without an ArbitraryLabel segment.
func op(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, Root+"/")
	if !ok {
		return "", false
	}
	seg := strings.Split(rest, "/")
	if len(seg) > 2 || seg[len(seg)-1] == "" {
		return "", false
	}
	return seg[len(seg)-1], true
}

// Handle serves one request (RFC 9148 §4.2-§4.5).
func (s *Server) Handle(id Identity, r Request) Response {
	name, ok := op(r.Path)
	if !ok {
		return fail(codes.NotFound)
	}
	if !id.authenticated() {
		return fail(codes.Unauthorized)
	}
	switch name {
	case "crts":
		if r.Code != codes.GET {
			return fail(codes.MethodNotAllowed)
		}
		return s.certs(r.Accept, s.CA.Cert)
	case "sen", "sren":
		if r.Code != codes.POST {
			return fail(codes.MethodNotAllowed)
		}
		return s.enroll(id, r, name == "sren")
	}
	return fail(codes.NotFound) // skg, skc, att: not offered
}

// certs renders certificates as 281 (the default without Accept) or, for
// a single certificate, 287 (RFC 9148 §4.3).
func (s *Server) certs(accept *uint16, certs ...*x509.Certificate) Response {
	f := FormatPKCS7CertsOnly
	if accept != nil {
		f = *accept
	}
	switch {
	case f == FormatPKCS7CertsOnly:
		der, err := CertsOnly(certs...)
		if err != nil {
			return fail(codes.InternalServerError)
		}
		return Response{Code: codes.Content, Format: &f, Payload: der}
	case f == FormatPKIXCert && len(certs) == 1:
		return Response{Code: codes.Content, Format: &f, Payload: certs[0].Raw}
	}
	return fail(codes.NotAcceptable)
}

func (s *Server) enroll(id Identity, r Request, re bool) Response {
	if r.Format == nil || *r.Format != FormatPKCS10 {
		return fail(codes.UnsupportedMediaType)
	}
	if r.Accept != nil && *r.Accept != FormatPKCS7CertsOnly && *r.Accept != FormatPKIXCert {
		return fail(codes.NotAcceptable)
	}
	csr, err := x509.ParseCertificateRequest(r.Payload)
	if err != nil || csr.CheckSignature() != nil { // proof of possession
		return fail(codes.BadRequest)
	}
	if re {
		// RFC 7030 §4.2.2: re-enroll with a certificate this CA issued;
		// Subject and SubjectAltName must be those of that certificate.
		if !s.issued(id.Cert) || !sameNames(id.Cert, csr) {
			return fail(codes.Forbidden)
		}
	} else if s.Authorize != nil {
		if err := s.Authorize(id, csr); err != nil {
			return fail(codes.Forbidden)
		}
	}
	cert, err := s.CA.Issue(csr)
	if err != nil {
		return fail(codes.InternalServerError)
	}
	resp := s.certs(r.Accept, cert)
	resp.Code = codes.Changed // 2.04 for POST (RFC 9148 §4.5)
	return resp
}

func (s *Server) issued(c *x509.Certificate) bool {
	if c == nil {
		return false
	}
	pool := x509.NewCertPool()
	pool.AddCert(s.CA.Cert)
	_, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: c.NotBefore.Add(time.Minute)})
	return err == nil
}

func sameNames(c *x509.Certificate, r *x509.CertificateRequest) bool {
	str := func(vs ...fmt.Stringer) string {
		var b strings.Builder
		for _, v := range vs {
			b.WriteString(v.String() + "\x00")
		}
		return b.String()
	}
	var cs, rs []fmt.Stringer
	for _, ip := range c.IPAddresses {
		cs = append(cs, ip)
	}
	for _, u := range c.URIs {
		cs = append(cs, u)
	}
	for _, ip := range r.IPAddresses {
		rs = append(rs, ip)
	}
	for _, u := range r.URIs {
		rs = append(rs, u)
	}
	return c.Subject.String() == r.Subject.String() && slices.Equal(c.DNSNames, r.DNSNames) &&
		slices.Equal(c.EmailAddresses, r.EmailAddresses) && str(cs...) == str(rs...)
}

// SecurityInstance is the /0/iid instance a Bootstrap-Server writes for
// Certificate mode with EST (T §5.2.9.5, E.1): Security Mode 4 and the
// server certificate in /0/x/4 (EST-01). /0/x/3 and /0/x/5 are omitted
// (EST-03): the client's certificate comes from EST and its private key
// never leaves the device.
func SecurityInstance(iid uint16, serverURI string, bootstrapServer bool, serverCert *x509.Certificate) []lwm2m.Node {
	base := lwm2m.Path{}.Append(0).Append(iid)
	return []lwm2m.Node{
		lwm2m.ValueNode(base.Append(0), lwm2m.String(serverURI)),
		lwm2m.ValueNode(base.Append(1), lwm2m.Boolean(bootstrapServer)),
		lwm2m.ValueNode(base.Append(2), lwm2m.Integer(4)),
		lwm2m.ValueNode(base.Append(4), lwm2m.Opaque(serverCert.Raw)),
	}
}

// ASN.1 for a degenerate certs-only SignedData (RFC 5652 §5, RFC 8551
// smime-type=certs-only).
var (
	oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidData       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
)

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue
}

type signedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
	ContentInfo      struct{ ContentType asn1.ObjectIdentifier }
	Certificates     asn1.RawValue
	SignerInfos      []asn1.RawValue `asn1:"set"`
}

// CertsOnly encodes certificates as a PKCS #7 certs-only container
// (Content-Format 281), DER.
func CertsOnly(certs ...*x509.Certificate) ([]byte, error) {
	var raw bytes.Buffer
	for _, c := range certs {
		raw.Write(c.Raw)
	}
	sd := signedData{Version: 1, DigestAlgorithms: []pkix.AlgorithmIdentifier{}, SignerInfos: []asn1.RawValue{},
		Certificates: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: raw.Bytes()}}
	sd.ContentInfo.ContentType = oidData
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(contentInfo{ContentType: oidSignedData,
		Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: sdDER}})
}

// ParseCertsOnly decodes a PKCS #7 certs-only container.
func ParseCertsOnly(der []byte) ([]*x509.Certificate, error) {
	var ci contentInfo
	if rest, err := asn1.Unmarshal(der, &ci); err != nil || len(rest) > 0 {
		return nil, fmt.Errorf("est: bad ContentInfo: %v", err)
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return nil, errors.New("est: not SignedData")
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("est: bad SignedData: %w", err)
	}
	return x509.ParseCertificates(sd.Certificates.Bytes)
}

// Mount serves the EST-coaps resources under the default root (no
// ArbitraryLabel routes) on a go-coap router; the client
// identity comes from the DTLS session (RFC 9148 §3: client certificate,
// or the PSK of the bootstrap credential).
func (s *Server) Mount(r *mux.Router) error {
	h := mux.HandlerFunc(func(w mux.ResponseWriter, m *mux.Message) {
		req := Request{Code: m.Code()}
		p, _ := m.Options().Path()
		req.Path = "/" + strings.TrimPrefix(p, "/")
		if cf, err := m.ContentFormat(); err == nil {
			f := uint16(cf)
			req.Format = &f
		}
		if a, err := m.Options().Accept(); err == nil {
			f := uint16(a)
			req.Accept = &f
		}
		if m.Body() != nil {
			req.Payload, _ = m.ReadBody()
		}
		resp := s.Handle(identityOf(w.Conn()), req)
		var body *bytes.Reader
		if resp.Payload != nil {
			body = bytes.NewReader(resp.Payload)
		}
		if body != nil {
			_ = w.SetResponse(resp.Code, message.TextPlain, body)
		} else {
			_ = w.SetResponse(resp.Code, message.TextPlain, nil)
		}
		if resp.Format == nil {
			w.Message().Remove(message.ContentFormat)
		} else {
			w.Message().SetContentFormat(message.MediaType(*resp.Format))
		}
	})
	for _, name := range []string{"crts", "sen", "sren", "skg", "skc", "att"} {
		if err := r.Handle(Root+"/"+name, h); err != nil {
			return err
		}
	}
	return nil
}

func identityOf(c mux.Conn) Identity {
	dc, ok := c.NetConn().(interface{ ConnectionState() (piondtls.State, bool) }) // dtlscoap or pion Conn
	if !ok {
		return Identity{}
	}
	st, ok := dc.ConnectionState()
	if !ok {
		return Identity{}
	}
	if len(st.IdentityHint) > 0 {
		return Identity{PSKIdentity: string(st.IdentityHint)}
	}
	if len(st.PeerCertificates) > 0 {
		if cert, err := x509.ParseCertificate(st.PeerCertificates[0]); err == nil {
			return Identity{Cert: cert}
		}
	}
	return Identity{}
}
