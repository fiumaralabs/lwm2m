package server

import (
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// sendFormats are the Content-Formats Send may use (SEND-01).
var sendFormats = map[lwm2m.ContentFormat]bool{
	lwm2m.FormatSenMLJSON: true, lwm2m.FormatSenMLCBOR: true, lwm2m.FormatLwM2MCBOR: true,
}

// send accepts a client Send on /dp (C §6.4.6).
func (s *Server) send(peer Peer, m *Message) reply {
	reg, ok := s.registrationForPeer(peer)
	if !ok {
		return replyCode(codes.BadRequest) // Send from an unregistered client
	}
	if m.Format == nil || !sendFormats[*m.Format] {
		return replyCode(codes.BadRequest) // SEND-01: Content-Format is mandatory and limited
	}
	cf := *m.Format
	c, err := codec.For(cf)
	if err != nil {
		return replyCode(codes.BadRequest)
	}
	nodes, err := c.Decode(lwm2m.Root, m.Payload, s.schema(reg))
	if err != nil || len(nodes) == 0 {
		return replyCode(codes.BadRequest)
	}
	for _, n := range nodes {
		if !sendTargetRegistered(reg, n.Path) {
			return replyCode(codes.NotFound) // SEND-02: object (instance) not registered
		}
	}
	return reply{msg: status(codes.Changed), after: func() {
		s.queues.wake(reg)
		s.emit(SendReceived{Registration: reg, ContentFormat: cf, Nodes: nodes})
	}}
}

// sendTargetRegistered reports whether a Send node belongs to a registered
// object, and to a registered instance when the client listed instances.
func sendTargetRegistered(reg *Registration, p lwm2m.Path) bool {
	if p.Len() == 0 {
		return false
	}
	o, ok := reg.Object(p.Object())
	if !ok {
		return false
	}
	return p.Len() < 2 || len(o.Instances) == 0 || reg.HasInstance(p.Object(), p.Instance())
}

// registrationForPeer finds the registration speaking on peer: the one
// bound to this session, else the one with the same authenticated
// identity (or, for NoSec, the same address).
func (s *Server) registrationForPeer(peer Peer) (*Registration, bool) {
	id := peer.Identity()
	for _, r := range s.store.All() {
		if r.peer == peer || r.Identity.Equal(id) {
			return r, true
		}
	}
	return nil, false
}

func (s *Server) rememberFormat(reg *Registration, cf lwm2m.ContentFormat, single bool) {
	if single && (cf == lwm2m.FormatText || cf == lwm2m.FormatOpaque) {
		return
	}
	s.formats.Store(reg.ID, cf)
}

func (s *Server) learnedFormat(reg *Registration) (lwm2m.ContentFormat, bool) {
	v, ok := s.formats.Load(reg.ID)
	if !ok {
		return 0, false
	}
	return v.(lwm2m.ContentFormat), true
}
