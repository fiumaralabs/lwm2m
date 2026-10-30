package selector

import "testing"

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", " \t\n", "foo", "!foo", "env=prod", "env!=prod", "tier in (frontend,backend)", "tier notin (frontend)", "a,,b", "a="} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		selector, err := Parse(s)
		if err == nil {
			_ = selector.Matches(map[string]string{"env": "prod", "tier": "frontend"})
		}
	})
}
