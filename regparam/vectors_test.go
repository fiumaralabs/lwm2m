package regparam

import (
	"reflect"
	"slices"
	"testing"

	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func TestVectors(t *testing.T) {
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, v := range vs {
		switch v.ExpectedShape() {
		case "request":
			n++
			var want struct {
				Method string             `json:"method"`
				Path   []string           `json:"path"`
				Query  map[string]*string `json:"query"`
			}
			if err := v.ExpectedObject("request", &want); err != nil {
				t.Fatal(err)
			}
			got, err := ParseRequest(*v.Text)
			if err != nil {
				t.Fatalf("%s: %v", v.ID, err)
			}
			if !reflect.DeepEqual(got, Request{want.Method, want.Path, want.Query}) {
				t.Errorf("%s: got %+v want %+v", v.ID, got, want)
			}
		case "profile_ids":
			n++
			var want []struct {
				Kind    string `json:"kind"`
				Suite   uint8  `json:"suite"`
				HashHex string `json:"hash_hex"`
			}
			if err := v.ExpectedObject("profile_ids", &want); err != nil {
				t.Fatal(err)
			}
			got, err := ParseProfileIDs(*v.Text)
			if err != nil {
				t.Fatalf("%s: %v", v.ID, err)
			}
			if len(got) != len(want) {
				t.Fatalf("%s: got %+v", v.ID, got)
			}
			for i, w := range want {
				if got[i] != (ProfileID{Kind: w.Kind, Suite: w.Suite, Hash: w.HashHex}) {
					t.Errorf("%s: got %+v want %+v", v.ID, got[i], w)
				}
			}
		}
	}
	t.Logf("%d vectors", n)
}

// Proves: REG-18
func TestParseBinding(t *testing.T) {
	ok := []struct {
		b, ver string
		want   Binding
	}{
		// 1.0, C10 §5.3.1.1 Table 8.
		{"U", "1.0", Binding{"U", false}}, {"UQ", "1.0", Binding{"U", true}},
		{"S", "1.0", Binding{"S", false}}, {"SQ", "1.0", Binding{"S", true}},
		{"US", "1.0", Binding{"US", false}}, {"UQS", "1.0", Binding{"US", true}},
		// 1.1: U T S N; 1.2 adds M H (Core §6.2.1.2).
		{"T", "1.1", Binding{"T", false}}, {"UT", "1.1", Binding{"UT", false}}, // T5
		{"UN", "1.1", Binding{"UN", false}}, {"N", "1.1", Binding{"N", false}},
		{"UTSN", "1.1", Binding{"UTSN", false}},
		{"M", "1.2", Binding{"M", false}}, {"UH", "1.2", Binding{"UH", false}}, {"MH", "1.2.1", Binding{"MH", false}},
		// T3: legacy queue inside b on 1.1+.
		{"UQ", "1.1", Binding{"U", true}}, {"UTQ", "1.2", Binding{"UT", true}}, {"UQS", "1.1", Binding{"US", true}},
	}
	for _, c := range ok {
		got, err := ParseBinding(c.b, c.ver)
		if err != nil || got != c.want {
			t.Errorf("b=%s lwm2m=%s: got %+v %v want %+v", c.b, c.ver, got, err, c.want)
		}
	}
	bad := [][2]string{
		{"UQSQ", "1.0"}, {"USQ", "1.0"}, {"T", "1.0"}, {"UN", "1.0"}, // REG-18: 1.0 table only
		{"M", "1.1"}, {"H", "1.1"}, {"UU", "1.1"}, {"", "1.1"}, {"Q", "1.1"}, {"u", "1.1"}, {"UQQ", "1.2"}, {"X", "1.2"},
		{"T", "2.0"},
	}
	for _, c := range bad {
		if got, err := ParseBinding(c[0], c[1]); err == nil {
			t.Errorf("b=%q lwm2m=%s: got %+v, want error", c[0], c[1], got)
		}
	}
}

func TestParseRegister(t *testing.T) {
	p, err := ParseRegister([]string{"lwm2m=1.1", "ep=node", "lt=86400", "b=U", "Q", "sms=12345678", `pid="6:1234abcd,oma:dev-1,v:x"`})
	if err != nil {
		t.Fatal(err)
	}
	if *p.Endpoint != "node" || *p.Lifetime != 86400 || *p.Version != "1.1" || *p.Binding != (Binding{"U", false}) || !p.Queue || *p.SMS != "12345678" {
		t.Fatalf("got %+v", p)
	}
	wantPID := []ProfileID{{Kind: "dynamic", Suite: 6, Hash: "1234abcd"}, {Kind: "oma", Value: "dev-1"}, {Kind: "v", Value: "x"}}
	if !slices.Equal(p.ProfileIDs, wantPID) {
		t.Fatalf("pid %+v", p.ProfileIDs)
	}
	// 1.0 queue inside b; "Q=" same as "Q" (T2); ep optional from 1.1 (REG-02).
	if p, err := ParseRegister([]string{"ep=a", "lt=60", "lwm2m=1.0", "b=UQ"}); err != nil || !p.Queue {
		t.Fatalf("1.0 UQ: %+v %v", p, err)
	}
	if p, err := ParseRegister([]string{"lt=60", "lwm2m=1.2", "Q="}); err != nil || !p.Queue || p.Endpoint != nil || p.Binding != nil {
		t.Fatalf("Q=: %+v %v", p, err)
	}
	for _, bad := range [][]string{
		{"ep=a", "lt=60"},                       // REG-03: lwm2m required
		{"ep=a", "lwm2m=1.1"},                   // REG-03: lt required
		{"lt=60", "lwm2m=1.0"},                  // REG-02: ep required for 1.0
		{"ep=a", "lt=60", "lwm2m=1.1", "apn=x"}, // REG-06: unknown parameter
		{"ep=a", "lt=60", "lt=61", "lwm2m=1.1"}, // T1: duplicate
		{"ep=a", "lt=-1", "lwm2m=1.1"},          // lt is 1*DIGIT
		{"ep=a", "lt=+1", "lwm2m=1.1"},
		{"ep=", "lt=60", "lwm2m=1.1"},
		{"ep=a", "lt=60", "lwm2m=1.1", "Q=1"},
		{"ep=a", "lt=60", "lwm2m=1.1", "b=M"},
		{"ep=a", "lt=60", "lwm2m=1.2", "pid=6:ABCD"}, // PROF-02: lowercase hex
		{"ep=a", "lt=60", "lwm2m=1.1", ""},
	} {
		if p, err := ParseRegister(bad); err == nil {
			t.Errorf("%q: got %+v, want error", bad, p)
		}
	}
}

// Proves: PROF-09
func TestParseUpdate(t *testing.T) {
	p, err := ParseUpdate(nil, "1.1")
	if err != nil || p.Lifetime != nil || p.Binding != nil || p.Queue {
		t.Fatalf("empty Update: %+v %v", p, err)
	}
	p, err = ParseUpdate([]string{"lt=600000", "b=UQ", "pid=6:ab"}, "1.0") // A-9: pid accepted on Update
	if err != nil || *p.Lifetime != 600000 || !p.Queue || len(p.ProfileIDs) != 1 {
		t.Fatalf("got %+v %v", p, err)
	}
	for _, bad := range [][]string{{"ep=x"}, {"lwm2m=1.1"}, {"lt=x"}} {
		if _, err := ParseUpdate(bad, "1.1"); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// Proves: PROF-02
func TestParseProfileIDs(t *testing.T) {
	for _, bad := range []string{"", "6", "6:", ":ab", "x:ab", "256:ab", "6:12G4", "oma:a_b", `"6:ab`, `"6:ab,"`, "-1:ab", "oma:", "v:", "vendor:x", "v:a.b", `"6:ab,,v:x"`, "6:ab cd", "+6:ab"} {
		if got, err := ParseProfileIDs(bad); err == nil {
			t.Errorf("%q: got %+v want error", bad, got)
		}
	}
}

func TestParseRequest(t *testing.T) {
	for _, bad := range []string{"POST", "POST rd", "/rd", "POST /rd?a&a"} {
		if _, err := ParseRequest(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
