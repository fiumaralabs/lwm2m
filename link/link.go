// Package link parses and encodes CoRE Link Format (RFC 6690, CoAP
// Content-Format 40) and interprets it for LwM2M: Register/Update object
// lists (Core §6.2.1), Discover and Bootstrap-Discover results (Core §6.3.2,
// §6.1.7.3).
package link

import (
	"errors"
	"fmt"
	"strings"
)

// Param is one link parameter. HasValue is false for a valueless
// parameter (";obs"). Quoted records a quoted-string value.
type Param struct {
	Name     string
	Value    string
	HasValue bool
	Quoted   bool
}

// Link is one link-value: a URI-reference and its ordered parameters.
type Link struct {
	URI    string
	Params []Param
}

// Param returns the named parameter.
func (l Link) Param(name string) (Param, bool) {
	for _, p := range l.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}

var ErrSyntax = errors.New("link: syntax error")

// Parse parses an RFC 6690 link-format document. An empty document is no
// links. Input tolerances (each allowed because the spec does not forbid
// accepting it; we never emit these forms):
//
//   - Whitespace after "," (A-20: every Core example does it).
//   - A line break, with optional surrounding whitespace, as a separator
//     (Core §6.4.4 Observe-Composite figure). Whitespace without a line
//     break between links stays an error, and so does no separator at all
//     (A-26).
//   - A leading "lwm2m=X.Y" with no URI-reference becomes a "</>" link
//     (Core §6.1.7.3 note, BS-27).
//
// Link targets must be absolute paths ("/", "/a/b"): LwM2M links are object
// paths, so URIs with a scheme, authority, query or fragment are rejected.
// Inside a quoted-string only \" is unescaped; other backslash pairs are
// kept verbatim (Leshan's reading, pinned by vectors).
func Parse(s string) ([]Link, error) {
	if s == "" {
		return nil, nil
	}
	p := &parser{s: s}
	var links []Link
	first := true
	for {
		var l Link
		var err error
		if first && strings.HasPrefix(s, "lwm2m=") {
			l.URI = "/"
			l.Params, err = p.params(false)
		} else {
			l, err = p.link()
		}
		if err != nil {
			return nil, err
		}
		first = false
		links = append(links, l)
		if p.i == len(s) {
			return links, nil
		}
		if err := p.sep(); err != nil {
			return nil, err
		}
	}
}

type parser struct {
	s string
	i int
}

func (p *parser) fail(msg string) error {
	return fmt.Errorf("%w at offset %d: %s", ErrSyntax, p.i, msg)
}

func (p *parser) sep() error {
	c := p.s[p.i]
	if c == ',' {
		p.i++
		for p.i < len(p.s) && isWS(p.s[p.i]) {
			p.i++
		}
		return nil
	}
	start := p.i
	for p.i < len(p.s) && isWS(p.s[p.i]) {
		p.i++
	}
	if p.i == start || !strings.Contains(p.s[start:p.i], "\n") {
		p.i = start
		return p.fail("expected ','")
	}
	return nil
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func (p *parser) link() (Link, error) {
	if p.i >= len(p.s) || p.s[p.i] != '<' {
		return Link{}, p.fail("expected '<'")
	}
	end := strings.IndexByte(p.s[p.i:], '>')
	if end < 0 {
		return Link{}, p.fail("unterminated '<'")
	}
	uri := p.s[p.i+1 : p.i+end]
	if err := checkURI(uri); err != nil {
		return Link{}, p.fail(err.Error())
	}
	p.i += end + 1
	params, err := p.params(true)
	return Link{URI: uri, Params: params}, err
}

// params parses *(";" link-param); with semi false the first parameter has
// no leading ";" (legacy BS-Discover form).
func (p *parser) params(semi bool) ([]Param, error) {
	var out []Param
	for {
		if semi {
			if p.i >= len(p.s) || p.s[p.i] != ';' {
				return out, nil
			}
			p.i++
		}
		semi = true
		start := p.i
		for p.i < len(p.s) && isAttrChar(p.s[p.i]) {
			p.i++
		}
		if p.i == start {
			return nil, p.fail("expected parameter name")
		}
		pr := Param{Name: p.s[start:p.i]}
		if p.i < len(p.s) && p.s[p.i] == '=' {
			p.i++
			pr.HasValue = true
			if p.i < len(p.s) && p.s[p.i] == '"' {
				v, err := p.quoted()
				if err != nil {
					return nil, err
				}
				pr.Value, pr.Quoted = v, true
			} else {
				start := p.i
				for p.i < len(p.s) && isPtokenChar(p.s[p.i]) {
					p.i++
				}
				if p.i == start {
					return nil, p.fail("empty parameter value")
				}
				pr.Value = p.s[start:p.i]
			}
		}
		out = append(out, pr)
	}
}

func (p *parser) quoted() (string, error) {
	p.i++ // opening quote
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return b.String(), nil
		case c == '\\' && p.i+1 < len(p.s):
			if p.s[p.i+1] != '"' {
				b.WriteByte('\\')
			}
			b.WriteByte(p.s[p.i+1])
			p.i += 2
		case c < 0x20 && c != '\t' || c == 0x7f:
			return "", p.fail("control character in quoted-string")
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "", p.fail("unterminated quoted-string")
}

// checkURI accepts "/" or an RFC 3986 path-absolute of pchars.
func checkURI(u string) error {
	if u == "" || u[0] != '/' {
		return errors.New("link target must be an absolute path")
	}
	if strings.HasPrefix(u, "//") {
		return errors.New("link target has an authority")
	}
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c == '%':
			if i+2 >= len(u) || !isHex(u[i+1]) || !isHex(u[i+2]) {
				return errors.New("bad percent-encoding")
			}
			i += 2
		case isAlnum(c) || strings.IndexByte("-._~!$&'()*+,;=:@/", c) >= 0:
		default:
			return fmt.Errorf("invalid character %q in link target", c)
		}
	}
	return nil
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// isAttrChar: RFC 5988 parmname = 1*attr-char (RFC 5987).
func isAttrChar(c byte) bool {
	return isAlnum(c) || strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

// isPtokenChar: RFC 6690 §2 ptokenchar.
func isPtokenChar(c byte) bool {
	return isAlnum(c) || strings.IndexByte("!#$%&'()*+-./:<=>?@[]^_`{|}~", c) >= 0
}

// Encode serializes links exactly as given: "," between links, no
// whitespace. A value is quoted when Quoted is set or when it is not a
// valid ptoken (so the output is always RFC 6690 conformant); inside quotes
// '"' and '\' are escaped. URIs are written verbatim.
func Encode(links []Link) string {
	var b strings.Builder
	for i, l := range links {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('<')
		b.WriteString(l.URI)
		b.WriteByte('>')
		for _, p := range l.Params {
			b.WriteByte(';')
			b.WriteString(p.Name)
			if !p.HasValue {
				continue
			}
			b.WriteByte('=')
			if !p.Quoted && isPtoken(p.Value) {
				b.WriteString(p.Value)
				continue
			}
			b.WriteByte('"')
			for j := 0; j < len(p.Value); j++ {
				if c := p.Value[j]; c == '"' || c == '\\' {
					b.WriteByte('\\')
				}
				b.WriteByte(p.Value[j])
			}
			b.WriteByte('"')
		}
	}
	return b.String()
}

func isPtoken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isPtokenChar(s[i]) {
			return false
		}
	}
	return true
}
