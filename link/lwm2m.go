package link

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/attr"
)

// Attrs returns the link's LwM2M attributes, typed (Core Tbl 7.3.1-1,
// 7.3.2-1). Parameters that are not LwM2M attributes (rt, ct, obs, ...) are
// ignored (REG-07). A malformed or valueless LwM2M attribute is an error.
func (l Link) Attrs() (attr.Attrs, error) {
	var out attr.Attrs
	for _, p := range l.Params {
		a, known, err := attr.ParseLinkParam(p.Name, p.Value, p.HasValue)
		if err != nil {
			return nil, fmt.Errorf("link <%s>: %w", l.URI, err)
		}
		if known {
			out = append(out, a)
		}
	}
	return out, nil
}

// Object is one object of a Register/Update object list.
type Object struct {
	ID uint16
	// Version is the "ver" attribute, "" when absent: the version implied
	// by the client's LwM2M version, or 1.0 for non-core objects (T14).
	Version   string
	Instances []uint16
}

// Registration is the interpreted Register/Update payload (Core §6.2.1).
type Registration struct {
	// Root is the alternate path from the link with rt="oma.lwm2m"
	// (T §6.4.1), "/" by default (T11).
	Root string
	// ContentFormats are the optional formats from "ct" on the root link,
	// as one value or a quoted space-separated list (REG-10, T12). nil when
	// absent.
	ContentFormats []lwm2m.ContentFormat
	Objects        []Object
}

// excluded objects never belong to the object list: Security, OSCORE,
// COSE (REG-09, T15) and MQTT Server (A-17). We drop them silently.
var excluded = []uint16{0, 21, 23, 24}

// ParseRegistration interprets a Register or Update object list.
// Unknown objects are kept (REG-08). Links that are not object or instance
// paths, and unknown parameters, are ignored (REG-07). One trailing slash
// is stripped (A-20). Object links outside the alternate path are taken
// as-is (T16). Duplicates merge.
func ParseRegistration(links []Link) (Registration, error) {
	r := Registration{Root: "/"}
	rootIdx := -1
	for i, l := range links {
		if rt, ok := l.Param("rt"); ok && slices.Contains(strings.Fields(rt.Value), "oma.lwm2m") {
			r.Root, rootIdx = trimSlash(l.URI), i
			break
		}
	}
	idx := map[uint16]int{}
	for i, l := range links {
		uri := stripRoot(trimSlash(l.URI), r.Root)
		if i == rootIdx || uri == "/" {
			if ct, ok := l.Param("ct"); ok {
				cfs, err := ParseContentFormats(ct.Value)
				if err != nil {
					return Registration{}, err
				}
				r.ContentFormats = append(r.ContentFormats, cfs...)
			}
			continue
		}
		p, err := lwm2m.ParsePath(uri)
		if err != nil || p.Len() > 2 || slices.Contains(excluded, p.Object()) {
			continue
		}
		j, ok := idx[p.Object()]
		if !ok {
			j = len(r.Objects)
			idx[p.Object()] = j
			r.Objects = append(r.Objects, Object{ID: p.Object()})
		}
		o := &r.Objects[j]
		if p.IsInstance() && !slices.Contains(o.Instances, p.Instance()) {
			o.Instances = append(o.Instances, p.Instance())
		}
		if v, ok := l.Param("ver"); ok {
			// ver may sit on the instance link of a single-instance object
			// (Core §7.2.2 example).
			if _, err := attr.ParseValue("ver", v.Value); err != nil {
				return Registration{}, err
			}
			o.Version = v.Value
		}
	}
	return r, nil
}

// ParseContentFormats splits a ct value: "110" or "110 112 60" (RFC 7252
// §7.2.1, REG-10, T12).
func ParseContentFormats(v string) ([]lwm2m.ContentFormat, error) {
	var out []lwm2m.ContentFormat
	for _, f := range strings.Fields(v) {
		n, err := strconv.ParseUint(f, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("%w: ct %q", ErrSyntax, v)
		}
		out = append(out, lwm2m.ContentFormat(n))
	}
	return out, nil
}

func trimSlash(u string) string {
	if len(u) > 1 {
		return strings.TrimSuffix(u, "/")
	}
	return u
}

// stripRoot removes the alternate path root from uri ("/lwm2m/3/0" ->
// "/3/0", "/lwm2m" -> "/"); other URIs are returned unchanged.
func stripRoot(uri, root string) string {
	if root == "" || root == "/" {
		return uri
	}
	if uri == root {
		return "/"
	}
	if strings.HasPrefix(uri, root+"/") {
		return uri[len(root):]
	}
	return uri
}

// Entry is one link of a Discover or Bootstrap-Discover result.
type Entry struct {
	Path  lwm2m.Path
	Attrs attr.Attrs
}

// ParseDiscover interprets a Discover (Core §6.3.2, DM-04) or
// Bootstrap-Discover (Core §6.1.7.3, BS-05) response. root is the client's
// alternate path ("" or "/" for none); it is stripped from targets when
// present. Each target must be an LwM2M path (one trailing slash is
// stripped, A-20). Attributes are typed: dim, ssid, uri, ver, lwm2m and the
// notification attributes; others are ignored. In a Bootstrap-Discover
// result the "</>" entry carries lwm2m, /0 instances ssid and uri, /1 /21
// /23 /24 instances ssid.
func ParseDiscover(links []Link, root string) ([]Entry, error) {
	root = trimSlash(root)
	out := make([]Entry, 0, len(links))
	for _, l := range links {
		uri := stripRoot(trimSlash(l.URI), root)
		p, err := lwm2m.ParsePath(uri)
		if err != nil {
			return nil, err
		}
		a, err := l.Attrs()
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Path: p, Attrs: a})
	}
	return out, nil
}
