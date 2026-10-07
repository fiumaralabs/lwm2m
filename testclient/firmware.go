package testclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// FirmwareConfig configures a Firmware Update object /5/0 (Core App. E.6).
type FirmwareConfig struct {
	Version   string  // object version: "1.0" (default), "1.1" or "1.2"
	Protocols []int64 // /5/0/8; default {0} (CoAP)
	Delivery  int64   // /5/0/9: 0 pull, 1 push, 2 both
	// Verify checks a downloaded package and returns 0 or a failure Update
	// Result (2 flash, 5 integrity, 6 package type...). nil accepts all.
	Verify func(pkg []byte) int64
	// Install runs on Execute while the State is Updating and returns the
	// Update Result (1 success). It may re-register, like a rebooting
	// device (FW-05). nil returns 1.
	Install func(pkg []byte) int64
	// Defer makes the user defer every Execute that /5/0/11 and /5/0/13
	// allow (v1.1+): Update Result 11, State stays Downloaded.
	Defer bool
	// PSK for coaps:// pulls; default the client's.
	PSKIdentity string
	PSKKey      []byte
	HTTPClient  *http.Client // http(s):// pulls; default http.DefaultClient
	BlockSize   int          // pull Block2 size; default 512 (Zephyr)
}

// PullBlock is one Block2 response the client received while pulling.
type PullBlock struct {
	Token   message.Token
	Num     int64
	Size    int64 // block size
	Size2   uint32
	HasETag bool
}

// Firmware emulates /5/0 the way Zephyr's lwm2m_obj_firmware does
// (zephyr-client-profile §7.1): push by Write /5/0/0, pull by Write
// /5/0/1 with a CoAP Block2 download that reuses one token, Size2: 0 and
// no query; HTTP(S) when /5/0/8 lists it. It answers /5/0 through the
// client's override, so other overrides must chain to Handle.
type Firmware struct {
	c    *Client
	cfg  FirmwareConfig
	mu   sync.Mutex
	pkg  []byte
	got  []PullBlock
	work chan func()
}

var (
	fwPackage  = lwm2m.MustParsePath("/5/0/0")
	fwURI      = lwm2m.MustParsePath("/5/0/1")
	fwState    = lwm2m.MustParsePath("/5/0/3")
	fwResult   = lwm2m.MustParsePath("/5/0/5")
	fwSeverity = lwm2m.MustParsePath("/5/0/11")
	fwChanged  = lwm2m.MustParsePath("/5/0/12")
	fwMaxDefer = lwm2m.MustParsePath("/5/0/13")
	fwAuto     = lwm2m.MustParsePath("/5/0/14")
)

// NewFirmware adds /5/0 to c and installs its override.
func NewFirmware(c *Client, cfg FirmwareConfig) *Firmware {
	if cfg.Version == "" {
		cfg.Version = "1.0"
	}
	if cfg.Protocols == nil {
		cfg.Protocols = []int64{0}
	}
	if cfg.BlockSize == 0 {
		cfg.BlockSize = 512
	}
	f := &Firmware{c: c, cfg: cfg, work: make(chan func(), 64)}
	// ponytail: the worker lives as long as the test binary; add a Close
	// if a test ever creates thousands of these.
	go func() {
		for fn := range f.work {
			fn()
		}
	}()
	c.Set(fwURI, lwm2m.String(""))
	c.Set(fwState, lwm2m.Integer(0))
	c.Set(fwResult, lwm2m.Integer(0))
	for i, p := range cfg.Protocols {
		c.Set(lwm2m.NewPath(5, 0, 8, uint16(i)), lwm2m.Integer(p))
	}
	c.Set(lwm2m.MustParsePath("/5/0/9"), lwm2m.Integer(cfg.Delivery))
	if f.minor() >= 1 {
		c.Set(fwSeverity, lwm2m.Integer(1))
		c.Set(fwChanged, lwm2m.TimeOf(time.Now()))
		c.Set(fwMaxDefer, lwm2m.Unsigned(0))
	}
	if f.minor() >= 2 {
		c.Set(fwAuto, lwm2m.Boolean(false))
	}
	c.SetOverride(f.Handle)
	return f
}

func (f *Firmware) minor() int { return int(f.cfg.Version[len(f.cfg.Version)-1] - '0') }

// Links is the Register payload with "ver" on /5 (VER-01).
func (f *Firmware) Links() string {
	return strings.Replace(f.c.ObjectLinks(), "</5/0>", "</5>;ver="+f.cfg.Version+",</5/0>", 1)
}

// Register registers with Links.
func (f *Firmware) Register(ctx context.Context) (*Response, error) {
	return f.c.RegisterRaw(ctx, f.c.RegisterQuery(), []byte(f.Links()), true)
}

// Package returns the stored package (push or pull).
func (f *Firmware) Package() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pkg
}

// Pulled returns the Block2 responses of the pulls so far.
func (f *Firmware) Pulled() []PullBlock {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got)
}

// State and Result read /5/0/3 and /5/0/5.
func (f *Firmware) State() int64  { v, _ := f.c.Get(fwState); return v.Int }
func (f *Firmware) Result() int64 { v, _ := f.c.Get(fwResult); return v.Int }

// Wait returns once earlier transitions and their notifications are done.
func (f *Firmware) Wait() {
	done := make(chan struct{})
	f.work <- func() { close(done) }
	<-done
}

// store changes values now (a Read sees them at once) and notifies
// observers from the worker, after the response is sent.
func (f *Firmware) store(vals map[lwm2m.Path]lwm2m.Value) {
	f.c.mu.Lock()
	for p, v := range vals {
		f.c.setLocked(p, v)
	}
	if _, ok := vals[fwState]; ok && f.minor() >= 1 {
		f.c.setLocked(fwChanged, lwm2m.TimeOf(time.Now()))
	}
	f.c.mu.Unlock()
	f.work <- func() {
		for _, p := range []lwm2m.Path{fwState, fwResult} {
			if _, ok := vals[p]; ok {
				f.c.notify(p)
			}
		}
	}
}

func setState(st, res int64) map[lwm2m.Path]lwm2m.Value {
	return map[lwm2m.Path]lwm2m.Value{fwState: lwm2m.Integer(st), fwResult: lwm2m.Integer(res)}
}

// Handle is the /5/0 override; other paths fall through.
func (f *Firmware) Handle(r Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	p, err := lwm2m.ParsePath(r.Path)
	if err != nil || !p.IsResource() || p.Object() != 5 || p.Instance() != 0 {
		return 0, nil, nil, false
	}
	known := p.Resource() <= 9 || f.minor() >= 1 && p.Resource() <= 13 || f.minor() >= 2 && p.Resource() == 14
	if !known {
		return codes.NotFound, nil, nil, true
	}
	switch {
	case r.Code == codes.POST && p.Resource() == 2:
		return f.execute(), nil, nil, true
	case r.Code == codes.POST && p.Resource() == 10:
		return f.cancel(), nil, nil, true
	case (r.Code == codes.PUT || r.Code == codes.POST) && p == fwPackage:
		v, ok := decodeOne(r, p, lwm2m.TypeOpaque)
		if !ok {
			return codes.BadRequest, nil, nil, true
		}
		return f.push(v.Bytes), nil, nil, true
	case (r.Code == codes.PUT || r.Code == codes.POST) && p == fwURI:
		v, ok := decodeOne(r, p, lwm2m.TypeString)
		if !ok {
			return codes.BadRequest, nil, nil, true
		}
		return f.setURI(v.Str), nil, nil, true
	}
	return 0, nil, nil, false
}

func decodeOne(r Request, p lwm2m.Path, t lwm2m.Type) (lwm2m.Value, bool) {
	if r.Format == nil {
		return lwm2m.Value{}, false
	}
	cd, err := codec.For(*r.Format)
	if err != nil {
		return lwm2m.Value{}, false
	}
	nodes, err := cd.Decode(p, r.Body, lwm2m.SchemaFunc(func(q lwm2m.Path) (lwm2m.ResourceDef, bool) {
		return lwm2m.ResourceDef{Type: t}, q == p
	}))
	if err != nil || len(nodes) != 1 || nodes[0].Value.Type != t {
		return lwm2m.Value{}, false
	}
	return nodes[0].Value, true
}

// push handles Write /5/0/0 (FW-02). go-coap has reassembled the Block1
// transfer already.
func (f *Firmware) push(pkg []byte) codes.Code {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.State()
	if len(pkg) == 0 || len(pkg) == 1 && pkg[0] == 0 { // NUL resets (FW-04)
		if st == 3 {
			return codes.MethodNotAllowed
		}
		f.pkg = nil
		f.store(setState(0, 0))
		return codes.Changed
	}
	if st >= 2 {
		return codes.MethodNotAllowed
	}
	f.pkg = slices.Clone(pkg)
	f.store(setState(1, 0))
	f.work <- func() { f.downloaded(pkg) }
	return codes.Changed
}

// setURI handles Write /5/0/1.
func (f *Firmware) setURI(uri string) codes.Code {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.State()
	if uri == "" { // empty URI resets (FW-04)
		if st == 3 {
			return codes.MethodNotAllowed
		}
		f.pkg = nil
		vals := setState(0, 0)
		vals[fwURI] = lwm2m.String("")
		f.store(vals)
		return codes.Changed
	}
	if st != 0 {
		return codes.Changed // Zephyr ignores it
	}
	vals := setState(1, 0)
	vals[fwURI] = lwm2m.String(uri)
	f.store(vals)
	f.work <- func() {
		pkg, res := f.pull(uri)
		if res != 0 {
			f.mu.Lock()
			f.store(setState(0, res))
			f.mu.Unlock()
			return
		}
		f.mu.Lock()
		f.pkg = pkg
		f.mu.Unlock()
		f.downloaded(pkg)
	}
	return codes.Changed
}

// downloaded verifies a complete package: Downloaded, or Idle with the
// failure result. With Automatic Upgrade at Download it installs at once.
func (f *Firmware) downloaded(pkg []byte) {
	res := int64(0)
	if f.cfg.Verify != nil {
		res = f.cfg.Verify(pkg) // unlocked: a slow check must not block Cancel
	}
	f.mu.Lock()
	if f.State() != 1 { // reset or cancelled meanwhile
		f.mu.Unlock()
		return
	}
	if res != 0 {
		f.pkg = nil
		f.store(setState(0, res))
		f.mu.Unlock()
		return
	}
	f.store(setState(2, 0))
	auto, _ := f.c.Get(fwAuto)
	f.mu.Unlock()
	if f.minor() >= 2 && auto.Bool {
		f.execute()
	}
}

// execute handles Execute /5/0/2: only in Downloaded (4.05 otherwise).
func (f *Firmware) execute() codes.Code {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.State() != 2 {
		return codes.MethodNotAllowed
	}
	sev, _ := f.c.Get(fwSeverity)
	maxDefer, _ := f.c.Get(fwMaxDefer)
	if f.cfg.Defer && f.minor() >= 1 && maxDefer.Uint > 0 && sev.Int != 0 {
		f.store(map[lwm2m.Path]lwm2m.Value{fwResult: lwm2m.Integer(11)})
		return codes.Changed
	}
	f.store(setState(3, 0))
	pkg := f.pkg
	f.work <- func() {
		res := int64(1)
		if f.cfg.Install != nil {
			res = f.cfg.Install(pkg)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if res == 1 {
			f.pkg = nil
			f.store(setState(0, 1))
		} else {
			f.store(setState(2, res))
		}
	}
	return codes.Changed
}

// cancel handles Execute /5/0/10 (v1.1+): 4.05 once installing.
func (f *Firmware) cancel() codes.Code {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.State() == 3 {
		return codes.MethodNotAllowed
	}
	f.pkg = nil
	f.store(setState(0, 10))
	return codes.Changed
}

// pull downloads uri and returns the package or a failure Update Result:
// 7 invalid URI, 9 unsupported protocol, 4 connection lost.
func (f *Firmware) pull(uri string) ([]byte, int64) {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return nil, 7
	}
	proto := map[string]int64{"coap": 0, "coaps": 1, "http": 2, "https": 3, "coap+tcp": 4, "coaps+tcp": 5}
	pr, ok := proto[strings.ToLower(u.Scheme)]
	if !ok || !(slices.Contains(f.cfg.Protocols, pr) || pr == 0 && len(f.cfg.Protocols) == 0) || pr > 3 { // no /5/0/8: CoAP
		return nil, 9
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var pkg []byte
	if pr >= 2 {
		pkg, err = f.pullHTTP(ctx, uri)
	} else {
		pkg, err = f.pullCoAP(ctx, u, pr == 1)
	}
	if err != nil {
		return nil, 4
	}
	return pkg, 0
}

func (f *Firmware) pullHTTP(ctx context.Context, uri string) ([]byte, error) {
	hc := f.cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, io.ErrUnexpectedEOF
	}
	return io.ReadAll(resp.Body)
}

// pullCoAP is Zephyr's lwm2m_pull_context: CON GET of the URI path (the
// query is dropped), Block2 from 0 with Size2: 0, the same token for every
// block, only 2.05 accepted, following the server's block size.
func (f *Firmware) pullCoAP(ctx context.Context, u *url.URL, secure bool) ([]byte, error) {
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), map[bool]string{false: "5683", true: "5684"}[secure])
	}
	noBW := options.WithBlockwise(false, blockwise.SZX1024, time.Minute)
	var conn *client.Conn
	var err error
	if secure {
		id, key := f.cfg.PSKIdentity, f.cfg.PSKKey
		if id == "" {
			id, key = f.c.cfg.PSKIdentity, f.c.cfg.PSKKey
		}
		conn, err = dtlscoap.Dial(host, pskOptions(id, key, false), noBW)
	} else {
		conn, err = udp.Dial(host, noBW)
	}
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tok, err := message.GetToken()
	if err != nil {
		return nil, err
	}
	szx := blockwise.SZX16
	for szx < blockwise.SZX1024 && szx.Size() < int64(f.cfg.BlockSize) {
		szx++
	}
	var data []byte
	for num := int64(0); ; {
		req := conn.AcquireMessage(ctx)
		req.SetCode(codes.GET)
		req.SetType(message.Confirmable)
		req.SetToken(tok)
		if err := req.SetPath(u.Path); err != nil {
			conn.ReleaseMessage(req)
			return nil, err
		}
		blk, _ := blockwise.EncodeBlockOption(szx, num, false)
		req.SetOptionUint32(message.Block2, blk)
		req.SetOptionUint32(message.Size2, 0)
		resp, err := conn.Do(req)
		conn.ReleaseMessage(req)
		if err != nil {
			return nil, err
		}
		body, _ := resp.ReadBody()
		code := resp.Code()
		bv, berr := resp.Options().GetUint32(message.Block2)
		size2, _ := resp.Options().GetUint32(message.Size2)
		_, etagErr := resp.Options().GetBytes(message.ETag)
		conn.ReleaseMessage(resp)
		if code != codes.Content {
			return nil, io.ErrUnexpectedEOF
		}
		if berr != nil { // not block-wise: the whole image
			return body, nil
		}
		s, n, more, err := blockwise.DecodeBlockOption(bv)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.got = append(f.got, PullBlock{Token: tok, Num: n, Size: s.Size(), Size2: size2, HasETag: etagErr == nil})
		f.mu.Unlock()
		if n*s.Size() == int64(len(data)) { // duplicates are ignored
			data = append(data, body...)
		}
		if !more {
			return data, nil
		}
		szx, num = s, int64(len(data))/s.Size()
	}
}

// SetDefer changes FirmwareConfig.Defer.
func (f *Firmware) SetDefer(d bool) {
	f.mu.Lock()
	f.cfg.Defer = d
	f.mu.Unlock()
}

// RemoveObject drops object oid with its instances and values, like
// firmware that no longer supports it (FW-05: the client deletes them and
// re-registers).
func (c *Client) RemoveObject(oid uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.objects, oid)
	for p := range c.instances {
		if p.Object() == oid {
			delete(c.instances, p)
		}
	}
	for p := range c.values {
		if p.Object() == oid {
			delete(c.values, p)
		}
	}
}
