package server

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/regparam"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

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

// linkPayload returns the request body and checks that, when present, it
// is application/link-format (REG-07). A missing Content-Format is
// tolerated (Wakaama sends Update payloads without one, client-ecosystem
// T-list).
func linkPayload(m *Message) ([]byte, bool) {
	if len(bytes.TrimSpace(m.Payload)) == 0 {
		return nil, true
	}
	if m.Format != nil && *m.Format != lwm2m.FormatLinkFormat {
		return nil, false
	}
	return m.Payload, true
}

// reply is a response plus actions to run once it has been sent (events,
// queue wake-up), so DM requests triggered by events follow the reply
// (GEN-10).
type reply struct {
	msg   *Message
	after func()
}

func replyCode(c codes.Code) reply { return reply{msg: status(c)} }

func (s *Server) register(peer Peer, m *Message) reply {
	p, err := regparam.ParseRegister(m.Query)
	if err != nil {
		// A version this server does not know is 4.12 even when the rest is
		// malformed, so clients can fall back (REG-04, Anjay C12).
		if v := versionParam(m.Query); v != "" {
			if _, ok := normaliseVersion(v); !ok {
				return replyCode(codes.PreconditionFailed)
			}
		}
		return replyCode(codes.BadRequest) // REG-06: missing mandatory or unknown parameter
	}
	version, ok := normaliseVersion(*p.Version)
	if !ok {
		return replyCode(codes.PreconditionFailed) // REG-04
	}

	id := peer.Identity()
	var epName string
	if p.Endpoint != nil {
		epName = *p.Endpoint
	}
	ep, code := s.authorizeEndpoint(epName, id)
	if code != 0 {
		return replyCode(code)
	}

	payload, ok := linkPayload(m)
	if !ok {
		return replyCode(codes.BadRequest)
	}
	objs, root, cfs, err := parseObjectLinks(payload)
	if err != nil {
		return replyCode(codes.BadRequest)
	}
	pids := pidKeys(p.ProfileIDs)
	if len(pids) > 0 {
		resolved, ok := s.resolveProfiles(pids)
		if !ok && payload == nil {
			return replyCode(codeConflict) // PROF-06
		}
		objs = mergeObjects(objs, resolved)
		if payload != nil {
			s.learnProfiles(pids, objs) // PROF-11
		}
	} else if payload == nil && version == "1.2" {
		return replyCode(codeConflict) // PROF-08: no way to determine the object list
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
		RawLinks:       string(payload),
		RootPath:       root,
		ContentFormats: cfs,
		Identity:       id,
		Addr:           peer.RemoteAddr(),
		RegisteredAt:   now,
		LastUpdate:     now,
		peer:           peer,
	}
	old := s.store.Add(reg)
	if old != nil {
		s.dropClientState(old)
	}
	return reply{
		msg: &Message{Code: codes.Created, Location: []string{"rd", reg.ID}},
		after: func() {
			s.emit(Registered{Registration: reg, Replaced: old})
			s.queues.wake(reg)
		},
	}
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
		if !has || !si.Matches(id) {
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

func (s *Server) location(peer Peer, m *Message, id string) reply {
	reg, found := s.store.ByID(id)
	if !found {
		return replyCode(codes.NotFound) // REG-13/14/16: unknown or removed location
	}
	switch m.Code {
	case codes.POST:
		return s.update(peer, m, reg)
	case codes.DELETE:
		return s.deregister(reg, peer.Identity())
	}
	return replyCode(codes.MethodNotAllowed)
}

// sameClient reports whether a request on a registration's location comes
// from the registered client. Secured registrations require the same
// authenticated identity on any session or address (C2). NoSec
// registrations are bound to the address (REG-17).
func sameClient(reg *Registration, peer Identity) bool {
	return reg.Identity.Equal(peer)
}

func (s *Server) update(peerConn Peer, m *Message, reg *Registration) reply {
	peer := peerConn.Identity()
	if !sameClient(reg, peer) {
		if reg.Identity.Secure() {
			return replyCode(codes.BadRequest)
		}
		return replyCode(codes.NotFound) // NoSec address change: client must Register again (REG-17)
	}
	p, err := regparam.ParseUpdate(m.Query, reg.Version)
	if err != nil {
		return replyCode(codes.BadRequest) // REG-14
	}
	payload, ok := linkPayload(m)
	if !ok {
		return replyCode(codes.BadRequest)
	}
	var objs []link.Object
	var root string
	var cfs []lwm2m.ContentFormat
	if payload != nil {
		var err error
		objs, root, cfs, err = parseObjectLinks(payload)
		if err != nil {
			return replyCode(codes.BadRequest)
		}
	}
	pids := pidKeys(p.ProfileIDs)
	newList := payload != nil
	if len(pids) > 0 { // PROF-09
		resolved, ok := s.resolveProfiles(pids)
		if !ok && payload == nil {
			return replyCode(codeConflict)
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
		r.RawLinks = string(payload)
		r.RootPath = root
		r.ContentFormats = cfs
	}
	r.LastUpdate = s.cfg.Now()
	r.Addr = peerConn.RemoteAddr()
	r.peer = peerConn
	reg = &r
	if !s.store.Update(reg) {
		return replyCode(codes.NotFound) // removed concurrently
	}
	return reply{msg: status(codes.Changed), after: func() {
		s.emit(Updated{Registration: reg, Previous: *prev})
		s.queues.wake(reg)
	}}
}

func (s *Server) deregister(reg *Registration, peer Identity) reply {
	if !sameClient(reg, peer) {
		// int-105: a NoSec registration with no credentials on file can be
		// removed from any address; secured ones only by their identity.
		_, hasCreds := s.security.ByEndpoint(reg.Endpoint)
		if reg.Identity.Secure() || hasCreds {
			return replyCode(codes.BadRequest)
		}
	}
	if _, ok := s.store.Remove(reg.ID); !ok {
		return replyCode(codes.NotFound)
	}
	s.dropClientState(reg)
	return reply{msg: status(codes.Deleted), after: func() {
		s.emit(Deregistered{Registration: reg, Reason: ReasonDeregistered})
	}}
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
