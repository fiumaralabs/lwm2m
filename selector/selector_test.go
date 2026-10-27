package selector

import "testing"

func TestExistence(t *testing.T) {
	for _, tt := range []struct {
		query  string
		labels map[string]string
		want   bool
	}{
		{"", nil, true}, {" \t\n", nil, true},
		{"k", nil, false}, {"k", map[string]string{"k": ""}, true},
		{" ! k ", nil, true}, {"!k", map[string]string{"k": ""}, false},
	} {
		s, err := Parse(tt.query)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.query, err)
		}
		if got := s.Matches(tt.labels); got != tt.want {
			t.Errorf("%q on %v = %v, want %v", tt.query, tt.labels, got, tt.want)
		}
	}
}

func TestOperators(t *testing.T) {
	for _, tt := range []struct {
		query                string
		missing, empty, a, b bool
	}{
		{"k=v", false, false, false, false},
		{"k=a", false, false, true, false},
		{" k = a ", false, false, true, false},
		{"k=", false, true, false, false},
		{"k!=a", true, true, false, true},
		{"k != ", true, false, true, true},
		{"k in (a,b)", false, false, true, true},
		{" k\t in \n( a , b ) ", false, false, true, true},
		{"k in (,)", false, true, false, false},
		{"k in ( , a, )", false, true, true, false},
		{"k notin (a,b)", true, true, false, false},
		{"k notin (,)", true, false, true, true},
		{"k in (a,a)", false, false, true, false},
		{"k=a,k!=b,!other", false, false, true, false},
		{"k in (a,b), k notin (b), k", false, false, true, false},
		{"k=a,k=b", false, false, false, false},
		{"k,other", false, false, false, false},
		{"!other", true, true, true, true},
		{"!k,k!=a", true, false, false, false},
		{"ns/key-_.9=V_./-9", false, false, false, false},
	} {
		t.Run(tt.query, func(t *testing.T) {
			s, err := Parse(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			for i, labels := range []map[string]string{nil, {"k": ""}, {"k": "a"}, {"k": "b"}} {
				if got, want := s.Matches(labels), []bool{tt.missing, tt.empty, tt.a, tt.b}[i]; got != want {
					t.Errorf("labels %v: got %v, want %v", labels, got, want)
				}
			}
		})
	}
	s, err := Parse("ns/key-_.9=V_./-9")
	if err != nil || !s.Matches(map[string]string{"ns/key-_.9": "V_./-9"}) {
		t.Fatal("ASCII tokens must match", err)
	}
	if !(Selector{}).Matches(nil) {
		t.Fatal("zero selector must match all")
	}
}

func TestInvalidGrammar(t *testing.T) {
	for _, query := range []string{
		",", "k,", ",k", "k,,other", "k, ,other", "!", "!!k",
		"=a", "!=a", "k==a", "k!==a", "k>a", "k<a", "k=a=b",
		"k a", "k in", "k notin", "k in ()", "k in ( )", "k notin ()",
		"k in (a", "k in a)", "k in ((a))", "k in (a,(b))", "k in (a))",
		"k in (a)junk", "k in(a),", "k in (a b)", "k in (a;b)", "k in (a=)",
		"k in (a),,x", "k in (a) x", "k inx (a)", "k not in (a)",
		"k(a)", "k)", "(k", "k=💥", "ké=a", "k:$=a", "k=a b",
		"!k=a", "!k in (a)", "k in (a),!", "k in (a),=b", "k=\"a\"",
	} {
		t.Run(query, func(t *testing.T) {
			if _, err := Parse(query); err == nil {
				t.Fatalf("accepted invalid selector %q", query)
			}
		})
	}
}
