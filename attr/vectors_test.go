package attr

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// tolerated: none. Every Leshan attribute "error" vector is also an error
// for us (parse or Validate).
var tolerated = map[string]string{}

// expected converts {"attributes":[{"name","value"?}]} by typing each JSON
// value with ParseValue, so numbers compare as the attribute's Go type.
func expected(t *testing.T, v vectors.Vector) Attrs {
	t.Helper()
	var js []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if err := v.ExpectedObject("attributes", &js); err != nil {
		t.Fatal(err)
	}
	var out Attrs
	for _, j := range js {
		a := Attr{Name: j.Name}
		if j.Value != nil {
			d := json.NewDecoder(bytes.NewReader(j.Value))
			d.UseNumber()
			var raw any
			if err := d.Decode(&raw); err != nil {
				t.Fatal(err)
			}
			s, _ := raw.(string)
			if n, ok := raw.(json.Number); ok {
				s = n.String()
			}
			val, err := ParseValue(j.Name, s)
			if err != nil {
				t.Fatal(err)
			}
			a.Value = val
		}
		out = append(out, a)
	}
	return out
}

// decode parses the Write-Attributes query and, when the vector names a
// target path, validates it there.
func decode(v vectors.Vector) (Attrs, error) {
	a, err := ParseQuery(*v.Text)
	if err != nil || v.Path == "" {
		return a, err
	}
	p, err := lwm2m.ParsePath(v.Path)
	if err != nil {
		return nil, err
	}
	return a, Validate(p, lwm2m.TypeNone, a)
}

// Proves: ATT-01, ATT-04, ATT-06, ATT-10, DM-07
func TestVectors(t *testing.T) {
	var all []vectors.Vector
	for _, name := range []string{"attributes", "spec-examples"} {
		vs, err := vectors.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if name == "attributes" || v.ExpectedShape() == "attributes" {
				all = append(all, v)
			}
		}
	}
	seen := map[string]bool{}
	var nDecode, nEncode, nError int
	for _, v := range all {
		seen[v.ID] = true
		t.Run(v.ID, func(t *testing.T) {
			if reason, ok := tolerated[v.ID]; ok {
				if _, err := decode(v); err != nil || !v.IsError() {
					t.Fatalf("tolerated (%s): %v", reason, err)
				}
				return
			}
			if v.IsError() {
				nError++
				if a, err := decode(v); err == nil {
					t.Fatalf("decoded %q to %+v, want error", *v.Text, a)
				}
				return
			}
			want := expected(t, v)
			if v.Decodes() {
				nDecode++
				got, err := decode(v)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("decode %q\n got %#v\nwant %#v", *v.Text, got, want)
				}
			}
			if v.Encodes() {
				nEncode++
				if got := want.Query(); got != *v.Text {
					t.Fatalf("encode got %q want %q", got, *v.Text)
				}
			}
		})
	}
	for id := range tolerated {
		if !seen[id] {
			t.Errorf("tolerated id %s not in vectors", id)
		}
	}
	t.Logf("%d vectors: %d decode, %d encode, %d must-fail", len(all), nDecode, nEncode, nError)
}

// Proves: ATT-10, ATT-11
func TestParseValue(t *testing.T) {
	ok := map[string]any{
		"pmin=0": uint64(0), "hqmax=10": uint64(10), "epmax=5": uint64(5),
		"gt=-30.5": -30.5, "lt=1e3": 1000.0, "st=0.5": 0.5, // A-22: sign and exponent on input
		"edge=0": false, "edge=1": true, "con=1": true,
		"dim=65535": uint64(65535), "ssid=1": uint64(1), "ssid=65534": uint64(65534),
		"ver=2.10": "2.10", "lwm2m=1.2": "1.2", "uri=coaps://x:5684": "coaps://x:5684",
	}
	for in, want := range ok {
		a, err := ParseQuery(in)
		if err != nil || len(a) != 1 || a[0].Value != want {
			t.Errorf("%s: got %#v %v, want %#v", in, a, err, want)
		}
	}
	for _, bad := range []string{"edge=2", "edge=true", "con=", "hqmax=-1", "ssid=0", "ssid=65535", "dim=65536",
		"ver=1", "ver=1.x", "lwm2m=v1.0", "gt=0x10", "gt=inf", "gt=NaN", "gt=1_0", "lt=1.", "lt=.5", "st=-1",
		"foo=1", "ssid", "uri", "ver", "lwm2m", "pmin=99999999999999999999"} {
		if a, err := ParseQuery(bad); err == nil {
			t.Errorf("%s: got %#v, want error", bad, a)
		}
	}
}

// Proves: ATT-04, ATT-06, ATT-07, ATT-10, DM-07
func TestValidate(t *testing.T) {
	cases := []struct {
		path string
		typ  lwm2m.Type
		q    string
		ok   bool
	}{
		{"/3/0/9", lwm2m.TypeNone, "pmin=10&pmax=10", true},  // ATT-04 1.2.1: equality allowed
		{"/3/0/9", lwm2m.TypeNone, "pmin=10&pmax=0", true},   // ATT-04: pmax 0 = no maximum
		{"/3/0/9", lwm2m.TypeNone, "pmin=10&pmax=9", false},  // ATT-04
		{"/3/0/9", lwm2m.TypeNone, "pmin&pmax=9", true},      // unset pmin: no cross check
		{"/3/0/9", lwm2m.TypeNone, "lt=10&gt=30&st=9", true}, // ATT-06: 28 < 30
		{"/3/0/9", lwm2m.TypeNone, "lt=10&gt=30&st=10", false},
		{"/3/0/9", lwm2m.TypeNone, "epmin=5&epmax=5", false}, // ATT-07 strict
		{"/3/0/9", lwm2m.TypeNone, "epmin=5&epmax=6", true},
		{"/3", lwm2m.TypeNone, "pmin=5&con=1&hqmax=3&epmin=1", true}, // ATT-10: any level
		{"/3/0/9/1", lwm2m.TypeNone, "gt=5", true},                   // ATT-10: resource instance
		{"/3/0", lwm2m.TypeNone, "gt=5", false},                      // ATT-10: not on instance
		{"/3", lwm2m.TypeNone, "st=1", false},
		{"/3/0/9", lwm2m.TypeString, "gt=5", false}, // ATT-05: numeric only
		{"/3/0/9", lwm2m.TypeUnsigned, "gt=5", true},
		{"/3/0/9", lwm2m.TypeBoolean, "edge=1", true}, // ATT-08
		{"/3/0/9", lwm2m.TypeInteger, "edge=1", false},
		{"/3/0", lwm2m.TypeNone, "edge=1", false},
		{"/3", lwm2m.TypeNone, "ver=1.1", false},   // DM-07: properties are read-only
		{"/3/0/6", lwm2m.TypeNone, "dim=2", false}, // DM-07
		{"/", lwm2m.TypeNone, "pmin=1", false},
	}
	for _, c := range cases {
		a, err := ParseQuery(c.q)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(lwm2m.MustParsePath(c.path), c.typ, a); (err == nil) != c.ok {
			t.Errorf("%s %v ?%s: err=%v, want ok=%v", c.path, c.typ, c.q, err, c.ok)
		}
	}
}

// Proves: ATT-02, DM-07
func TestApplyResolve(t *testing.T) {
	q := func(s string) Attrs {
		a, err := ParseQuery(s)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	// DM-07: a valueless attribute unsets it at that level.
	level := Apply(q("pmin=5&gt=10"), q("pmin&pmax=60&gt=11"))
	if got := level.Query(); got != "gt=11&pmax=60" {
		t.Fatalf("Apply: %s", got)
	}
	// ATT-02: defaults (/1/x/2, /1/x/3), object, instance, resource, RI;
	// the lowest level wins, unset levels fall through.
	defaults := Attrs{{"pmin", uint64(0)}, {"pmax", uint64(300)}}
	got := Resolve(defaults, q("pmin=10"), nil, q("pmax=60&gt=50"), q("lt=1"))
	if s := got.Query(); s != "pmin=10&pmax=60&gt=50&lt=1" {
		t.Fatalf("Resolve: %s", s)
	}
	if s := Resolve(defaults, q("pmin")).Query(); s != "pmin=0&pmax=300" {
		t.Fatalf("Resolve with unset: %s", s)
	}
}

func TestSince(t *testing.T) {
	for name, want := range map[string]string{"pmin": "1.0", "ver": "1.0", "epmin": "1.1", "edge": "1.2", "hqmax": "1.2", "x": ""} {
		if got := Since(name); got != want {
			t.Errorf("Since(%s) = %q want %q", name, got, want)
		}
	}
}
