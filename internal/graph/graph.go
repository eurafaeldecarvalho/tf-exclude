// Package graph turns a `terraform graph` dot file into the set of resource
// addresses excluded by OpenTofu's -exclude rules.
//
// terraform graph points each edge from the dependent node to the node it
// depends on. On a normal plan that means "B -> A" when B depends on A, so
// the nodes that can reach an excluded resource are the ones -target would
// pull back in. On a destroy plan Terraform reverses those edges, because
// destroy -target pulls dependents (they must be destroyed first) rather than
// dependencies. Walking the edges Terraform actually emitted implements both
// cases with one rule: also exclude every resource that can reach a matched
// resource.
//
// The CLI graph is not instance-expanded. An edge between configuration
// addresses applies to every instance of those resources, including across
// count/for_each module instances. That matches OpenTofu -exclude, which uses
// the same unexpanded edges.
package graph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/eurafaeldecarvalho/tf-exclude/internal/addr"
)

// Edge is a dependency edge: From depends on To.
type Edge struct {
	From string
	To   string
}

var edgeRE = regexp.MustCompile(`"((?:\\.|[^"\\])*)"\s*->\s*"((?:\\.|[^"\\])*)"`)

// ParseDOT extracts dependency edges from terraform graph output.
// Node ids are normalized to configuration addresses: the [root] prefix and a
// trailing (expand), (destroy), (orphan), or (close) suffix are removed.
// Self-edges created by that normalization are dropped.
func ParseDOT(dot string) []Edge {
	matches := edgeRE.FindAllStringSubmatch(dot, -1)
	out := make([]Edge, 0, len(matches))
	seen := make(map[Edge]struct{}, len(matches))
	for _, m := range matches {
		// "(close)" nodes order provider/module teardown. Stripping that suffix
		// merges them into the provider node and makes every resource look
		// dependent on every other resource. They are not configuration edges.
		if strings.Contains(m[1], "(close)") || strings.Contains(m[2], "(close)") {
			continue
		}
		from := Normalize(unescape(m[1]))
		to := Normalize(unescape(m[2]))
		if from == "" || to == "" || from == to {
			continue
		}
		e := Edge{From: from, To: to}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

// Normalize converts one graph node id to a configuration address.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[root] ")
	for {
		i := strings.LastIndex(s, " (")
		if i < 0 || !strings.HasSuffix(s, ")") {
			break
		}
		mid := s[i+2 : len(s)-1]
		if mid == "" || strings.ContainsAny(mid, " \t") || !isLowerWord(mid) {
			break
		}
		s = s[:i]
	}
	return s
}

func isLowerWord(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func unescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Selection is the result of applying patterns to a concrete address list.
type Selection struct {
	// Resources is every managed address considered, sorted.
	Resources []string
	// Matched are addresses selected directly by a pattern, sorted.
	Matched []string
	// Excluded is Matched plus transitive dependents from the graph, sorted.
	// In positive -target mode, Excluded is empty and Targets is Matched.
	Excluded []string
	// Targets is Resources minus Excluded, sorted. These are the -target values.
	Targets []string
}

// Exclude selects every address except pattern matches and resources that can
// reach a match by following dependency edges (including through locals,
// variables, and outputs).
func Exclude(addresses []string, edges []Edge, patterns []addr.Pattern) (Selection, error) {
	return selectAddresses(addresses, edges, patterns, true)
}

// ExpandTargets returns the addresses matched by positive -target patterns.
// Dependencies are not added here; terraform -target pulls those itself.
func ExpandTargets(addresses []string, patterns []addr.Pattern) (Selection, error) {
	return selectAddresses(addresses, nil, patterns, false)
}

func selectAddresses(addresses []string, edges []Edge, patterns []addr.Pattern, exclude bool) (Selection, error) {
	if len(patterns) == 0 {
		return Selection{}, fmt.Errorf("no address pattern")
	}
	resources := uniqueSorted(addresses)
	if len(resources) == 0 {
		return Selection{}, fmt.Errorf("the plan contains no managed resources")
	}

	matchedBy := make([][]string, len(patterns))
	var matched []string
	seenMatch := map[string]struct{}{}
	for i, p := range patterns {
		for _, a := range resources {
			if !p.Match(a) {
				continue
			}
			matchedBy[i] = append(matchedBy[i], a)
			if _, ok := seenMatch[a]; ok {
				continue
			}
			seenMatch[a] = struct{}{}
			matched = append(matched, a)
		}
		if len(matchedBy[i]) == 0 {
			kind := "address"
			if p.IsGlob() {
				kind = "glob"
			}
			return Selection{}, fmt.Errorf("%s %q matched no resource in the plan", kind, p.String())
		}
	}
	sort.Strings(matched)

	sel := Selection{Resources: resources, Matched: matched}
	if !exclude {
		sel.Targets = append([]string(nil), matched...)
		return sel, nil
	}

	excluded := map[string]struct{}{}
	for _, a := range matched {
		excluded[a] = struct{}{}
	}
	dependentKeys := dependentResourceKeys(matched, edges)
	for _, a := range resources {
		parsed, err := addr.Parse(a)
		if err != nil || !parsed.HasResource {
			continue
		}
		if _, ok := dependentKeys[parsed.Key()]; ok {
			excluded[a] = struct{}{}
		}
	}
	if len(excluded) >= len(resources) {
		return Selection{}, fmt.Errorf("exclusion removes every resource (%d, including dependents); refusing to run an empty plan", len(resources))
	}
	for _, a := range resources {
		if _, ok := excluded[a]; ok {
			sel.Excluded = append(sel.Excluded, a)
			continue
		}
		sel.Targets = append(sel.Targets, a)
	}
	return sel, nil
}

// dependentResourceKeys returns configuration keys that transitively depend on
// any matched address. Sibling instances of a matched resource are not included
// unless a real edge reaches that resource key again through other nodes.
func dependentResourceKeys(matched []string, edges []Edge) map[string]struct{} {
	rev := map[string][]string{}
	for _, e := range edges {
		if e.From == "" || e.To == "" || e.From == e.To {
			continue
		}
		rev[e.To] = append(rev[e.To], e.From)
	}
	seeds := map[string]struct{}{}
	for _, a := range matched {
		parsed, err := addr.Parse(a)
		if err != nil || !parsed.HasResource {
			continue
		}
		seeds[parsed.Key()] = struct{}{}
	}
	out := map[string]struct{}{}
	visited := map[string]struct{}{}
	var walk func(string)
	walk = func(n string) {
		for _, dep := range rev[n] {
			if _, ok := visited[dep]; ok {
				continue
			}
			visited[dep] = struct{}{}
			if addr.IsGraphResource(dep) {
				parsed, err := addr.Parse(dep)
				if err == nil {
					out[parsed.Key()] = struct{}{}
				}
			}
			walk(dep)
		}
	}
	for seed := range seeds {
		walk(seed)
	}
	// A matched resource's own key is a seed, not automatically a dependent.
	// Drop it when the only way it was recorded is irrelevant; keep it when a
	// cycle through another node added it, which means every instance depends
	// on the matched one. Cycles that are only the seed itself were not walked.
	return out
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
