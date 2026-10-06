// Package testclient is a scriptable LwM2M client used to prove server
// behaviour. It holds an in-memory object store, answers every Device
// Management and Information Reporting operation, records each request the
// server sends, and lets tests inject responses and raw requests.
package testclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/codec/senml"
	"github.com/fiumaralabs/lwm2m/model"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/options/config"
	"github.com/plgd-dev/go-coap/v3/udp"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// Config configures a test client.
type Config struct {
	Endpoint string
	Version  string // lwm2m=, default "1.1"
	Lifetime uint32 // lt=, default 86400
	Binding  string // b=, omitted when ""
	Queue    bool   // Q flag
	// PSK selects DTLS with a pre-shared key; empty means NoSec.
	PSKIdentity string
	PSKKey      []byte
	// CID makes the client offer a zero-length Connection ID, like Zephyr.
	CID bool
	// Format is the client's preferred format when the server sends no
	// Accept. Default: TLV for 1.0, SenML CBOR for 1.1, LwM2M CBOR for 1.2.
	Format lwm2m.ContentFormat
	// Formats lists the multi-value formats the client accepts in writes;
	// others get 4.15. Default: all.
	Formats []lwm2m.ContentFormat
}

// Request is one request the server sent to the client.
type Request struct {
	Code    codes.Code
	Type    message.Type
	Path    string
	Queries []string
	Format  *lwm2m.ContentFormat
	Accept  *lwm2m.ContentFormat
	Observe *uint32
	Token   message.Token
	Body    []byte
}

// Override lets a test answer a request itself; handled=false falls back
// to the default behaviour.
type Override func(r Request) (code codes.Code, cf *lwm2m.ContentFormat, body []byte, handled bool)

// Client is a test LwM2M client.
type Client struct {
	cfg  Config
	conn *client.Conn

	mu              sync.Mutex
	objects         map[uint16]bool            // registered objects
	instances       map[lwm2m.Path]bool        // /o/i
	values          map[lwm2m.Path]lwm2m.Value // /o/i/r or /o/i/r/ri
	multiple        map[lwm2m.Path]bool        // multi-instance resources
	attrs           map[lwm2m.Path][]string    // Write-Attributes queries
	observers       map[string]*observer       // by token
	requests        []Request
	executed        []Request
	override        Override
	location        string
	seq             uint32
	offline         bool
	aclSSID         uint16 // non-zero: enforce /2 Access Control (acl.go)
	bindingOverride string
	bootstrapHook   ExecHook
	notifyMu        sync.Mutex        // one notification in flight at a time
	resetCh         chan message.Type // Reset for the notification in flight
}

type observer struct {
	token     message.Token
	paths     []lwm2m.Path
	composite bool
	accept    *lwm2m.ContentFormat
	query     []string  // 1.2 attributes in the Observe request
	st        *obsState // attribute state (behaviour.go)
}

// New returns a client with an empty store. Add objects with Set or
// AddInstance before Register.
func New(cfg Config) *Client {
	if cfg.Version == "" {
		cfg.Version = "1.1"
	}
	if cfg.Lifetime == 0 {
		cfg.Lifetime = 86400
	}
	if cfg.Format == 0 {
		switch cfg.Version {
		case "1.0":
			cfg.Format = lwm2m.FormatTLV
		case "1.1":
			cfg.Format = lwm2m.FormatSenMLCBOR
		default:
			cfg.Format = lwm2m.FormatLwM2MCBOR
		}
	}
	return &Client{
		cfg:       cfg,
		objects:   map[uint16]bool{},
		instances: map[lwm2m.Path]bool{},
		values:    map[lwm2m.Path]lwm2m.Value{},
		multiple:  map[lwm2m.Path]bool{},
		attrs:     map[lwm2m.Path][]string{},
		observers: map[string]*observer{},
	}
}

// process notes Resets for our notifications. go-coap matches the Reset
// to the pending CON first (ending WriteMessage) and then still hands it to
// this hook, so a Reset is seen right after WriteMessage returns.
func (c *Client) process(req *pool.Message, cc *client.Conn, handler config.HandlerFunc[*client.Conn]) {
	if req.Type() == message.Reset {
		c.mu.Lock()
		ch := c.resetCh
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- message.Reset:
			default:
			}
			cc.ReleaseMessage(req)
			return
		}
	}
	cc.ProcessReceivedMessageWithHandler(req, handler)
}

// Dial connects to the server at addr.
func (c *Client) Dial(addr string) error {
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(c.handle))
	hook := options.WithProcessReceivedMessageFunc(c.process)
	if c.cfg.PSKIdentity != "" {
		cfg := &piondtls.Config{
			PSK:             func([]byte) ([]byte, error) { return c.cfg.PSKKey, nil },
			PSKIdentityHint: []byte(c.cfg.PSKIdentity),
			CipherSuites:    []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8},
		}
		if c.cfg.CID {
			cfg.ConnectionIDGenerator = piondtls.OnlySendCIDGenerator()
		}
		conn, err := coapdtls.Dial(addr, cfg, options.WithMux(r), options.WithBlockwise(true, 0x6, 30*time.Second), hook)
		if err != nil {
			return err
		}
		c.conn = conn
		return nil
	}
	conn, err := udp.Dial(addr, options.WithMux(r), options.WithBlockwise(true, 0x6, 30*time.Second), hook)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// LocalAddr is the client's source address.
func (c *Client) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// Location is the registration location segment ("" before Register).
func (c *Client) Location() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.location
}

// SetOverride installs a response override (nil removes it).
func (c *Client) SetOverride(o Override) {
	c.mu.Lock()
	c.override = o
	c.mu.Unlock()
}

// Requests returns the requests received so far.
func (c *Client) Requests() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Request(nil), c.requests...)
}

// LastRequest returns the most recent request.
func (c *Client) LastRequest() (Request, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		return Request{}, false
	}
	return c.requests[len(c.requests)-1], true
}

// Attributes returns the attributes last written on p.
func (c *Client) Attributes(p lwm2m.Path) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attrs[p]
}

// --- object store -------------------------------------------------------

// AddObject announces object oid with no instances.
func (c *Client) AddObject(oid uint16) {
	c.mu.Lock()
	c.objects[oid] = true
	c.mu.Unlock()
}

// Set stores a value at /o/i/r or /o/i/r/ri, creating the object and
// instance, and notifies observers (C §6.4.2).
func (c *Client) Set(p lwm2m.Path, v lwm2m.Value) {
	c.mu.Lock()
	c.setLocked(p, v)
	c.mu.Unlock()
	c.notify(p)
}

func (c *Client) setLocked(p lwm2m.Path, v lwm2m.Value) {
	c.objects[p.Object()] = true
	c.instances[p.Truncate(2)] = true
	if p.IsResourceInstance() {
		c.multiple[p.Parent()] = true
	}
	c.values[p] = v
}

// Get returns the value at p.
func (c *Client) Get(p lwm2m.Path) (lwm2m.Value, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[p]
	return v, ok
}

// Nodes returns the stored values under p, sorted.
func (c *Client) Nodes(p lwm2m.Path) []lwm2m.Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodesLocked(p)
}

func (c *Client) nodesLocked(p lwm2m.Path) []lwm2m.Node {
	var out []lwm2m.Node
	for vp, v := range c.values {
		if vp.HasPrefix(p) {
			out = append(out, lwm2m.ValueNode(vp, v))
		}
	}
	lwm2m.SortNodes(out)
	return out
}

func (c *Client) existsLocked(p lwm2m.Path) bool {
	switch p.Len() {
	case 0:
		return true
	case 1:
		return c.objects[p.Object()]
	case 2:
		return c.instances[p]
	case 3:
		if c.multiple[p] {
			return true
		}
		_, ok := c.values[p]
		return ok
	}
	_, ok := c.values[p]
	return ok
}

// registry is the object model the test client knows (like a real client,
// it types values of resources it has not stored yet from the model).
var registry = model.Default()

// schema types incoming values from the stored ones, then from the OMA
// model at the client's default object versions.
func (c *Client) schema() lwm2m.Schema {
	c.mu.Lock()
	defs := map[lwm2m.Path]lwm2m.ResourceDef{}
	versions := map[uint16]model.Version{}
	for p, v := range c.values {
		r := p.Truncate(3)
		defs[r] = lwm2m.ResourceDef{Type: v.Type, Multiple: c.multiple[r]}
	}
	for o := range c.objects {
		versions[o] = model.DefaultVersion(c.cfg.Version, o)
	}
	c.mu.Unlock()
	fallback := registry.Schema(versions)
	return lwm2m.SchemaFunc(func(p lwm2m.Path) (lwm2m.ResourceDef, bool) {
		if d, ok := defs[p]; ok {
			return d, true
		}
		return fallback.Resource(p)
	})
}

// ObjectLinks renders the Register object list: </o> for objects without
// instances, </o/i> per instance; /0 is never listed (REG-09).
func (c *Client) ObjectLinks() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var oids []int
	for o := range c.objects {
		if o != 0 && o != 21 && o != 23 {
			oids = append(oids, int(o))
		}
	}
	sort.Ints(oids)
	var parts []string
	for _, o := range oids {
		var iids []int
		for p := range c.instances {
			if int(p.Object()) == o {
				iids = append(iids, int(p.Instance()))
			}
		}
		sort.Ints(iids)
		if len(iids) == 0 {
			parts = append(parts, fmt.Sprintf("</%d>", o))
		}
		for _, i := range iids {
			parts = append(parts, fmt.Sprintf("</%d/%d>", o, i))
		}
	}
	return strings.Join(parts, ",")
}

// --- uplink -------------------------------------------------------------

// Response is a server reply to an uplink request.
type Response struct {
	Code     codes.Code
	Location []string
	Body     []byte
}

// RegisterQuery is the query the client sends by default.
func (c *Client) RegisterQuery() []string {
	q := []string{"ep=" + c.cfg.Endpoint, "lt=" + strconv.Itoa(int(c.cfg.Lifetime)), "lwm2m=" + c.cfg.Version}
	if c.cfg.Endpoint == "" {
		q = q[1:]
	}
	if c.cfg.Binding != "" {
		q = append(q, "b="+c.cfg.Binding)
	}
	if c.cfg.Queue {
		q = append(q, "Q")
	}
	return q
}

// Register registers with the default query and object list. A 2.01
// stores the location.
func (c *Client) Register(ctx context.Context) (*Response, error) {
	return c.RegisterRaw(ctx, c.RegisterQuery(), []byte(c.ObjectLinks()), true)
}

// RegisterRaw sends POST /rd with the given query and payload.
func (c *Client) RegisterRaw(ctx context.Context, query []string, payload []byte, withCF bool) (*Response, error) {
	r, err := c.request(ctx, codes.POST, "/rd", query, payload, withCF)
	if err == nil && r.Code == codes.Created && len(r.Location) == 2 {
		c.mu.Lock()
		c.location = r.Location[1]
		c.observers = map[string]*observer{} // a Register voids observations (OBS-03)
		c.mu.Unlock()
	}
	return r, err
}

// Update sends POST /rd/<location>.
func (c *Client) Update(ctx context.Context, query []string, payload []byte) (*Response, error) {
	return c.request(ctx, codes.POST, "/rd/"+c.Location(), query, payload, payload != nil)
}

// Deregister sends DELETE /rd/<location>.
func (c *Client) Deregister(ctx context.Context) (*Response, error) {
	return c.request(ctx, codes.DELETE, "/rd/"+c.Location(), nil, nil, false)
}

// Send sends nodes to /dp in format cf (C §6.4.6).
func (c *Client) Send(ctx context.Context, nodes []lwm2m.Node, cf lwm2m.ContentFormat) (*Response, error) {
	cd, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	body, err := cd.Encode(lwm2m.Root, nodes)
	if err != nil {
		return nil, err
	}
	return c.Raw(ctx, codes.POST, "/dp", nil, &cf, body)
}

func (c *Client) request(ctx context.Context, code codes.Code, path string, query []string, payload []byte, withCF bool) (*Response, error) {
	var cf *lwm2m.ContentFormat
	if withCF {
		f := lwm2m.FormatLinkFormat
		cf = &f
	}
	return c.Raw(ctx, code, path, query, cf, payload)
}

// Raw sends an arbitrary request to the server.
func (c *Client) Raw(ctx context.Context, code codes.Code, path string, query []string, cf *lwm2m.ContentFormat, body []byte) (*Response, error) {
	m := c.conn.AcquireMessage(ctx)
	defer c.conn.ReleaseMessage(m)
	m.SetCode(code)
	m.SetType(message.Confirmable)
	if err := m.SetPath(path); err != nil {
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
	if cf != nil {
		m.SetContentFormat(message.MediaType(*cf))
	}
	if body != nil {
		m.SetBody(bytes.NewReader(body))
	}
	res, err := c.conn.Do(m)
	if err != nil {
		return nil, err
	}
	defer c.conn.ReleaseMessage(res)
	out := &Response{Code: res.Code()}
	if lp, err := res.Options().LocationPath(); err == nil && lp != "" {
		out.Location = strings.Split(strings.Trim(lp, "/"), "/")
	}
	if res.Body() != nil {
		out.Body, _ = io.ReadAll(res.Body())
	}
	return out, nil
}

// --- downlink handling --------------------------------------------------

func toRequest(m *mux.Message) Request {
	r := Request{Code: m.Code(), Type: m.Type(), Token: append(message.Token(nil), m.Token()...)}
	r.Path, _ = m.Options().Path()
	r.Path = "/" + strings.TrimPrefix(r.Path, "/")
	r.Queries, _ = m.Options().Queries()
	if cf, err := m.ContentFormat(); err == nil {
		f := lwm2m.ContentFormat(cf)
		r.Format = &f
	}
	if a, err := m.Options().Accept(); err == nil {
		f := lwm2m.ContentFormat(a)
		r.Accept = &f
	}
	if o, err := m.Observe(); err == nil {
		r.Observe = &o
	}
	if m.Body() != nil {
		r.Body, _ = io.ReadAll(m.Body())
	}
	return r
}

func (c *Client) handle(w mux.ResponseWriter, m *mux.Message) {
	r := toRequest(m)
	c.mu.Lock()
	c.requests = append(c.requests, r)
	ov := c.override
	c.mu.Unlock()
	if ov != nil {
		if code, cf, body, ok := ov(r); ok {
			respond(w, code, cf, body)
			return
		}
	}
	if code, ok := c.checkAccess(r); !ok {
		respond(w, code, nil, nil)
		return
	}
	code, cf, body, extra := c.serve(r)
	respond(w, code, cf, body, extra...)
}

func respond(w mux.ResponseWriter, code codes.Code, cf *lwm2m.ContentFormat, body []byte, opts ...message.Option) {
	var rd io.ReadSeeker
	if body != nil {
		rd = bytes.NewReader(body)
	}
	_ = w.SetResponse(code, message.TextPlain, rd, opts...)
	if cf == nil {
		w.Message().Remove(message.ContentFormat)
	} else {
		w.Message().SetContentFormat(message.MediaType(*cf))
	}
}

func fp(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// serve implements the default client behaviour.
func (c *Client) serve(r Request) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	if r.Code == 5 || r.Code == 7 { // FETCH, iPATCH on /
		return c.serveComposite(r)
	}
	p, err := lwm2m.ParsePath(r.Path)
	if err != nil {
		return codes.NotFound, nil, nil, nil
	}
	switch r.Code {
	case codes.GET:
		if r.Accept != nil && *r.Accept == lwm2m.FormatLinkFormat {
			return c.discover(p)
		}
		if r.Observe != nil {
			return c.observe(r, p)
		}
		return c.read(p, r.Accept)
	case codes.PUT:
		if r.Body == nil && len(r.Queries) > 0 {
			c.mu.Lock()
			c.applyAttrs(p, r.Queries)
			c.mu.Unlock()
			return codes.Changed, nil, nil, nil
		}
		return c.write(p, r, true)
	case codes.POST:
		switch p.Len() {
		case 1:
			return c.create(p, r)
		case 3:
			c.mu.Lock()
			c.executed = append(c.executed, r)
			c.mu.Unlock()
			go c.onExecute(p, r)
			return codes.Changed, nil, nil, nil
		}
		return c.write(p, r, false)
	case codes.DELETE:
		if !p.IsInstance() {
			return codes.MethodNotAllowed, nil, nil, nil
		}
		c.mu.Lock()
		if !c.instances[p] {
			c.mu.Unlock()
			return codes.NotFound, nil, nil, nil
		}
		delete(c.instances, p)
		for vp := range c.values {
			if vp.HasPrefix(p) {
				delete(c.values, vp)
			}
		}
		c.mu.Unlock()
		c.afterDelete(p)
		return codes.Deleted, nil, nil, nil
	}
	return codes.MethodNotAllowed, nil, nil, nil
}

// Executed returns the Execute requests received.
func (c *Client) Executed() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Request(nil), c.executed...)
}

func (c *Client) responseFormat(p lwm2m.Path, accept *lwm2m.ContentFormat, nodes []lwm2m.Node) lwm2m.ContentFormat {
	if accept != nil {
		return *accept
	}
	if len(nodes) == 1 && nodes[0].Path == p && (p.IsResource() || p.IsResourceInstance()) {
		if nodes[0].Value.Type == lwm2m.TypeOpaque {
			return lwm2m.FormatOpaque
		}
		return lwm2m.FormatText
	}
	return c.cfg.Format
}

func (c *Client) encode(p lwm2m.Path, cf lwm2m.ContentFormat, nodes []lwm2m.Node) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	cd, err := codec.For(cf)
	if err != nil {
		return codes.NotAcceptable, nil, nil, nil
	}
	body, err := cd.Encode(p, nodes)
	if err != nil {
		return codes.NotAcceptable, nil, nil, nil
	}
	return codes.Content, &cf, body, nil
}

func (c *Client) read(p lwm2m.Path, accept *lwm2m.ContentFormat) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	c.mu.Lock()
	if p.IsRoot() || p.Object() == 0 {
		c.mu.Unlock()
		return codes.Unauthorized, nil, nil, nil // DM-11
	}
	if !c.existsLocked(p) {
		c.mu.Unlock()
		return codes.NotFound, nil, nil, nil
	}
	nodes := c.nodesLocked(p)
	c.mu.Unlock()
	return c.encode(p, c.responseFormat(p, accept, nodes), nodes)
}

func (c *Client) discover(p lwm2m.Path) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.existsLocked(p) || p.IsRoot() {
		return codes.NotFound, nil, nil, nil
	}
	seen := map[lwm2m.Path]bool{}
	var links []string
	add := func(q lwm2m.Path, extra string) {
		if !seen[q] {
			seen[q] = true
			links = append(links, "<"+q.String()+">"+extra)
		}
	}
	add(p, "")
	var paths []lwm2m.Path
	for vp := range c.values {
		if vp.HasPrefix(p) {
			paths = append(paths, vp.Truncate(3))
		}
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Compare(paths[j]) < 0 })
	for _, rp := range paths {
		if rp.Len() > 2 && p.Len() <= 2 {
			add(rp.Truncate(2), "")
		}
		extra := ""
		if c.multiple[rp] {
			n := 0
			for vp := range c.values {
				if vp.Parent() == rp && vp.IsResourceInstance() {
					n++
				}
			}
			extra = ";dim=" + strconv.Itoa(n)
		}
		add(rp, extra)
	}
	return codes.Content, fp(lwm2m.FormatLinkFormat), []byte(strings.Join(links, ",")), nil
}

func (c *Client) acceptsFormat(cf lwm2m.ContentFormat) bool {
	if len(c.cfg.Formats) == 0 || cf == lwm2m.FormatText || cf == lwm2m.FormatOpaque {
		return true
	}
	for _, f := range c.cfg.Formats {
		if f == cf {
			return true
		}
	}
	return false
}

func (c *Client) write(p lwm2m.Path, r Request, replace bool) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	if r.Format == nil {
		return codes.BadRequest, nil, nil, nil // FMT-03
	}
	if !c.acceptsFormat(*r.Format) {
		return codes.UnsupportedMediaType, nil, nil, nil
	}
	cd, err := codec.For(*r.Format)
	if err != nil {
		return codes.UnsupportedMediaType, nil, nil, nil
	}
	nodes, err := cd.Decode(p, r.Body, c.schema())
	if err != nil {
		return codes.BadRequest, nil, nil, nil
	}
	c.mu.Lock()
	if !c.existsLocked(p.Truncate(2)) {
		c.mu.Unlock()
		return codes.NotFound, nil, nil, nil
	}
	if replace {
		for vp := range c.values {
			if vp.HasPrefix(p) {
				delete(c.values, vp)
			}
		}
	}
	for _, n := range nodes {
		if n.Kind == lwm2m.KindValue {
			c.setLocked(n.Path, n.Value)
		}
	}
	c.mu.Unlock()
	c.notify(p)
	return codes.Changed, nil, nil, nil
}

func (c *Client) create(p lwm2m.Path, r Request) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	if r.Format == nil {
		return codes.BadRequest, nil, nil, nil
	}
	cd, err := codec.For(*r.Format)
	if err != nil {
		return codes.UnsupportedMediaType, nil, nil, nil
	}
	nodes, err := cd.Decode(p, r.Body, c.schema())
	if err != nil || len(nodes) == 0 {
		return codes.BadRequest, nil, nil, nil
	}
	c.mu.Lock()
	inst := nodes[0].Path.Truncate(2)
	if c.instances[inst] {
		c.mu.Unlock()
		return codes.BadRequest, nil, nil, nil // DM-09: instance exists
	}
	c.instances[inst] = true
	for _, n := range nodes {
		if n.Kind == lwm2m.KindValue {
			c.setLocked(n.Path, n.Value)
		}
	}
	c.mu.Unlock()
	c.afterCreate(inst)
	return codes.Created, nil, nil, []message.Option{
		{ID: message.LocationPath, Value: []byte(strconv.Itoa(int(inst.Object())))},
		{ID: message.LocationPath, Value: []byte(strconv.Itoa(int(inst.Instance())))},
	}
}

func (c *Client) serveComposite(r Request) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	if r.Path != "/" || len(r.Queries) > 0 && r.Code == 7 {
		return codes.BadRequest, nil, nil, nil
	}
	if r.Format == nil {
		return codes.BadRequest, nil, nil, nil
	}
	cd, err := codec.For(*r.Format)
	if err != nil {
		return codes.UnsupportedMediaType, nil, nil, nil
	}
	if r.Code == 7 { // iPATCH: Write-Composite
		nodes, err := cd.Decode(lwm2m.Root, r.Body, c.schema())
		if err != nil {
			return codes.BadRequest, nil, nil, nil
		}
		c.mu.Lock()
		for _, n := range nodes {
			if n.Kind == lwm2m.KindValue {
				c.setLocked(n.Path, n.Value)
			}
		}
		c.mu.Unlock()
		return codes.Changed, nil, nil, nil
	}
	sc, ok := cd.(senml.Codec)
	if !ok {
		return codes.UnsupportedMediaType, nil, nil, nil
	}
	paths, err := sc.DecodePaths(r.Body)
	if err != nil {
		return codes.BadRequest, nil, nil, nil
	}
	if r.Observe != nil && *r.Observe == 1 {
		c.mu.Lock()
		delete(c.observers, string(r.Token))
		c.mu.Unlock()
	}
	nodes := c.compositeNodes(paths)
	accept := lwm2m.FormatSenMLCBOR
	if r.Accept != nil {
		accept = *r.Accept
	}
	code, cf, body, opts := c.encode(lwm2m.Root, accept, nodes)
	if r.Observe != nil && *r.Observe == 0 && code == codes.Content {
		c.mu.Lock()
		c.observers[string(r.Token)] = &observer{token: r.Token, paths: paths, composite: true, accept: &accept}
		c.seq++
		seq := c.seq
		c.mu.Unlock()
		opts = append(opts, observeOpt(seq))
	}
	return code, cf, body, opts
}

func (c *Client) compositeNodes(paths []lwm2m.Path) []lwm2m.Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []lwm2m.Node
	for _, p := range paths {
		out = append(out, c.nodesLocked(p)...)
	}
	return out
}

func observeOpt(seq uint32) message.Option {
	return message.Option{ID: message.Observe, Value: encodeUint(seq)}
}

func encodeUint(v uint32) []byte {
	switch {
	case v == 0:
		return nil
	case v < 1<<8:
		return []byte{byte(v)}
	case v < 1<<16:
		return []byte{byte(v >> 8), byte(v)}
	}
	return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
}

func (c *Client) observe(r Request, p lwm2m.Path) (codes.Code, *lwm2m.ContentFormat, []byte, []message.Option) {
	if *r.Observe == 1 {
		c.mu.Lock()
		old := c.observers[string(r.Token)]
		delete(c.observers, string(r.Token))
		c.mu.Unlock()
		c.stopObservation(old)
		return c.read(p, r.Accept)
	}
	code, cf, body, opts := c.read(p, r.Accept)
	if code != codes.Content {
		return code, cf, body, opts
	}
	o := &observer{token: r.Token, paths: []lwm2m.Path{p}, accept: r.Accept, query: r.Queries}
	c.mu.Lock()
	c.observers[string(r.Token)] = o
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	c.startObservation(o)
	return code, cf, body, append(opts, observeOpt(seq))
}

// Observers returns how many observations the client holds.
func (c *Client) Observers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.observers)
}

// notify sends a CON notification to every observer whose paths cover p.
func (c *Client) notify(p lwm2m.Path) {
	c.mu.Lock()
	var targets []*observer
	for _, o := range c.observers {
		for _, op := range o.paths {
			if p.HasPrefix(op) || op.HasPrefix(p) {
				targets = append(targets, o)
				break
			}
		}
	}
	c.mu.Unlock()
	for _, o := range targets {
		c.evaluate(o)
	}
}

// NotifyResult says how the server answered a notification.
type NotifyResult uint8

const (
	NotifyAcked NotifyResult = iota
	NotifyReset
)

// Notify sends a CON notification for the observation with token tok, even
// if the client already forgot it (to test the server's Reset, OBS-02).
func (c *Client) Notify(ctx context.Context, tok message.Token) (NotifyResult, error) {
	c.mu.Lock()
	o, ok := c.observers[string(tok)]
	if !ok {
		o = &observer{token: tok, paths: []lwm2m.Path{lwm2m.MustParsePath("/3/0")}}
	}
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	base := o.paths[0]
	var nodes []lwm2m.Node
	if o.composite {
		base = lwm2m.Root
		nodes = c.compositeNodes(o.paths)
	} else {
		nodes = c.Nodes(base)
	}
	_, cf, body, _ := c.encode(base, c.responseFormat(base, o.accept, nodes), nodes)
	if cf == nil {
		return 0, fmt.Errorf("testclient: cannot encode notification")
	}
	return c.sendNotification(ctx, tok, seq, *cf, body)
}

// NotifyRaw sends a CON notification with an arbitrary payload for token
// tok (e.g. historical SenML records stored while offline).
func (c *Client) NotifyRaw(ctx context.Context, tok message.Token, cf lwm2m.ContentFormat, body []byte) (NotifyResult, error) {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	return c.sendNotification(ctx, tok, seq, cf, body)
}

func (c *Client) sendNotification(ctx context.Context, tok message.Token, seq uint32, cf lwm2m.ContentFormat, body []byte) (NotifyResult, error) {
	m := c.conn.AcquireMessage(ctx)
	defer c.conn.ReleaseMessage(m)
	m.SetCode(codes.Content)
	m.SetType(message.Confirmable)
	m.SetToken(tok)
	m.SetObserve(seq)
	m.SetContentFormat(message.MediaType(cf))
	m.SetBody(bytes.NewReader(body))
	// go-coap assigns the message ID inside WriteMessage, so a Reset is
	// matched to the one notification in flight.
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	got := make(chan message.Type, 1)
	c.mu.Lock()
	c.resetCh = got
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.resetCh = nil
		c.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m.SetContext(ctx)
	if err := c.conn.WriteMessage(m); err != nil { // CON: returns on ACK or RST
		return 0, err
	}
	select {
	case <-got:
		// A Reset cancels the observation (RFC 7641 §3.6).
		c.mu.Lock()
		delete(c.observers, string(tok))
		c.mu.Unlock()
		return NotifyReset, nil
	case <-time.After(100 * time.Millisecond):
		return NotifyAcked, nil
	}
}
