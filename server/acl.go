package server

import (
	"context"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/acl"
)

// ReadACL reads the client's Access Control objects /2 (C §8, E.3).
func (s *Server) ReadACL(ctx context.Context, ep string) ([]acl.Instance, error) {
	r, err := s.Read(ctx, ep, lwm2m.NewPath(2), ReadOptions{})
	if err != nil {
		return nil, err
	}
	if !r.Success() {
		return nil, fmt.Errorf("server: reading /2: %s", CodeString(r.Code))
	}
	return acl.FromNodes(r.Nodes)
}

// SetRights sets ACL[ssid] of the /2 instance aclID. Only that instance's
// Access Control Owner may do so; the client answers 4.01 otherwise
// (DM-14, DM-17). The write is a Partial Update of the multi-instance
// ACL resource, so other servers' entries are kept.
func (s *Server) SetRights(ctx context.Context, ep string, aclID, ssid uint16, r acl.Rights) (*Response, error) {
	if !r.Valid() || ssid == lwm2m.MaxID {
		return nil, fmt.Errorf("%w: rights %d for SSID %d (DM-18)", ErrBadRequest, r, ssid)
	}
	p := lwm2m.NewPath(2, aclID, 2)
	return s.Write(ctx, ep, p, []lwm2m.Node{lwm2m.ValueNode(p.Append(ssid), lwm2m.Integer(int64(r)))}, WriteOptions{Mode: PartialUpdate})
}
