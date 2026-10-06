package server

import (
	"errors"
	"fmt"
	"sync"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/model"
)

// Models types and validates client payloads with an object registry. It
// resolves each registered object's version from its "ver" attribute or
// the client's LwM2M version (VER-01) and caches one schema per
// registration. Use it as Config.Schema and Config.Validator:
//
//	m := server.NewModels(model.Default())
//	server.New(server.Config{Schema: m.Schema, Validator: m})
type Models struct {
	reg   *model.Registry
	cache sync.Map // registration ID -> cached
}

type cached struct {
	reg    *Registration
	schema *model.Schema
}

func NewModels(r *model.Registry) *Models { return &Models{reg: r} }

func (m *Models) schemaOf(reg *Registration) *model.Schema {
	if c, ok := m.cache.Load(reg.ID); ok && c.(cached).reg == reg {
		return c.(cached).schema
	}
	versions := map[uint16]model.Version{}
	for _, o := range reg.Objects {
		v, err := model.ResolveVersion(reg.Version, o.ID, o.Version)
		if err != nil {
			v = model.DefaultVersion(reg.Version, o.ID)
		}
		versions[o.ID] = v
	}
	s := m.reg.Schema(versions)
	// Registrations are immutable, so the pointer identifies the version of
	// the object list. ponytail: entries of ended registrations stay until
	// the ID is reused; add eviction if registrations churn heavily.
	m.cache.Store(reg.ID, cached{reg, s})
	return s
}

// Schema implements Config.Schema.
func (m *Models) Schema(reg *Registration) lwm2m.Schema { return m.schemaOf(reg) }

// CheckWrite implements Validator (DM-06): types, ranges and writability
// from the model, and Objlnk targets (DT-03).
func (m *Models) CheckWrite(reg *Registration, nodes []lwm2m.Node) error {
	if err := m.schemaOf(reg).CheckWrite(nodes); err != nil {
		return err
	}
	return checkLinks(reg, nodes)
}

// CheckCreate implements Validator (DM-09).
func (m *Models) CheckCreate(reg *Registration, object uint16, nodes []lwm2m.Node) error {
	if err := m.schemaOf(reg).CheckCreate(object, nodes); err != nil {
		return err
	}
	return checkLinks(reg, nodes)
}

// ErrBadObjlnk: an Objlnk value names neither the null link, a registered
// object (oid:65535) nor a registered instance (DT-03).
var ErrBadObjlnk = errors.New("server: Objlnk target not registered")

// checkLinks validates Objlnk targets against the client's object list.
// When the client listed no instances of an object, any instance of it is
// accepted (the list says nothing about which exist).
func checkLinks(reg *Registration, nodes []lwm2m.Node) error {
	for _, n := range nodes {
		if n.Kind == lwm2m.KindValue && n.Value.Type == lwm2m.TypeCorelnk {
			if err := checkCorelnk(reg, n); err != nil {
				return err
			}
			continue
		}
		if n.Kind != lwm2m.KindValue || n.Value.Type != lwm2m.TypeObjlnk {
			continue
		}
		l := n.Value.Link
		if l == lwm2m.NullObjLink {
			continue
		}
		o, ok := reg.Object(l.Object)
		if !ok {
			return fmt.Errorf("%w: %s -> %s", ErrBadObjlnk, n.Path, l)
		}
		if l.Instance != lwm2m.MaxID && len(o.Instances) > 0 && !reg.HasInstance(l.Object, l.Instance) {
			return fmt.Errorf("%w: %s -> %s", ErrBadObjlnk, n.Path, l)
		}
	}
	return nil
}

// checkCorelnk applies the Objlnk target rule to a Corelnk value: every
// link naming an LwM2M object or instance must name a registered one
// (DT-03, C §6.3.3). Links that are not LwM2M paths are left alone.
func checkCorelnk(reg *Registration, n lwm2m.Node) error {
	links, err := link.Parse(n.Value.Str)
	if err != nil {
		return nil // not an LwM2M object/instance link list (e.g. absolute URIs)
	}
	for _, l := range links {
		p, err := lwm2m.ParsePath(l.URI)
		if err != nil || p.IsRoot() {
			continue
		}
		o, ok := reg.Object(p.Object())
		if !ok || (p.Len() >= 2 && len(o.Instances) > 0 && !reg.HasInstance(p.Object(), p.Instance())) {
			return fmt.Errorf("%w: %s -> %s", ErrBadObjlnk, n.Path, l.URI)
		}
	}
	return nil
}
