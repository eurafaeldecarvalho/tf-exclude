package graph

import (
	"strings"
	"testing"

	"github.com/eurafaeldecarvalho/tf-exclude/internal/addr"
)

func mustPatterns(t *testing.T, raw ...string) []addr.Pattern {
	t.Helper()
	out := make([]addr.Pattern, len(raw))
	for i, r := range raw {
		p, err := addr.Compile(r)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = p
	}
	return out
}

func TestParseDOTBothFormats(t *testing.T) {
	detailed := `
digraph {
  "[root] local.b (expand)" -> "[root] terraform_data.a (expand)"
  "[root] terraform_data.c (expand)" -> "[root] local.b (expand)"
  "[root] terraform_data.explicit (expand)" -> "[root] terraform_data.c (expand)"
  "[root] terraform_data.a (expand)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
  "[root] provider[\"terraform.io/builtin/terraform\"] (close)" -> "[root] terraform_data.explicit (expand)"
}
`
	classic := `
digraph G {
  "terraform_data.c" -> "terraform_data.a";
  "terraform_data.explicit" -> "terraform_data.c";
}
`
	d := ParseDOT(detailed)
	if len(d) < 4 {
		t.Fatalf("detailed edges = %d, %+v", len(d), d)
	}
	c := ParseDOT(classic)
	if len(c) != 2 {
		t.Fatalf("classic edges = %+v", c)
	}
}

func TestExcludeApplyDirection(t *testing.T) {
	dot := `
"[root] local.b (expand)" -> "[root] terraform_data.a (expand)"
"[root] terraform_data.c (expand)" -> "[root] local.b (expand)"
"[root] terraform_data.explicit (expand)" -> "[root] terraform_data.c (expand)"
"[root] terraform_data.a (expand)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
`
	edges := ParseDOT(dot)
	addrs := []string{"terraform_data.a", "terraform_data.c", "terraform_data.explicit"}

	if _, err := Exclude(addrs, edges, mustPatterns(t, "terraform_data.a")); err == nil {
		t.Fatal("exclude a removes every resource and must be refused")
	}

	sel, err := Exclude(addrs, edges, mustPatterns(t, "terraform_data.explicit"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Excluded, ",") != "terraform_data.explicit" {
		t.Fatalf("exclude explicit: %+v", sel)
	}
	if strings.Join(sel.Targets, ",") != "terraform_data.a,terraform_data.c" {
		t.Fatalf("targets: %v", sel.Targets)
	}

	sel, err = Exclude(addrs, edges, mustPatterns(t, "terraform_data.c"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Excluded, ",") != "terraform_data.c,terraform_data.explicit" {
		t.Fatalf("exclude c: %+v", sel)
	}
}

func TestExcludeDestroyDirection(t *testing.T) {
	// Destroy graph edges are reversed from the apply graph.
	dot := `
"[root] terraform_data.a (destroy)" -> "[root] terraform_data.c (destroy)"
"[root] terraform_data.c (destroy)" -> "[root] terraform_data.explicit (destroy)"
"[root] terraform_data.explicit (destroy)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
`
	edges := ParseDOT(dot)
	addrs := []string{"terraform_data.a", "terraform_data.c", "terraform_data.explicit"}

	cases := []struct {
		pat      string
		excluded string
	}{
		{"terraform_data.a", "terraform_data.a"},
		{"terraform_data.c", "terraform_data.a,terraform_data.c"},
		{"terraform_data.explicit", "terraform_data.a,terraform_data.c,terraform_data.explicit"},
	}
	for _, tc := range cases {
		sel, err := Exclude(addrs, edges, mustPatterns(t, tc.pat))
		if err != nil && tc.excluded != strings.Join(addrs, ",") {
			t.Fatal(tc.pat, err)
		}
		if tc.excluded == strings.Join(addrs, ",") {
			if err == nil {
				t.Fatalf("exclude %s should refuse an empty plan", tc.pat)
			}
			continue
		}
		if strings.Join(sel.Excluded, ",") != tc.excluded {
			t.Fatalf("destroy exclude %s: got %v", tc.pat, sel.Excluded)
		}
	}
}

func TestInstanceBroadcast(t *testing.T) {
	edges := []Edge{{
		From: "terraform_data.indexed_dep",
		To:   "terraform_data.counted",
	}}
	addrs := []string{
		"terraform_data.counted[0]",
		"terraform_data.counted[1]",
		"terraform_data.indexed_dep[0]",
		"terraform_data.indexed_dep[1]",
	}
	sel, err := Exclude(addrs, edges, mustPatterns(t, "terraform_data.counted[0]"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Excluded, ",") != "terraform_data.counted[0],terraform_data.indexed_dep[0],terraform_data.indexed_dep[1]" {
		t.Fatalf("excluded %+v", sel.Excluded)
	}
	if strings.Join(sel.Targets, ",") != "terraform_data.counted[1]" {
		t.Fatalf("targets %+v", sel.Targets)
	}
}

func TestModuleInstanceBroadcast(t *testing.T) {
	edges := []Edge{{
		From: "module.instances.terraform_data.uses_leaf",
		To:   "module.instances.terraform_data.leaf",
	}}
	addrs := []string{
		"module.instances[0].terraform_data.leaf",
		"module.instances[0].terraform_data.uses_leaf",
		"module.instances[1].terraform_data.leaf",
		"module.instances[1].terraform_data.uses_leaf",
	}
	sel, err := Exclude(addrs, edges, mustPatterns(t, "module.instances[0].terraform_data.leaf"))
	if err != nil {
		t.Fatal(err)
	}
	wantEx := "module.instances[0].terraform_data.leaf,module.instances[0].terraform_data.uses_leaf,module.instances[1].terraform_data.uses_leaf"
	if strings.Join(sel.Excluded, ",") != wantEx {
		t.Fatalf("exclude leaf[0]: %v", sel.Excluded)
	}

	sel, err = Exclude(addrs, edges, mustPatterns(t, "module.instances[1]"))
	if err != nil {
		t.Fatal(err)
	}
	wantEx = "module.instances[0].terraform_data.uses_leaf,module.instances[1].terraform_data.leaf,module.instances[1].terraform_data.uses_leaf"
	if strings.Join(sel.Excluded, ",") != wantEx {
		t.Fatalf("exclude module.instances[1]: %v", sel.Excluded)
	}
	if strings.Join(sel.Targets, ",") != "module.instances[0].terraform_data.leaf" {
		t.Fatalf("targets %v", sel.Targets)
	}
}

func TestRefusals(t *testing.T) {
	addrs := []string{"terraform_data.a", "terraform_data.b"}
	edges := []Edge{{From: "terraform_data.b", To: "terraform_data.a"}}

	if _, err := Exclude(addrs, edges, mustPatterns(t, "terraform_data.missing")); err == nil {
		t.Fatal("expected unmatched address error")
	}
	if _, err := Exclude(addrs, edges, mustPatterns(t, "nope*")); err == nil {
		t.Fatal("expected unmatched glob error")
	}
	if _, err := Exclude(addrs, edges, mustPatterns(t, "terraform_data.a")); err == nil {
		t.Fatal("expected empty-plan refusal")
	}
}

func TestExpandTargetsDoesNotCloseDependents(t *testing.T) {
	addrs := []string{"terraform_data.a", "terraform_data.b[0]", "terraform_data.b[1]"}
	sel, err := ExpandTargets(addrs, mustPatterns(t, "terraform_data.b[*]"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Targets, ",") != "terraform_data.b[0],terraform_data.b[1]" {
		t.Fatalf("targets %v", sel.Targets)
	}
	if len(sel.Excluded) != 0 {
		t.Fatalf("positive target should not exclude, got %v", sel.Excluded)
	}
}

func TestOutputBridge(t *testing.T) {
	edges := []Edge{
		{From: "terraform_data.uses_deep", To: "module.wrap.output.id"},
		{From: "module.wrap.output.id", To: "module.wrap.module.inner.terraform_data.deep"},
	}
	addrs := []string{
		"module.wrap.module.inner.terraform_data.deep",
		"terraform_data.uses_deep",
		"terraform_data.other",
	}
	sel, err := Exclude(addrs, edges, mustPatterns(t, "module.wrap.module.inner.terraform_data.deep"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Excluded, ",") != "module.wrap.module.inner.terraform_data.deep,terraform_data.uses_deep" {
		t.Fatalf("excluded %v", sel.Excluded)
	}
	if strings.Join(sel.Targets, ",") != "terraform_data.other" {
		t.Fatalf("targets %v", sel.Targets)
	}
}

func TestCloseNodesDoNotConnectResources(t *testing.T) {
	dot := `
"[root] terraform_data.a (expand)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
"[root] terraform_data.b (expand)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
"[root] provider[\"terraform.io/builtin/terraform\"] (close)" -> "[root] terraform_data.a (expand)"
"[root] provider[\"terraform.io/builtin/terraform\"] (close)" -> "[root] terraform_data.b (expand)"
`
	edges := ParseDOT(dot)
	sel, err := Exclude(
		[]string{"terraform_data.a", "terraform_data.b"},
		edges,
		mustPatterns(t, "terraform_data.a"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Excluded, ",") != "terraform_data.a" {
		t.Fatalf("close nodes coupled resources: %v edges %+v", sel.Excluded, edges)
	}
}

func TestSelfLoopDoesNotExcludeSiblings(t *testing.T) {
	edges := []Edge{{From: "terraform_data.a", To: "terraform_data.a"}}
	addrs := []string{"terraform_data.a[0]", "terraform_data.a[1]"}
	sel, err := Exclude(addrs, edges, mustPatterns(t, "terraform_data.a[0]"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Excluded, ",") != "terraform_data.a[0]" {
		t.Fatalf("excluded %v", sel.Excluded)
	}
}
