package lwm2m

import (
	"fmt"
	"sort"
	"strings"
)

// Kind distinguishes a value leaf from the empty-container markers a
// payload can carry.
type Kind uint8

const (
	KindValue         Kind = iota // a resource or resource-instance value
	KindEmptyInstance             // an object instance with no resources
	KindEmptyMultiple             // a multi-instance resource with no instances
)

// Node is one flattened entry of an LwM2M payload. Every codec decodes to and
// encodes from a list of Nodes:
//   - KindValue: Path is /o/i/r (single-instance) or /o/i/r/ri, Value is set.
//   - KindEmptyInstance: Path is /o/i.
//   - KindEmptyMultiple: Path is /o/i/r.
//
// Time is an optional SenML/JSON timestamp in seconds (absolute after base
// time resolution); HasTime says whether it is set.
//
// Prefix is the LwM2M Gateway end-device prefix (Gateway TS 1.1.1 §8.3.1,
// §9): "" for the client (the gateway) itself, else the device whose
// object Path addresses ("d01" for /d01/3/0/0).
type Node struct {
	Prefix  string
	Path    Path
	Kind    Kind
	Value   Value
	Time    float64
	HasTime bool
}

func ValueNode(p Path, v Value) Node { return Node{Path: p, Value: v} }

// PathString is the path with its prefix: "/3/0/0" or "/d01/3/0/0".
func (n Node) PathString() string { return PrefixedPath(n.Prefix, n.Path) }

func (n Node) String() string {
	switch n.Kind {
	case KindEmptyInstance:
		return n.PathString() + " (empty instance)"
	case KindEmptyMultiple:
		return n.PathString() + " (empty multiple)"
	}
	s := n.PathString() + "=" + n.Value.String() + ":" + n.Value.Type.String()
	if n.HasTime {
		s += fmt.Sprintf("@%g", n.Time)
	}
	return s
}

// Equal compares two nodes exactly.
func (n Node) Equal(m Node) bool {
	return n.Prefix == m.Prefix && n.Path == m.Path && n.Kind == m.Kind && n.HasTime == m.HasTime &&
		(!n.HasTime || n.Time == m.Time) && (n.Kind != KindValue || n.Value.Equal(m.Value))
}

// SortNodes orders nodes by prefix, then path, then by time (SenML may carry several
// timestamped values for one path), keeping input order otherwise.
func SortNodes(ns []Node) {
	sort.SliceStable(ns, func(i, j int) bool {
		if ns[i].Prefix != ns[j].Prefix {
			return ns[i].Prefix < ns[j].Prefix
		}
		if c := ns[i].Path.Compare(ns[j].Path); c != 0 {
			return c < 0
		}
		return ns[i].HasTime && ns[j].HasTime && ns[i].Time < ns[j].Time
	})
}

// NodesEqual reports whether a and b hold the same nodes, ignoring order.
func NodesEqual(a, b []Node) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]Node(nil), a...)
	y := append([]Node(nil), b...)
	SortNodes(x)
	SortNodes(y)
	for i := range x {
		if !x[i].Equal(y[i]) {
			return false
		}
	}
	return true
}

// FormatNodes renders nodes one per line, for test diffs.
func FormatNodes(ns []Node) string {
	var b strings.Builder
	for _, n := range ns {
		b.WriteString(n.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// ResourceDef is what a codec needs to know about a resource to decode
// untyped formats (TLV, plain text, opaque, CBOR) and to validate typed ones.
type ResourceDef struct {
	Type     Type
	Multiple bool
}

// Schema resolves resource definitions. Decoders call it with a resource
// path (/o/i/r); ok=false means the resource is unknown.
type Schema interface {
	Resource(p Path) (def ResourceDef, ok bool)
}

// SchemaFunc adapts a function to Schema.
type SchemaFunc func(Path) (ResourceDef, bool)

func (f SchemaFunc) Resource(p Path) (ResourceDef, bool) { return f(p) }
