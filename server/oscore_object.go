package server

import (
	"errors"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/oscore"
)

// OSCOREParams reads the client's OSCORE input parameters from the nodes
// of one /21 instance (T §5.4.7.1, E.9): 0 Master Secret, 1 Sender ID and
// 2 Recipient ID are mandatory; 3 AEAD Algorithm (RFC 8152 Tbl 10),
// 4 HMAC Algorithm (Tbl 7), 5 Master Salt and 6 ID Context are optional.
// The TS stores the secrets "as an UTF-8 string" in Opaque resources; both
// encodings are taken as the same bytes (A-18). The server's own context
// is the result's Reverse.
func OSCOREParams(nodes []lwm2m.Node) (oscore.Params, error) {
	var p oscore.Params
	seen := map[uint16]bool{}
	for _, n := range nodes {
		if !n.Path.IsResource() || n.Path.Object() != 21 {
			continue
		}
		r := n.Path.Resource()
		seen[r] = true
		bytesOf := func() []byte {
			if n.Value.Type == lwm2m.TypeString {
				return []byte(n.Value.Str)
			}
			return append([]byte{}, n.Value.Bytes...)
		}
		switch r {
		case 0:
			p.MasterSecret = bytesOf()
		case 1:
			p.SenderID = bytesOf()
		case 2:
			p.RecipientID = bytesOf()
		case 3:
			p.AEAD = int(n.Value.Int)
		case 4:
			p.HKDF = int(n.Value.Int)
		case 5:
			p.MasterSalt = bytesOf()
		case 6:
			p.IDContext = bytesOf()
		}
	}
	for _, r := range []uint16{0, 1, 2} {
		if !seen[r] {
			return p, fmt.Errorf("server: /21 resource %d is mandatory (OSC-07)", r)
		}
	}
	if _, err := oscore.New(p); err != nil {
		return p, err
	}
	return p, nil
}

// ErrOSCORELink reports a /0/x/17 OSCORE Security Mode link that is
// invalid (OSC-07).
var ErrOSCORELink = errors.New("server: invalid OSCORE Security Mode link")

// CheckOSCORELinks validates the /0 and /21 nodes of a bootstrap
// configuration (T §5.4.7.1): each /0/x/17 links to an existing /21
// instance with valid parameters, and no /21 instance is linked from more
// than one /0 instance.
func CheckOSCORELinks(nodes []lwm2m.Node) error {
	inst := map[uint16][]lwm2m.Node{}
	for _, n := range nodes {
		if n.Path.Len() >= 2 && n.Path.Object() == 21 {
			inst[n.Path.Instance()] = append(inst[n.Path.Instance()], n)
		}
	}
	linkedBy := map[uint16]uint16{}
	for _, n := range nodes {
		if !n.Path.IsResource() || n.Path.Object() != 0 || n.Path.Resource() != 17 {
			continue
		}
		l := n.Value.Link
		if n.Value.Type != lwm2m.TypeObjlnk || l.Object != 21 {
			return fmt.Errorf("%w: %s does not link to /21", ErrOSCORELink, n.Path)
		}
		ns, ok := inst[l.Instance]
		if !ok {
			return fmt.Errorf("%w: %s links to missing /21/%d", ErrOSCORELink, n.Path, l.Instance)
		}
		if other, dup := linkedBy[l.Instance]; dup {
			return fmt.Errorf("%w: /21/%d is linked from /0/%d and /0/%d", ErrOSCORELink, l.Instance, other, n.Path.Instance())
		}
		linkedBy[l.Instance] = n.Path.Instance()
		if _, err := OSCOREParams(ns); err != nil {
			return err
		}
	}
	return nil
}
