package dtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
)

// Proves: TLS13-02
// /0/x/16 values are the IANA suite bytes as one integer (0xC0,0xA8 →
// 49320), the custom 0xC023 included.
func TestResource16(t *testing.T) {
	v := Resource16([]ciphersuite.ID{TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256, ciphersuite.TLS_PSK_WITH_AES_128_CCM_8})
	if len(v) != 2 || v[0] != 0xC023 || v[1] != 49320 || Custom()[0].ID() != 0xC023 {
		t.Fatalf("%v", v)
	}
}

func cert(t *testing.T, cn string, ca bool, parent *x509.Certificate, pk *ecdsa.PrivateKey, signer *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: ca, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if !ca {
		tmpl.DNSNames = []string{cn}
	}
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &pk.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

// Proves: SEC-19, SEC-18
// Each /0/x/15 usage accepts what RFC 6698 allows and rejects the rest:
// CA constraint and service certificate constraint need a PKIX path (and
// the provisioned name), trust anchor assertion needs a path to /0/x/4
// only, domain-issued pins the end entity and skips PKIX and the name.
func TestVerifyServerCertificate(t *testing.T) {
	rk, ik, lk := gen(t), gen(t), gen(t)
	root := cert(t, "root", true, nil, rk, rk)
	inter := cert(t, "inter", true, root, ik, rk)
	leaf := cert(t, "srv.lwm2m.test", false, inter, lk, ik)
	roots := x509.NewCertPool()
	roots.AddCert(root)
	chain := [][]byte{leaf.Raw, inter.Raw}
	other := cert(t, "other", true, nil, gen(t), gen(t))
	now := time.Now()
	ok := func(u CertificateUsage, m MatchingType, assoc []byte, pool *x509.CertPool, name string) error {
		return VerifyServerCertificate(u, m, assoc, chain, pool, name, now)
	}
	for _, c := range []struct {
		err   bool
		u     CertificateUsage
		m     MatchingType
		assoc []byte
		pool  *x509.CertPool
		name  string
	}{
		{false, UsageCAConstraint, MatchExact, root.Raw, roots, "srv.lwm2m.test"},
		{true, UsageCAConstraint, MatchExact, leaf.Raw, roots, "srv.lwm2m.test"},  // the EE is no CA constraint
		{true, UsageCAConstraint, MatchExact, root.Raw, roots, "evil.lwm2m.test"}, // SEC-18 name check
		{true, UsageCAConstraint, MatchExact, root.Raw, x509.NewCertPool(), ""},   // no PKIX path
		{false, UsageServiceCertConstraint, MatchSHA256, Digest(MatchSHA256, leaf.Raw), roots, ""},
		{true, UsageServiceCertConstraint, MatchSHA256, Digest(MatchSHA256, leaf.Raw), x509.NewCertPool(), ""},
		{false, UsageTrustAnchorAssertion, MatchExact, inter.Raw, nil, "srv.lwm2m.test"},
		{true, UsageTrustAnchorAssertion, MatchExact, other.Raw, roots, ""}, // roots are ignored
		{false, UsageDomainIssued, MatchSHA384, Digest(MatchSHA384, leaf.Raw), nil, "evil.lwm2m.test"},
		{true, UsageDomainIssued, MatchSHA512, Digest(MatchSHA256, leaf.Raw), nil, ""}, // wrong digest type
		{true, UsageDomainIssued, 9, leaf.Raw, nil, ""},                                // unknown matching type
		{true, 7, MatchExact, leaf.Raw, roots, ""},                                     // unknown usage
	} {
		if err := ok(c.u, c.m, c.assoc, c.pool, c.name); (err != nil) != c.err {
			t.Errorf("usage %d match %d name %q: err %v, want error %v", c.u, c.m, c.name, err, c.err)
		}
	}
}

func gen(t *testing.T) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
