package lwm2m_test

import (
	"encoding/json"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// TestPathVectors proves path parsing (Core §7.3) against spec/vectors/path.json.
func TestPathVectors(t *testing.T) {
	vs, err := vectors.Load("path")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Run(v.ID, func(t *testing.T) {
			var want struct {
				Object           *int `json:"object"`
				Instance         *int `json:"instance"`
				Resource         *int `json:"resource"`
				ResourceInstance *int `json:"resource_instance"`
			}
			if err := json.Unmarshal(v.Expected, &want); err != nil {
				t.Fatal(err)
			}
			p, err := lwm2m.ParsePath(*v.Text)
			if err != nil {
				t.Fatal(err)
			}
			levels := []*int{want.Object, want.Instance, want.Resource, want.ResourceInstance}
			n := 0
			for _, l := range levels {
				if l != nil {
					n++
				}
			}
			if p.Len() != n {
				t.Fatalf("len %d, want %d", p.Len(), n)
			}
			for i := 0; i < n; i++ {
				if int(p.ID(i)) != *levels[i] {
					t.Fatalf("level %d = %d, want %d", i, p.ID(i), *levels[i])
				}
			}
		})
	}
}

// TestPathRejects proves the path rules from Core §7.3 / T §6.4.4 that the
// vectors don't cover.
func TestPathRejects(t *testing.T) {
	for _, s := range []string{"", "//", "/3/", "/3//0", "/a", "/3/0/1/2/3", "/65535", "/-1", "/3/0x1"} {
		if _, err := lwm2m.ParsePath(s); err == nil {
			t.Errorf("ParsePath(%q) accepted", s)
		}
	}
	p := lwm2m.MustParsePath("/3/0/11/2")
	if p.String() != "/3/0/11/2" || p.Parent().String() != "/3/0/11" || !p.HasPrefix(lwm2m.MustParsePath("/3/0")) {
		t.Fatal("path helpers")
	}
	if lwm2m.MustParsePath("/3/0").Compare(lwm2m.MustParsePath("/3/0/1")) >= 0 {
		t.Fatal("ancestor must sort first")
	}
}
