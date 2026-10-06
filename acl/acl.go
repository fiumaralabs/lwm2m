// Package acl models LwM2M Access Control (Core §8, object /2, E.3): the
// rights an ACL grants, which right each operation needs, and how a
// client resolves a server's rights on an object instance.
package acl

import (
	"fmt"

	"github.com/fiumaralabs/lwm2m"
)

// Rights is the ACL bitmask of /2/x/2 (E.3 Tbl E.3-2).
type Rights uint8

const (
	Read    Rights = 1 << 0 // Read, Observe, Write-Attributes
	Write   Rights = 1 << 1
	Execute Rights = 1 << 2
	Delete  Rights = 1 << 3
	Create  Rights = 1 << 4
	All     Rights = Read | Write | Execute | Delete | Create
)

// Valid reports whether only defined bits are set (ACL 0..31, DM-18).
func (r Rights) Valid() bool { return r&^All == 0 }

func (r Rights) String() string {
	s := ""
	for _, b := range []struct {
		bit Rights
		c   byte
	}{{Read, 'R'}, {Write, 'W'}, {Execute, 'E'}, {Delete, 'D'}, {Create, 'C'}} {
		if r&b.bit != 0 {
			s += string(b.c)
		}
	}
	if s == "" {
		return "-"
	}
	return s
}

// Operation is a DM/IR operation subject to access control.
type Operation uint8

const (
	OpRead Operation = iota
	OpReadComposite
	OpObserve
	OpObserveComposite
	OpWriteAttributes
	OpWrite
	OpWriteComposite
	OpDiscover
	OpDelete
	OpExecute
	OpCreate
)

// Required returns the right an operation needs (C §8.2 Tbl 8.2-1, DM-15).
// Discover needs none (ok=false).
func Required(op Operation) (Rights, bool) {
	switch op {
	case OpRead, OpReadComposite, OpObserve, OpObserveComposite, OpWriteAttributes:
		return Read, true
	case OpWrite, OpWriteComposite:
		return Write, true
	case OpDelete:
		return Delete, true
	case OpExecute:
		return Execute, true
	case OpCreate:
		return Create, true
	}
	return 0, false
}

// BootstrapOwner is the Access Control Owner value meaning the instance is
// managed by the Bootstrap-Server only (E.3, DM-18).
const BootstrapOwner = 65535

// Instance is one /2 Access Control object instance (E.3).
type Instance struct {
	ID       uint16
	Object   uint16            // /2/x/0, 1..65534
	Instance uint16            // /2/x/1, 0..65535 (65535: the object, for Create)
	ACL      map[uint16]Rights // /2/x/2: SSID -> rights; SSID 0 is the default
	Owner    uint16            // /2/x/3: SSID of the Access Control Owner
}

// Validate checks the ranges of E.3 (DM-18).
func (a Instance) Validate() error {
	if a.Object == 0 || a.Object == lwm2m.MaxID {
		return fmt.Errorf("acl: object ID %d out of 1..65534", a.Object)
	}
	for ssid, r := range a.ACL {
		if !r.Valid() {
			return fmt.Errorf("acl: rights %d for SSID %d out of 0..31", r, ssid)
		}
		if ssid == lwm2m.MaxID {
			return fmt.Errorf("acl: SSID 65535 is reserved")
		}
	}
	if a.Owner == 0 {
		return fmt.Errorf("acl: owner 0 is not a valid SSID")
	}
	return nil
}

// Nodes encodes the instance as /2/<ID> resources.
func (a Instance) Nodes() []lwm2m.Node {
	base := lwm2m.NewPath(2, a.ID)
	out := []lwm2m.Node{
		lwm2m.ValueNode(base.Append(0), lwm2m.Integer(int64(a.Object))),
		lwm2m.ValueNode(base.Append(1), lwm2m.Integer(int64(a.Instance))),
		lwm2m.ValueNode(base.Append(3), lwm2m.Integer(int64(a.Owner))),
	}
	for ssid, r := range a.ACL {
		out = append(out, lwm2m.ValueNode(base.Append(2).Append(ssid), lwm2m.Integer(int64(r))))
	}
	lwm2m.SortNodes(out)
	return out
}

// FromNodes decodes /2 instances from Read results.
func FromNodes(nodes []lwm2m.Node) ([]Instance, error) {
	byID := map[uint16]*Instance{}
	var order []uint16
	for _, n := range nodes {
		if n.Kind != lwm2m.KindValue || n.Path.Len() < 3 || n.Path.Object() != 2 {
			continue
		}
		id := n.Path.Instance()
		a, ok := byID[id]
		if !ok {
			a = &Instance{ID: id, ACL: map[uint16]Rights{}}
			byID[id] = a
			order = append(order, id)
		}
		v := n.Value.Int
		if n.Value.Type == lwm2m.TypeUnsigned {
			v = int64(n.Value.Uint)
		}
		switch n.Path.Resource() {
		case 0:
			a.Object = uint16(v)
		case 1:
			a.Instance = uint16(v)
		case 2:
			if !n.Path.IsResourceInstance() {
				return nil, fmt.Errorf("acl: /2/%d/2 must be multi-instance", id)
			}
			a.ACL[n.Path.ResourceInstance()] = Rights(v)
		case 3:
			a.Owner = uint16(v)
		}
	}
	out := make([]Instance, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// Decide resolves the rights of server ssid on object instance (oid, iid)
// per C §8.2.1 (DM-16), in order: the server's own ACL entry; full rights
// if it is the Access Control Owner without its own entry; the default
// entry (SSID 0); otherwise none. With no /2 instance for the target
// nothing is granted. single reports a deployment with one server, where
// access control is not enabled and everything is allowed.
func Decide(insts []Instance, ssid, oid, iid uint16, single bool) Rights {
	if single {
		return All
	}
	for _, a := range insts {
		if a.Object != oid || a.Instance != iid {
			continue
		}
		if r, ok := a.ACL[ssid]; ok {
			return r
		}
		if a.Owner == ssid {
			return All
		}
		return a.ACL[0]
	}
	return 0
}

// Allowed reports whether op on path p is permitted for ssid (DM-13,
// DM-15). Discover needs no right; at object level Write-Attributes is
// always performed and Create needs C on the object's /2 instance
// (instance ID 65535); Read and Observe on an object are decided per
// instance by the caller (see Readable).
func Allowed(insts []Instance, ssid uint16, op Operation, p lwm2m.Path, single bool) bool {
	need, ok := Required(op)
	if !ok || p.IsRoot() {
		return true
	}
	if p.IsObject() {
		switch op {
		case OpWriteAttributes:
			return true
		case OpCreate:
			return Decide(insts, ssid, p.Object(), lwm2m.MaxID, single)&Create != 0
		}
		return true // object-level Read/Observe: performed, the caller keeps the Readable instances (C §8.2.3)
	}
	return Decide(insts, ssid, p.Object(), p.Instance(), single)&need != 0
}

// Readable filters instance paths to those ssid may read: object-level
// Read/Observe and Read-/Observe-Composite skip the others (DM-13, DM-16).
func Readable(insts []Instance, ssid uint16, paths []lwm2m.Path, single bool) []lwm2m.Path {
	var out []lwm2m.Path
	for _, p := range paths {
		if p.Len() < 2 || Decide(insts, ssid, p.Object(), p.Instance(), single)&Read != 0 {
			out = append(out, p)
		}
	}
	return out
}

// WritableAll reports whether ssid may write every target of a
// Write-Composite; if one is not writable nothing is granted (DM-16).
func WritableAll(insts []Instance, ssid uint16, paths []lwm2m.Path, single bool) bool {
	for _, p := range paths {
		if p.Len() < 2 || Decide(insts, ssid, p.Object(), p.Instance(), single)&Write == 0 {
			return false
		}
	}
	return true
}

// ForCreated is the /2 instance a client creates when server ssid creates
// object instance (oid, iid) in an access-controlled deployment: owner is
// that server, with full rights (C §6.3.6, §8.1.2.2, DM-17).
func ForCreated(id, oid, iid, ssid uint16) Instance {
	return Instance{ID: id, Object: oid, Instance: iid, Owner: ssid, ACL: map[uint16]Rights{ssid: All}}
}

// Unbootstrap returns the owner to hand an instance to when the server
// removedSSID is unbootstrapped: the remaining server with the highest
// sum of Write and Delete rights (C §8.1.2.2 / T §5.2.5, DM-19). ok=false
// when no other server has rights, so the instance is deleted.
func Unbootstrap(a Instance, removedSSID uint16) (owner uint16, ok bool) {
	best, score := uint16(0), -1
	for ssid, r := range a.ACL {
		if ssid == 0 || ssid == removedSSID {
			continue
		}
		s := 0
		if r&Write != 0 {
			s++
		}
		if r&Delete != 0 {
			s++
		}
		if s > score || (s == score && ssid < best) {
			best, score = ssid, s
		}
	}
	return best, score >= 0
}
