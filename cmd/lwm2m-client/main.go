// Command lwm2m-client runs a simulated LwM2M sensor against an LwM2M
// server: a small battery-powered environment sensor with Device,
// Connectivity Monitoring, Location, Temperature, Humidity and Voltage
// objects whose values change over time. It answers Read, Observe,
// Write-Attributes, Discover, Write and Execute, sends periodic Updates and
// deregisters on Ctrl-C.
//
//	go run github.com/fiumaralabs/lwm2m/cmd/lwm2m-client@latest -server coap://lwm2m.fiumaralabs.com:5683
//
// It is built on testclient (the repository's reference client) and
// internal/simdevice.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/internal/simdevice"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

const maxLifetime = 3600

// CoAP FETCH and iPATCH (RFC 8132), used by the composite operations.
const (
	fetch  codes.Code = 5
	ipatch codes.Code = 7
)

type config struct {
	server   string // the -server URI, for messages
	addr     string // host:port
	secure   bool
	endpoint string
	psk      []byte
	version  string
	lifetime uint32
	queue    bool
	send     time.Duration
	interval time.Duration
	timeout  time.Duration
	now      func() time.Time // simulation clock (tests)
}

const usage = `Usage: lwm2m-client [flags]

Runs a simulated LwM2M sensor (Device, Connectivity Monitoring, Location,
Temperature, Humidity, Voltage) against an LwM2M server and prints one line
per event. Ctrl-C deregisters and exits.

  lwm2m-client -server coap://lwm2m.fiumaralabs.com:5683
  lwm2m-client -server coaps://lwm2m.fiumaralabs.com:5684 -psk 000102030405060708090a0b0c0d0e0f

With -psk the PSK identity is the endpoint name.

Flags:
`

func parseFlags(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("lwm2m-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", "coap://lwm2m.fiumaralabs.com:5683", "server `URI`: coap://host[:port] (NoSec) or coaps://host[:port] (DTLS-PSK)")
	ep := fs.String("ep", "", "endpoint `name` (default: random demo-xxxxxx)")
	psk := fs.String("psk", "", "pre-shared `key` in hex, for coaps://")
	version := fs.String("lwm2m", "1.1", "LwM2M `version`: 1.0, 1.1 or 1.2")
	lifetime := fs.Uint("lifetime", 300, "registration lifetime in `seconds` (max 3600)")
	binding := fs.String("binding", "U", "U, or UQ for queue mode")
	send := fs.Duration("send", 0, "also Send temperature, humidity and battery to /dp every `interval` (LwM2M 1.1+)")
	interval := fs.Duration("interval", 5*time.Second, "how often the simulated values change")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	cfg := config{server: *server, version: *version, send: *send, interval: *interval, timeout: 30 * time.Second, now: time.Now}
	var err error
	if cfg.addr, cfg.secure, err = parseServer(*server); err != nil {
		return config{}, err
	}
	switch *version {
	case "1.0", "1.1", "1.2":
	default:
		return config{}, fmt.Errorf("-lwm2m %s: use 1.0, 1.1 or 1.2", *version)
	}
	switch strings.ToUpper(*binding) {
	case "U":
	case "UQ":
		cfg.queue = true
	default:
		return config{}, fmt.Errorf("-binding %s: use U or UQ", *binding)
	}
	if *lifetime < 30 {
		return config{}, fmt.Errorf("-lifetime %d: use 30 to %d seconds", *lifetime, maxLifetime)
	}
	cfg.lifetime = uint32(min(*lifetime, maxLifetime))
	if *lifetime > maxLifetime {
		fmt.Fprintf(stderr, "lifetime capped at %d s\n", maxLifetime)
	}
	if cfg.send > 0 && cfg.version == "1.0" {
		return config{}, errors.New("-send needs LwM2M 1.1 or 1.2 (Send does not exist in 1.0)")
	}
	if cfg.send < 0 || cfg.interval <= 0 {
		return config{}, errors.New("-send and -interval must be positive")
	}
	cfg.endpoint = *ep
	if cfg.endpoint == "" {
		cfg.endpoint = randomName()
	}
	if len(cfg.endpoint) > 64 || strings.ContainsAny(cfg.endpoint, " \t\r\n&?/") {
		return config{}, fmt.Errorf("-ep %q: use up to 64 characters without spaces, '&', '?' or '/'", cfg.endpoint)
	}
	if *psk != "" {
		if cfg.psk, err = hex.DecodeString(*psk); err != nil || len(cfg.psk) == 0 {
			return config{}, fmt.Errorf("-psk: not a hex key")
		}
	}
	switch {
	case cfg.secure && cfg.psk == nil:
		return config{}, fmt.Errorf("%s needs -psk <hex key> (the public demo key is 000102030405060708090a0b0c0d0e0f)", *server)
	case !cfg.secure && cfg.psk != nil:
		return config{}, fmt.Errorf("-psk needs a coaps:// server URI")
	}
	return cfg, nil
}

func parseServer(s string) (addr string, secure bool, err error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", false, fmt.Errorf("-server %q: use coap://host[:port] or coaps://host[:port]", s)
	}
	port := ""
	switch u.Scheme {
	case "coap":
		port = "5683"
	case "coaps":
		port, secure = "5684", true
	default:
		return "", false, fmt.Errorf("-server %q: scheme must be coap or coaps (UDP)", s)
	}
	if p := u.Port(); p != "" {
		port = p
	}
	return net.JoinHostPort(u.Hostname(), port), secure, nil
}

func randomName() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "demo-" + hex.EncodeToString(b)
}

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lwm2m-client:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logf := func(format string, a ...any) {
		fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
	}
	if err := run(ctx, cfg, logf); err != nil {
		fmt.Fprintln(os.Stderr, "lwm2m-client:", err)
		os.Exit(1)
	}
}

// code renders a CoAP code as "4.03 Forbidden".
func code(c codes.Code) string {
	return fmt.Sprintf("%d.%02d %v", c>>5, c&31, c)
}

type client struct {
	cfg  config
	c    *testclient.Client
	dev  *simdevice.Device
	logf func(string, ...any)
	// sockErr is the last socket error go-coap saw (e.g. ICMP port
	// unreachable), added to timeout messages.
	sockErr atomic.Value
}

func (cl *client) hint() string {
	if s, _ := cl.sockErr.Load().(string); s != "" {
		if i := strings.LastIndex(s, ": "); i >= 0 {
			s = s[i+2:]
		}
		return "; last socket error: " + s
	}
	return ""
}

// run registers, keeps the registration and the simulation going until ctx
// ends, then deregisters.
func run(ctx context.Context, cfg config, logf func(string, ...any)) error {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.timeout == 0 {
		cfg.timeout = 30 * time.Second
	}
	tc := testclient.Config{Endpoint: cfg.endpoint, Version: cfg.version, Lifetime: cfg.lifetime, Binding: "U", NoHistory: true}
	binding := "U"
	if cfg.queue {
		if cfg.version == "1.0" {
			tc.Binding, binding = "UQ", "UQ" // 1.0: queue mode is a binding mode
		} else {
			tc.Queue = true // 1.1+: the Q parameter
		}
	}
	if cfg.psk != nil {
		tc.PSKIdentity, tc.PSKKey, tc.CID = cfg.endpoint, cfg.psk, true
	}
	cl := &client{cfg: cfg, logf: logf}
	tc.Errors = func(err error) { cl.sockErr.Store(err.Error()) }
	cl.c = testclient.New(tc)
	cl.dev = simdevice.New(cl.c, simdevice.Options{Endpoint: cfg.endpoint, Lifetime: cfg.lifetime, Binding: binding}, cfg.now())
	cl.c.SetOverride(cl.onRequest)
	cl.c.SetNotifyHook(cl.onNotify)

	if err := cl.dial(ctx); err != nil {
		return err
	}
	defer cl.c.Close()
	if err := cl.register(ctx); err != nil {
		return err
	}

	step := time.NewTicker(cfg.interval)
	defer step.Stop()
	updateEvery := time.Duration(cfg.lifetime) * time.Second / 2
	if cfg.queue && updateEvery > time.Minute {
		updateEvery = time.Minute // queue mode: the server can reach us only after an uplink
	}
	update := time.NewTicker(updateEvery)
	defer update.Stop()
	var sendC <-chan time.Time
	if cfg.send > 0 {
		t := time.NewTicker(cfg.send)
		defer t.Stop()
		sendC = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return cl.deregister()
		case <-step.C:
			cl.dev.Step(cfg.now())
		case <-update.C:
			if err := cl.update(ctx); err != nil {
				return err
			}
		case <-sendC:
			cl.sendSample(ctx)
		}
	}
}

func (cl *client) dial(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- cl.c.Dial(cl.cfg.addr) }()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if cl.cfg.secure {
			return fmt.Errorf("DTLS handshake with %s failed: %v (check -psk; the identity is the endpoint name %q)", cl.cfg.addr, err, cl.cfg.endpoint)
		}
		return fmt.Errorf("connect %s: %v", cl.cfg.addr, err)
	case <-time.After(cl.cfg.timeout):
		return fmt.Errorf("connect %s: no answer within %v", cl.cfg.addr, cl.cfg.timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (cl *client) register(ctx context.Context) error {
	rctx, cancel := context.WithTimeout(ctx, cl.cfg.timeout)
	defer cancel()
	r, err := cl.bounded(rctx, cl.c.Register)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("interrupted before the registration completed")
		}
		if errors.Is(err, context.DeadlineExceeded) && cl.cfg.secure {
			return fmt.Errorf("register: no answer from %s within %v: the DTLS handshake did not complete (wrong -psk? the identity is the endpoint name %q) or UDP is blocked%s", cl.cfg.addr, cl.cfg.timeout, cl.cfg.endpoint, cl.hint())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("register: no answer from %s within %v (is UDP reachable? a proxied DNS name does not carry CoAP)%s", cl.cfg.addr, cl.cfg.timeout, cl.hint())
		}
		if strings.Contains(err.Error(), "connection was closed") {
			return fmt.Errorf("register: %s closed the connection (port unreachable: is the server listening on UDP %s?)", cl.cfg.addr, cl.cfg.addr)
		}
		return fmt.Errorf("register: %v%s", err, cl.hint())
	}
	if r.Code != codes.Created {
		msg := "register: server answered " + code(r.Code)
		if b := strings.TrimSpace(string(r.Body)); b != "" {
			msg += ": " + b
		}
		return errors.New(msg)
	}
	mode := "NoSec"
	if cl.cfg.secure {
		mode = "DTLS-PSK"
	}
	q := ""
	if cl.cfg.queue {
		q = ", queue mode"
	}
	cl.logf("registered %s at /rd/%s on %s (%s, LwM2M %s, lifetime %ds%s)", cl.cfg.endpoint, cl.c.Location(), cl.cfg.server, mode, cl.cfg.version, cl.cfg.lifetime, q)
	return nil
}

// bounded runs a request but returns when ctx ends even if the request
// does not: over DTLS the handshake runs inside the first write and does
// not watch the context.
func (cl *client) bounded(ctx context.Context, f func(context.Context) (*testclient.Response, error)) (*testclient.Response, error) {
	type result struct {
		r   *testclient.Response
		err error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := f(ctx)
		ch <- result{r, err}
	}()
	select {
	case res := <-ch:
		return res.r, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (cl *client) update(ctx context.Context) error {
	uctx, cancel := context.WithTimeout(ctx, cl.cfg.timeout)
	r, err := cl.bounded(uctx, func(ctx context.Context) (*testclient.Response, error) { return cl.c.Update(ctx, nil, nil) })
	cancel()
	switch {
	case ctx.Err() != nil:
		return nil // shutting down; the loop deregisters
	case err == nil && r.Code == codes.Changed:
		cl.logf("update -> %s", code(r.Code))
		return nil
	case err == nil:
		cl.logf("update -> %s; registering again", code(r.Code))
	default:
		cl.logf("update failed: %v; registering again", err)
	}
	if err := cl.register(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("lost the registration: %w", err)
	}
	return nil
}

func (cl *client) deregister() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := cl.bounded(ctx, cl.c.Deregister)
	if err != nil {
		return fmt.Errorf("deregister: %v", err)
	}
	if r.Code != codes.Deleted {
		return fmt.Errorf("deregister: server answered %s", code(r.Code))
	}
	cl.logf("deregistered (%s)", code(r.Code))
	return nil
}

func (cl *client) sendSample(ctx context.Context) {
	nodes := cl.dev.Sample()
	sctx, cancel := context.WithTimeout(ctx, cl.cfg.timeout)
	defer cancel()
	r, err := cl.c.Send(sctx, nodes, lwm2m.FormatSenMLCBOR)
	if err != nil {
		if ctx.Err() == nil {
			cl.logf("send failed: %v", err)
		}
		return
	}
	cl.logf("send %s -> %s", describe(nodes), code(r.Code))
}

func describe(nodes []lwm2m.Node) string {
	var parts []string
	for _, n := range nodes {
		parts = append(parts, n.Path.String()+"="+n.Value.String())
	}
	return strings.Join(parts, " ")
}

var execNames = map[string]string{
	"/1/0/4": "Disable", "/1/0/8": "Registration Update Trigger",
	"/3/0/4": "Reboot", "/3/0/5": "Factory Reset", "/3/0/12": "Reset Error Code",
}

// onRequest logs each server request; the default testclient behaviour
// then answers it (handled=false).
func (cl *client) onRequest(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	q := ""
	if len(r.Queries) > 0 {
		q = "?" + strings.Join(r.Queries, "&")
	}
	switch r.Code {
	case codes.GET:
		switch {
		case r.Accept != nil && *r.Accept == lwm2m.FormatLinkFormat:
			cl.logf("discover %s", r.Path)
		case r.Observe != nil && *r.Observe == 0:
			cl.logf("observe %s%s", r.Path, q)
		case r.Observe != nil:
			cl.logf("observe cancelled %s", r.Path)
		case strings.HasPrefix(r.Path, "/0"):
			cl.logf("read %s (refused: Security is not readable)", r.Path)
		default:
			cl.logf("read %s", r.Path)
		}
	case fetch:
		if r.Observe != nil && *r.Observe == 0 {
			cl.logf("observe-composite")
		} else {
			cl.logf("read-composite")
		}
	case ipatch:
		cl.logf("write-composite (stored locally)")
	case codes.PUT:
		if r.Body == nil && q != "" {
			cl.logf("write-attributes %s%s", r.Path, q)
		} else {
			cl.logf("write %s (replace; stored locally)", r.Path)
		}
	case codes.POST:
		p, err := lwm2m.ParsePath(r.Path)
		switch {
		case err != nil:
			cl.logf("POST %s", r.Path)
		case p.Len() == 1:
			cl.logf("create in %s (stored locally)", r.Path)
		case p.Len() == 3:
			cl.execute(p, r)
		default:
			cl.logf("write %s (update; stored locally)", r.Path)
		}
	case codes.DELETE:
		cl.logf("delete %s (local only)", r.Path)
	default:
		cl.logf("%v %s", r.Code, r.Path)
	}
	return 0, nil, nil, false
}

func (cl *client) execute(p lwm2m.Path, r testclient.Request) {
	arg := ""
	if len(r.Body) > 0 {
		arg = " arg " + string(r.Body)
	}
	name := execNames[p.String()]
	switch {
	case p.Resource() == 5605 && p.Instance() == 0:
		// go: SetNodes may send notifications; not inside the request handler.
		go cl.dev.ResetMinMax(p.Object())
		cl.logf("execute %s%s (Reset Min and Max Measured Values)", p, arg)
	case name == "Reboot" || name == "Factory Reset":
		cl.logf("execute %s%s (%s: simulated device, nothing restarts)", p, arg, name)
	case name != "":
		cl.logf("execute %s%s (%s)", p, arg, name)
	default:
		cl.logf("execute %s%s (no action on this device)", p, arg)
	}
}

func (cl *client) onNotify(n testclient.Notification) {
	what := "notify-composite"
	if len(n.Paths) == 1 {
		what = "notify " + n.Paths[0].String()
		if v, ok := cl.c.Get(n.Paths[0]); ok && (n.Paths[0].IsResource() || n.Paths[0].IsResourceInstance()) {
			what += " = " + v.String()
		}
	}
	switch {
	case n.Err != nil:
		cl.logf("%s failed: %v", what, n.Err)
	case n.Reset:
		cl.logf("%s: server sent Reset, observation ended", what)
	case !n.Confirmable:
		cl.logf("%s (NON)", what)
	default:
		cl.logf("%s", what)
	}
}
