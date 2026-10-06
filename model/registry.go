package model

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The core OMA objects 0-28 from the OMNA LwM2M registry (commit 7d5204dd),
// every file of version_history/. The registry root files for these IDs are
// byte-identical to the newest history file, so they are not duplicated.
// OMA BSD-3-Clause; see THIRD_PARTY_NOTICES.md.
//
//go:embed registry/*.xml
var coreFS embed.FS

// Registry holds object definitions by (ID, version). It is safe for
// concurrent use; the Objects it returns are immutable.
type Registry struct {
	mu   sync.RWMutex
	objs map[uint16]map[Version]*Object
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{objs: map[uint16]map[Version]*Object{}} }

// Default returns a new registry preloaded with the embedded core objects.
func Default() *Registry {
	r := NewRegistry()
	if err := r.LoadFS(coreFS); err != nil {
		panic(err) // embedded data is tested
	}
	return r
}

// CoreFS exposes the embedded registry files.
func CoreFS() fs.FS { return coreFS }

var objFileRE = regexp.MustCompile(`^\d+(-\d+_\d+)?\.xml$`)

// LoadFS loads every object file of fsys, recursively: names `<id>.xml` and
// `<id>-<maj>_<min>.xml`, the OMNA registry layout (so a full registry clone
// loads as is via os.DirFS). Other files (DDF.xml, Common.xml, ...) are skipped.
func (r *Registry) LoadFS(fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !objFileRE.MatchString(path.Base(p)) {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err == nil {
			_, err = r.Register(b)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		return nil
	})
}

// Register parses an object definition document (vendor or OMA) and adds its
// objects, replacing any definition with the same ID and version.
func (r *Registry) Register(xmlDoc []byte) ([]*Object, error) {
	objs, err := ParseXML(xmlDoc)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range objs {
		if r.objs[o.ID] == nil {
			r.objs[o.ID] = map[Version]*Object{}
		}
		r.objs[o.ID][o.Version] = o
	}
	return objs, nil
}

// Versions lists the known versions of an object, ascending.
func (r *Registry) Versions(id uint16) []Version {
	r.mu.RLock()
	defer r.mu.RUnlock()
	vs := make([]Version, 0, len(r.objs[id]))
	for v := range r.objs[id] {
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Less(vs[j]) })
	return vs
}

// Get returns the definition of object id at version v. Minor versions within
// a major are compatible (Core §7.2.2: type II changes only add optional
// items), so when v itself is unknown Get falls back to the highest known
// minor below v, else the lowest above it, within the same major (VER-02).
// The returned Object's Version says which one was picked. A different major
// never matches.
func (r *Registry) Get(id uint16, v Version) (*Object, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byVer := r.objs[id]
	if o, ok := byVer[v]; ok {
		return o, true
	}
	var below, above *Object
	for w, o := range byVer {
		switch {
		case w.Major != v.Major:
		case w.Less(v) && (below == nil || below.Version.Less(w)):
			below = o
		case v.Less(w) && (above == nil || w.Less(above.Version)):
			above = o
		}
	}
	if below != nil {
		return below, true
	}
	return above, above != nil
}

// coreVersions maps an enabler version to the object versions a client of that
// version implies by omitting `ver` (VER-01, Core §7.2.3). Objects not listed
// default to 1.0.
//   - 1.0: every object is 1.0 (1.0 has no object versioning, C 1.0 §6.2).
//   - 1.1: the spec docs carry no 1.1 table; this is Leshan's
//     LwM2mCoreObjectVersionRegistry for 1.1.1 (tie-breaker, README §1.4),
//     consistent with the registry's LWM2MVersion=1.1 files.
//   - 1.2: Core 1.2.2 App. E Table E-1 (VER-03), /5 as 1.1 per A-15.
var coreVersions = map[Version]map[uint16]Version{
	{1, 1}: {0: {1, 1}, 1: {1, 1}, 2: {1, 0}, 3: {1, 1}, 4: {1, 2}, 5: {1, 0}, 6: {1, 0}, 7: {1, 0}, 21: {1, 0}},
	{1, 2}: {0: {1, 2}, 1: {1, 2}, 2: {1, 1}, 3: {1, 2}, 4: {1, 3}, 5: {1, 1}, 6: {1, 0}, 7: {1, 0},
		21: {2, 0}, 23: {1, 0}, 24: {1, 0}, 25: {1, 0}, 26: {1, 0}, 27: {1, 0}},
}

// enabler reduces an `lwm2m=` value ("1.0", "1.1", "1.2", "1.2.1") to
// major.minor; ok=false when it is not a version.
func enabler(lwm2mVer string) (Version, bool) {
	parts := strings.Split(lwm2mVer, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, false
	}
	for _, p := range parts {
		if _, err := strconv.ParseUint(p, 10, 16); err != nil {
			return Version{}, false
		}
	}
	v, err := ParseVersion(parts[0] + "." + parts[1])
	return v, err == nil
}

// DefaultVersion is the version object id has when a client declaring
// `lwm2m=lwm2mVer` registers it without `ver` (VER-01, VER-03).
func DefaultVersion(lwm2mVer string, id uint16) Version {
	if e, ok := enabler(lwm2mVer); ok {
		if v, ok := coreVersions[e][id]; ok {
			return v
		}
	}
	return V1_0
}

// ResolveVersion returns an object's version from a Register/Update/Discover
// link: the explicit `ver` attribute when present, else DefaultVersion.
func ResolveVersion(lwm2mVer string, id uint16, ver string) (Version, error) {
	if ver == "" {
		return DefaultVersion(lwm2mVer, id), nil
	}
	return ParseVersion(ver)
}
