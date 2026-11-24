// Package admission contains proposed contract fixtures, not hook implementation tests.
package admission

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

type pathFixture struct {
	Name          string `json:"name"`
	Operation     string `json:"operation"`
	Authenticated bool   `json:"authenticated"`
	Admitted      bool   `json:"admitted"`
	AutoProvision bool   `json:"auto_provision"`
	Consult       bool   `json:"consult"`
}

func TestQualifyingPaths(t *testing.T) {
	data, err := os.ReadFile("testdata/paths.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []pathFixture
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	want := map[string]pathFixture{
		"first register":          {"first register", "Register", true, false, false, true},
		"first update":            {"first update", "Update", true, false, false, true},
		"first send":              {"first send", "Send", true, false, false, true},
		"first notify":            {"first notify", "Notify", true, false, false, true},
		"auto-provision register": {"auto-provision register", "Register", true, false, true, true},
		"auto-provision update":   {"auto-provision update", "Update", true, false, true, true},
		"auto-provision send":     {"auto-provision send", "Send", true, false, true, true},
		"auto-provision notify":   {"auto-provision notify", "Notify", true, false, true, true},
		"already admitted":        {"already admitted", "Register", true, true, false, false},
		"admitted update":         {"admitted update", "Update", true, true, false, false},
		"admitted send":           {"admitted send", "Send", true, true, false, false},
		"admitted notify":         {"admitted notify", "Notify", true, true, false, false},
		"bootstrap":               {"bootstrap", "Bootstrap", true, false, false, false},
		"security":                {"security", "Security", true, false, false, false},
		"unauthenticated":         {"unauthenticated", "Register", false, false, false, false},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			expected, ok := want[c.Name]
			if !ok || seen[c.Name] || c != expected {
				t.Errorf("unexpected or duplicate fixture: %+v", c)
			}
			seen[c.Name] = true
		})
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing fixture %q", name)
		}
	}
}

// The hook is absent today. Replace this guard with a real default-off and
// no-outbound-call test when admission configuration is introduced.
func TestDefaultConfigHasNoAdmissionHook(t *testing.T) {
	typ := reflect.TypeOf(server.Config{})
	for i := 0; i < typ.NumField(); i++ {
		if strings.Contains(strings.ToLower(typ.Field(i).Name), "admission") {
			t.Errorf("admission configuration %s added: test actual defaults and outbound calls", typ.Field(i).Name)
		}
	}
}
