// Package attr parses, encodes, validates and resolves LwM2M attributes:
// the <NOTIFICATION> attributes of Write-Attributes (pmin pmax gt lt st
// epmin epmax edge con hqmax) and the <PROPERTIES> reported by Discover
// (dim ssid uri ver lwm2m). Core 1.2.2 §7.3, Tbl 7.3.1-1, Tbl 7.3.2-1.
package attr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fiumaralabs/lwm2m"
)

// Attr is one attribute. Value is nil for a valueless attribute (in a
// Write-Attributes query: unset at that level, Core §6.3.4), otherwise
// uint64 (pmin pmax epmin epmax hqmax dim ssid), float64 (gt lt st),
// bool (edge con) or string (uri ver lwm2m).
type Attr struct {
	Name  string
	Value any
}

// Attrs is an ordered attribute list.
type Attrs []Attr

type kind uint8

const (
	kInt kind = iota + 1
	kFloat
	kBool
	kString
	kVersion
)

var kinds = map[string]kind{
	"pmin": kInt, "pmax": kInt, "epmin": kInt, "epmax": kInt, "hqmax": kInt,
	"gt": kFloat, "lt": kFloat, "st": kFloat,
	"edge": kBool, "con": kBool,
	"dim": kInt, "ssid": kInt, "uri": kString, "ver": kVersion, "lwm2m": kVersion,
}

// IsNotification reports whether name is a writable <NOTIFICATION>
// attribute (Core 1.2.2 §7.3.2); the others are read-only <PROPERTIES>.
func IsNotification(name string) bool {
	switch name {
	case "pmin", "pmax", "gt", "lt", "st", "epmin", "epmax", "edge", "con", "hqmax":
		return true
	}
	return false
}

// Since returns the first LwM2M version defining the attribute: epmin and
// epmax are 1.1, edge con hqmax 1.2, the rest 1.0 (T41: only send what the
// client's version knows). Unknown names return "".
func Since(name string) string {
	switch name {
	case "epmin", "epmax":
		return "1.1"
	case "edge", "con", "hqmax":
		return "1.2"
	}
	if _, ok := kinds[name]; ok {
		return "1.0"
	}
	return ""
}

var ErrInvalid = errors.New("attr: invalid attribute")

func errf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// ParseValue types one attribute value by name (Core Tbl 7.3.1-1, 7.3.2-1).
func ParseValue(name, v string) (any, error) {
	k, ok := kinds[name]
	if !ok {
		return nil, errf("unknown attribute %q", name)
	}
	switch k {
	case kInt:
		if !digits(v) {
			return nil, errf("%s=%q: want 1*DIGIT", name, v)
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, errf("%s=%q: %v", name, v, err)
		}
		// ATT-11: dim 0..65535, ssid 1..65534 (Core Tbl 7.3.1-1, Tbl 7.4-1).
		if name == "dim" && n > 65535 || name == "ssid" && (n < 1 || n > 65534) {
			return nil, errf("%s=%d out of range", name, n)
		}
		return n, nil
	case kFloat:
		// Tbl 7.3.2-1 says 1*DIGIT ["." 1*DIGIT]; A-22: also accept a sign
		// (gt/lt only: st is a non-negative step) and an exponent on input.
		if !decimal(v, name != "st") {
			return nil, errf("%s=%q: want a decimal", name, v)
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, errf("%s=%q: %v", name, v, err)
		}
		return f, nil
	case kBool:
		// ATT-08: edge and con are "0" or "1".
		switch v {
		case "0":
			return false, nil
		case "1":
			return true, nil
		}
		return nil, errf("%s=%q: want 0 or 1", name, v)
	case kVersion:
		// ATT-11: 1*DIGIT "." 1*DIGIT.
		maj, min, ok := strings.Cut(v, ".")
		if !ok || !digits(maj) || !digits(min) {
			return nil, errf("%s=%q: want MAJOR.MINOR", name, v)
		}
		return v, nil
	}
	return v, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// decimal matches [-]1*DIGIT["."1*DIGIT][("e"/"E")["+"/"-"]1*DIGIT]. It
// keeps strconv's hex, inf, nan and underscore forms out.
func decimal(s string, signed bool) bool {
	if signed {
		s = strings.TrimPrefix(s, "-")
	}
	mant, exp, hasExp := strings.Cut(strings.ToLower(s), "e")
	ip, fp, hasFrac := strings.Cut(mant, ".")
	if !digits(ip) || hasFrac && !digits(fp) {
		return false
	}
	if hasExp {
		exp = strings.TrimLeft(exp, "+-")
		return digits(exp)
	}
	return true
}

// FormatValue renders a value as it appears on the wire: plain decimal
// (never scientific notation, A-22), "0"/"1" for booleans.
func FormatValue(v any) string {
	switch x := v.(type) {
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "1"
		}
		return "0"
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// ParseQuery parses a Write-Attributes Uri-Query, segments joined by "&"
// (Core §6.3.4, T §6.4.4): "pmin=10&pmax&gt=2.5". A valueless notification
// attribute means unset. Empty segments, unknown names, duplicates (T1) and
// valueless properties are rejected. Properties (dim, ver...) parse here but
// are not writable; Validate rejects them.
func ParseQuery(q string) (Attrs, error) {
	var out Attrs
	seen := map[string]bool{}
	for _, seg := range strings.Split(q, "&") {
		name, v, hasValue := strings.Cut(seg, "=")
		if name == "" {
			return nil, errf("empty query segment in %q", q)
		}
		if seen[name] {
			return nil, errf("duplicate %q", name)
		}
		seen[name] = true
		if _, ok := kinds[name]; !ok {
			return nil, errf("unknown attribute %q", name)
		}
		if !hasValue {
			if !IsNotification(name) {
				return nil, errf("%s has no valueless form", name)
			}
			out = append(out, Attr{Name: name})
			continue
		}
		val, err := ParseValue(name, v)
		if err != nil {
			return nil, err
		}
		out = append(out, Attr{Name: name, Value: val})
	}
	return out, nil
}

// Query encodes the attributes as a Uri-Query string, in order.
func (a Attrs) Query() string {
	var b strings.Builder
	for i, x := range a {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(x.Name)
		if x.Value != nil {
			b.WriteByte('=')
			b.WriteString(FormatValue(x.Value))
		}
	}
	return b.String()
}

// ParseLinkParam types one link parameter of a Discover or
// Bootstrap-Discover response. known is false for parameters that are not
// LwM2M attributes; those must be ignored (Core §6.2.1, REG-07). A known
// attribute without a value is an error in a link.
func ParseLinkParam(name, value string, hasValue bool) (a Attr, known bool, err error) {
	if _, ok := kinds[name]; !ok {
		return Attr{}, false, nil
	}
	if !hasValue {
		return Attr{}, true, errf("%s without a value in a link", name)
	}
	v, err := ParseValue(name, value)
	return Attr{Name: name, Value: v}, true, err
}

// Get returns the value of name, or nil.
func (a Attrs) Get(name string) any {
	for _, x := range a {
		if x.Name == name {
			return x.Value
		}
	}
	return nil
}

// Validate checks a Write-Attributes set for target p before it is sent
// (ATT-06: "the server should validate before sending"). t is the
// resource's data type, or lwm2m.TypeNone when unknown or not a resource.
//
//   - DM-07: only <NOTIFICATION> attributes are writable; not on "/".
//   - ATT-10: gt, lt, st only on a resource or resource instance.
//   - ATT-05/ATT-10: gt, lt, st only on numeric resources; ATT-08: edge only
//     on Boolean ones (checked when t is known).
//   - ATT-04: pmax ≥ pmin when both are set and pmax ≠ 0 (0 = no maximum).
//     The client ignores a smaller pmax; DM-07 requires a consistent set.
//   - ATT-06: lt < gt and lt + 2·st < gt.
//   - ATT-07: epmin < epmax.
func Validate(p lwm2m.Path, t lwm2m.Type, a Attrs) error {
	if p.IsRoot() {
		return errf("Write-Attributes on /")
	}
	vals := map[string]any{}
	for _, x := range a {
		if !IsNotification(x.Name) {
			return errf("%s is a read-only property (DM-07)", x.Name)
		}
		switch x.Name {
		case "gt", "lt", "st":
			if p.Len() < 3 {
				return errf("%s only on a resource or resource instance (ATT-10)", x.Name)
			}
			if t != lwm2m.TypeNone && t != lwm2m.TypeInteger && t != lwm2m.TypeUnsigned && t != lwm2m.TypeFloat {
				return errf("%s only on a numeric resource (ATT-05)", x.Name)
			}
		case "edge":
			if p.Len() < 3 {
				return errf("edge only on a resource or resource instance (ATT-10)")
			}
			if t != lwm2m.TypeNone && t != lwm2m.TypeBoolean {
				return errf("edge only on a Boolean resource (ATT-08)")
			}
		}
		if x.Value != nil {
			vals[x.Name] = x.Value
		}
	}
	u := func(n string) (uint64, bool) { v, ok := vals[n].(uint64); return v, ok }
	f := func(n string) (float64, bool) { v, ok := vals[n].(float64); return v, ok }
	if pmin, ok := u("pmin"); ok {
		if pmax, ok := u("pmax"); ok && pmax != 0 && pmax < pmin {
			return errf("pmax %d < pmin %d (ATT-04)", pmax, pmin)
		}
	}
	if lt, ok := f("lt"); ok {
		if gt, ok := f("gt"); ok {
			if lt >= gt {
				return errf("lt %v must be < gt %v (ATT-06)", lt, gt)
			}
			if st, ok := f("st"); ok && lt+2*st >= gt {
				return errf("lt + 2*st must be < gt (ATT-06)")
			}
		}
	}
	if epmin, ok := u("epmin"); ok {
		if epmax, ok := u("epmax"); ok && epmin >= epmax {
			return errf("epmin %d must be < epmax %d (ATT-07)", epmin, epmax)
		}
	}
	return nil
}

// Apply applies a Write-Attributes set to the attributes attached at one
// level: a valued attribute sets, a valueless one removes it from that
// level (Core §6.3.4, DM-07). stored is not modified.
func Apply(stored, w Attrs) Attrs {
	out := append(Attrs(nil), stored...)
	for _, x := range w {
		i := out.index(x.Name)
		switch {
		case x.Value == nil && i >= 0:
			out = append(out[:i], out[i+1:]...)
		case x.Value != nil && i >= 0:
			out[i].Value = x.Value
		case x.Value != nil:
			out = append(out, x)
		}
	}
	return out
}

// Resolve returns the attributes in effect at the lowest level (ATT-02):
// an attribute applies at its level and below, and a lower level overrides
// a higher one. Pass the levels from highest to lowest, starting with the
// server defaults (pmin = /1/x/2, pmax = /1/x/3), then object, instance,
// resource and resource instance. Valueless entries are skipped.
func Resolve(levels ...Attrs) Attrs {
	var out Attrs
	for _, l := range levels {
		for _, x := range l {
			if x.Value == nil {
				continue
			}
			if i := out.index(x.Name); i >= 0 {
				out[i].Value = x.Value
			} else {
				out = append(out, x)
			}
		}
	}
	return out
}

func (a Attrs) index(name string) int {
	for i, x := range a {
		if x.Name == name {
			return i
		}
	}
	return -1
}
