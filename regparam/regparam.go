// Package regparam parses the Uri-Query of Register and Update requests
// (Core §6.2.1 Tbl 6.2.1-1, §6.2.2 Tbl 6.2.2-1; T §6.4.3 Tbl 6.4.3-1/-2):
// ep, lt, lwm2m, b, Q, sms, pid.
package regparam

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("regparam: invalid parameter")

func errf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// SplitQuery turns Uri-Query options ("k=v" or "k") into a map; a
// valueless key maps to nil. Order is irrelevant (T1); a duplicate key or an
// empty option is an error (4.00, T1). Values are taken as-is: CoAP
// Uri-Query options carry no percent-encoding.
func SplitQuery(opts []string) (map[string]*string, error) {
	m := make(map[string]*string, len(opts))
	for _, o := range opts {
		k, v, hasValue := strings.Cut(o, "=")
		if k == "" {
			return nil, errf("empty query option %q", o)
		}
		if _, dup := m[k]; dup {
			return nil, errf("duplicate %q", k)
		}
		if hasValue {
			m[k] = &v
		} else {
			m[k] = nil
		}
	}
	return m, nil
}

// Request is a request line "METHOD /a/b?k=v&k2" split into its parts.
type Request struct {
	Method string
	Path   []string
	Query  map[string]*string
}

// ParseRequest parses the text form used by the TS examples, e.g.
// "POST /rd?ep=node&lt=86400&lwm2m=1.1&b=U&Q".
func ParseRequest(line string) (Request, error) {
	method, target, ok := strings.Cut(line, " ")
	if !ok || method == "" || !strings.HasPrefix(target, "/") {
		return Request{}, errf("request line %q", line)
	}
	path, q, hasQ := strings.Cut(target, "?")
	r := Request{Method: method, Path: strings.Split(strings.TrimPrefix(path, "/"), "/")}
	var opts []string
	if hasQ {
		opts = strings.Split(q, "&")
	}
	var err error
	r.Query, err = SplitQuery(opts)
	return r, err
}

// Binding is a parsed "b" value. 1.0 carried queue mode inside b (UQ, SQ,
// UQS); 1.1+ uses the separate Q flag, which Params.Queue merges in.
type Binding struct {
	Transports string // letters in order, e.g. "U", "US", "UT"
	Queue      bool
}

// ParseBinding parses b for the client's LwM2M version (REG-18).
//
//   - 1.0 (C10 §5.3.1.1 Tbl 8): exactly U, UQ, S, SQ, US or UQS. UQSQ and
//     USQ are not valid.
//   - 1.1: any non-empty set of U, T, S, N; 1.2 adds M and H (Core §6.2.1.2).
//     Tolerance T3: a trailing Q is read as queue mode, and the 1.0 values
//     are accepted too.
func ParseBinding(b, version string) (Binding, error) {
	legacy := map[string]Binding{
		"U": {"U", false}, "UQ": {"U", true}, "S": {"S", false},
		"SQ": {"S", true}, "US": {"US", false}, "UQS": {"US", true},
	}
	if l, ok := legacy[b]; ok {
		return l, nil
	}
	allowed := ""
	switch minor(version) {
	case "1.0":
		return Binding{}, errf("b=%q is not a 1.0 binding", b)
	case "1.1":
		allowed = "UTSN"
	case "1.2":
		allowed = "UTSNMH"
	default:
		return Binding{}, errf("unsupported lwm2m version %q", version)
	}
	var out Binding
	if t, ok := strings.CutSuffix(b, "Q"); ok {
		b, out.Queue = t, true
	}
	if b == "" {
		return Binding{}, errf("empty binding")
	}
	for _, c := range b {
		if !strings.ContainsRune(allowed, c) || strings.ContainsRune(out.Transports, c) {
			return Binding{}, errf("b=%q: bad or repeated %q for %s", b, c, version)
		}
		out.Transports += string(c)
	}
	return out, nil
}

// minor maps "1.2.1" to "1.2"; served with the 1.2 semantics (README §2).
func minor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

// ProfileID is one Profile ID (Core 1.2.2 §6.2.1, PROF-02).
type ProfileID struct {
	Kind  string // "dynamic", "oma" or "v" (vendor)
	Suite uint8  // dynamic: RFC 6920 hash suite ID
	Hash  string // dynamic: lowercase hex
	Value string // oma / v: the pre-configured name
}

// ParseProfileIDs parses a pid value per PROF-02:
//
//	value = profileID / ( DQUOTE profileID *( "," profileID ) DQUOTE )
//	profileID = dynamicID / preConfigID
//	dynamicID = suite-identifier ":" 1*(DIGIT / "a"-"f")
//	suite-identifier = 1*DIGIT
//	preConfigID = ("oma" / "v") ":" 1*(ALPHA / DIGIT / "-")
func ParseProfileIDs(v string) ([]ProfileID, error) {
	list := []string{v}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		list = strings.Split(v[1:len(v)-1], ",")
	}
	out := make([]ProfileID, 0, len(list))
	for _, s := range list {
		head, tail, ok := strings.Cut(s, ":")
		if !ok || tail == "" {
			return nil, errf("pid %q", s)
		}
		switch {
		case head == "oma" || head == "v":
			for _, c := range tail {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return nil, errf("pid %q", s)
				}
			}
			out = append(out, ProfileID{Kind: head, Value: tail})
		default:
			suite, err := strconv.ParseUint(head, 10, 8)
			if err != nil || strings.Trim(head, "0123456789") != "" {
				return nil, errf("pid %q: suite", s)
			}
			if strings.Trim(tail, "0123456789abcdef") != "" {
				return nil, errf("pid %q: hash must be lowercase hex", s)
			}
			out = append(out, ProfileID{Kind: "dynamic", Suite: uint8(suite), Hash: tail})
		}
	}
	return out, nil
}

// Params are the Register/Update parameters. A nil pointer means absent;
// on Update absent means unchanged (Core §6.2.2, T29).
type Params struct {
	Endpoint   *string // ep
	Lifetime   *uint32 // lt, seconds; 0 = no expiry (REG-23)
	Version    *string // lwm2m, Register only
	Binding    *Binding
	Queue      bool // Q flag, or Q inside a 1.0-style b
	SMS        *string
	ProfileIDs []ProfileID
}

// ParseRegister parses Register Uri-Query options. lt and lwm2m are
// required (REG-03); ep is required for 1.0 and optional from 1.1 (REG-02).
// Unknown parameters are an error (REG-06: 4.00). Supporting the declared
// version (REG-04, 4.12) is the caller's decision.
func ParseRegister(opts []string) (Params, error) {
	q, err := SplitQuery(opts)
	if err != nil {
		return Params{}, err
	}
	v, ok := q["lwm2m"]
	if !ok || v == nil {
		return Params{}, errf("lwm2m is required")
	}
	p, err := parse(q, *v, "ep", "lt", "lwm2m", "b", "Q", "sms", "pid")
	if err != nil {
		return Params{}, err
	}
	if p.Lifetime == nil {
		return Params{}, errf("lt is required")
	}
	if p.Endpoint == nil && minor(*v) == "1.0" {
		return Params{}, errf("ep is required for 1.0")
	}
	return p, nil
}

// ParseUpdate parses Update Uri-Query options: lt, b, Q, sms and pid
// (A-9). version is the client's registered LwM2M version, used for b.
func ParseUpdate(opts []string, version string) (Params, error) {
	q, err := SplitQuery(opts)
	if err != nil {
		return Params{}, err
	}
	return parse(q, version, "lt", "b", "Q", "sms", "pid")
}

func parse(q map[string]*string, version string, allowed ...string) (Params, error) {
	var p Params
	for k, v := range q {
		if !slices.Contains(allowed, k) {
			return Params{}, errf("unknown parameter %q", k)
		}
		if k == "Q" {
			// T2: "Q" and "Q=" are the same flag.
			if v != nil && *v != "" {
				return Params{}, errf("Q takes no value")
			}
			p.Queue = true
			continue
		}
		if v == nil || *v == "" {
			return Params{}, errf("%s needs a value", k)
		}
		switch k {
		case "ep":
			p.Endpoint = v
		case "lwm2m":
			p.Version = v
		case "sms":
			p.SMS = v
		case "lt":
			n, err := strconv.ParseUint(*v, 10, 32)
			if err != nil || strings.Trim(*v, "0123456789") != "" {
				return Params{}, errf("lt=%q", *v)
			}
			lt := uint32(n)
			p.Lifetime = &lt
		case "b":
			b, err := ParseBinding(*v, version)
			if err != nil {
				return Params{}, err
			}
			p.Binding = &b
		case "pid":
			ids, err := ParseProfileIDs(*v)
			if err != nil {
				return Params{}, err
			}
			p.ProfileIDs = ids
		}
	}
	if p.Binding != nil && p.Binding.Queue {
		p.Queue = true
	}
	return p, nil
}
