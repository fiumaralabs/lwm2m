package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// opensslWithRPK returns the openssl binary when it can do DTLS with RFC
// 7250 raw public keys (OpenSSL 3.2+), else skips.
func opensslWithRPK(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not installed")
	}
	help, _ := exec.Command(bin, "s_client", "-help").CombinedOutput()
	if !bytes.Contains(help, []byte("-enable_client_rpk")) || !bytes.Contains(help, []byte("-dtls1_2")) {
		t.Skip("openssl lacks DTLS 1.2 or RFC 7250 support")
	}
	return bin
}

func writePEM(t *testing.T, dir, name, typ string, ders ...[]byte) string {
	t.Helper()
	var b bytes.Buffer
	for _, d := range ders {
		_ = pem.Encode(&b, &pem.Block{Type: typ, Bytes: d})
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// coapRegister is a CON POST /rd?ep=..&lt=60&lwm2m=1.1 with a link-format body.
func coapRegister(ep string) []byte {
	m := []byte{0x41, 0x02, 0x12, 0x34, 0xab} // CON, TKL 1, POST, MID, token
	opt := func(delta int, v []byte) {
		h := len(m)
		m = append(m, 0)
		nib := func(x int) byte { return byte(min(x, 13)) } // 13: one extension byte follows
		m[h] = nib(delta)<<4 | nib(len(v))
		if delta >= 13 {
			m = append(m, byte(delta-13))
		}
		if len(v) >= 13 {
			m = append(m, byte(len(v)-13))
		}
		m = append(m, v...)
	}
	opt(11, []byte("rd"))
	opt(1, []byte{40}) // Content-Format 12: link-format
	opt(3, []byte("ep="+ep))
	opt(0, []byte("lt=60"))
	opt(0, []byte("lwm2m=1.1"))
	return append(append(m, 0xff), "</1/0>,</3/0>"...)
}

// opensslRegister runs openssl s_client as the LwM2M client, sends a
// Register and returns the CoAP response code.
func opensslRegister(t *testing.T, bin, addr, ep string, args ...string) byte {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"s_client", "-dtls1_2", "-connect", addr, "-quiet", "-nocommands"}, args...)...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if _, err := stdin.Write(coapRegister(ep)); err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() {
		b := make([]byte, 512)
		n, _ := io.ReadAtLeast(stdout, b, 4)
		got <- b[:n]
	}()
	select {
	case b := <-got:
		if len(b) < 4 || b[0]>>4 != 0x6 { // version 1, ACK
			t.Fatalf("no CoAP ACK from the server: %x\n%s", b, stderr.String())
		}
		return b[1]
	case <-time.After(10 * time.Second):
		t.Fatalf("openssl: no response\n%s", stderr.String())
	}
	return 0
}

// Proves: SEC-09, SEC-10, SEC-04
// Interop with an independent DTLS stack (OpenSSL 3.2+ s_client as the
// LwM2M client): the server negotiates RFC 7250 raw public keys in both
// directions and registers the client by its exact key; an X.509 client
// on TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 (0xC023, security/dtls) and a PSK
// client on TLS_PSK_WITH_AES_128_CBC_SHA256 register too.
func TestOpenSSLInterop(t *testing.T) {
	bin := opensslWithRPK(t)
	s := newSecure(t)
	dir := t.TempDir()

	// RPK: s_client needs a certificate file, but sends only its SPKI.
	rpkCert := s.pki.issue(t, "ignored")
	k := rpkCert.PrivateKey.(*ecdsa.PrivateKey)
	if err := s.srv.Security().Put(SecurityInfo{Endpoint: "ossl-rpk", PublicKey: spkiOf(t, k)}); err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	if code := opensslRegister(t, bin, s.addr, "ossl-rpk",
		"-enable_client_rpk", "-enable_server_rpk", "-cipher", "ECDHE-ECDSA-AES128-CCM8:@SECLEVEL=0",
		"-cert", writePEM(t, dir, "rpk.pem", "CERTIFICATE", rpkCert.Certificate[0]),
		"-key", writePEM(t, dir, "rpk.key", "PRIVATE KEY", der)); code != 0x41 {
		t.Fatalf("RPK Register: code %s", CodeString(codes.Code(code)))
	}
	reg, ok := s.srv.Store().ByEndpoint("ossl-rpk")
	if !ok || reg.Identity.Mode != ModeRPK {
		t.Fatalf("RPK registration %v %+v", ok, reg)
	}

	// X.509 over 0xC023.
	xc := s.x509Client("ossl-x509")
	der, _ = x509.MarshalPKCS8PrivateKey(xc.PrivateKey)
	if code := opensslRegister(t, bin, s.addr, "ossl-x509", "-cipher", "ECDHE-ECDSA-AES128-SHA256",
		"-cert", writePEM(t, dir, "x.pem", "CERTIFICATE", xc.Certificate[0]),
		"-cert_chain", writePEM(t, dir, "chain.pem", "CERTIFICATE", xc.Certificate[1]),
		"-key", writePEM(t, dir, "x.key", "PRIVATE KEY", der)); code != 0x41 {
		t.Fatalf("X.509 Register: code %s", CodeString(codes.Code(code)))
	}
	if reg, ok := s.srv.Store().ByEndpoint("ossl-x509"); !ok || reg.Identity.Mode != ModeX509 {
		t.Fatalf("X.509 registration %v", ok)
	}

	// PSK over 0x00AE.
	if err := s.srv.Security().Put(SecurityInfo{Endpoint: "ossl-psk", PSKIdentity: "ossl-psk", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	if code := opensslRegister(t, bin, s.addr, "ossl-psk", "-cipher", "PSK-AES128-CBC-SHA256",
		"-psk_identity", "ossl-psk", "-psk", "30313233343536373839616263646566"); code != 0x41 {
		t.Fatalf("PSK Register: code %s", CodeString(codes.Code(code)))
	}
}
