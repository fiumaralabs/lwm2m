package server

import (
	"io"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// sendFormats are the Content-Formats Send may use (SEND-01).
var sendFormats = map[lwm2m.ContentFormat]bool{
	lwm2m.FormatSenMLJSON: true, lwm2m.FormatSenMLCBOR: true, lwm2m.FormatLwM2MCBOR: true,
}

// handleSend accepts a client Send on /dp (C §6.4.6).
func (s *Server) handleSend(w mux.ResponseWriter, m *mux.Message) {
	if m.Code() != codes.POST {
		reply(w, codes.MethodNotAllowed)
		return
	}
	cc, ok := w.Conn().(*client.Conn)
	if !ok {
		reply(w, codes.InternalServerError)
		return
	}
	reg, ok := s.registrationForConn(cc)
	if !ok {
		reply(w, codes.BadRequest) // Send from an unregistered client
		return
	}
	cf, err := m.ContentFormat()
	if err != nil || !sendFormats[lwm2m.ContentFormat(cf)] {
		reply(w, codes.BadRequest) // SEND-01: Content-Format is mandatory and limited
		return
	}
	var body []byte
	if m.Body() != nil {
		if body, err = io.ReadAll(m.Body()); err != nil {
			reply(w, codes.BadRequest)
			return
		}
	}
	c, err := codec.For(lwm2m.ContentFormat(cf))
	if err != nil {
		reply(w, codes.BadRequest)
		return
	}
	nodes, err := c.Decode(lwm2m.Root, body, s.schema(reg))
	if err != nil || len(nodes) == 0 {
		reply(w, codes.BadRequest)
		return
	}
	for _, n := range nodes {
		if !sendTargetRegistered(reg, n.Path) {
			reply(w, codes.NotFound) // SEND-02: object (instance) not registered
			return
		}
	}
	reply(w, codes.Changed)
	s.queues.wake(reg)
	s.emit(SendReceived{Registration: reg, ContentFormat: lwm2m.ContentFormat(cf), Nodes: nodes})
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

// registrationForConn finds the registration speaking on cc: the one bound
// to this connection, else the one whose authenticated identity matches.
func (s *Server) registrationForConn(cc *client.Conn) (*Registration, bool) {
	id := identityOf(cc.NetConn(), cc.RemoteAddr())
	for _, r := range s.store.All() {
		if r.conn == cc || (id.Secure() && r.Identity.Equal(id)) || (!id.Secure() && !r.Identity.Secure() && r.Identity.Addr == id.Addr) {
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
