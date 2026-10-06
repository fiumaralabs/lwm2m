package lwm2m_test

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSpecCoverage ties the spec to the tests. Every requirement ID defined in
// spec/standards.md and spec/standards-1.2.md must be claimed by at least one
// test through a comment of the form
//
//	// Proves: REG-04, SEC-06
//
// IDs not yet proven are listed in spec/coverage-pending.txt. The test fails
// when an ID is neither proven nor pending, when a pending ID has become
// proven (remove it from the list), or when a test claims an unknown ID.
// The spec is fully implemented when coverage-pending.txt is empty.
func TestSpecCoverage(t *testing.T) {
	defined := specIDs(t)
	proven := provenIDs(t)
	pending := pendingIDs(t)

	for id := range proven {
		if !defined[id] {
			t.Errorf("a test claims unknown requirement %s", id)
		}
	}
	var missing, stale []string
	for id := range defined {
		switch {
		case proven[id] && pending[id]:
			stale = append(stale, id)
		case !proven[id] && !pending[id]:
			missing = append(missing, id)
		}
	}
	for id := range pending {
		if !defined[id] {
			t.Errorf("coverage-pending.txt lists unknown requirement %s", id)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("requirements with no proving test and not pending: %s", strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("proven requirements still in coverage-pending.txt (remove them): %s", strings.Join(stale, ", "))
	}
	t.Logf("requirements: %d defined, %d proven, %d pending", len(defined), len(defined)-len(pending), len(pending))
}

var (
	specRowRE = regexp.MustCompile(`^\|\s*([A-Z][A-Z0-9]*-\d+[a-z]?)\s*\|`)
	provesRE  = regexp.MustCompile(`//\s*Proves:\s*(.+)$`)
	idRE      = regexp.MustCompile(`[A-Z][A-Z0-9]*-\d+[a-z]?`)
)

func specIDs(t *testing.T) map[string]bool {
	ids := map[string]bool{}
	for _, f := range []string{"spec/standards.md", "spec/standards-1.2.md"} {
		eachLine(t, f, func(line string) {
			if m := specRowRE.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], "A-") {
				ids[m[1]] = true
			}
		})
	}
	if len(ids) == 0 {
		t.Fatal("no requirement IDs found in spec")
	}
	return ids
}

func provenIDs(t *testing.T) map[string]bool {
	ids := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "spec" || strings.HasPrefix(d.Name(), ".")) && path != "." {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, "_test.go") && path != "spec_coverage_test.go" {
			eachLine(t, path, func(line string) {
				if m := provesRE.FindStringSubmatch(line); m != nil {
					for _, id := range idRE.FindAllString(m[1], -1) {
						ids[id] = true
					}
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func pendingIDs(t *testing.T) map[string]bool {
	ids := map[string]bool{}
	eachLine(t, "spec/coverage-pending.txt", func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return
		}
		ids[strings.Fields(line)[0]] = true
	})
	return ids
}

func eachLine(t *testing.T, path string, fn func(string)) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fn(sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
