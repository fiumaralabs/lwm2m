// Package lwm2m holds the core LwM2M data model shared by codecs, the server
// and the bootstrap server: paths, typed values and resource nodes.
package lwm2m

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxID is reserved (Core §7.3): it is never a valid object, instance,
// resource or resource-instance ID inside a path.
const MaxID = 65535

// Path addresses the root, an object, instance, resource or resource
// instance: /, /o, /o/i, /o/i/r, /o/i/r/ri (Core §7.3).
type Path struct {
	ids [4]uint16
	n   uint8
}

// Root is the "/" path.
var Root = Path{}

// NewPath builds a path from 0..4 IDs.
func NewPath(ids ...uint16) Path {
	if len(ids) > 4 {
		panic("lwm2m: path deeper than 4 levels")
	}
	var p Path
	copy(p.ids[:], ids)
	p.n = uint8(len(ids))
	return p
}

var ErrInvalidPath = errors.New("lwm2m: invalid path")

// ParsePath parses "/", "/3", "/3/0", "/3/0/1" or "/3/0/1/2". The leading
// slash is optional ("3/0" is accepted). Empty, trailing or non-decimal
// segments, IDs of 65535 and above and more than four levels are rejected
// (Core §7.3, T §6.4.4).
func ParsePath(s string) (Path, error) {
	if s == "/" {
		return Root, nil
	}
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return Path{}, fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	segs := strings.Split(s, "/")
	if len(segs) > 4 {
		return Path{}, fmt.Errorf("%w: %q: more than 4 levels", ErrInvalidPath, s)
	}
	var p Path
	for i, seg := range segs {
		id, err := parseID(seg)
		if err != nil {
			return Path{}, fmt.Errorf("%w: %q: %v", ErrInvalidPath, s, err)
		}
		p.ids[i] = id
	}
	p.n = uint8(len(segs))
	return p, nil
}

// MustParsePath is ParsePath that panics; for constants and tests.
func MustParsePath(s string) Path {
	p, err := ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

func parseID(seg string) (uint16, error) {
	if seg == "" {
		return 0, errors.New("empty segment")
	}
	for _, c := range seg {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-decimal segment %q", seg)
		}
	}
	v, err := strconv.ParseUint(seg, 10, 32)
	if err != nil || v >= MaxID {
		return 0, fmt.Errorf("segment %q out of range", seg)
	}
	return uint16(v), nil
}

// Len is the number of levels: 0 root, 1 object, 2 instance, 3 resource,
// 4 resource instance.
func (p Path) Len() int { return int(p.n) }

func (p Path) IsRoot() bool             { return p.n == 0 }
func (p Path) IsObject() bool           { return p.n == 1 }
func (p Path) IsInstance() bool         { return p.n == 2 }
func (p Path) IsResource() bool         { return p.n == 3 }
func (p Path) IsResourceInstance() bool { return p.n == 4 }

// ID returns the ID at level i (0-based). It panics when i >= Len().
func (p Path) ID(i int) uint16 {
	if i >= int(p.n) {
		panic("lwm2m: path level out of range")
	}
	return p.ids[i]
}

func (p Path) Object() uint16           { return p.ID(0) }
func (p Path) Instance() uint16         { return p.ID(1) }
func (p Path) Resource() uint16         { return p.ID(2) }
func (p Path) ResourceInstance() uint16 { return p.ID(3) }

// Append returns p with one more level.
func (p Path) Append(id uint16) Path {
	if p.n == 4 {
		panic("lwm2m: path deeper than 4 levels")
	}
	p.ids[p.n] = id
	p.n++
	return p
}

// Parent returns p without its last level; the parent of root is root.
func (p Path) Parent() Path {
	if p.n == 0 {
		return p
	}
	p.n--
	p.ids[p.n] = 0
	return p
}

// Truncate returns the first n levels of p.
func (p Path) Truncate(n int) Path {
	if n >= int(p.n) {
		return p
	}
	for i := n; i < 4; i++ {
		p.ids[i] = 0
	}
	p.n = uint8(n)
	return p
}

// HasPrefix reports whether q is p or an ancestor of p.
func (p Path) HasPrefix(q Path) bool {
	if q.n > p.n {
		return false
	}
	for i := 0; i < int(q.n); i++ {
		if p.ids[i] != q.ids[i] {
			return false
		}
	}
	return true
}

// Compare orders paths depth-first by ID (ancestors before descendants).
func (p Path) Compare(q Path) int {
	for i := 0; i < 4; i++ {
		if i >= int(p.n) || i >= int(q.n) {
			return int(p.n) - int(q.n)
		}
		if p.ids[i] != q.ids[i] {
			if p.ids[i] < q.ids[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (p Path) String() string {
	if p.n == 0 {
		return "/"
	}
	var b strings.Builder
	for i := 0; i < int(p.n); i++ {
		b.WriteByte('/')
		b.WriteString(strconv.Itoa(int(p.ids[i])))
	}
	return b.String()
}

// MarshalText makes Path usable as a JSON string and map key.
func (p Path) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

func (p *Path) UnmarshalText(b []byte) error {
	q, err := ParsePath(string(b))
	if err != nil {
		return err
	}
	*p = q
	return nil
}

// ValidPrefix accepts a Gateway end-device prefix (GW §8.3.1): one URI path
// segment that cannot be mistaken for an object ID.
func ValidPrefix(p string) error {
	if p == "" || strings.Trim(p, "0123456789") == "" || strings.ContainsAny(p, "/?#") {
		return fmt.Errorf("%w: prefix %q", ErrInvalidPath, p)
	}
	return nil
}

// SplitPrefix cuts a leading end-device prefix, a first segment that is not
// a decimal ID, off a path string: "/d01/3/0" gives "d01", "/3/0". Without
// one prefix is "" and rest is s.
func SplitPrefix(s string) (prefix, rest string) {
	head, tail, _ := strings.Cut(strings.TrimPrefix(s, "/"), "/")
	if head == "" || strings.Trim(head, "0123456789") == "" {
		return "", s
	}
	return head, "/" + tail
}

// ParsePrefixedPath parses "/3/0" (the client itself) or "/d01/3303/0" (an
// object of the end device d01, GW §8.3.1). A prefixed path needs an
// object ID.
func ParsePrefixedPath(s string) (string, Path, error) {
	prefix, rest := SplitPrefix(s)
	if prefix == "" {
		p, err := ParsePath(s)
		return "", p, err
	}
	if err := ValidPrefix(prefix); err != nil {
		return "", Path{}, err
	}
	p, err := ParsePath(rest)
	if err != nil || p.IsRoot() {
		return "", Path{}, fmt.Errorf("%w: %q: an end-device path needs an object ID", ErrInvalidPath, s)
	}
	return prefix, p, nil
}

// PrefixedPath renders p under prefix: "/d01/3/0", or p alone for "".
func PrefixedPath(prefix string, p Path) string {
	switch {
	case prefix == "":
		return p.String()
	case p.IsRoot():
		return "/" + prefix
	}
	return "/" + prefix + p.String()
}
