package model

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"

	lwm2m "github.com/fiumaralabs/lwm2m"
)

// Schema is one client's object model: the definitions of the objects it
// registered, at the versions it registered. It implements lwm2m.Schema.
type Schema struct {
	objs map[uint16]*Object
}

var _ lwm2m.Schema = (*Schema)(nil)

// Schema builds a client schema from its object list (object ID -> version,
// as resolved with ResolveVersion). Objects with no known definition are
// left out, so their resources are unknown to codecs.
func (r *Registry) Schema(objects map[uint16]Version) *Schema {
	s := &Schema{objs: map[uint16]*Object{}}
	for id, v := range objects {
		if o, ok := r.Get(id, v); ok {
			s.objs[id] = o
		}
	}
	return s
}

// Object returns the client's definition of object id.
func (s *Schema) Object(id uint16) (*Object, bool) {
	o, ok := s.objs[id]
	return o, ok
}

// ResourceModel returns the full definition of the resource p points into
// (p is /o/i/r or /o/i/r/ri).
func (s *Schema) ResourceModel(p lwm2m.Path) (*Resource, bool) {
	if p.Len() < 3 {
		return nil, false
	}
	o, ok := s.objs[p.Object()]
	if !ok {
		return nil, false
	}
	return o.Resource(p.Resource())
}

// Resource implements lwm2m.Schema.
func (s *Schema) Resource(p lwm2m.Path) (lwm2m.ResourceDef, bool) {
	r, ok := s.ResourceModel(p)
	if !ok {
		return lwm2m.ResourceDef{}, false
	}
	return r.Def(), true
}

var (
	ErrUnknownObject   = errors.New("model: unknown object")
	ErrUnknownResource = errors.New("model: unknown resource")
	ErrNotWritable     = errors.New("model: resource not writable")
	ErrNotExecutable   = errors.New("model: resource not executable")
	ErrType            = errors.New("model: wrong value type")
	ErrRange           = errors.New("model: value out of range")
	ErrNotMultiple     = errors.New("model: resource is single-instance")
	ErrMissing         = errors.New("model: mandatory resource missing")
)

var rangeRE = regexp.MustCompile(`^(-?\d+(?:\.\d+)?)\.\.(-?\d+(?:\.\d+)?)(?i:\s*bytes?)?$`)

// CheckValue type-checks v against the resource (Core App. C, DT-02) and
// enforces its RangeEnumeration when it is the "a..b" form, inclusive; for
// String and Opaque the range is a length in octets (Core App. D.1).
// ponytail: other range forms ("0-999", "8 bit", enumerated lists, prose) are
// free text in the registry and not enforced; add parsers when a client
// trips on one.
func (r *Resource) CheckValue(v lwm2m.Value) error {
	if r.Type == lwm2m.TypeNone || v.Type != r.Type {
		return fmt.Errorf("%w: resource %d is %s, value is %s", ErrType, r.ID, r.Type, v.Type)
	}
	if v.Type == lwm2m.TypeString && !utf8.ValidString(v.Str) {
		return fmt.Errorf("%w: resource %d: string is not UTF-8", ErrType, r.ID)
	}
	m := rangeRE.FindStringSubmatch(r.Range)
	if m == nil {
		return nil
	}
	lo, _ := strconv.ParseFloat(m[1], 64)
	hi, _ := strconv.ParseFloat(m[2], 64)
	var x float64
	switch v.Type {
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		x = float64(v.Int)
	case lwm2m.TypeUnsigned:
		x = float64(v.Uint)
	case lwm2m.TypeFloat:
		x = v.Float
	case lwm2m.TypeString:
		x = float64(len(v.Str))
	case lwm2m.TypeOpaque:
		x = float64(len(v.Bytes))
	default:
		return nil
	}
	if !(x >= lo && x <= hi) {
		return fmt.Errorf("%w: resource %d: %s not in %s", ErrRange, r.ID, v, r.Range)
	}
	return nil
}

// checkNode validates one node's shape and value against the schema.
func (s *Schema) checkNode(n lwm2m.Node) (*Resource, error) {
	if n.Path.Len() < 2 {
		return nil, fmt.Errorf("model: %s: not an instance or resource path", n.Path)
	}
	if _, ok := s.objs[n.Path.Object()]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownObject, n.Path)
	}
	if n.Kind == lwm2m.KindEmptyInstance {
		return nil, nil
	}
	r, ok := s.ResourceModel(n.Path)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownResource, n.Path)
	}
	if (n.Path.IsResourceInstance() || n.Kind == lwm2m.KindEmptyMultiple) && !r.Multiple {
		return nil, fmt.Errorf("%w: %s", ErrNotMultiple, n.Path)
	}
	if n.Kind == lwm2m.KindValue {
		if r.Multiple && !n.Path.IsResourceInstance() {
			return nil, fmt.Errorf("model: %s: multi-instance resource needs a resource-instance path", n.Path)
		}
		if err := r.CheckValue(n.Value); err != nil {
			return nil, fmt.Errorf("%s: %w", n.Path, err)
		}
	}
	return r, nil
}

// CheckWrite validates a Write or Write-Composite payload before it is sent
// (DM-06): every resource is known, writable, of the right type and range,
// and resource-instance paths only target multi-instance resources.
func (s *Schema) CheckWrite(nodes []lwm2m.Node) error {
	for _, n := range nodes {
		r, err := s.checkNode(n)
		if err != nil {
			return err
		}
		if r != nil && !r.Writable() {
			return fmt.Errorf("%w: %s", ErrNotWritable, n.Path)
		}
	}
	return nil
}

// CheckCreate validates a Create payload for object obj (DM-09): all nodes
// belong to obj and type-check, and every mandatory writable resource is
// present. Read-only values are allowed (the client ignores them, DM-09);
// mandatory read-only and executable resources are the client's to create.
func (s *Schema) CheckCreate(obj uint16, nodes []lwm2m.Node) error {
	o, ok := s.objs[obj]
	if !ok {
		return fmt.Errorf("%w: /%d", ErrUnknownObject, obj)
	}
	seen := map[uint16]bool{}
	for _, n := range nodes {
		if n.Path.Object() != obj {
			return fmt.Errorf("model: %s: not in object /%d", n.Path, obj)
		}
		r, err := s.checkNode(n)
		if err != nil {
			return err
		}
		if r != nil {
			seen[r.ID] = true
		}
	}
	for _, r := range o.Resources {
		if r.Mandatory && r.Writable() && !seen[r.ID] {
			return fmt.Errorf("%w: /%d/%d %s", ErrMissing, obj, r.ID, r.Name)
		}
	}
	return nil
}

// CheckExecute validates an Execute target: a known executable resource
// (Core §6.3.5; an executable resource is Single, so p is /o/i/r).
func (s *Schema) CheckExecute(p lwm2m.Path) error {
	if !p.IsResource() {
		return fmt.Errorf("model: execute %s: not a resource path", p)
	}
	r, ok := s.ResourceModel(p)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownResource, p)
	}
	if !r.Executable() {
		return fmt.Errorf("%w: %s", ErrNotExecutable, p)
	}
	return nil
}
