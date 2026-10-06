// Package compat serves the Leshan demo server REST API (HTTP + SSE) over
// the native server, so the Zephyr LwM2M interop harness (leshan.py) runs
// unchanged against it (spec/README.md §4, spec/zephyr-interop.md §3).
//
// It is a thin adapter: every device operation goes through the native
// server API, and its outcome is returned as HTTP 200 with Leshan's
// response object ("status": "NAME(code)"). Requests to a queue-mode client
// block until delivered or until the request timeout (spec C3); the
// "delayed" answer of Leshan is never produced.
//
//	api := compat.New(compat.Options{})
//	srv := server.New(server.Config{OnEvent: api.OnEvent, ...})
//	api.Attach(srv)
//	http.ListenAndServe(":8080", api)
package compat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Options configures the REST API. Zero values select Leshan's defaults.
type Options struct {
	// Prefix is the mount point, default "/api".
	Prefix string
	// Timeout applies when a request carries no valid ?timeout= (seconds);
	// Leshan's default is 5 s.
	Timeout time.Duration
	// Heartbeat is the SSE keep-alive period, default 2 s (≤5 s, §3.8).
	Heartbeat time.Duration
	// Schema types empty multi-instance resources in node JSON; pass the
	// same function as server.Config.Schema. Optional.
	Schema func(*server.Registration) lwm2m.Schema
	// Bootstrap, when set, is mounted at <Prefix>/bootstrap/, to serve
	// both REST APIs on one port. Leshan (and lwm2md) use a separate
	// listener instead: see NewBootstrap.
	Bootstrap http.Handler
}

// API is the Leshan-compatible REST handler.
type API struct {
	opts Options
	srv  atomic.Pointer[server.Server]
	mux  *http.ServeMux
	hub  hub
}

// New returns an API with no server; call Attach before serving.
func New(o Options) *API {
	if o.Prefix == "" {
		o.Prefix = "/api"
	}
	o.Prefix = strings.TrimSuffix(o.Prefix, "/")
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Heartbeat == 0 {
		o.Heartbeat = 2 * time.Second
	}
	a := &API{opts: o, mux: http.NewServeMux(), hub: hub{subs: map[*sub]struct{}{}}}
	p := o.Prefix
	a.mux.HandleFunc(p+"/clients", a.serveClients)
	a.mux.HandleFunc(p+"/clients/", a.serveClients)
	a.mux.HandleFunc(p+"/security/", a.serveSecurity)
	a.mux.HandleFunc(p+"/event", a.serveEvents)
	a.mux.HandleFunc(p+"/event/", a.serveEvents)
	if o.Bootstrap != nil {
		a.mux.Handle(p+"/bootstrap/", o.Bootstrap)
	}
	return a
}

// Attach sets the server the API drives. Events received before Attach are
// still streamed.
func (a *API) Attach(s *server.Server) { a.srv.Store(s) }

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.mux.ServeHTTP(w, r) }

func (a *API) schema(r *server.Registration) lwm2m.Schema {
	if a.opts.Schema == nil {
		return nil
	}
	return a.opts.Schema(r)
}

func textError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain;charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg)
}

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		textError(w, http.StatusInternalServerError, "Unexpected exception: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// segments splits like StringUtils.split(path, '/'): no empty segments.
func segments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// --- /clients --------------------------------------------------------------

// call is one device operation: fn talks to the device, render fills the
// type-specific fields of the response object.
type call struct {
	paths  []lwm2m.Path // targets, for the Security-object rule
	fn     func(ctx context.Context) (*server.Response, error)
	render func(resp *server.Response, j *responseJSON) error
}

func (a *API) serveClients(w http.ResponseWriter, r *http.Request) {
	srv := a.srv.Load()
	if srv == nil {
		textError(w, http.StatusServiceUnavailable, "server not attached")
		return
	}
	segs := segments(strings.TrimPrefix(r.URL.Path, a.opts.Prefix+"/clients"))
	if len(segs) == 0 {
		if r.Method != http.MethodGet {
			textError(w, http.StatusMethodNotAllowed, "")
			return
		}
		regs := srv.Store().All()
		slices.SortFunc(regs, func(x, y *server.Registration) int { return strings.Compare(x.Endpoint, y.Endpoint) })
		out := make([]registrationJSON, 0, len(regs))
		for _, reg := range regs {
			out = append(out, a.registration(srv, reg))
		}
		writeJSON(w, out)
		return
	}
	ep := segs[0]
	reg, ok := srv.Store().ByEndpoint(ep)
	if !ok {
		textError(w, http.StatusBadRequest, fmt.Sprintf("No registered client with id '%s'", ep))
		return
	}
	last := segs[len(segs)-1]
	composite := len(segs) >= 2 && segs[1] == "composite"
	var c *call
	var err error
	switch r.Method {
	case http.MethodGet:
		switch {
		case len(segs) == 1:
			writeJSON(w, a.registration(srv, reg))
			return
		case len(segs) == 2 && composite:
			c, err = a.readComposite(srv, reg, r)
		case len(segs) >= 3 && last == "discover":
			c, err = a.discover(srv, reg, segs[1:len(segs)-1])
		default:
			c, err = a.read(srv, reg, r, segs[1:])
		}
	case http.MethodPut:
		switch {
		case len(segs) == 2 && composite:
			c, err = a.writeComposite(srv, reg, r)
		case len(segs) < 3:
			textError(w, http.StatusBadRequest, "Invalid path")
			return
		case last == "attributes":
			c, err = a.writeAttributes(srv, reg, r, segs[1:len(segs)-1])
		default:
			c, err = a.write(srv, reg, r, segs[1:])
		}
	case http.MethodPost:
		switch {
		case len(segs) == 3 && composite && last == "observe":
			c, err = a.observeComposite(srv, reg, r)
		case len(segs) >= 3 && last == "observe":
			c, err = a.observe(srv, reg, r, segs[1:len(segs)-1])
		case len(segs) == 4:
			c, err = a.execute(srv, reg, r, segs[1:])
		case len(segs) >= 2 && len(segs) <= 3:
			c, err = a.create(srv, reg, r, segs[1:])
		default:
			textError(w, http.StatusBadRequest, "Invalid path")
			return
		}
	case http.MethodDelete:
		switch {
		case len(segs) == 3 && composite && last == "observe":
			a.cancelComposite(w, r, srv, reg)
			return
		case len(segs) >= 3 && last == "observe":
			a.cancel(w, r, srv, reg, segs[1:len(segs)-1])
			return
		default:
			c, err = a.delete(srv, reg, segs[1:])
		}
	default:
		textError(w, http.StatusMethodNotAllowed, "")
		return
	}
	if err != nil {
		textError(w, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	a.run(w, r, c)
}

func (a *API) registration(srv *server.Server, reg *server.Registration) registrationJSON {
	var sleeping *bool
	if reg.QueueMode {
		s := !srv.Awake(reg.Endpoint)
		sleeping = &s
	}
	return newRegistrationJSON(reg, sleeping)
}

// timeout reads ?timeout= in seconds (CS:787-801).
func (a *API) timeout(r *http.Request) time.Duration {
	if n, err := strconv.ParseInt(r.URL.Query().Get("timeout"), 10, 64); err == nil {
		return time.Duration(n) * time.Second
	}
	return a.opts.Timeout
}

// reservedObject: the Security, OSCORE and Bootstrap objects are closed to
// a DM server. The native API refuses them locally (DM-11); a client would
// answer 4.01, which is what Leshan forwards and the harness asserts
// (int-221), so that answer is reported.
func reservedObject(paths []lwm2m.Path) bool {
	for _, p := range paths {
		if p.Len() > 0 && (p.Object() == 0 || p.Object() == 21 || p.Object() == 23) {
			return true
		}
	}
	return false
}

func (a *API) run(w http.ResponseWriter, r *http.Request, c *call) {
	if reservedObject(c.paths) {
		writeJSON(w, newResponse(codes.Unauthorized, nil))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.timeout(r))
	defer cancel()
	resp, err := c.fn(ctx)
	switch {
	case err != nil && ctx.Err() != nil:
		textError(w, http.StatusGatewayTimeout, "Request timeout")
		return
	case errors.Is(err, server.ErrBadRequest), errors.Is(err, server.ErrNotRegistered), errors.Is(err, server.ErrQueueDropped):
		textError(w, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	case err != nil:
		textError(w, http.StatusInternalServerError, "Unexpected exception: "+err.Error())
		return
	case resp == nil: // passive cancel and the like
		w.WriteHeader(http.StatusOK)
		return
	}
	j := newResponse(resp.Code, resp.Payload)
	if resp.Success() && c.render != nil {
		if err := c.render(resp, &j); err != nil {
			textError(w, http.StatusInternalServerError, "Invalid Response: "+err.Error())
			return
		}
	}
	writeJSON(w, j)
}

func parsePath(segs []string) (lwm2m.Path, error) {
	return lwm2m.ParsePath(strings.Join(segs, "/"))
}

// content renders a read-like response at p.
func (a *API) content(reg *server.Registration, p lwm2m.Path) func(*server.Response, *responseJSON) error {
	return func(resp *server.Response, j *responseJSON) error {
		if resp.DecodeErr != nil {
			return resp.DecodeErr
		}
		if v, ok := encodeNode(p, resp.Nodes, a.schema(reg)); ok {
			j.Content = v
		}
		return nil
	}
}

func (a *API) compositeRender(reg *server.Registration, paths []lwm2m.Path) func(*server.Response, *responseJSON) error {
	return func(resp *server.Response, j *responseJSON) error {
		if resp.DecodeErr != nil {
			return resp.DecodeErr
		}
		j.Content = compositeContent(paths, resp.Nodes, a.schema(reg))
		return nil
	}
}

func (a *API) read(srv *server.Server, reg *server.Registration, r *http.Request, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	o := server.ReadOptions{Accept: formatParam(r.URL.Query().Get("format"))}
	return &call{paths: []lwm2m.Path{p}, render: a.content(reg, p), fn: func(ctx context.Context) (*server.Response, error) {
		return srv.Read(ctx, reg.Endpoint, p, o)
	}}, nil
}

func (a *API) discover(srv *server.Server, reg *server.Registration, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	return &call{paths: []lwm2m.Path{p},
		fn: func(ctx context.Context) (*server.Response, error) { return srv.Discover(ctx, reg.Endpoint, p, nil) },
		render: func(resp *server.Response, j *responseJSON) error {
			links, err := linksJSON(resp.Payload)
			j.ObjectLinks = links
			if j.ObjectLinks == nil {
				j.ObjectLinks = []linkJSON{}
			}
			return err
		}}, nil
}

// compositePaths reads ?paths=/a,/b.
func compositePaths(r *http.Request) ([]lwm2m.Path, error) {
	v := r.URL.Query().Get("paths")
	if v == "" {
		return nil, fmt.Errorf("missing paths")
	}
	var out []lwm2m.Path
	for _, s := range strings.Split(v, ",") {
		p, err := lwm2m.ParsePath(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func compositeOptions(r *http.Request) server.CompositeOptions {
	q := r.URL.Query()
	return server.CompositeOptions{Format: formatParam(q.Get("pathformat")), Accept: formatParam(q.Get("nodeformat"))}
}

func (a *API) readComposite(srv *server.Server, reg *server.Registration, r *http.Request) (*call, error) {
	paths, err := compositePaths(r)
	if err != nil {
		return nil, err
	}
	o := compositeOptions(r)
	return &call{paths: paths, render: a.compositeRender(reg, paths), fn: func(ctx context.Context) (*server.Response, error) {
		return srv.ReadComposite(ctx, reg.Endpoint, paths, o)
	}}, nil
}

// body decodes the request node at p (extractLwM2mNode: JSON node, or a
// text/plain string resource).
func body(r *http.Request, p lwm2m.Path) (jsonNode, []lwm2m.Node, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return jsonNode{}, nil, err
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		var j jsonNode
		if err := json.Unmarshal(b, &j); err != nil {
			return j, nil, fmt.Errorf("unable to parse json: %v", err)
		}
		if p.Len() == 1 { // create: the instance id comes from the node
			return j, nil, nil
		}
		ns, err := j.nodes(p)
		return j, ns, err
	case "text/plain":
		return jsonNode{}, []lwm2m.Node{lwm2m.ValueNode(p, lwm2m.String(string(b)))}, nil
	}
	return jsonNode{}, nil, fmt.Errorf("content type %s not supported", r.Header.Get("Content-Type"))
}

func (a *API) write(srv *server.Server, reg *server.Registration, r *http.Request, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	_, nodes, err := body(r, p)
	if err != nil {
		return nil, err
	}
	o := server.WriteOptions{Format: formatParam(r.URL.Query().Get("format"))}
	if v := r.URL.Query().Get("replace"); v != "" && !strings.EqualFold(v, "true") {
		o.Mode = server.PartialUpdate // Boolean.valueOf: anything but "true" is false
	}
	return &call{paths: []lwm2m.Path{p}, fn: func(ctx context.Context) (*server.Response, error) {
		return srv.Write(ctx, reg.Endpoint, p, nodes, o)
	}}, nil
}

func (a *API) writeComposite(srv *server.Server, reg *server.Registration, r *http.Request) (*call, error) {
	var m map[string]jsonNode
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("unable to parse json: %v", err)
	}
	var nodes []lwm2m.Node
	var paths []lwm2m.Path
	for k, j := range m {
		p, err := lwm2m.ParsePath(k)
		if err != nil {
			return nil, err
		}
		ns, err := j.nodes(p)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
		nodes = append(nodes, ns...)
	}
	lwm2m.SortNodes(nodes)
	f := formatParam(r.URL.Query().Get("nodeformat"))
	return &call{paths: paths, fn: func(ctx context.Context) (*server.Response, error) {
		return srv.WriteComposite(ctx, reg.Endpoint, nodes, f)
	}}, nil
}

// attributeQuery is the raw query as items: "pmin=10", or a bare "pmin"
// to unset (DefaultLwM2mAttributeParser.parseUriQuery).
func attributeQuery(raw string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(raw, "&") {
		if s == "" {
			continue
		}
		u, err := url.QueryUnescape(s)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func (a *API) writeAttributes(srv *server.Server, reg *server.Registration, r *http.Request, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	q, err := attributeQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	return &call{paths: []lwm2m.Path{p}, fn: func(ctx context.Context) (*server.Response, error) {
		return srv.WriteAttributes(ctx, reg.Endpoint, p, q)
	}}, nil
}

func (a *API) execute(srv *server.Server, reg *server.Registration, r *http.Request, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return &call{paths: []lwm2m.Path{p}, fn: func(ctx context.Context) (*server.Response, error) {
		return srv.Execute(ctx, reg.Endpoint, p, string(b))
	}}, nil
}

func (a *API) create(srv *server.Server, reg *server.Registration, r *http.Request, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	if !p.IsObject() {
		return nil, fmt.Errorf("create targets an object path, not %s", p)
	}
	j, _, err := body(r, p)
	if err != nil {
		return nil, err
	}
	if j.Kind != "instance" && j.Resources == nil {
		return nil, fmt.Errorf("payload must contain an object instance")
	}
	iid, ok := j.id()
	if !ok {
		// Leshan lets the client pick the id; the native Create always
		// carries it (C9), so pick the lowest one not registered.
		// ponytail: races a concurrent create of the same object.
		o, _ := reg.Object(p.Object())
		for slices.Contains(o.Instances, iid) {
			iid++
		}
	}
	inst := p.Append(iid)
	nodes, err := j.nodes(inst)
	if err != nil {
		return nil, err
	}
	f := formatParam(r.URL.Query().Get("format"))
	return &call{paths: []lwm2m.Path{p},
		fn: func(ctx context.Context) (*server.Response, error) { return srv.Create(ctx, reg.Endpoint, p, nodes, f) },
		render: func(resp *server.Response, j *responseJSON) error {
			j.Location = inst.String()
			if len(resp.Location) > 0 {
				j.Location = "/" + strings.Join(resp.Location, "/")
			}
			return nil
		}}, nil
}

func (a *API) delete(srv *server.Server, reg *server.Registration, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	return &call{paths: []lwm2m.Path{p}, fn: func(ctx context.Context) (*server.Response, error) {
		return srv.Delete(ctx, reg.Endpoint, p)
	}}, nil
}

func (a *API) observe(srv *server.Server, reg *server.Registration, r *http.Request, segs []string) (*call, error) {
	p, err := parsePath(segs)
	if err != nil {
		return nil, err
	}
	o := server.ObserveOptions{Accept: formatParam(r.URL.Query().Get("format"))}
	return &call{paths: []lwm2m.Path{p}, render: a.content(reg, p), fn: func(ctx context.Context) (*server.Response, error) {
		_, resp, err := srv.Observe(ctx, reg.Endpoint, p, o)
		return resp, err
	}}, nil
}

func (a *API) observeComposite(srv *server.Server, reg *server.Registration, r *http.Request) (*call, error) {
	paths, err := compositePaths(r)
	if err != nil {
		return nil, err
	}
	o := compositeOptions(r)
	return &call{paths: paths, render: a.compositeRender(reg, paths), fn: func(ctx context.Context) (*server.Response, error) {
		_, resp, err := srv.ObserveComposite(ctx, reg.Endpoint, paths, o)
		return resp, err
	}}, nil
}

// cancel ends single observations of a path. With ?active it sends a
// cancel for the first one and answers 404 if there is none; otherwise it
// forgets them all and the client gets a Reset on its next notification
// (CS:605-639; spec C5: an active cancel also forgets locally).
func (a *API) cancel(w http.ResponseWriter, r *http.Request, srv *server.Server, reg *server.Registration, segs []string) {
	p, err := parsePath(segs)
	if err != nil {
		textError(w, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	var obs []*server.Observation
	for _, ob := range srv.Observations(reg.Endpoint) {
		if !ob.Composite && ob.Paths[0] == p {
			obs = append(obs, ob)
		}
	}
	a.cancelObservations(w, r, srv, reg, obs, a.content(reg, p),
		fmt.Sprintf("no observation for path %s for  client '%s'", p, reg.Endpoint))
}

// cancelComposite matches the observation whose path list equals ?paths=
// in order (CS:641-679).
func (a *API) cancelComposite(w http.ResponseWriter, r *http.Request, srv *server.Server, reg *server.Registration) {
	paths, err := compositePaths(r)
	if err != nil {
		textError(w, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	var obs []*server.Observation
	for _, ob := range srv.Observations(reg.Endpoint) {
		if ob.Composite && slices.Equal(ob.Paths, paths) {
			obs = append(obs, ob)
		}
	}
	a.cancelObservations(w, r, srv, reg, obs, a.compositeRender(reg, paths),
		fmt.Sprintf("no composite observation for paths %v for  client '%s'", paths, reg.Endpoint))
}

func (a *API) cancelObservations(w http.ResponseWriter, r *http.Request, srv *server.Server, reg *server.Registration,
	obs []*server.Observation, render func(*server.Response, *responseJSON) error, notFound string) {
	if _, active := r.URL.Query()["active"]; !active {
		for _, ob := range obs {
			_, _ = srv.CancelObservation(r.Context(), ob, false)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if len(obs) == 0 {
		textError(w, http.StatusNotFound, notFound)
		return
	}
	a.run(w, r, &call{render: render, fn: func(ctx context.Context) (*server.Response, error) {
		return srv.CancelObservation(ctx, obs[0], true)
	}})
}

// --- /security -------------------------------------------------------------

func (a *API) serveSecurity(w http.ResponseWriter, r *http.Request) {
	srv := a.srv.Load()
	if srv == nil {
		textError(w, http.StatusServiceUnavailable, "server not attached")
		return
	}
	serveSecurity(w, r, srv.Security(), a.opts.Prefix)
}

// serveSecurity is Leshan's SecurityServlet over store, shared by the
// server API (:8080) and the bootstrap API (:8081).
func serveSecurity(w http.ResponseWriter, r *http.Request, store server.SecurityStore, prefix string) {
	segs := segments(strings.TrimPrefix(r.URL.Path, prefix+"/security"))
	switch {
	case r.Method == http.MethodGet && len(segs) == 1 && segs[0] == "clients":
		out := []securityJSON{}
		for _, si := range store.All() {
			out = append(out, newSecurityJSON(si))
		}
		writeJSON(w, out)
	case r.Method == http.MethodGet && len(segs) == 1 && segs[0] == "server":
		// ponytail: no server RPK/certificate is exposed yet; Leshan sends
		// {"pubkey":...} or {"certificate":...} when it has one.
		writeJSON(w, map[string]any{})
	case r.Method == http.MethodPut && len(segs) == 1 && segs[0] == "clients":
		var j securityJSON
		if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
			textError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
			return
		}
		si, err := j.info()
		if err != nil {
			textError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
			return
		}
		if err := store.Put(si); err != nil {
			textError(w, http.StatusBadRequest, err.Error()) // NonUniqueSecurityInfoException
			return
		}
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodDelete && len(segs) >= 2 && segs[0] == "clients":
		// ponytail: Leshan also evicts the endpoint's registration here
		// (infosAreCompromised); the native API has no operator-remove.
		if _, ok := store.Remove(strings.Join(segs[1:], "/")); !ok {
			writeJSON(w, map[string]string{"message": "not_found"})
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		textError(w, http.StatusBadRequest, "")
	}
}
