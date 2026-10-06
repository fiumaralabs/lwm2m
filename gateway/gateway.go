// Package gateway is the server side of the LwM2M Gateway enabler
// (OMA-TS-LWM2M_Gateway-V1_1_1, Core 1.2.2 E.12/E.13): the end-device
// registry kept in a gateway's /25 instances, prefix paths that address
// end-device objects, prefixed SenML and LwM2M CBOR payloads, and the
// Device Management, Information Reporting and Send rules for them.
package gateway

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
)

// Object IDs of the gateway objects.
const (
	ObjectGateway = 25
	ObjectRouting = 26
)

// Node is a payload node of the gateway itself (Prefix "") or of the end
// device with that prefix.
type Node = lwm2m.Node

// Path addresses a gateway object (Prefix "") or an object of the end
// device with Prefix (GW §8.3.1).
type Path struct {
	Prefix string
	lwm2m.Path
}

var ErrPath = lwm2m.ErrInvalidPath

// ParsePath parses "/3/0" (gateway) or "/d01/3303/0" (end device). A
// prefix is a first segment that is not a decimal ID.
func ParsePath(s string) (Path, error) {
	pre, p, err := lwm2m.ParsePrefixedPath(s)
	return Path{pre, p}, err
}

// ValidPrefix accepts a prefix usable as one URI path segment that cannot
// be mistaken for an object ID.
func ValidPrefix(p string) error { return lwm2m.ValidPrefix(p) }

func (p Path) String() string { return lwm2m.PrefixedPath(p.Prefix, p.Path) }

// URI is the request path: the prefix treated as an alternate path, after
// the gateway's own alternate path root when it has one (GW §8.3.1).
func (p Path) URI(root string) string {
	root = strings.TrimSuffix(root, "/")
	if p.Prefix == "" && p.IsRoot() {
		if root == "" {
			return "/"
		}
		return root
	}
	return root + p.String()
}

// Device is one IoT device behind the gateway: one /25 instance (GW §6).
type Device struct {
	Instance uint16
	ID       string        // /25/x/0 Device ID
	Prefix   string        // /25/x/1
	Objects  []link.Object // v2.0: /25/x/3; v1.0: from the /26 routing entry
	Mapping  string        // v1.0: /26/y/1 Mapping Info ("" = identity)
}

// Registry is the set of devices a gateway exposes.
type Registry struct{ Devices []Device }

// ByPrefix returns the device with prefix p.
func (r Registry) ByPrefix(p string) (Device, bool) {
	for _, d := range r.Devices {
		if d.Prefix == p {
			return d, true
		}
	}
	return Device{}, false
}

// Version returns the /25 object version of a registration: "ver" from its
// object list, else 1.0, the Core E.12 definition (GW-01).
func Version(reg *server.Registration) string {
	if o, ok := reg.Object(ObjectGateway); ok && o.Version != "" {
		return o.Version
	}
	return "1.0"
}

// Instances lists the /25 instances of a registration: one per device
// (GW §8.2). Device objects themselves are never in the object list.
func Instances(reg *server.Registration) []uint16 {
	o, _ := reg.Object(ObjectGateway)
	return o.Instances
}

// Changed reports whether an Update changed the /25 instances, so the
// registry must be read again (GW §8.2).
func Changed(prev, cur *server.Registration) bool {
	a, b := slices.Clone(Instances(prev)), slices.Clone(Instances(cur))
	slices.Sort(a)
	slices.Sort(b)
	return !slices.Equal(a, b)
}

// ParseDevices builds the registry from a Read of /25 (and, for /25 v1.0,
// of /26). version is the /25 object version (Version). v2.0 (GW TS 1.1)
// lists device objects in resource 3; v1.0 (Core E.12) links resource 2 to
// a /26 routing entry whose Object ID the device exposes. Device IDs and
// prefixes must be unique (GW §6).
func ParseDevices(version string, nodes []lwm2m.Node) (Registry, error) {
	v2 := strings.HasPrefix(version, "2.")
	res := map[lwm2m.Path]lwm2m.Value{}
	var insts []uint16
	for _, n := range nodes {
		if n.Path.Len() < 2 || n.Path.Object() != ObjectGateway && n.Path.Object() != ObjectRouting {
			continue
		}
		if n.Path.Object() == ObjectGateway && !slices.Contains(insts, n.Path.Instance()) {
			insts = append(insts, n.Path.Instance())
		}
		if n.Kind == lwm2m.KindValue {
			res[n.Path.Truncate(3)] = n.Value
		}
	}
	var r Registry
	seenID, seenPrefix := map[string]bool{}, map[string]bool{}
	for _, i := range insts {
		get := func(o, i, rid uint16) (lwm2m.Value, bool) { v, ok := res[lwm2m.NewPath(o, i, rid)]; return v, ok }
		id, ok1 := get(ObjectGateway, i, 0)
		pre, ok2 := get(ObjectGateway, i, 1)
		if !ok1 || !ok2 || id.Str == "" {
			return Registry{}, fmt.Errorf("gateway: /25/%d lacks Device ID or Prefix", i)
		}
		if err := ValidPrefix(pre.Str); err != nil {
			return Registry{}, fmt.Errorf("gateway: /25/%d: %w", i, err)
		}
		if seenID[id.Str] || seenPrefix[pre.Str] {
			return Registry{}, fmt.Errorf("gateway: /25/%d: Device ID %q or Prefix %q not unique", i, id.Str, pre.Str)
		}
		seenID[id.Str], seenPrefix[pre.Str] = true, true
		d := Device{Instance: i, ID: id.Str, Prefix: pre.Str}
		if v2 {
			if l, ok := get(ObjectGateway, i, 3); ok && l.Str != "" {
				links, err := link.Parse(l.Str)
				if err != nil {
					return Registry{}, fmt.Errorf("gateway: /25/%d/3: %w", i, err)
				}
				lr, err := link.ParseRegistration(links)
				if err != nil {
					return Registry{}, fmt.Errorf("gateway: /25/%d/3: %w", i, err)
				}
				d.Objects = lr.Objects
			}
		} else if l, ok := get(ObjectGateway, i, 2); ok && l.Type == lwm2m.TypeObjlnk && l.Link.Object == ObjectRouting {
			oid, ok := get(ObjectRouting, l.Link.Instance, 0)
			if !ok {
				return Registry{}, fmt.Errorf("gateway: /25/%d/2 links to /26/%d, not read", i, l.Link.Instance)
			}
			id := oid.Uint
			if oid.Type == lwm2m.TypeInteger {
				id = uint64(oid.Int)
			}
			d.Objects = []link.Object{{ID: uint16(id)}}
			m, _ := get(ObjectRouting, l.Link.Instance, 1)
			d.Mapping = m.Str
		}
		r.Devices = append(r.Devices, d)
	}
	return r, nil
}

var (
	ErrUnknownPrefix = errors.New("gateway: unknown end-device prefix")
	ErrBootstrap     = errors.New("gateway: bootstrap must not touch device objects")
)

// CheckBootstrap refuses a Bootstrap-Discover, -Read, -Write or -Delete
// target, or Bootstrap Information (a Bootstrap-Pack included), that
// involves device objects (GW §8.1).
func CheckBootstrap(targets []Path, nodes []Node) error {
	for _, p := range targets {
		if p.Prefix != "" {
			return fmt.Errorf("%w: %s", ErrBootstrap, p)
		}
	}
	for _, n := range nodes {
		if n.Prefix != "" {
			return fmt.Errorf("%w: %s", ErrBootstrap, n.PathString())
		}
	}
	return nil
}

// SendStatus checks a Send (GW §8.3.3): gateway nodes must belong to
// registered object instances (SEND-02); device nodes may be any object of
// a known device, registered or not. An error means 4.04.
func SendStatus(reg *server.Registration, devs Registry, nodes []Node) error {
	for _, n := range nodes {
		if n.Prefix != "" {
			if _, ok := devs.ByPrefix(n.Prefix); !ok {
				return fmt.Errorf("%w: %q", ErrUnknownPrefix, n.Prefix)
			}
			continue
		}
		o, ok := reg.Object(n.Path.Object())
		if !ok || n.Path.Len() >= 2 && len(o.Instances) > 0 && !reg.HasInstance(n.Path.Object(), n.Path.Instance()) {
			return fmt.Errorf("gateway: %s not registered", n.Path)
		}
	}
	return nil
}
