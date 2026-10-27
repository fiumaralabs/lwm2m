// Package selector parses comma-conjoined label predicates.
package selector

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type requirement struct {
	key, operator string
	values        []string
}

// Selector is a conjunction of requirements. Its zero value matches all labels.
type Selector struct{ requirements []requirement }

const token = `[A-Za-z0-9_./-]`

var (
	existence = regexp.MustCompile(`^(!\s*)?(` + token + `+)$`)
	equality  = regexp.MustCompile(`^(` + token + `+)\s*(!=|=)\s*(` + token + `*)$`)
	set       = regexp.MustCompile(`(?s)^(` + token + `+)\s+(in|notin)\s*\((.*)\)$`)
	value     = regexp.MustCompile(`^` + token + `*$`)
)

// Parse parses equality, membership and existence predicates joined by commas.
// Empty values are valid; empty sets and empty requirements are not.
func Parse(input string) (Selector, error) {
	s := Selector{}
	if strings.TrimSpace(input) == "" {
		return s, nil
	}
	depth, start := 0, 0
	var parts []string
	for i, ch := range input {
		switch ch {
		case '(':
			depth++
			if depth > 1 {
				return Selector{}, fmt.Errorf("nested parentheses")
			}
		case ')':
			depth--
			if depth < 0 {
				return Selector{}, fmt.Errorf("unbalanced parentheses")
			}
		case ',':
			if depth == 0 {
				parts = append(parts, input[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return Selector{}, fmt.Errorf("unbalanced parentheses")
	}
	parts = append(parts, input[start:])
	for _, part := range parts {
		part = strings.TrimSpace(part)
		r := requirement{}
		switch {
		case equality.MatchString(part):
			m := equality.FindStringSubmatch(part)
			r = requirement{m[1], m[2], []string{m[3]}}
		case set.MatchString(part):
			m := set.FindStringSubmatch(part)
			if strings.TrimSpace(m[3]) == "" {
				return Selector{}, fmt.Errorf("empty set")
			}
			r = requirement{m[1], m[2], strings.Split(m[3], ",")}
			for i, v := range r.values {
				r.values[i] = strings.TrimSpace(v)
				if !value.MatchString(r.values[i]) {
					return Selector{}, fmt.Errorf("invalid set value %q", v)
				}
			}
		case existence.MatchString(part):
			m := existence.FindStringSubmatch(part)
			r = requirement{key: m[2], operator: "exists"}
			if m[1] != "" {
				r.operator = "missing"
			}
		default:
			return Selector{}, fmt.Errorf("invalid requirement %q", part)
		}
		s.requirements = append(s.requirements, r)
	}
	return s, nil
}

// Matches reports whether labels satisfy every requirement, without mutating labels.
func (s Selector) Matches(labels map[string]string) bool {
	for _, r := range s.requirements {
		v, present := labels[r.key]
		match := present
		switch r.operator {
		case "missing":
			match = !present
		case "=", "in":
			match = present && slices.Contains(r.values, v)
		case "!=", "notin":
			match = !present || !slices.Contains(r.values, v)
		}
		if !match {
			return false
		}
	}
	return true
}
