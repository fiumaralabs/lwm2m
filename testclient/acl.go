package testclient

import (
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/acl"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// EnableAccessControl makes the client enforce /2 Access Control for the
// server with Short Server ID ssid, as a client with several servers does
// (C §8). The /2 instances are stored as ordinary values under /2.
func (c *Client) EnableAccessControl(ssid uint16, insts ...acl.Instance) {
	c.mu.Lock()
	c.aclSSID = ssid
	for _, a := range insts {
		for _, n := range a.Nodes() {
			c.setLocked(n.Path, n.Value)
		}
	}
	c.mu.Unlock()
}

func (c *Client) aclInstances() []acl.Instance {
	insts, _ := acl.FromNodes(c.Nodes(lwm2m.NewPath(2)))
	return insts
}

// operation maps a request to the access-controlled operation.
func operation(r Request, p lwm2m.Path) acl.Operation {
	switch {
	case r.Code == 5 && r.Observe != nil:
		return acl.OpObserveComposite
	case r.Code == 5:
		return acl.OpReadComposite
	case r.Code == 7:
		return acl.OpWriteComposite
	case r.Code == codes.GET && r.Accept != nil && *r.Accept == lwm2m.FormatLinkFormat:
		return acl.OpDiscover
	case r.Code == codes.GET && r.Observe != nil:
		return acl.OpObserve
	case r.Code == codes.GET:
		return acl.OpRead
	case r.Code == codes.PUT && r.Body == nil:
		return acl.OpWriteAttributes
	case r.Code == codes.DELETE:
		return acl.OpDelete
	case r.Code == codes.POST && p.IsObject():
		return acl.OpCreate
	case r.Code == codes.POST && p.IsResource():
		return acl.OpExecute
	}
	return acl.OpWrite
}

// checkAccess returns 4.01 when access control denies r (DM-15, DM-16).
// Only the Access Control Owner may change a /2 instance (DM-17).
func (c *Client) checkAccess(r Request) (codes.Code, bool) {
	c.mu.Lock()
	ssid := c.aclSSID
	c.mu.Unlock()
	if ssid == 0 {
		return 0, true
	}
	p, err := lwm2m.ParsePath(r.Path)
	if err != nil || p.IsRoot() {
		return 0, true
	}
	insts := c.aclInstances()
	op := operation(r, p)
	if p.Object() == 2 {
		if op == acl.OpRead || op == acl.OpObserve || op == acl.OpDiscover {
			return 0, true
		}
		for _, a := range insts {
			if p.Len() >= 2 && a.ID == p.Instance() && a.Owner == ssid {
				return 0, true
			}
		}
		return codes.Unauthorized, false
	}
	if !acl.Allowed(insts, ssid, op, p, false) {
		return codes.Unauthorized, false
	}
	return 0, true
}

// afterCreate records the /2 instance of a created object instance with
// its creator as owner (DM-17).
func (c *Client) afterCreate(inst lwm2m.Path) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aclSSID == 0 {
		return
	}
	id := uint16(0)
	for p := range c.instances {
		if p.Object() == 2 && p.Instance() >= id {
			id = p.Instance() + 1
		}
	}
	for _, n := range acl.ForCreated(id, inst.Object(), inst.Instance(), c.aclSSID).Nodes() {
		c.setLocked(n.Path, n.Value)
	}
}

// afterDelete removes the /2 instance of a deleted object instance (DM-17).
func (c *Client) afterDelete(inst lwm2m.Path) {
	if c.aclSSID == 0 {
		return
	}
	for _, a := range c.aclInstances() {
		if a.Object == inst.Object() && a.Instance == inst.Instance() {
			c.mu.Lock()
			ip := lwm2m.NewPath(2, a.ID)
			delete(c.instances, ip)
			for vp := range c.values {
				if vp.HasPrefix(ip) {
					delete(c.values, vp)
				}
			}
			c.mu.Unlock()
		}
	}
}

// readableNodes keeps, for an object-level Read or Observe under access
// control, only the instances the server may read (C §8.2.3, DM-13).
func (c *Client) readableNodes(p lwm2m.Path, nodes []lwm2m.Node) []lwm2m.Node {
	c.mu.Lock()
	ssid := c.aclSSID
	c.mu.Unlock()
	if ssid == 0 || !p.IsObject() || p.Object() == 2 {
		return nodes
	}
	insts := c.aclInstances()
	var out []lwm2m.Node
	for _, n := range nodes {
		if acl.Decide(insts, ssid, n.Path.Object(), n.Path.Instance(), false)&acl.Read != 0 {
			out = append(out, n)
		}
	}
	return out
}
