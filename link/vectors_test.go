package link

import (
	"reflect"
	"slices"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/attr"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

type jsonParam struct {
	Name   string  `json:"name"`
	Value  *string `json:"value"`
	Quoted bool    `json:"quoted"`
}

type jsonLink struct {
	URI        string      `json:"uri"`
	Attributes []jsonParam `json:"attributes"`
}

func expectedLinks(t *testing.T, v vectors.Vector) []Link {
	t.Helper()
	var js []jsonLink
	if err := v.ExpectedObject("links", &js); err != nil {
		t.Fatal(err)
	}
	var out []Link
	for _, j := range js {
		l := Link{URI: j.URI}
		for _, a := range j.Attributes {
			p := Param{Name: a.Name, Quoted: a.Quoted}
			if a.Value != nil {
				p.Value, p.HasValue = *a.Value, true
			}
			l.Params = append(l.Params, p)
		}
		out = append(out, l)
	}
	return out
}

// tolerated lists Leshan "error" vectors that we accept on input because
// the spec permits accepting them. The test fails if one stops parsing or
// its id disappears from the vectors.
var tolerated = map[string]string{
	"lf-leshan-invalid-24": "whitespace after ',' between links: every Core example uses it (Core 1.2.2 §6.1.7.3, §6.2.1, §6.3.2); A-20 says skip it",
	"lf-leshan-lwm2m-43":   "dim=256: Core 1.2.2 Tbl 7.3.1-1 gives dim 0..65535 (ATT-11); Leshan still caps at 255",
}

// decode parses the payload and types each link's LwM2M attributes, which
// is what the server does with any link-format payload.
func decode(s string) ([]Link, error) {
	links, err := Parse(s)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if _, err := l.Attrs(); err != nil {
			return nil, err
		}
	}
	return links, nil
}

// Proves: DM-04, BS-05, BS-27, REG-07, REG-10
func TestVectors(t *testing.T) {
	var all []vectors.Vector
	for _, name := range []string{"link_format", "spec-examples"} {
		vs, err := vectors.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if v.ContentFormat != nil && *v.ContentFormat == 40 {
				all = append(all, v)
			}
		}
	}
	seen := map[string]bool{}
	var nDecode, nEncode, nError int
	for _, v := range all {
		seen[v.ID] = true
		t.Run(v.ID, func(t *testing.T) {
			text := *v.Text
			if reason, ok := tolerated[v.ID]; ok {
				if !v.IsError() {
					t.Fatalf("tolerated vector is no longer an error vector")
				}
				if _, err := decode(text); err != nil {
					t.Fatalf("tolerated (%s) but rejected: %v", reason, err)
				}
				return
			}
			if v.IsError() {
				nError++
				if links, err := decode(text); err == nil {
					t.Fatalf("decoded %q to %+v, want error", text, links)
				}
				return
			}
			want := expectedLinks(t, v)
			if v.Decodes() {
				nDecode++
				got, err := decode(text)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("decode %q\n got %+v\nwant %+v", text, got, want)
				}
			}
			if v.Encodes() {
				nEncode++
				if got := Encode(want); got != text {
					t.Fatalf("encode\n got %q\nwant %q", got, text)
				}
			}
		})
	}
	for id := range tolerated {
		if !seen[id] {
			t.Errorf("tolerated id %s not found in vectors", id)
		}
	}
	t.Logf("%d vectors: %d decode, %d encode, %d must-fail, %d tolerated", len(all), nDecode, nEncode, nError, len(tolerated))
}

// Proves: REG-07, REG-08, REG-09, REG-10
func TestParseRegistration(t *testing.T) {
	cases := []struct {
		name, in string
		want     Registration
	}{
		{"zephyr 1.1 (zephyr-client-profile §1.3)", "</>;ct=112,</1>;ver=1.1,</1/0>,</3>;ver=1.0,</3/0>,</5/0>,</3303/0>",
			Registration{Root: "/", ContentFormats: cf(112), Objects: []Object{
				{1, "1.1", []uint16{0}}, {3, "1.0", []uint16{0}}, {5, "", []uint16{0}}, {3303, "", []uint16{0}}}}},
		{"quoted ct list (REG-10, T12)", `</>;ct="110 112 60", </1/0>,</5>`,
			Registration{Root: "/", ContentFormats: cf(110, 112, 60), Objects: []Object{{1, "", []uint16{0}}, {5, "", nil}}}},
		{"no root link (T11)", "</1/0>,</1/1>,</3/0>",
			Registration{Root: "/", Objects: []Object{{1, "", []uint16{0, 1}}, {3, "", []uint16{0}}}}},
		{"ver on instance link (Core §7.2.2)", "</44/0>;ver=2.2", Registration{Root: "/", Objects: []Object{{44, "2.2", []uint16{0}}}}},
		{"quoted ver (T13)", `</6>;ver="2.0",</6/0>`, Registration{Root: "/", Objects: []Object{{6, "2.0", []uint16{0}}}}},
		{"alternate path (T §6.4.1)", `</lwm2m>;rt="oma.lwm2m";ct=110, </lwm2m/1/0>,</lwm2m/5>`,
			Registration{Root: "/lwm2m", ContentFormats: cf(110), Objects: []Object{{1, "", []uint16{0}}, {5, "", nil}}}},
		{"alt-path root, unprefixed objects (T16)", `</alt>;rt="oma.lwm2m",</3/0>`, Registration{Root: "/alt", Objects: []Object{{3, "", []uint16{0}}}}},
		{"excluded /0 /21 /23 /24 (REG-09, T15, A-17)", "</0/0>,</1/0>,</21/0>,</23/0>,</24/0>", Registration{Root: "/", Objects: []Object{{1, "", []uint16{0}}}}},
		{"unknown object and params ignored (REG-07, REG-08)", "</1/0>;foo=bar;obs,</32769/0>;ver=1.3,</1/0/1>,</nope>",
			Registration{Root: "/", Objects: []Object{{1, "", []uint16{0}}, {32769, "1.3", []uint16{0}}}}},
		{"trailing slash (A-20)", "</1/0/>,</3/>", Registration{Root: "/", Objects: []Object{{1, "", []uint16{0}}, {3, "", nil}}}},
		{"duplicates merge", "</1/0>,</1/0>,</1>", Registration{Root: "/", Objects: []Object{{1, "", []uint16{0}}}}},
	}
	for _, c := range cases {
		links, err := Parse(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, err := ParseRegistration(links)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	for _, bad := range []string{"</3>;ver=x", "</>;ct=json"} {
		links, _ := Parse(bad)
		if _, err := ParseRegistration(links); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func cf(n ...int) (out []lwm2m.ContentFormat) {
	for _, x := range n {
		out = append(out, lwm2m.ContentFormat(x))
	}
	return out
}

// Proves: DM-04, BS-05, ATT-11
func TestParseDiscover(t *testing.T) {
	links, err := Parse(`</>;lwm2m=1.1,</0/0>;ssid=101;uri="coaps://s1", </0/1/>, </1/0>;ssid=101,</3/0/7>;dim=2;pmin=10;gt=50;lt=42.2;edge=1`)
	if err != nil {
		t.Fatal(err)
	}
	es, err := ParseDiscover(links, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		s := e.Path.String()
		for _, a := range e.Attrs {
			s += ";" + a.Name + "=" + attr.FormatValue(a.Value)
		}
		got = append(got, s)
	}
	want := []string{"/;lwm2m=1.1", "/0/0;ssid=101;uri=coaps://s1", "/0/1", "/1/0;ssid=101", "/3/0/7;dim=2;pmin=10;gt=50;lt=42.2;edge=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Alternate path is stripped.
	links, _ = Parse("</rp/6>;ver=2.0,</rp/6/0>")
	if es, err := ParseDiscover(links, "/rp"); err != nil || es[0].Path.String() != "/6" || es[1].Path.String() != "/6/0" {
		t.Fatalf("alt path: %v %v", es, err)
	}
	for _, bad := range []string{"</foo>", "</0/0>;ssid=0", "</0/0>;ssid=65535", "</3/0/7>;pmin", "</3>;ver=1", "</3/0/1>;edge=2", "</3/0/1>;dim=65536"} {
		links, err := Parse(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseDiscover(links, ""); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// RFC 6690 §2 grammar details the vectors do not reach.
func TestParseSyntax(t *testing.T) {
	ok := map[string][]Link{
		"</a>;x;y=1":         {{"/a", []Param{{"x", "", false, false}, {"y", "1", true, false}}}},
		"</a>\r\n</b>":       {{"/a", nil}, {"/b", nil}},
		"</a>,\n\t</b>":      {{"/a", nil}, {"/b", nil}},
		`</a>;t="x\"y"`:      {{"/a", []Param{{"t", `x"y`, true, true}}}},
		`</a>;t=""`:          {{"/a", []Param{{"t", "", true, true}}}},
		"lwm2m=1.0;x,</0/0>": {{"/", []Param{{"lwm2m", "1.0", true, false}, {"x", "", false, false}}}, {"/0/0", nil}},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"</a>  </b>", "</a>,", "</a>\n", "</a>;t=\"\x01\"", "ver=1.0,</0>", "</a>;t=x,y"} {
		if l, err := Parse(bad); err == nil {
			t.Errorf("%q: got %+v, want error", bad, l)
		}
	}
}

// The encoder never emits a value that is not RFC 6690: values that are
// not a ptoken are quoted, and '"' '\' escaped inside quotes.
func TestEncodeStrict(t *testing.T) {
	got := Encode([]Link{{"/", []Param{{"ct", "110 112", true, false}, {"t", `a"b\c`, true, true}, {"e", "", true, false}}}})
	if want := `</>;ct="110 112";t="a\"b\\c";e=""`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
