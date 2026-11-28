package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/regparam"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

var versionRE = regexp.MustCompile(`^1\.([012])(\.\d+)?$`)

// registrationLock serializes registration changes for an endpoint.
func (s *Server) registrationLock(endpoint string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(endpoint))
	mu := &s.registrationLocks[h.Sum32()%uint32(len(s.registrationLocks))]
	mu.Lock()
	return mu.Unlock
}

// replayLookup verifies the cached location still names a live registration.
func (s *Server) replayLookup(key [32]byte, id Identity) *Registration {
	s.replayMu.Lock()
	entry, ok := s.replays[key]
	if ok && !time.Now().Before(entry.at.Add(replayLifetime)) {
		delete(s.replays, key)
		ok = false
	}
	s.replayMu.Unlock()
	if !ok {
		if store, direct := s.store.(*MemoryStore); direct {
			entry, ok = store.lookupReplay(key)
		}
	}
	if !ok {
		return nil
	}
	current, live := s.store.ByID(entry.registration.ID)
	if !live || current.Expired(s.cfg.Now()) || !current.Identity.Equal(id) {
		s.replayMu.Lock()
		delete(s.replays, key)
		s.replayMu.Unlock()
		if store, direct := s.store.(*MemoryStore); direct {
			store.forgetReplay(key)
		}
		return nil
	}
	return current
}

func (s *Server) replayRemember(key [32]byte, reg *Registration) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	now := time.Now()
	var oldestKey [32]byte
	var oldest time.Time
	for k, entry := range s.replays {
		if !now.Before(entry.at.Add(replayLifetime)) {
			delete(s.replays, k)
			continue
		}
		if oldest.IsZero() || entry.at.Before(oldest) {
			oldest, oldestKey = entry.at, k
		}
	}
	if s.replays == nil {
		s.replays = make(map[[32]byte]replayEntry)
	}
	if len(s.replays) >= replayLimit {
		delete(s.replays, oldestKey)
	}
	s.replays[key] = replayEntry{registration: reg, at: now}
	if s.replayTimer == nil {
		s.replayTimer = time.AfterFunc(replayLifetime, s.expireReplayCache)
	}
}

func (s *Server) expireReplayCache() {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	now := time.Now()
	var next time.Time
	for key, entry := range s.replays {
		expires := entry.at.Add(replayLifetime)
		if !now.Before(expires) {
			delete(s.replays, key)
			continue
		}
		if next.IsZero() || expires.Before(next) {
			next = expires
		}
	}
	s.replayTimer = nil
	if !next.IsZero() {
		s.replayTimer = time.AfterFunc(time.Until(next), s.expireReplayCache)
	}
}

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

// registerFingerprint uses request bytes rather than resolved profile state, so
// a retry after a restart does not depend on the server's profile cache.
func registerFingerprint(ep string, id Identity, m *Message) [32]byte {
	var identity []byte
	switch id.Mode {
	case ModeOSCORE:
		identity = []byte(fmt.Sprintf("%x/%x", id.OSCOREIDContext, id.OSCORESenderID))
	case ModePSK:
		identity = []byte(id.PSKIdentity)
	case ModeRPK:
		identity = id.PublicKey
	case ModeX509:
		if id.Cert != nil {
			identity = id.Cert.Raw
		}
	default:
		identity = []byte(id.Addr)
	}
	query := slices.Clone(m.Query)
	slices.Sort(query)
	queryBytes := make([][]byte, len(query))
	for i, option := range query {
		queryBytes[i] = []byte(option)
	}
	// Byte strings preserve arbitrary CoAP option and identity octets in JSON.
	// This fixed schema contains only JSON-supported values, so Marshal cannot fail.
	canonical, _ := json.Marshal(struct {
		Endpoint []byte
		Mode     SecurityMode
		Identity []byte
		Query    [][]byte
		Payload  []byte
	}{[]byte(ep), id.Mode, identity, queryBytes, m.Payload})
	return sha256.Sum256(canonical)
}

// registerExchange binds a datagram to its authenticated identity, network peer,
// request content, token and message ID. IDs may be reused after exchange expiry.
func registerExchange(peer Peer, m *Message, fingerprint [32]byte) *[32]byte {
	if m.MessageID == nil || peer.RemoteAddr() == nil {
		return nil
	}
	canonical, _ := json.Marshal(struct {
		Fingerprint [32]byte
		Binding     string
		Peer        string
		MessageID   uint16
		Token       []byte
		Format      *lwm2m.ContentFormat
	}{fingerprint, peer.Binding(), peer.RemoteAddr().String(), *m.MessageID, m.Token, m.Format})
	exchange := sha256.Sum256(canonical)
	return &exchange
}

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
	ep, code := s.authenticateEndpoint(epName, id)
	if code != 0 {
		return replyCode(code)
	}
	unlock := s.registrationLock(ep)
	defer unlock()
	// Recheck credentials after waiting for the endpoint lock.
	if _, code := s.authenticateEndpoint(ep, id); code != 0 {
		return replyCode(code)
	}
	exchange := registerExchange(peer, m, registerFingerprint(ep, id, m))
	if exchange != nil {
		var cached *Registration
		if err := guardPeer(peer, func() error { cached = s.replayLookup(*exchange, id); return nil }); err != nil {
			return replyCode(codes.Forbidden)
		}
		if cached != nil {
			return reply{msg: &Message{Code: codes.Created, Location: []string{"rd", cached.ID}}}
		}
	}
	if s.cfg.Authorize != nil && !s.cfg.Authorize(ep, id) {
		return replyCode(codes.Forbidden)
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
		models:         &registrationModels{},
	}
	var old *Registration
	var replayed bool
	var previous *Registration
	if _, ok := s.store.(CommittedRegistrationPublisher); ok {
		previous, _ = s.store.ByEndpoint(ep)
	}
	if err := guardPeer(peer, func() error {
		if checked, ok := s.store.(ContextCheckedAdder); ok {
			var err error
			ctx := RegisterContext{Endpoint: ep, Identity: id, Fingerprint: registerFingerprint(ep, id, m)}
			ctx.Exchange = registerExchange(peer, m, ctx.Fingerprint)
			old, err = checked.AddCheckedContext(ctx, reg)
			if err == nil {
				if publisher, ok := s.store.(CommittedRegistrationPublisher); ok {
					publisher.PublishCommitted(reg)
				}
			}
			return err
		}
		if checked, ok := s.store.(CheckedAdder); ok {
			var err error
			old, err = checked.AddChecked(reg)
			if err == nil {
				if publisher, ok := s.store.(CommittedRegistrationPublisher); ok {
					publisher.PublishCommitted(reg)
				}
			}
			return err
		}
		if checked, ok := s.store.(*MemoryStore); ok && exchange != nil {
			ctx := RegisterContext{Endpoint: ep, Identity: id, Fingerprint: registerFingerprint(ep, id, m)}
			ctx.Exchange = registerExchange(peer, m, ctx.Fingerprint)
			var err error
			reg, old, replayed, err = checked.AddCheckedReplay(ctx, reg)
			if err == nil && !replayed {
				if publisher, ok := s.store.(CommittedRegistrationPublisher); ok {
					publisher.PublishCommitted(reg)
				}
			}
			return err
		}
		old = s.store.Add(reg)
		return nil
	}); err != nil {
		if errors.Is(err, ErrRegistrationUnavailable) {
			return replyCode(codes.ServiceUnavailable)
		}
		return replyCode(codes.Forbidden)
	}
	if replayed {
		return reply{msg: &Message{Code: codes.Created, Location: []string{"rd", reg.ID}}}
	}
	if exchange != nil {
		s.replayRemember(*exchange, reg)
	}
	if old != nil && previous != nil && old.ID == previous.ID {
		old = previous
	}
	if len(pids) > 0 && payload != nil {
		s.learnProfiles(pids, objs) // PROF-11: learn only from committed registrations
	}
	if old != nil {
		s.queues.handover(old.ID, reg.ID) // before dropClientState: queued requests follow the new registration (T24)
		s.dropClientState(old)
	}
	wasAsleep := s.queues.markAwake(reg) // awake at once; waiters go after the reply (GEN-10)
	return reply{
		msg: &Message{Code: codes.Created, Location: []string{"rd", reg.ID}},
		after: func() {
			_ = guardPeer(peer, func() error { s.emit(Registered{Registration: reg, Replaced: old}); return nil })
			s.queues.release(reg, wasAsleep)
		},
	}
}

// authenticateEndpoint resolves the endpoint name and checks it against the
// authenticated identity (REG-02, SEC-06, SEC-07). It returns a non-zero
// CoAP code to reject.
func (s *Server) authenticateEndpoint(ep string, id Identity) (string, codes.Code) {
	if ep == "" {
		derived, ok := endpointFromIdentity(s.security, id)
		if !ok {
			return "", codes.BadRequest // REG-02: ep needed without an authenticated identifier
		}
		ep = derived
	}
	si, has := s.security.ByEndpoint(ep)
	if id.Mode == ModeOSCORE {
		// The OSCORE layer verified the context and bound its endpoint.
		if has || id.OSCOREEndpoint == "" || id.OSCOREEndpoint != ep {
			return "", codes.BadRequest
		}
	} else if id.Secure() {
		if !has || !si.Matches(id) {
			return "", codes.BadRequest // SEC-06: ep does not match the authenticated identity
		}
	} else if has {
		return "", codes.BadRequest // SEC-07: credentials exist, NoSec is not this client
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
	unlock := s.registrationLock(reg.Endpoint)
	defer unlock()
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
	var updated bool
	if err := guardPeer(peerConn, func() error { updated = s.store.Update(reg); return nil }); err != nil {
		return replyCode(codes.Forbidden)
	}
	if !updated {
		return replyCode(codes.NotFound) // removed concurrently
	}
	wasAsleep := s.queues.markAwake(reg) // awake at once; waiters go after the reply (GEN-10)
	return reply{msg: status(codes.Changed), after: func() {
		_ = guardPeer(peerConn, func() error { s.emit(Updated{Registration: reg, Previous: *prev}); return nil })
		s.queues.release(reg, wasAsleep)
	}}
}

func (s *Server) deregister(reg *Registration, peer Identity) reply {
	unlock := s.registrationLock(reg.Endpoint)
	defer unlock()
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

// mergeObjects adds the objects and instances of b to a: every Profile ID
// and the payload together make up the client's list (PROF-07). b is
// never aliased, so cached profile lists stay intact.
func mergeObjects(a, b []link.Object) []link.Object {
	for _, o := range b {
		i := slices.IndexFunc(a, func(x link.Object) bool { return x.ID == o.ID })
		if i < 0 {
			a = append(a, link.Object{ID: o.ID, Version: o.Version, Instances: slices.Clone(o.Instances)})
			continue
		}
		for _, iid := range o.Instances {
			if !slices.Contains(a[i].Instances, iid) {
				a[i].Instances = append(slices.Clip(a[i].Instances), iid)
			}
		}
		if a[i].Version == "" {
			a[i].Version = o.Version
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
