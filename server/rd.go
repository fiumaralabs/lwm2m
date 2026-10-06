package server

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/regparam"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

func (s *Server) routes() {
	_ = s.router.Handle("/rd", mux.HandlerFunc(s.handleRegister))
	_ = s.router.Handle("/rd/{id}", mux.HandlerFunc(s.handleRDLocation))
	_ = s.router.Handle("/dp", mux.HandlerFunc(s.handleSend))
	s.router.DefaultHandle(mux.HandlerFunc(s.handleDefault))
}

// reply sets a response with no payload.
func reply(w mux.ResponseWriter, code codes.Code, opts ...message.Option) {
	_ = w.SetResponse(code, message.TextPlain, nil, opts...)
	w.Message().Remove(message.ContentFormat)
}

func queries(m *mux.Message) []string {
	qs, _ := m.Options().Queries()
	return qs
}

var versionRE = regexp.MustCompile(`^1\.([012])(\.\d+)?$`)

// normaliseVersion maps the lwm2m= parameter to "1.0", "1.1" or "1.2";
// ok=false for versions this server does not support (REG-04).
func normaliseVersion(v string) (string, bool) {
	m := versionRE.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	return "1." + m[1], true
}

// pidKeys renders Profile IDs as cache keys ("6:1234abcd", "oma:x").
func pidKeys(ids []regparam.ProfileID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		if id.Kind == "dynamic" {
			out[i] = strconv.Itoa(int(id.Suite)) + ":" + id.Hash
		} else {
			out[i] = id.Kind + ":" + id.Value
		}
	}
	return out
}

func seconds(n uint32) time.Duration { return time.Duration(n) * time.Second }

// readPayload returns the request body and checks that, when present, it is
// application/link-format (REG-07). A missing Content-Format is tolerated
// (Wakaama sends Update payloads without one, client-ecosystem T-list).
func readPayload(m *mux.Message) ([]byte, bool) {
	var body []byte
	if m.Body() != nil {
		b, err := io.ReadAll(m.Body())
		if err != nil {
			return nil, false
		}
		body = b
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, true
	}
	if cf, err := m.ContentFormat(); err == nil && cf != message.AppLinkFormat {
		return nil, false
	}
	return body, true
}

func (s *Server) handleRegister(w mux.ResponseWriter, m *mux.Message) {
	if m.Code() != codes.POST {
		reply(w, codes.MethodNotAllowed)
		return
	}
	cc, ok := w.Conn().(*client.Conn)
	if !ok {
		reply(w, codes.InternalServerError)
		return
	}
	p, err := regparam.ParseRegister(queries(m))
	if err != nil {
		// A version this server does not know is 4.12 even when the rest is
		// malformed, so clients can fall back (REG-04, Anjay C12).
		if v := versionParam(queries(m)); v != "" {
			if _, ok := normaliseVersion(v); !ok {
				reply(w, codes.PreconditionFailed)
				return
			}
		}
		reply(w, codes.BadRequest) // REG-06: missing mandatory or unknown parameter
		return
	}
	version, ok := normaliseVersion(*p.Version)
	if !ok {
		reply(w, codes.PreconditionFailed) // REG-04
		return
	}

	id := identityOf(cc.NetConn(), cc.RemoteAddr())
	var epName string
	if p.Endpoint != nil {
		epName = *p.Endpoint
	}
	ep, code := s.authorizeEndpoint(epName, id)
	if code != 0 {
		reply(w, code)
		return
	}

	payload, ok := readPayload(m)
	if !ok {
		reply(w, codes.BadRequest)
		return
	}
	objs, root, cfs, err := parseObjectLinks(payload)
	if err != nil {
		reply(w, codes.BadRequest)
		return
	}
	pids := pidKeys(p.ProfileIDs)
	if len(pids) > 0 {
		resolved, ok := s.resolveProfiles(pids)
		if !ok && payload == nil {
			reply(w, codeConflict) // PROF-06
			return
		}
		objs = mergeObjects(objs, resolved)
		if payload != nil {
			s.learnProfiles(pids, objs) // PROF-11
		}
	} else if payload == nil && version == "1.2" {
		reply(w, codeConflict) // PROF-08: no way to determine the object list
		return
	}

	binding := "U" // REG-18 default
	if p.Binding != nil {
		binding = p.Binding.Transports
	}
	var sms string
	if p.SMS != nil {
		sms = *p.SMS
	}
	now := s.cfg.Now()
	reg := &Registration{
		ID:             newRegistrationID(),
		Endpoint:       ep,
		Version:        version,
		Lifetime:       seconds(*p.Lifetime),
		Binding:        binding,
		QueueMode:      p.Queue,
		SMS:            sms,
		ProfileIDs:     pids,
		Objects:        objs,
		RootPath:       root,
		ContentFormats: cfs,
		Identity:       id,
		Addr:           cc.RemoteAddr(),
		RegisteredAt:   now,
		LastUpdate:     now,
		conn:           cc,
	}
	old := s.store.Add(reg)
	if old != nil {
		s.dropClientState(old)
	}
	reply(w, codes.Created,
		message.Option{ID: message.LocationPath, Value: []byte("rd")},
		message.Option{ID: message.LocationPath, Value: []byte(reg.ID)})
	s.emit(Registered{Registration: reg, Replaced: old})
	s.queues.wake(reg)
}

// authorizeEndpoint resolves the endpoint name and checks it against the
// authenticated identity (REG-02, SEC-06, SEC-07). It returns a non-zero
// CoAP code to reject.
func (s *Server) authorizeEndpoint(ep string, id Identity) (string, codes.Code) {
	if ep == "" {
		derived, ok := endpointFromIdentity(s.security, id)
		if !ok {
			return "", codes.BadRequest // REG-02: ep needed without an authenticated identifier
		}
		ep = derived
	}
	si, has := s.security.ByEndpoint(ep)
	if id.Secure() {
		if !has || !si.matches(id) {
			return "", codes.BadRequest // SEC-06: ep does not match the authenticated identity
		}
	} else if has {
		return "", codes.BadRequest // SEC-07: credentials exist, NoSec is not this client
	}
	if s.cfg.Authorize != nil && !s.cfg.Authorize(ep, id) {
		return "", codes.Forbidden // REG-06: endpoint not allowed
	}
	return ep, 0
}

func (s *Server) handleRDLocation(w mux.ResponseWriter, m *mux.Message) {
	id := m.RouteParams.Vars["id"]
	cc, ok := w.Conn().(*client.Conn)
	if !ok {
		reply(w, codes.InternalServerError)
		return
	}
	reg, found := s.store.ByID(id)
	if !found {
		reply(w, codes.NotFound) // REG-13/14/16: unknown or removed location
		return
	}
	peer := identityOf(cc.NetConn(), cc.RemoteAddr())
	switch m.Code() {
	case codes.POST:
		s.handleUpdate(w, m, cc, reg, peer)
	case codes.DELETE:
		s.handleDeregister(w, reg, peer)
	default:
		reply(w, codes.MethodNotAllowed)
	}
}

// sameClient reports whether a request on a registration's location comes
// from the registered client. Secured registrations require the same
// authenticated identity on any session or address (C2). NoSec
// registrations are bound to the address (REG-17).
func sameClient(reg *Registration, peer Identity) bool {
	return reg.Identity.Equal(peer)
}

func (s *Server) handleUpdate(w mux.ResponseWriter, m *mux.Message, cc *client.Conn, reg *Registration, peer Identity) {
	if !sameClient(reg, peer) {
		if reg.Identity.Secure() {
			reply(w, codes.BadRequest)
		} else {
			reply(w, codes.NotFound) // NoSec address change: client must Register again (REG-17)
		}
		return
	}
	p, err := regparam.ParseUpdate(queries(m), reg.Version)
	if err != nil {
		reply(w, codes.BadRequest) // REG-14
		return
	}
	payload, ok := readPayload(m)
	if !ok {
		reply(w, codes.BadRequest)
		return
	}
	var objs []link.Object
	var root string
	var cfs []lwm2m.ContentFormat
	if payload != nil {
		var err error
		objs, root, cfs, err = parseObjectLinks(payload)
		if err != nil {
			reply(w, codes.BadRequest)
			return
		}
	}
	pids := pidKeys(p.ProfileIDs)
	newList := payload != nil
	if len(pids) > 0 { // PROF-09
		resolved, ok := s.resolveProfiles(pids)
		if !ok && payload == nil {
			reply(w, codeConflict)
			return
		}
		objs = mergeObjects(objs, resolved)
		newList = true
	}

	prev := reg
	r := *reg // registrations are immutable once stored; Update stores a copy
	if p.Lifetime != nil {
		r.Lifetime = seconds(*p.Lifetime)
	}
	if p.Binding != nil {
		r.Binding = p.Binding.Transports
		r.QueueMode = p.Queue // a new binding restates queue mode
	} else if p.Queue {
		r.QueueMode = true
	}
	if p.SMS != nil {
		r.SMS = *p.SMS
	}
	if len(pids) > 0 {
		r.ProfileIDs = pids
	}
	if newList {
		r.Objects = objs // REG-15: a list replaces, it is not a delta
		r.RootPath = root
		r.ContentFormats = cfs
	}
	r.LastUpdate = s.cfg.Now()
	r.Addr = cc.RemoteAddr()
	r.conn = cc
	reg = &r
	if !s.store.Update(reg) {
		reply(w, codes.NotFound) // removed concurrently
		return
	}
	reply(w, codes.Changed)
	s.emit(Updated{Registration: reg, Previous: *prev})
	s.queues.wake(reg)
}

func (s *Server) handleDeregister(w mux.ResponseWriter, reg *Registration, peer Identity) {
	if !sameClient(reg, peer) {
		// int-105: a NoSec registration with no credentials on file can be
		// removed from any address; secured ones only by their identity.
		_, hasCreds := s.security.ByEndpoint(reg.Endpoint)
		if reg.Identity.Secure() || hasCreds {
			reply(w, codes.BadRequest)
			return
		}
	}
	if _, ok := s.store.Remove(reg.ID); !ok {
		reply(w, codes.NotFound)
		return
	}
	s.dropClientState(reg)
	reply(w, codes.Deleted)
	s.emit(Deregistered{Registration: reg, Reason: ReasonDeregistered})
}

// mergeObjects adds objects from b that are not already in a.
func mergeObjects(a, b []link.Object) []link.Object {
	for _, o := range b {
		dup := false
		for _, x := range a {
			if x.ID == o.ID {
				dup = true
				break
			}
		}
		if !dup {
			a = append(a, o)
		}
	}
	return a
}

// versionParam returns the raw lwm2m= value, "" if absent.
func versionParam(qs []string) string {
	for _, q := range qs {
		if v, ok := strings.CutPrefix(q, "lwm2m="); ok {
			return v
		}
	}
	return ""
}
