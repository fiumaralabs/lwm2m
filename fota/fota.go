// Package fota orchestrates firmware updates through the Firmware Update
// object /5 (Core App. E.6) using only the public server API: push by
// Block1 Write to /5/0/0, pull by writing /5/0/1 and serving the image
// from FileServer, protocol checks against /5/0/8, and state tracking via
// Observe on /5/0/3 and /5/0/5 for /5 v1.0, v1.1 and v1.2.
//
// Wire a Manager to the server's events:
//
//	var m *fota.Manager
//	srv := server.New(server.Config{OnEvent: func(e server.Event) { m.HandleEvent(e) }})
//	m = fota.New(srv)
package fota

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Resources of /5/0.
var (
	PathPackage     = lwm2m.MustParsePath("/5/0/0")
	PathPackageURI  = lwm2m.MustParsePath("/5/0/1")
	PathUpdate      = lwm2m.MustParsePath("/5/0/2")
	PathState       = lwm2m.MustParsePath("/5/0/3")
	PathResult      = lwm2m.MustParsePath("/5/0/5")
	PathProtocols   = lwm2m.MustParsePath("/5/0/8")
	PathDelivery    = lwm2m.MustParsePath("/5/0/9")
	PathCancel      = lwm2m.MustParsePath("/5/0/10")
	PathSeverity    = lwm2m.MustParsePath("/5/0/11")
	PathMaxDefer    = lwm2m.MustParsePath("/5/0/13")
	PathAutoUpgrade = lwm2m.MustParsePath("/5/0/14")
)

// Protocol is a /5/0/8 Firmware Update Protocol Support value (FW-03).
type Protocol int64

const (
	CoAP    Protocol = iota // coap:// with block-wise, the default
	CoAPS                   // coaps://
	HTTP                    // http:// (HTTP 1.1)
	HTTPS                   // https://
	CoAPTCP                 // coap+tcp://
	CoAPTLS                 // coaps+tcp://
)

var schemes = map[string]Protocol{"coap": CoAP, "coaps": CoAPS, "http": HTTP, "https": HTTPS, "coap+tcp": CoAPTCP, "coaps+tcp": CoAPTLS}

// ProtocolOf returns the protocol a Package URI needs.
func ProtocolOf(uri string) (Protocol, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return 0, err
	}
	p, ok := schemes[strings.ToLower(u.Scheme)]
	if !ok {
		return 0, fmt.Errorf("%w: scheme %q", ErrUnsupportedProtocol, u.Scheme)
	}
	return p, nil
}

// State is /5/0/3.
type State int64

const (
	Idle State = iota
	Downloading
	Downloaded
	Updating
)

func (s State) String() string {
	if s >= 0 && s <= Updating {
		return [...]string{"idle", "downloading", "downloaded", "updating"}[s]
	}
	return "state(" + strconv.FormatInt(int64(s), 10) + ")"
}

// Result is /5/0/5 Update Result (FW-04). 10 and 11 exist from /5 v1.1.
type Result int64

const (
	Initial Result = iota
	Success
	NoFlash
	NoRAM
	ConnectionLost
	IntegrityFailure
	UnsupportedPackage
	InvalidURI
	UpdateFailed
	UnsupportedProtocolResult
	Cancelled
	Deferred
)

func (r Result) String() string {
	names := [...]string{"initial", "success", "not enough flash", "out of RAM", "connection lost",
		"integrity check failure", "unsupported package type", "invalid URI", "update failed",
		"unsupported protocol", "cancelled", "deferred"}
	if r >= 0 && int(r) < len(names) {
		return names[r]
	}
	return "result(" + strconv.FormatInt(int64(r), 10) + ")"
}

// Method selects how the package reaches the client.
type Method uint8

const (
	Auto Method = iota // pull when a usable URI and /5/0/9 allow it, else push (FW-07)
	Push               // Write /5/0/0 with Block1
	Pull               // Write /5/0/1
)

func (m Method) String() string { return [...]string{"auto", "push", "pull"}[m] }

var (
	ErrNoFirmwareObject    = errors.New("fota: client has no /5/0")
	ErrUnsupportedProtocol = errors.New("fota: no Package URI uses a protocol the client supports")
	ErrNoMethod            = errors.New("fota: no delivery method fits the job and /5/0/9")
	ErrBusy                = errors.New("fota: update already in progress")
	ErrVersion             = errors.New("fota: resource needs a newer /5 version")
	ErrRejected            = errors.New("fota: client rejected the operation")
)

// ResultError is a failed update: Update Result reported an error (FW-04:
// errors are reported only through Update Result).
type ResultError struct{ Result Result }

func (e *ResultError) Error() string { return "fota: update result " + e.Result.String() }

// Job describes one update.
type Job struct {
	Method  Method
	Package []byte   // image for push
	URIs    []string // pull candidates; the first one whose scheme /5/0/8 lists is used (FW-03)
	// PushTimeout bounds one Block1 transfer; PushAttempts (default 1)
	// restarts a timed-out transfer from block 0 with a new token (bw-2).
	PushTimeout  time.Duration
	PushAttempts int
	// /5 v1.1+: Severity (0 critical, 1 mandatory, 2 optional) and Maximum
	// Defer Period in seconds (0 forbids deferring). /5 v1.2: Automatic
	// Upgrade at Download; true means the server does not Execute /5/0/2.
	Severity       *int64
	MaxDeferPeriod *uint64
	AutoUpgrade    *bool
	// Resume executes a package already Downloaded (e.g. after Update
	// Result 11 deferred) instead of resetting and downloading again.
	Resume bool
	// Poll re-reads State and Update Result at this interval, for clients
	// whose notifications are lost or that refuse Observe. Default 30 s.
	Poll time.Duration
}

// Outcome reports a finished job.
type Outcome struct {
	Result  Result
	Method  Method
	URI     string        // the Package URI written, for pull
	Version model.Version // /5 object version
	// Before and After are the registrations at the start and end. A client
	// whose object set changed re-registers (FW-05); Added and Removed list
	// the /o and /o/i entries that differ.
	Before, After  *server.Registration
	Added, Removed []lwm2m.Path
}

// Manager runs firmware updates, one at a time per endpoint.
type Manager struct {
	srv  *server.Server
	mu   sync.Mutex
	jobs map[string]*watch
}

// New returns a Manager for srv. Forward the server's events to
// HandleEvent.
func New(srv *server.Server) *Manager {
	return &Manager{srv: srv, jobs: map[string]*watch{}}
}

// watch is the live view of one endpoint's /5/0 during a job.
type watch struct {
	mu        sync.Mutex
	state     State
	result    Result
	reg       *server.Registration
	reregs    int // Registered events seen (observations were voided, REG-12)
	sig       chan struct{}
	ownObs    []*server.Observation
	observing bool
}

func (w *watch) signal() {
	select {
	case w.sig <- struct{}{}:
	default:
	}
}

func (w *watch) apply(nodes []lwm2m.Node) {
	w.mu.Lock()
	for _, n := range nodes {
		v, ok := intOf(n.Value)
		if n.Kind != lwm2m.KindValue || !ok {
			continue
		}
		switch n.Path {
		case PathState:
			w.state = State(v)
		case PathResult:
			w.result = Result(v)
		}
	}
	w.mu.Unlock()
	w.signal()
}

func (w *watch) snapshot() (State, Result) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, w.result
}

// HandleEvent feeds server events to running jobs. It also reconciles
// observations when an Update shrinks a client's object list (FW-05):
// observations of removed objects or instances are forgotten. It never
// blocks.
func (m *Manager) HandleEvent(e server.Event) {
	if m == nil {
		return
	}
	switch e := e.(type) {
	case server.Notification:
		if w := m.job(e.Registration.Endpoint); w != nil && e.Response != nil && e.Response.Success() &&
			len(e.Observation.Paths) == 1 && (e.Observation.Paths[0] == PathState || e.Observation.Paths[0] == PathResult) {
			w.apply(e.Response.Nodes)
		}
	case server.Registered:
		if w := m.job(e.Registration.Endpoint); w != nil {
			w.mu.Lock()
			w.reg, w.reregs, w.ownObs, w.observing = e.Registration, w.reregs+1, nil, false
			w.mu.Unlock()
			w.signal()
		}
	case server.Updated:
		_, removed := diff(e.Previous.Objects, e.Registration.Objects)
		for _, ob := range m.srv.Observations(e.Registration.Endpoint) {
			if slices.ContainsFunc(ob.Paths, func(p lwm2m.Path) bool { return underAny(p, removed) }) {
				_, _ = m.srv.CancelObservation(context.Background(), ob, false) // passive: no I/O
			}
		}
		if w := m.job(e.Registration.Endpoint); w != nil {
			w.mu.Lock()
			w.reg = e.Registration
			w.mu.Unlock()
			w.signal()
		}
	}
}

func (m *Manager) job(ep string) *watch {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[ep]
}

// Version returns the /5 object version of a registration (VER-01).
func Version(reg *server.Registration) (model.Version, error) {
	o, ok := reg.Object(5)
	if !ok {
		return model.Version{}, ErrNoFirmwareObject
	}
	return model.ResolveVersion(reg.Version, 5, o.Version)
}

func atLeast(v model.Version, minor uint16) bool {
	return !v.Less(model.Version{Major: 1, Minor: minor})
}

// Run performs job on ep and blocks until the update succeeds, fails, is
// deferred (Result Deferred, nil error) or ctx ends.
func (m *Manager) Run(ctx context.Context, ep string, job Job) (*Outcome, error) {
	reg, ok := m.srv.Store().ByEndpoint(ep)
	if !ok {
		return nil, server.ErrNotRegistered
	}
	ver, err := Version(reg)
	if err != nil {
		return nil, err
	}
	if err := checkVersion(ver, job); err != nil {
		return nil, err
	}
	w := &watch{reg: reg, sig: make(chan struct{}, 1)}
	m.mu.Lock()
	if m.jobs[ep] != nil {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	m.jobs[ep] = w
	m.mu.Unlock()
	defer m.finish(ep, w)

	out := &Outcome{Version: ver, Before: reg}
	m.observe(ctx, ep, w)
	m.poll(ctx, ep, w)
	st, res := w.snapshot()
	resume := job.Resume && st == Downloaded
	switch {
	case st == Updating:
		return nil, ErrBusy
	case resume:
	case st != Idle || res != Initial:
		// A previous attempt left state behind; reset so its result is not
		// mistaken for this one (FW-04).
		if err := m.reset(ctx, ep, job.Method == Push || len(job.URIs) == 0); err != nil {
			return nil, err
		}
		m.poll(ctx, ep, w)
	}
	if err := m.writeOptions(ctx, ep, job); err != nil {
		return nil, err
	}
	auto := job.AutoUpgrade != nil && *job.AutoUpgrade
	if !resume {
		if err := m.deliver(ctx, ep, job, out); err != nil {
			return out, err
		}
		if !auto {
			if err := m.wait(ctx, ep, w, job, func(st State, r Result) (bool, error) {
				if r != Initial {
					return true, &ResultError{r}
				}
				return st == Downloaded, nil
			}); err != nil {
				return m.finalize(out, w), err
			}
		}
	}
	if !auto {
		// Forget a Deferred from an earlier Execute: the client resets the
		// result once the update starts, or reports Deferred again.
		w.mu.Lock()
		w.result = Initial
		w.mu.Unlock()
		r, err := m.srv.Execute(ctx, ep, PathUpdate, "")
		if err != nil {
			return out, err
		}
		if !r.Success() {
			return out, fmt.Errorf("%w: Execute /5/0/2: %s", ErrRejected, server.CodeString(r.Code))
		}
	}
	err = m.wait(ctx, ep, w, job, func(_ State, r Result) (bool, error) {
		switch r {
		case Initial:
			return false, nil
		case Success, Deferred:
			return true, nil
		}
		return true, &ResultError{r}
	})
	return m.finalize(out, w), err
}

func checkVersion(v model.Version, job Job) error {
	if (job.Severity != nil || job.MaxDeferPeriod != nil) && !atLeast(v, 1) {
		return fmt.Errorf("%w: Severity and Maximum Defer Period need /5 v1.1, client has %s", ErrVersion, v)
	}
	if job.AutoUpgrade != nil && !atLeast(v, 2) {
		return fmt.Errorf("%w: Automatic Upgrade at Download needs /5 v1.2, client has %s", ErrVersion, v)
	}
	return nil
}

func (m *Manager) finalize(out *Outcome, w *watch) *Outcome {
	_, out.Result = w.snapshot()
	w.mu.Lock()
	out.After = w.reg
	w.mu.Unlock()
	if cur, ok := m.srv.Store().ByEndpoint(out.Before.Endpoint); ok {
		out.After = cur
	}
	out.Added, out.Removed = diff(out.Before.Objects, out.After.Objects)
	return out
}

func (m *Manager) finish(ep string, w *watch) {
	m.mu.Lock()
	delete(m.jobs, ep)
	m.mu.Unlock()
	w.mu.Lock()
	obs := w.ownObs
	w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, ob := range obs {
		_, _ = m.srv.CancelObservation(ctx, ob, true)
	}
}

// observe starts Observe on State and Update Result. A refusal is not an
// error: polling covers clients that do not notify.
func (m *Manager) observe(ctx context.Context, ep string, w *watch) {
	var own []*server.Observation
	for _, p := range []lwm2m.Path{PathState, PathResult} {
		ob, r, err := m.srv.Observe(ctx, ep, p, server.ObserveOptions{})
		if err != nil || r == nil || !r.Success() {
			continue
		}
		own = append(own, ob)
		w.apply(r.Nodes)
	}
	w.mu.Lock()
	w.ownObs, w.observing = append(w.ownObs, own...), true
	w.mu.Unlock()
}

// poll reads State and Update Result.
func (m *Manager) poll(ctx context.Context, ep string, w *watch) {
	for _, p := range []lwm2m.Path{PathState, PathResult} {
		if r, err := m.srv.Read(ctx, ep, p, server.ReadOptions{}); err == nil && r.Success() {
			w.apply(r.Nodes)
		}
	}
}

// wait blocks until done reports true. Notifications, polls and
// re-registrations (after which it observes again) update the view.
func (m *Manager) wait(ctx context.Context, ep string, w *watch, job Job, done func(State, Result) (bool, error)) error {
	every := job.Poll
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		w.mu.Lock()
		reobserve := !w.observing
		w.mu.Unlock()
		if reobserve {
			m.observe(ctx, ep, w)
			m.poll(ctx, ep, w)
		}
		if ok, err := done(w.snapshot()); ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.sig:
		case <-t.C:
			m.poll(ctx, ep, w)
		}
	}
}

// deliver pushes or writes the Package URI.
func (m *Manager) deliver(ctx context.Context, ep string, job Job, out *Outcome) error {
	method, uri, err := m.choose(ctx, ep, job)
	if err != nil {
		return err
	}
	out.Method, out.URI = method, uri
	if method == Pull {
		return m.write(ctx, ep, PathPackageURI, lwm2m.String(uri), nil)
	}
	attempts := max(job.PushAttempts, 1)
	opaque := lwm2m.FormatOpaque
	for i := 0; ; i++ {
		pctx, cancel := ctx, context.CancelFunc(func() {})
		if job.PushTimeout > 0 {
			pctx, cancel = context.WithTimeout(ctx, job.PushTimeout)
		}
		// go-coap splits a body over the block size into Block1 blocks with
		// one token (FW-01, FW-02); a new Write gets a new token and starts
		// again from block 0.
		err := m.write(pctx, ep, PathPackage, lwm2m.Opaque(job.Package), &opaque)
		cancel()
		if err == nil || errors.Is(err, ErrRejected) || i+1 >= attempts || ctx.Err() != nil {
			return err
		}
	}
}

func (m *Manager) write(ctx context.Context, ep string, p lwm2m.Path, v lwm2m.Value, cf *lwm2m.ContentFormat) error {
	r, err := m.srv.Write(ctx, ep, p, []lwm2m.Node{lwm2m.ValueNode(p, v)}, server.WriteOptions{Format: cf})
	if err != nil {
		return err
	}
	if !r.Success() {
		return fmt.Errorf("%w: Write %s: %s", ErrRejected, p, server.CodeString(r.Code))
	}
	return nil
}

// choose picks push or pull and the URI (FW-03, FW-07).
func (m *Manager) choose(ctx context.Context, ep string, job Job) (Method, string, error) {
	delivery := int64(2) // both, when /5/0/9 cannot be read
	if r, err := m.srv.Read(ctx, ep, PathDelivery, server.ReadOptions{}); err == nil && r.Success() && len(r.Nodes) == 1 {
		if v, ok := intOf(r.Nodes[0].Value); ok {
			delivery = v
		}
	}
	supported, err := m.Protocols(ctx, ep)
	if err != nil {
		return 0, "", err
	}
	uri := ""
	for _, u := range job.URIs {
		p, err := ProtocolOf(u)
		if err == nil && slices.Contains(supported, p) && len(u) <= 255 { // /5/0/1 range 0..255 (OBJ-02)
			uri = u
			break
		}
	}
	canPull, canPush := delivery == 0 || delivery == 2, delivery == 1 || delivery == 2
	switch {
	case job.Method == Pull && uri == "":
		return 0, "", ErrUnsupportedProtocol
	case job.Method == Pull && !canPull, job.Method == Push && !canPush:
		return 0, "", fmt.Errorf("%w: /5/0/9 is %d", ErrNoMethod, delivery)
	case job.Method == Pull:
		return Pull, uri, nil
	case job.Method == Push:
		return Push, "", nil
	case canPull && uri != "":
		return Pull, uri, nil
	case canPush && job.Package != nil:
		return Push, "", nil
	case len(job.URIs) > 0 && uri == "":
		return 0, "", ErrUnsupportedProtocol
	}
	return 0, "", ErrNoMethod
}

// Protocols reads /5/0/8. Unknown values are ignored, and a missing
// resource or one with no known value means CoAP only (FW-03).
func (m *Manager) Protocols(ctx context.Context, ep string) ([]Protocol, error) {
	r, err := m.srv.Read(ctx, ep, PathProtocols, server.ReadOptions{})
	if err != nil {
		return nil, err
	}
	if !r.Success() {
		return []Protocol{CoAP}, nil
	}
	var out []Protocol
	for _, n := range r.Nodes {
		if v, ok := intOf(n.Value); ok && n.Kind == lwm2m.KindValue && v >= int64(CoAP) && v <= int64(CoAPTLS) {
			out = append(out, Protocol(v))
		}
	}
	if len(out) == 0 {
		out = []Protocol{CoAP} // nothing understood: CoAP is the default setting (C E.6 res 8)
	}
	return out, nil
}

// writeOptions writes Severity, Maximum Defer Period and Automatic
// Upgrade at Download (FW-06) before delivery.
func (m *Manager) writeOptions(ctx context.Context, ep string, job Job) error {
	if job.Severity != nil {
		if err := m.write(ctx, ep, PathSeverity, lwm2m.Integer(*job.Severity), nil); err != nil {
			return err
		}
	}
	if job.MaxDeferPeriod != nil {
		if err := m.write(ctx, ep, PathMaxDefer, lwm2m.Unsigned(*job.MaxDeferPeriod), nil); err != nil {
			return err
		}
	}
	if job.AutoUpgrade != nil {
		return m.write(ctx, ep, PathAutoUpgrade, lwm2m.Boolean(*job.AutoUpgrade), nil)
	}
	return nil
}

// Reset returns the state machine to Idle (FW-04): an empty Package URI,
// or with viaPackage a Package of one NUL byte.
func (m *Manager) Reset(ctx context.Context, ep string, viaPackage bool) error {
	return m.reset(ctx, ep, viaPackage)
}

func (m *Manager) reset(ctx context.Context, ep string, viaPackage bool) error {
	if viaPackage {
		opaque := lwm2m.FormatOpaque
		return m.write(ctx, ep, PathPackage, lwm2m.Opaque([]byte{0}), &opaque)
	}
	return m.write(ctx, ep, PathPackageURI, lwm2m.String(""), nil)
}

// Cancel executes /5/0/10 (/5 v1.1+, FW-06). A client already installing
// answers 4.05, returned as ErrRejected. A running Run then ends with
// ResultError{Cancelled}.
func (m *Manager) Cancel(ctx context.Context, ep string) error {
	reg, ok := m.srv.Store().ByEndpoint(ep)
	if !ok {
		return server.ErrNotRegistered
	}
	v, err := Version(reg)
	if err != nil {
		return err
	}
	if !atLeast(v, 1) {
		return fmt.Errorf("%w: Cancel needs /5 v1.1, client has %s", ErrVersion, v)
	}
	r, err := m.srv.Execute(ctx, ep, PathCancel, "")
	if err != nil {
		return err
	}
	if r.Code != codes.Changed {
		return fmt.Errorf("%w: Cancel: %s", ErrRejected, server.CodeString(r.Code))
	}
	return nil
}

// intOf reads an integer from any numeric or decimal-text value (a client
// without a schema may report text).
func intOf(v lwm2m.Value) (int64, bool) {
	switch v.Type {
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		return v.Int, true
	case lwm2m.TypeUnsigned:
		return int64(v.Uint), true
	case lwm2m.TypeFloat:
		return int64(v.Float), v.Float == float64(int64(v.Float))
	case lwm2m.TypeString:
		i, err := strconv.ParseInt(strings.TrimSpace(v.Str), 10, 64)
		return i, err == nil
	case lwm2m.TypeOpaque:
		i, err := strconv.ParseInt(string(v.Bytes), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// diff lists the /o and /o/i entries only in after (added) or only in
// before (removed).
func diff(before, after []link.Object) (added, removed []lwm2m.Path) {
	b, a := entries(before), entries(after)
	for p := range a {
		if !b[p] {
			added = append(added, p)
		}
	}
	for p := range b {
		if !a[p] {
			removed = append(removed, p)
		}
	}
	sortPaths(added)
	sortPaths(removed)
	return added, removed
}

func entries(objs []link.Object) map[lwm2m.Path]bool {
	out := map[lwm2m.Path]bool{}
	for _, o := range objs {
		out[lwm2m.NewPath(o.ID)] = true
		for _, i := range o.Instances {
			out[lwm2m.NewPath(o.ID, i)] = true
		}
	}
	return out
}

func sortPaths(ps []lwm2m.Path) { slices.SortFunc(ps, lwm2m.Path.Compare) }

func underAny(p lwm2m.Path, roots []lwm2m.Path) bool {
	return slices.ContainsFunc(roots, p.HasPrefix)
}
