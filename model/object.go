// Package model is the LwM2M object model: OMA object definitions parsed from
// the registry XML (Core App. D, LWM2M.xsd / LWM2M-v1_1.xsd), a versioned
// registry with the core OMA objects embedded, per-client schemas for codecs,
// and the validation the server runs before sending Write, Create and Execute.
package model

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	lwm2m "github.com/fiumaralabs/lwm2m"
)

// Version is an object version "major.minor" (Core §7.2.2). The zero value
// is not valid; objects without a version are 1.0.
type Version struct{ Major, Minor uint16 }

var V1_0 = Version{1, 0}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Less orders versions.
func (v Version) Less(w Version) bool {
	return v.Major < w.Major || v.Major == w.Major && v.Minor < w.Minor
}

var versionRE = regexp.MustCompile(`^(\d{1,5})\.(\d{1,5})$`)

// ParseVersion parses `1*DIGIT "." 1*DIGIT` (Core §7.2.2, the `ver` attribute).
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("model: invalid version %q", s)
	}
	a, err1 := strconv.ParseUint(m[1], 10, 16)
	b, err2 := strconv.ParseUint(m[2], 10, 16)
	if err1 != nil || err2 != nil {
		return Version{}, fmt.Errorf("model: invalid version %q", s)
	}
	return Version{uint16(a), uint16(b)}, nil
}

// Operations is the set of operations a resource allows (Core App. D.1).
// Zero means Bootstrap-only access.
type Operations uint8

const (
	OpRead Operations = 1 << iota
	OpWrite
	OpExecute
)

func (o Operations) String() string {
	switch o {
	case OpRead:
		return "R"
	case OpWrite:
		return "W"
	case OpRead | OpWrite:
		return "RW"
	case OpExecute:
		return "E"
	}
	return ""
}

// Resource is one <Item> of an object definition.
type Resource struct {
	ID          uint16
	Name        string
	Operations  Operations
	Multiple    bool
	Mandatory   bool
	Type        lwm2m.Type
	Range       string // RangeEnumeration, verbatim
	Units       string
	Description string
}

func (r *Resource) Readable() bool   { return r.Operations&OpRead != 0 }
func (r *Resource) Writable() bool   { return r.Operations&OpWrite != 0 }
func (r *Resource) Executable() bool { return r.Operations&OpExecute != 0 }

// Def is the codec view of the resource.
func (r *Resource) Def() lwm2m.ResourceDef {
	return lwm2m.ResourceDef{Type: r.Type, Multiple: r.Multiple}
}

// Object is one object definition at one version.
type Object struct {
	ID           uint16
	Name         string
	Description  string
	URN          string
	LwM2MVersion string // minimum enabler version; "" in pre-1.1 files
	Version      Version
	Multiple     bool
	Mandatory    bool
	Resources    map[uint16]*Resource
}

// Resource returns the definition of resource id, if any.
func (o *Object) Resource(id uint16) (*Resource, bool) {
	r, ok := o.Resources[id]
	return r, ok
}

var ErrInvalidXML = errors.New("model: invalid object definition")

type xmlDoc struct {
	XMLName xml.Name    `xml:"LWM2M"`
	Objects []xmlObject `xml:"Object"`
}

type xmlObject struct {
	Name          string  `xml:"Name"`
	Description1  string  `xml:"Description1"`
	ObjectID      *string `xml:"ObjectID"`
	ObjectURN     string  `xml:"ObjectURN"`
	LWM2MVersion  string  `xml:"LWM2MVersion"`
	ObjectVersion string  `xml:"ObjectVersion"`
	Multiple      string  `xml:"MultipleInstances"`
	Mandatory     string  `xml:"Mandatory"`
	Resources     *struct {
		Items []xmlItem `xml:"Item"`
	} `xml:"Resources"`
}

type xmlItem struct {
	ID          *string `xml:"ID,attr"`
	Name        string  `xml:"Name"`
	Operations  *string `xml:"Operations"`
	Multiple    string  `xml:"MultipleInstances"`
	Mandatory   string  `xml:"Mandatory"`
	Type        *string `xml:"Type"`
	Range       string  `xml:"RangeEnumeration"`
	Units       string  `xml:"Units"`
	Description string  `xml:"Description"`
}

var urnRE = regexp.MustCompile(`^urn:oma:lwm2m:(oma|ext|x):(\d+)(?::(\d+\.\d+))?$`)

// ParseXML parses an OMA object definition document (LWM2M.xsd or
// LWM2M-v1_1.xsd; Core App. D). A document may hold several objects. Besides
// the schema it enforces IDs below MAX_ID (Core §7.3), unique resource IDs,
// and an ObjectURN consistent with ObjectID and ObjectVersion (Core §7.2.1).
func ParseXML(data []byte) ([]*Object, error) {
	var doc xmlDoc
	dec := xml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidXML, err)
	}
	if len(doc.Objects) == 0 {
		return nil, fmt.Errorf("%w: no <Object>", ErrInvalidXML)
	}
	out := make([]*Object, 0, len(doc.Objects))
	for _, x := range doc.Objects {
		o, err := x.convert()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidXML, err)
		}
		out = append(out, o)
	}
	return out, nil
}

func parseID(s *string, what string) (uint16, error) {
	if s == nil {
		return 0, fmt.Errorf("missing %s", what)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(*s), 10, 16)
	if err != nil || v == lwm2m.MaxID {
		return 0, fmt.Errorf("%s %q out of range 0..65534", what, *s)
	}
	return uint16(v), nil
}

func parseMultiple(s, where string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "Multiple":
		return true, nil
	case "Single":
		return false, nil
	}
	return false, fmt.Errorf("%s: MultipleInstances %q", where, s)
}

func parseMandatory(s, where string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "Mandatory":
		return true, nil
	case "Optional":
		return false, nil
	}
	return false, fmt.Errorf("%s: Mandatory %q", where, s)
}

func (x xmlObject) convert() (*Object, error) {
	id, err := parseID(x.ObjectID, "ObjectID")
	if err != nil {
		return nil, err
	}
	where := fmt.Sprintf("object %d", id)
	o := &Object{
		ID:           id,
		Name:         strings.TrimSpace(x.Name),
		Description:  strings.TrimSpace(x.Description1),
		URN:          strings.TrimSpace(x.ObjectURN),
		LwM2MVersion: strings.TrimSpace(x.LWM2MVersion),
		Version:      V1_0,
		Resources:    map[uint16]*Resource{},
	}
	if v := strings.TrimSpace(x.ObjectVersion); v != "" {
		if o.Version, err = ParseVersion(v); err != nil {
			return nil, fmt.Errorf("%s: %v", where, err)
		}
	}
	if o.URN != "" {
		m := urnRE.FindStringSubmatch(o.URN)
		if m == nil || m[2] != strconv.Itoa(int(id)) {
			return nil, fmt.Errorf("%s: ObjectURN %q does not match", where, o.URN)
		}
		// Core §7.2.1: 1.0 may be omitted from the URN.
		if uv := m[3]; (uv == "" && o.Version != V1_0) || (uv != "" && uv != o.Version.String()) {
			return nil, fmt.Errorf("%s: ObjectURN %q vs ObjectVersion %s", where, o.URN, o.Version)
		}
	}
	if o.Multiple, err = parseMultiple(x.Multiple, where); err != nil {
		return nil, err
	}
	if o.Mandatory, err = parseMandatory(x.Mandatory, where); err != nil {
		return nil, err
	}
	if x.Resources == nil {
		return nil, fmt.Errorf("%s: missing <Resources>", where)
	}
	for _, it := range x.Resources.Items {
		r, err := it.convert(where)
		if err != nil {
			return nil, err
		}
		if _, dup := o.Resources[r.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate resource %d", where, r.ID)
		}
		o.Resources[r.ID] = r
	}
	return o, nil
}

func (it xmlItem) convert(where string) (*Resource, error) {
	id, err := parseID(it.ID, where+": Item ID")
	if err != nil {
		return nil, err
	}
	where = fmt.Sprintf("%s resource %d", where, id)
	r := &Resource{
		ID:          id,
		Name:        strings.TrimSpace(it.Name),
		Range:       strings.TrimSpace(it.Range),
		Units:       strings.TrimSpace(it.Units),
		Description: strings.TrimSpace(it.Description),
	}
	if it.Operations == nil {
		return nil, fmt.Errorf("%s: missing Operations", where)
	}
	switch strings.ToUpper(strings.TrimSpace(*it.Operations)) {
	case "":
	case "R":
		r.Operations = OpRead
	case "W":
		r.Operations = OpWrite
	case "RW":
		r.Operations = OpRead | OpWrite
	case "E":
		r.Operations = OpExecute
	default:
		return nil, fmt.Errorf("%s: Operations %q", where, *it.Operations)
	}
	if r.Multiple, err = parseMultiple(it.Multiple, where); err != nil {
		return nil, err
	}
	if r.Mandatory, err = parseMandatory(it.Mandatory, where); err != nil {
		return nil, err
	}
	if it.Type == nil {
		return nil, fmt.Errorf("%s: missing Type", where)
	}
	if r.Type, err = lwm2m.ParseType(*it.Type); err != nil {
		return nil, fmt.Errorf("%s: %v", where, err)
	}
	if strings.EqualFold(strings.TrimSpace(*it.Type), "none") {
		return nil, fmt.Errorf("%s: Type %q", where, *it.Type) // not in the XSD
	}
	// Tolerance (README §1.3): App. D.1 says executables are Single with type
	// none, but published registry files break it (15-1_0 /6 is a Multiple E,
	// 10260-1_0 /3 an E of type String, 10482 /5 an R with no type). Load
	// them: an executable carries no value, so its type is dropped; an untyped
	// readable resource stays TypeNone and so never passes CheckValue.
	if r.Executable() {
		r.Type = lwm2m.TypeNone
	}
	return r, nil
}
