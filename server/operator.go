package server

import (
	"context"
	"io"

	"github.com/plgd-dev/go-coap/v3/message"
)

// Do sends a raw binding-neutral request to a registered client through
// the normal path: queue mode (QM-02) and the per-transmission timeout
// apply. Path is relative to the client root; the alternate path is added
// (GEN-08). Bindings and extensions (e.g. gateway prefixes) use it for
// requests the typed API does not cover.
func (s *Server) Do(ctx context.Context, ep string, req *Message) (*Message, error) {
	reg, err := s.lookup(ep)
	if err != nil {
		return nil, err
	}
	var out *Message
	err = s.queues.run(ctx, reg, func(cur *Registration) error {
		if cur.peer == nil {
			return ErrNotRegistered
		}
		tctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
		defer cancel()
		m := *req
		if m.Token == nil {
			tok, err := message.GetToken()
			if err != nil {
				return err
			}
			m.Token = tok
		}
		if cur.RootPath != "" {
			m.Path = cur.RootPath + m.Path
			if req.Path == "/" {
				m.Path = cur.RootPath
			}
		}
		r, err := cur.peer.Exchange(tctx, &m)
		out = r
		return err
	})
	return out, err
}

// Wake marks a queue-mode client as reachable now, for bindings that see
// client traffic the core does not (e.g. empty LoRaWAN uplinks), so queued
// requests are sent (QM-02, QM-03).
func (s *Server) Wake(ep string) {
	if reg, ok := s.store.ByEndpoint(ep); ok {
		s.queues.wake(reg)
	}
}

// RemoveRegistration removes a registration as an operator action: its
// observations and queued requests are dropped, its transport session is
// closed when the binding allows it, and a Deregistered event with
// ReasonRemoved is emitted.
func (s *Server) RemoveRegistration(ep string) bool {
	reg, ok := s.store.ByEndpoint(ep)
	if !ok {
		return false
	}
	if _, ok := s.store.Remove(reg.ID); !ok {
		return false
	}
	s.dropClientState(reg)
	if c, ok := reg.peer.(io.Closer); ok {
		_ = c.Close()
	}
	s.emit(Deregistered{Registration: reg, Reason: ReasonRemoved})
	return true
}

// RevokeSecurity removes an endpoint's credentials, and with them its
// registration and session: a compromised credential must not keep an
// authenticated session alive (leshan-tests "non-obvious" rules, T §5.2).
func (s *Server) RevokeSecurity(ep string) (SecurityInfo, bool) {
	si, ok := s.security.Remove(ep)
	s.RemoveRegistration(ep)
	return si, ok
}
