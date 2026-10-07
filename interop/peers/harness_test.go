//go:build interop

// Package peers runs our LwM2M Server and Bootstrap-Server in-process against
// real open-source LwM2M clients (Wakaama, Anjay, Anjay Lite) built by
// Dockerfile in this directory. Each test skips when its client binary is
// not available (env WAKAAMA_CLIENT, ANJAY_DEMO, ANJAY_LITE_APP, ...).
package peers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/transport/coap"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

const evTimeout = 30 * time.Second

func p(s string) lwm2m.Path { return lwm2m.MustParsePath(s) }

func cf(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// bin returns the peer binary named by env, or skips the test.
func bin(t *testing.T, env string) string {
	t.Helper()
	b := os.Getenv(env)
	if b == "" {
		t.Skipf("%s not set: run inside the interop/peers image", env)
	}
	if _, err := os.Stat(b); err != nil {
		t.Skipf("%s: %v", env, err)
	}
	return b
}

// events records server events; wait consumes the first unconsumed match,
// so events that arrive before the test asks for them are not lost.
type events struct {
	mu   sync.Mutex
	l    []server.Event
	used []bool
	ping chan struct{}
}

func newEvents() *events { return &events{ping: make(chan struct{}, 1)} }

func (e *events) on(ev server.Event) {
	e.mu.Lock()
	e.l = append(e.l, ev)
	e.used = append(e.used, false)
	e.mu.Unlock()
	select {
	case e.ping <- struct{}{}:
	default:
	}
}

func (e *events) take(match func(server.Event) bool) server.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, ev := range e.l {
		if !e.used[i] && match(ev) {
			e.used[i] = true
			return ev
		}
	}
	return nil
}

func (e *events) wait(t *testing.T, d time.Duration, what string, match func(server.Event) bool) server.Event {
	t.Helper()
	deadline := time.After(d)
	for {
		if ev := e.take(match); ev != nil {
			return ev
		}
		select {
		case <-e.ping:
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out after %v waiting for %s", d, what)
			return nil
		}
	}
}

// none reports a failure if an event matching match arrives within d.
func (e *events) none(t *testing.T, d time.Duration, what string, match func(server.Event) bool) {
	t.Helper()
	time.Sleep(d)
	if ev := e.take(match); ev != nil {
		t.Fatalf("unexpected %s: %#v", what, ev)
	}
}

func isRegistered(ep string) func(server.Event) bool {
	return func(ev server.Event) bool {
		r, ok := ev.(server.Registered)
		return ok && r.Registration.Endpoint == ep
	}
}

func isUpdated(ep string) func(server.Event) bool {
	return func(ev server.Event) bool {
		r, ok := ev.(server.Updated)
		return ok && r.Registration.Endpoint == ep
	}
}

func isDeregistered(ep string) func(server.Event) bool {
	return func(ev server.Event) bool {
		r, ok := ev.(server.Deregistered)
		return ok && r.Registration.Endpoint == ep
	}
}

func isNotification(ob *server.Observation) func(server.Event) bool {
	return func(ev server.Event) bool {
		n, ok := ev.(server.Notification)
		return ok && n.Observation.ID == ob.ID
	}
}

func isSend(ep string) func(server.Event) bool {
	return func(ev server.Event) bool {
		s, ok := ev.(server.SendReceived)
		return ok && s.Registration.Endpoint == ep
	}
}

// env is one LwM2M Server (UDP, DTLS with CID, TCP) with its own events.
type env struct {
	t    *testing.T
	srv  *server.Server
	sec  *server.MemorySecurityStore
	ev   *events
	udp  int
	dtls int
	tcp  int
	ctx  context.Context
	cert *serverCert
}

type envOpt func(*server.Config, *coap.CertificateModes)

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	e := &env{t: t, ev: newEvents(), sec: server.NewMemorySecurityStore()}
	models := server.NewModels(model.Default())
	cfg := server.Config{OnEvent: e.ev.on, Security: e.sec, Schema: models.Schema,
		RequestTimeout: 20 * time.Second}
	var cm coap.CertificateModes
	for _, o := range opts {
		o(&cfg, &cm)
	}
	e.srv = server.New(cfg)
	t.Cleanup(func() { _ = e.srv.Close() })
	cb := coap.New(e.srv)
	t.Cleanup(func() { _ = cb.Close() })
	a, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.udp = port(a)
	dc := coap.DTLSConfig{CIDLength: 6}
	if len(cm.Certificates) > 0 {
		if dc, err = cb.DTLSConfig(cm); err != nil {
			t.Fatal(err)
		}
		dc.CIDLength = 6
	}
	if a, err = cb.ListenDTLS("127.0.0.1:0", dc); err != nil {
		t.Fatal(err)
	}
	e.dtls = port(a)
	if a, err = cb.ListenTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	e.tcp = port(a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	e.ctx = ctx
	return e
}

// withX509 serves certificate clients signed by the returned CA.
func withX509(sc *serverCert) envOpt {
	return func(_ *server.Config, cm *coap.CertificateModes) {
		cm.Certificates = []tls.Certificate{sc.tls}
		pool := x509.NewCertPool()
		pool.AddCert(sc.ca)
		cm.ClientCAs = pool
	}
}

func withConfig(f func(*server.Config)) envOpt {
	return func(c *server.Config, _ *coap.CertificateModes) { f(c) }
}

func port(a net.Addr) int {
	_, ps, _ := net.SplitHostPort(a.String())
	n, _ := strconv.Atoi(ps)
	return n
}

func (e *env) registered(ep string) *server.Registration {
	e.t.Helper()
	return e.ev.wait(e.t, evTimeout, "Register of "+ep, isRegistered(ep)).(server.Registered).Registration
}

func (e *env) putPSK(ep, id string, key []byte) {
	e.t.Helper()
	if err := e.sec.Put(server.SecurityInfo{Endpoint: ep, PSKIdentity: id, PSKKey: key}); err != nil {
		e.t.Fatal(err)
	}
}

// bsEnv is a Bootstrap-Server over UDP and DTLS.
type bsEnv struct {
	bs      *bootstrap.Server
	configs *bootstrap.MemoryConfigStore
	sec     *server.MemorySecurityStore
	results chan bootstrap.Result
	udp     int
	dtls    int
}

func newBS(t *testing.T, mod ...func(*bootstrap.Config)) *bsEnv {
	t.Helper()
	b := &bsEnv{configs: bootstrap.NewMemoryConfigStore(), sec: server.NewMemorySecurityStore(),
		results: make(chan bootstrap.Result, 8)}
	cfg := bootstrap.Config{Configs: b.configs, Security: b.sec, RequestTimeout: 10 * time.Second,
		OnSession: func(r bootstrap.Result) { b.results <- r }}
	for _, m := range mod {
		m(&cfg)
	}
	b.bs = bootstrap.New(cfg)
	t.Cleanup(func() { _ = b.bs.Close() })
	a, err := b.bs.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.udp = port(a)
	if a, err = b.bs.ListenDTLS("127.0.0.1:0", bootstrap.DTLSConfig{}); err != nil {
		t.Fatal(err)
	}
	b.dtls = port(a)
	return b
}

func (b *bsEnv) result(t *testing.T) bootstrap.Result {
	t.Helper()
	select {
	case r := <-b.results:
		return r
	case <-time.After(60 * time.Second):
		t.Fatal("no bootstrap session result")
		return bootstrap.Result{}
	}
}

// session is result, skipping refused Bootstrap-Pack-Requests (the client
// then falls back to Bootstrap-Request).
func (b *bsEnv) session(t *testing.T) bootstrap.Result {
	t.Helper()
	for {
		r := b.result(t)
		if !r.Pack || !errors.Is(r.Err, bootstrap.ErrPackRefused) {
			return r
		}
	}
}

// proc is a running peer process. Its output is kept and logged when the
// test fails; stdin stays open for CLI commands.
type proc struct {
	t     *testing.T
	name  string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	mu    sync.Mutex
	out   bytes.Buffer
	done  chan struct{}
}

func start(t *testing.T, path string, args ...string) *proc {
	t.Helper()
	pr := &proc{t: t, name: filepath.Base(path), done: make(chan struct{})}
	pr.cmd = exec.Command(path, args...)
	pr.cmd.Dir = t.TempDir()
	stdin, err := pr.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	pr.stdin = stdin
	pipe, err := pr.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pr.cmd.Stderr = pr.cmd.Stdout
	if err := pr.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			pr.mu.Lock()
			pr.out.Write(sc.Bytes())
			pr.out.WriteByte('\n')
			pr.mu.Unlock()
		}
		_ = pr.cmd.Wait()
		close(pr.done)
	}()
	t.Cleanup(func() {
		pr.stop()
		if t.Failed() || os.Getenv("PEERS_LOG") != "" {
			out := pr.output()
			if len(out) > 16<<10 && os.Getenv("PEERS_LOG") == "" {
				out = "...\n" + out[len(out)-16<<10:]
			}
			t.Logf("---- %s %s output ----\n%s", pr.name, strings.Join(args, " "), out)
		}
	})
	return pr
}

func (pr *proc) output() string {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.out.String()
}

func (pr *proc) send(line string) {
	pr.t.Helper()
	if _, err := io.WriteString(pr.stdin, line+"\n"); err != nil {
		pr.t.Fatalf("%s stdin: %v", pr.name, err)
	}
}

// signal sends sig and waits up to d for the process to exit.
func (pr *proc) signal(sig syscall.Signal, d time.Duration) bool {
	_ = pr.cmd.Process.Signal(sig)
	select {
	case <-pr.done:
		return true
	case <-time.After(d):
		return false
	}
}

// closeStdin ends the CLI; the Anjay demo de-registers and exits on EOF.
func (pr *proc) closeStdin() { _ = pr.stdin.Close() }

func (pr *proc) stop() {
	select {
	case <-pr.done:
		return
	default:
	}
	pr.closeStdin()
	if pr.exited(3 * time.Second) {
		return
	}
	if !pr.signal(syscall.SIGINT, 3*time.Second) {
		_ = pr.cmd.Process.Kill()
		<-pr.done
	}
}

func (pr *proc) exited(d time.Duration) bool {
	select {
	case <-pr.done:
		return true
	case <-time.After(d):
		return false
	}
}

// waitOutput waits for substr in the process output.
func (pr *proc) waitOutput(substr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(pr.output(), substr) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// ---- assertions ----

// ok returns a checker of an operation result that requires 2.xx:
// ok(t, "read")(srv.Read(...)).
func ok(t *testing.T, what string) func(*server.Response, error) *server.Response {
	return func(r *server.Response, err error) *server.Response {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if !r.Success() {
			t.Fatalf("%s: %s %q", what, server.CodeString(r.Code), r.Payload)
		}
		return r
	}
}

// code is ok for one exact response code.
func code(t *testing.T, what string, want codes.Code) func(*server.Response, error) *server.Response {
	return func(r *server.Response, err error) *server.Response {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if r.Code != want {
			t.Fatalf("%s: %s %q, want %s", what, server.CodeString(r.Code), r.Payload, server.CodeString(want))
		}
		return r
	}
}

// value returns the node at path in nodes.
func value(t *testing.T, nodes []lwm2m.Node, path string) lwm2m.Value {
	t.Helper()
	for _, n := range nodes {
		if n.Path.String() == path && n.Kind == lwm2m.KindValue {
			return n.Value
		}
	}
	t.Fatalf("no %s in %s", path, lwm2m.FormatNodes(nodes))
	return lwm2m.Value{}
}

func hasNode(nodes []lwm2m.Node, path string) bool {
	for _, n := range nodes {
		if n.Path.String() == path {
			return true
		}
	}
	return false
}

// pick returns the nodes of nodes at the given paths, for cross-format checks
// that leave out time-varying resources.
func pick(nodes []lwm2m.Node, paths ...string) []lwm2m.Node {
	var out []lwm2m.Node
	for _, n := range nodes {
		for _, q := range paths {
			if n.Path.String() == q {
				n.Time, n.HasTime = 0, false
				out = append(out, n)
			}
		}
	}
	lwm2m.SortNodes(out)
	return out
}

// sameValue compares values across formats: text, CBOR and SenML carry no
// type for integers, strings or opaque the way TLV does, so compare the
// rendered value.
func sameValue(a, b lwm2m.Value) bool {
	return a.Equal(b) || plain(a) == plain(b)
}

// plain renders a value without type decoration: strings unquoted, opaque
// as its bytes, so an untyped decode ("55" string) matches a typed one (55).
func plain(v lwm2m.Value) string {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return v.Str
	case lwm2m.TypeOpaque:
		return string(v.Bytes)
	}
	return v.String()
}

func sameNodes(a, b []lwm2m.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || !sameValue(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

// ---- credentials ----

type serverCert struct {
	ca      *x509.Certificate
	caKey   *ecdsa.PrivateKey
	tls     tls.Certificate
	leafDER []byte
}

func newServerCert(t *testing.T) *serverCert {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "interop CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return &serverCert{ca: ca, caKey: caKey, leafDER: der,
		tls: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}}
}

// clientCert issues a client certificate with CN ep and writes it and its
// key as DER files to dir.
func (sc *serverCert) clientCert(t *testing.T, dir, ep string) (certFile, keyFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: ep},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, sc.ca, &key.PublicKey, sc.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, ep+".der"), filepath.Join(dir, ep+".key.der")
	write(t, certFile, der)
	write(t, keyFile, kder)
	return certFile, keyFile
}

func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func hexKey(b []byte) string { return hex.EncodeToString(b) }

// ---- UDP NAT rebinding proxy (CID) ----

// natProxy forwards UDP between one client and a server. rebind moves the
// upstream side to a new source port, as a NAT does after an idle timeout:
// without DTLS Connection ID the server can no longer route the session.
type natProxy struct {
	front    *net.UDPConn
	upstream *net.UDPAddr
	mu       sync.Mutex
	back     *net.UDPConn
	client   *net.UDPAddr
	closed   chan struct{}
}

func newNATProxy(t *testing.T, serverPort int) *natProxy {
	t.Helper()
	front, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	px := &natProxy{front: front, upstream: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: serverPort},
		closed: make(chan struct{})}
	px.rebind(t)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, a, err := front.ReadFromUDP(buf)
			if err != nil {
				return
			}
			px.mu.Lock()
			px.client = a
			back := px.back
			px.mu.Unlock()
			_, _ = back.WriteToUDP(buf[:n], px.upstream)
		}
	}()
	t.Cleanup(func() {
		close(px.closed)
		_ = front.Close()
		px.mu.Lock()
		_ = px.back.Close()
		px.mu.Unlock()
	})
	return px
}

func (px *natProxy) port() int { return port(px.front.LocalAddr()) }

func (px *natProxy) rebind(t *testing.T) {
	t.Helper()
	back, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	px.mu.Lock()
	old := px.back
	px.back = back
	px.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := back.ReadFromUDP(buf)
			if err != nil {
				return
			}
			px.mu.Lock()
			c := px.client
			px.mu.Unlock()
			if c != nil {
				_, _ = px.front.WriteToUDP(buf[:n], c)
			}
		}
	}()
}

func (px *natProxy) backPort() int {
	px.mu.Lock()
	defer px.mu.Unlock()
	return port(px.back.LocalAddr())
}

func uri(scheme string, port int) string { return fmt.Sprintf("%s://127.0.0.1:%d", scheme, port) }
