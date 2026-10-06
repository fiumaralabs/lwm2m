package testclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// BootstrapClient is a Client that also plays the client side of the
// Bootstrap interface (Core §6.1): Bootstrap-Request, Bootstrap-Pack-
// Request, and Bootstrap-Write, -Delete, -Discover, -Read and -Finish on
// the object store. Outside a bootstrap session it is a plain Client.
type BootstrapClient struct {
	*Client
	// OnTrigger runs in its own goroutine when a server executes /1/x/9
	// Bootstrap-Request Trigger (C §6.1.3.4). Set it before Dial.
	OnTrigger func()

	bmu      sync.Mutex
	override Override
	active   bool
	finished chan codes.Code
}

// NewBootstrap returns a bootstrap-capable client with an empty store.
func NewBootstrap(cfg Config) *BootstrapClient {
	b := &BootstrapClient{Client: New(cfg), finished: make(chan codes.Code, 16)}
	b.Client.SetOverride(b.serveBS)
	return b
}

// SetOverride installs a response override that runs before the bootstrap
// and default behaviour (nil removes it).
func (b *BootstrapClient) SetOverride(o Override) {
	b.bmu.Lock()
	b.override = o
	b.bmu.Unlock()
}

// BootstrapQuery is the default Bootstrap-Request query: ep and pct.
func (b *BootstrapClient) BootstrapQuery(pct *lwm2m.ContentFormat) []string {
	var q []string
	if b.cfg.Endpoint != "" {
		q = append(q, "ep="+b.cfg.Endpoint)
	}
	if pct != nil {
		q = append(q, "pct="+strconv.Itoa(int(*pct)))
	}
	return q
}

// BootstrapRequest sends POST /bs?ep=&pct= (C §6.1.7.1). The client is in
// bootstrap mode from the request until Finish (or a non-2.04 answer).
func (b *BootstrapClient) BootstrapRequest(ctx context.Context, query []string) (*Response, error) {
	return b.BootstrapRequestWith(func() (*Response, error) { return b.Raw(ctx, codes.POST, "/bs", query, nil, nil) })
}

// BootstrapRequestWith enters bootstrap mode and sends the
// Bootstrap-Request with send, e.g. over another transport (TCP, OSCORE).
// The client leaves bootstrap mode unless the answer is 2.04.
func (b *BootstrapClient) BootstrapRequestWith(send func() (*Response, error)) (*Response, error) {
	b.bmu.Lock()
	b.active = true // the BS may send its first request before our Do returns
	b.bmu.Unlock()
	r, err := send()
	if err != nil || r.Code != codes.Changed {
		b.bmu.Lock()
		b.active = false
		b.bmu.Unlock()
	}
	return r, err
}

// WaitFinish waits for Bootstrap-Finish and returns the code the client
// answered (2.04, or 4.06 for an inconsistent configuration).
func (b *BootstrapClient) WaitFinish(ctx context.Context) (codes.Code, error) {
	select {
	case c := <-b.finished:
		return c, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Bootstrapping reports whether a bootstrap session is open.
func (b *BootstrapClient) Bootstrapping() bool {
	b.bmu.Lock()
	defer b.bmu.Unlock()
	return b.active
}

// PackResponse is the answer to a Bootstrap-Pack-Request.
type PackResponse struct {
	Code   codes.Code
	Format *lwm2m.ContentFormat
	Body   []byte
	Nodes  []lwm2m.Node // decoded Pack (2.05 only)
}

// PackRequest sends GET /bspack?ep=&acc= with Accept (C §6.1.7.7). A 2.05
// Pack is applied: each object in it replaces all its instances except
// the BS account and /3/0 (BS-15). It errors if the Pack cannot be decoded
// or leaves no Server Account; the client must then use Bootstrap-Request.
func (b *BootstrapClient) PackRequest(ctx context.Context, query []string, accept *lwm2m.ContentFormat) (*PackResponse, error) {
	m := b.conn.AcquireMessage(ctx)
	defer b.conn.ReleaseMessage(m)
	m.SetCode(codes.GET)
	m.SetType(message.Confirmable)
	if err := m.SetPath("/bspack"); err != nil {
		return nil, err
	}
	tok, err := message.GetToken()
	if err != nil {
		return nil, err
	}
	m.SetToken(tok)
	for _, q := range query {
		m.AddQuery(q)
	}
	if accept != nil {
		m.SetAccept(message.MediaType(*accept))
	}
	res, err := b.conn.Do(m)
	if err != nil {
		return nil, err
	}
	defer b.conn.ReleaseMessage(res)
	out := &PackResponse{Code: res.Code()}
	if cf, err := res.ContentFormat(); err == nil {
		f := lwm2m.ContentFormat(cf)
		out.Format = &f
	}
	if res.Body() != nil {
		out.Body, _ = io.ReadAll(res.Body())
	}
	if out.Code != codes.Content {
		return out, nil
	}
	if out.Format == nil {
		return out, fmt.Errorf("testclient: Pack without Content-Format")
	}
	cd, err := codec.For(*out.Format)
	if err != nil {
		return out, err
	}
	out.Nodes, err = cd.Decode(lwm2m.Root, out.Body, b.bsSchema())
	if err != nil {
		return out, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	objs := map[uint16]bool{}
	for _, n := range out.Nodes {
		objs[n.Path.Object()] = true
	}
	for o := range objs {
		b.deleteLocked(lwm2m.NewPath(o))
	}
	b.applyLocked(out.Nodes)
	if !b.consistentLocked() {
		return out, fmt.Errorf("testclient: Pack holds no Server Account")
	}
	return out, nil
}

// BSAccountLinks renders acc: the BS-account instances as CoRE links with
// no parameters (C §6.1.7.7, BS-14).
func (b *BootstrapClient) BSAccountLinks() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ls []string
	for _, p := range b.sortedInstancesLocked(lwm2m.Root) {
		if b.bsAccountLocked(p) && p.Object() != 3 {
			ls = append(ls, "<"+p.String()+">")
		}
	}
	return strings.Join(ls, ",")
}

// ServerAccount returns the first LwM2M Server Account in /0: URI, PSK
// identity (or certificate) and secret key.
func (b *BootstrapClient) ServerAccount() (uri string, identity, key []byte, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.sortedInstancesLocked(lwm2m.NewPath(0)) {
		if b.values[p.Append(1)].Bool {
			continue
		}
		return b.values[p.Append(0)].Str, b.values[p.Append(3)].Bytes, b.values[p.Append(5)].Bytes, true
	}
	return "", nil, nil, false
}

// --- bootstrap request handling -------------------------------------------

var bsModel = model.Default().Schema(map[uint16]model.Version{
	0: model.DefaultVersion("1.2", 0), 1: model.DefaultVersion("1.2", 1), 2: model.DefaultVersion("1.2", 2),
	21: model.DefaultVersion("1.2", 21),
})

// bsSchema types writes by the core object model, then by stored values.
func (b *BootstrapClient) bsSchema() lwm2m.Schema {
	stored := b.schema()
	return lwm2m.SchemaFunc(func(p lwm2m.Path) (lwm2m.ResourceDef, bool) {
		if d, ok := bsModel.Resource(p); ok {
			return d, true
		}
		return stored.Resource(p)
	})
}

func (b *BootstrapClient) serveBS(r Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	b.bmu.Lock()
	ov, active := b.override, b.active
	b.bmu.Unlock()
	if ov != nil {
		if code, cf, body, ok := ov(r); ok {
			return code, cf, body, true
		}
	}
	if !active {
		if r.Code == codes.POST && b.OnTrigger != nil {
			if p, err := lwm2m.ParsePath(r.Path); err == nil && p.IsResource() && p.Object() == 1 && p.Resource() == 9 {
				go b.OnTrigger()
			}
		}
		return 0, nil, nil, false
	}
	if r.Code == codes.POST && r.Path == "/bs" {
		return b.bsFinish()
	}
	p, err := lwm2m.ParsePath(r.Path)
	if err != nil {
		return codes.NotFound, nil, nil, true
	}
	switch r.Code {
	case codes.PUT:
		return b.bsWrite(p, r)
	case codes.DELETE:
		return b.bsDelete(p)
	case codes.GET:
		if r.Accept != nil && *r.Accept == lwm2m.FormatLinkFormat {
			return b.bsDiscover(p)
		}
		return b.bsRead(p, r.Accept)
	}
	return codes.MethodNotAllowed, nil, nil, true
}

// bsWrite is Bootstrap-Write (C §6.1.7.5): written whether or not the
// target exists; an instance-level write replaces the instance.
func (b *BootstrapClient) bsWrite(p lwm2m.Path, r Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	if r.Format == nil || p.IsRoot() {
		return codes.BadRequest, nil, nil, true
	}
	if !b.acceptsFormat(*r.Format) {
		return codes.UnsupportedMediaType, nil, nil, true
	}
	cd, err := codec.For(*r.Format)
	if err != nil {
		return codes.UnsupportedMediaType, nil, nil, true
	}
	nodes, err := cd.Decode(p, r.Body, b.bsSchema())
	if err != nil {
		return codes.BadRequest, nil, nil, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range nodes {
		target := n.Path.Truncate(3)
		if p.Len() <= 2 {
			target = n.Path.Truncate(2)
		}
		for vp := range b.values {
			if vp.HasPrefix(target) {
				delete(b.values, vp)
			}
		}
	}
	b.applyLocked(nodes)
	return codes.Changed, nil, nil, true
}

func (b *BootstrapClient) applyLocked(nodes []lwm2m.Node) {
	for _, n := range nodes {
		switch n.Kind {
		case lwm2m.KindValue:
			b.setLocked(n.Path, n.Value)
		default:
			b.objects[n.Path.Object()] = true
			b.instances[n.Path.Truncate(2)] = true
		}
	}
}

// bsAccountLocked reports an instance that is part of the Bootstrap-Server
// Account (its /0 instance and the /21, /23, /24 instances that instance
// links to), or /3/0: Bootstrap-Delete never touches them (C §6.1.7.6).
func (b *BootstrapClient) bsAccountLocked(inst lwm2m.Path) bool {
	if inst == lwm2m.NewPath(3, 0) {
		return true
	}
	for p := range b.instances {
		if p.Object() != 0 || !b.values[p.Append(1)].Bool {
			continue
		}
		if p == inst {
			return true
		}
		for _, r := range []uint16{17, 26, 27} {
			if v, ok := b.values[p.Append(r)]; ok && v.Type == lwm2m.TypeObjlnk &&
				lwm2m.NewPath(v.Link.Object, v.Link.Instance) == inst {
				return true
			}
		}
	}
	return false
}

func (b *BootstrapClient) deleteLocked(p lwm2m.Path) {
	for inst := range b.instances {
		if !inst.HasPrefix(p) || b.bsAccountLocked(inst) {
			continue
		}
		delete(b.instances, inst)
		for vp := range b.values {
			if vp.HasPrefix(inst) {
				delete(b.values, vp)
			}
		}
	}
}

// bsDelete is Bootstrap-Delete on /, /o or /o/i (C §6.1.7.6).
func (b *BootstrapClient) bsDelete(p lwm2m.Path) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	if p.Len() > 2 {
		return codes.BadRequest, nil, nil, true
	}
	b.mu.Lock()
	b.deleteLocked(p)
	b.mu.Unlock()
	return codes.Deleted, nil, nil, true
}

func (b *BootstrapClient) sortedInstancesLocked(under lwm2m.Path) []lwm2m.Path {
	var out []lwm2m.Path
	for p := range b.instances {
		if p.HasPrefix(under) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Compare(out[j]) < 0 })
	return out
}

// bsDiscover is Bootstrap-Discover on / or /o (C §6.1.7.3): </>;lwm2m=,
// then each object's instances; /0 instances carry ssid (not for the BS
// account) and uri, /1 instances ssid.
func (b *BootstrapClient) bsDiscover(p lwm2m.Path) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	if p.Len() > 1 {
		return codes.BadRequest, nil, nil, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var links []string
	if p.IsRoot() {
		links = append(links, "</>;lwm2m="+b.cfg.Version)
	}
	var oids []int
	for o := range b.objects {
		if p.IsRoot() || o == p.Object() {
			oids = append(oids, int(o))
		}
	}
	sort.Ints(oids)
	for _, o := range oids {
		insts := b.sortedInstancesLocked(lwm2m.NewPath(uint16(o)))
		if len(insts) == 0 {
			links = append(links, fmt.Sprintf("</%d>", o))
		}
		for _, inst := range insts {
			l := "<" + inst.String() + ">"
			switch o {
			case 0:
				if v, ok := b.values[inst.Append(10)]; ok && !b.values[inst.Append(1)].Bool {
					l += ";ssid=" + strconv.FormatInt(v.Int, 10)
				}
				if v, ok := b.values[inst.Append(0)]; ok {
					l += ";uri=\"" + v.Str + "\""
				}
			case 1:
				if v, ok := b.values[inst.Append(0)]; ok {
					l += ";ssid=" + strconv.FormatInt(v.Int, 10)
				}
			}
			links = append(links, l)
		}
	}
	return codes.Content, fp(lwm2m.FormatLinkFormat), []byte(strings.Join(links, ",")), true
}

// bsRead is Bootstrap-Read on /1, /1/i, /2 or /2/i only; others are 4.00
// like Wakaama, Anjay and Leshan (T63).
func (b *BootstrapClient) bsRead(p lwm2m.Path, accept *lwm2m.ContentFormat) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	if p.Len() < 1 || p.Len() > 2 || p.Object() != 1 && p.Object() != 2 {
		return codes.BadRequest, nil, nil, true
	}
	b.mu.Lock()
	exists := b.existsLocked(p)
	nodes := b.nodesLocked(p)
	b.mu.Unlock()
	if !exists {
		return codes.NotFound, nil, nil, true
	}
	cf := b.cfg.Format
	if accept != nil {
		cf = *accept
	}
	if !b.acceptsFormat(cf) {
		return codes.NotAcceptable, nil, nil, true
	}
	code, f, body, _ := b.encode(p, cf, nodes)
	return code, f, body, true
}

// consistentLocked is the Finish check: at most one BS account and at
// least one Server Account, a /0 instance (res 1 false) paired with a /1
// instance by Short Server ID (C §6.1.2, BS-16).
func (b *BootstrapClient) consistentLocked() bool {
	bs, ssids := 0, map[int64]bool{}
	for p := range b.instances {
		if p.Object() != 0 {
			continue
		}
		if b.values[p.Append(1)].Bool {
			bs++
		} else if v, ok := b.values[p.Append(10)]; ok {
			ssids[v.Int] = true
		}
	}
	if bs > 1 {
		return false
	}
	for p := range b.instances {
		if p.Object() == 1 && ssids[b.values[p.Append(0)].Int] {
			return true
		}
	}
	return false
}

func (b *BootstrapClient) bsFinish() (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
	b.mu.Lock()
	ok := b.consistentLocked()
	b.mu.Unlock()
	code := codes.Changed
	if !ok {
		code = codes.NotAcceptable // BS-07: inconsistent configuration
	}
	b.bmu.Lock()
	b.active = false
	b.bmu.Unlock()
	select {
	case b.finished <- code:
	default:
	}
	return code, nil, nil, true
}

// Dump renders the store for test failure messages.
func (b *BootstrapClient) Dump() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var buf bytes.Buffer
	for _, n := range b.nodesLocked(lwm2m.Root) {
		fmt.Fprintln(&buf, n)
	}
	return buf.String()
}
