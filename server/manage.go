package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// serverInstance finds this server's /1 instance on the client: the one
// whose Short Server ID (/1/i/0) equals ssid, or the only instance when
// ssid is 0.
func (s *Server) serverInstance(ctx context.Context, ep string, ssid uint16) (uint16, error) {
	r, err := s.Read(ctx, ep, lwm2m.NewPath(1), ReadOptions{})
	if err != nil {
		return 0, err
	}
	if !r.Success() {
		return 0, fmt.Errorf("server: reading /1 failed: %s", CodeString(r.Code))
	}
	var insts []uint16
	for _, n := range r.Nodes {
		if n.Kind != lwm2m.KindValue || n.Path.Len() < 3 || n.Path.Resource() != 0 {
			continue
		}
		if ssid == 0 || n.Value.Int == int64(ssid) {
			insts = append(insts, n.Path.Instance())
		}
	}
	if len(insts) != 1 {
		return 0, fmt.Errorf("%w: cannot identify this server's /1 instance (ssid %d, %d candidates)", ErrBadRequest, ssid, len(insts))
	}
	return insts[0], nil
}

// TriggerUpdate executes the Registration Update Trigger /1/x/8 (REG-19).
// A non-empty binding asks the client to reconnect over that binding
// (GEN-15): the server only offers bindings listed in the client's /1/x/7,
// as argument 0='<binding>' (C §6.2.1.2; the argument syntax follows the
// Execute grammar of C §6.3.5).
func (s *Server) TriggerUpdate(ctx context.Context, ep string, binding string) (*Response, error) {
	inst, err := s.serverInstance(ctx, ep, s.cfg.ShortServerID)
	if err != nil {
		return nil, err
	}
	args := ""
	if binding != "" {
		r, err := s.Read(ctx, ep, lwm2m.NewPath(1, inst, 7), ReadOptions{})
		if err != nil {
			return nil, err
		}
		if !r.Success() || len(r.Nodes) != 1 {
			return nil, fmt.Errorf("server: cannot read /1/%d/7", inst)
		}
		offered := r.Nodes[0].Value.Str
		for _, b := range binding {
			if b != 'U' && !strings.ContainsRune(offered, b) { // U is always supported (GEN-05)
				return nil, fmt.Errorf("%w: binding %q not in /1/%d/7 %q (GEN-15)", ErrBadRequest, binding, inst, offered)
			}
		}
		args = "0='" + binding + "'"
	}
	return s.Execute(ctx, ep, lwm2m.NewPath(1, inst, 8), args)
}

// TriggerBootstrap executes the Bootstrap-Request Trigger /1/x/9
// (REG-19, BS-11): the client starts Client Initiated Bootstrap.
func (s *Server) TriggerBootstrap(ctx context.Context, ep string) (*Response, error) {
	inst, err := s.serverInstance(ctx, ep, s.cfg.ShortServerID)
	if err != nil {
		return nil, err
	}
	return s.Execute(ctx, ep, lwm2m.NewPath(1, inst, 9), "")
}

// Disable executes /1/x/4 (REG-24): the client de-registers and stays away
// for /1/x/5 seconds before registering again.
func (s *Server) Disable(ctx context.Context, ep string) (*Response, error) {
	inst, err := s.serverInstance(ctx, ep, s.cfg.ShortServerID)
	if err != nil {
		return nil, err
	}
	return s.Execute(ctx, ep, lwm2m.NewPath(1, inst, 4), "")
}

// writeServerResource writes one resource of this server's /1 instance.
func (s *Server) writeServerResource(ctx context.Context, ep string, rid uint16, nodes func(lwm2m.Path) []lwm2m.Node) (*Response, error) {
	inst, err := s.serverInstance(ctx, ep, s.cfg.ShortServerID)
	if err != nil {
		return nil, err
	}
	p := lwm2m.NewPath(1, inst, rid)
	return s.Write(ctx, ep, p, nodes(p), WriteOptions{})
}

// SetPreferredTransport writes /1/x/22 (GEN-14): the single binding the
// client uses to initiate its next connection, e.g. "U" for FOTA.
func (s *Server) SetPreferredTransport(ctx context.Context, ep, binding string) (*Response, error) {
	if len(binding) != 1 || !strings.Contains("UTSNMH", binding) {
		return nil, fmt.Errorf("%w: preferred transport is one binding letter", ErrBadRequest)
	}
	return s.writeServerResource(ctx, ep, 22, func(p lwm2m.Path) []lwm2m.Node {
		return []lwm2m.Node{lwm2m.ValueNode(p, lwm2m.String(binding))}
	})
}

// SetBinding writes /1/x/7 (REG-25). Per GEN-13 the change applies to the
// client's future transport sessions only.
func (s *Server) SetBinding(ctx context.Context, ep, binding string) (*Response, error) {
	return s.writeServerResource(ctx, ep, 7, func(p lwm2m.Path) []lwm2m.Node {
		return []lwm2m.Node{lwm2m.ValueNode(p, lwm2m.String(binding))}
	})
}

// SetLifetime writes the Lifetime /1/x/1 (REG-03): on 2.04 the value
// becomes the registration's lifetime at once, without waiting for the
// client's Update (C §6.2). 0 means no expiry (REG-23).
func (s *Server) SetLifetime(ctx context.Context, ep string, lifetime time.Duration) (*Response, error) {
	secs := int64(lifetime / time.Second)
	if secs < 0 || secs > 1<<32-1 {
		return nil, fmt.Errorf("%w: lifetime %v out of range", ErrBadRequest, lifetime)
	}
	r, err := s.writeServerResource(ctx, ep, 1, func(p lwm2m.Path) []lwm2m.Node {
		return []lwm2m.Node{lwm2m.ValueNode(p, lwm2m.Integer(secs))}
	})
	if err != nil || r.Code != codes.Changed {
		return r, err
	}
	if reg, ok := s.store.ByEndpoint(ep); ok {
		cp := *reg // stored registrations are immutable
		cp.Lifetime = seconds(uint32(secs))
		s.store.Update(&cp)
	}
	return r, nil
}

// SupportedVersions are the enabler versions this server implements,
// advertised in /1/x/25 (REG-26).
var SupportedVersions = []string{"1.0", "1.1", "1.2"}

// AdvertiseVersions writes SupportedVersions to /1/x/25 (REG-26).
func (s *Server) AdvertiseVersions(ctx context.Context, ep string) (*Response, error) {
	return s.writeServerResource(ctx, ep, 25, func(p lwm2m.Path) []lwm2m.Node {
		var out []lwm2m.Node
		for i, v := range SupportedVersions {
			out = append(out, lwm2m.ValueNode(p.Append(uint16(i)), lwm2m.String(v)))
		}
		return out
	})
}

// SetProfileHashAlgorithm writes /1/x/27 (PROF-10): the RFC 6920 suite the
// server prefers for dynamic Profile IDs.
func (s *Server) SetProfileHashAlgorithm(ctx context.Context, ep string, suite uint8) (*Response, error) {
	return s.writeServerResource(ctx, ep, 27, func(p lwm2m.Path) []lwm2m.Node {
		return []lwm2m.Node{lwm2m.ValueNode(p, lwm2m.Unsigned(uint64(suite)))}
	})
}

// ChangeObservationAttributes writes new attributes on an observed path
// and, for deterministic behaviour, cancels and re-creates the
// observation (ATT-12). It returns the new observation.
func (s *Server) ChangeObservationAttributes(ctx context.Context, ob *Observation, query []string) (*Observation, *Response, error) {
	if ob.Composite {
		return nil, nil, fmt.Errorf("%w: composite observations take attributes in the request (OBS-05)", ErrBadRequest)
	}
	if _, err := s.CancelObservation(ctx, ob, true); err != nil {
		return nil, nil, err
	}
	r, err := s.WriteAttributes(ctx, ob.Endpoint, ob.Paths[0], query)
	if err != nil {
		return nil, nil, err
	}
	if r.Code != codes.Changed {
		return nil, r, nil
	}
	return s.Observe(ctx, ob.Endpoint, ob.Paths[0], ObserveOptions{Accept: ob.Accept, Query: ob.Query})
}
