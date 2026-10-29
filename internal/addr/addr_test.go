package addr

import "testing"

func TestParseRoundTrip(t *testing.T) {
	samples := []string{
		"aws_instance.web",
		"aws_instance.web[0]",
		"aws_instance.web[10]",
		`aws_instance.web["k"]`,
		`aws_instance.web["a.b"]`,
		`aws_instance.web["a\"b"]`,
		"module.a.aws_instance.web",
		"module.a.module.b.res.name",
		`module.a.module.b.res.name["k"]`,
		"module.a[0].module.b[1].res.name[2]",
		`module.foreach["red"].terraform_data.item`,
		"module.legacy",
		"module.a.module.b",
		"module.instances[0]",
		`module.foreach["a.b"]`,
	}
	for _, s := range samples {
		a, err := Parse(s)
		if err != nil {
			t.Fatalf("parse %s: %v", s, err)
		}
		if got := a.String(); got != s {
			t.Fatalf("round trip %s -> %s", s, got)
		}
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"",
		"module.",
		"aws_instance",
		"aws_instance.",
		"aws_instance.web[01]",
		"aws_instance.web[-1]",
		"aws_instance.web[1",
		`aws_instance.web["unterminated]`,
		"1bad.name",
		"module.a.aws_instance.web.extra",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Fatalf("expected error for %q", s)
		}
	}
}

func TestKeyStripsIndexes(t *testing.T) {
	a, err := Parse(`module.instances[0].module.child["k"].terraform_data.leaf[2]`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := a.Key(), "module.instances.module.child.terraform_data.leaf"; got != want {
		t.Fatalf("key = %s, want %s", got, want)
	}
}

func TestOpenTofuMatch(t *testing.T) {
	cases := []struct {
		pat  string
		addr string
		ok   bool
	}{
		{"aws_instance.web", "aws_instance.web", true},
		{"aws_instance.web", "aws_instance.web[0]", true},
		{"aws_instance.web", `aws_instance.web["a"]`, true},
		{"aws_instance.web", "aws_instance.web2", false},
		{"aws_instance.web", "aws_instance.web_other", false},
		{"aws_instance.web[0]", "aws_instance.web[0]", true},
		{"aws_instance.web[0]", "aws_instance.web[1]", false},
		{"aws_instance.web[1]", "aws_instance.web[10]", false},
		{"aws_instance.web[10]", "aws_instance.web[10]", true},
		{`aws_instance.web["a"]`, `aws_instance.web["a"]`, true},
		{`aws_instance.web["a"]`, `aws_instance.web["a.b"]`, false},
		{`aws_instance.web["a.b"]`, `aws_instance.web["a.b"]`, true},
		{"module.a", "module.a.aws_instance.web", true},
		{"module.a", "module.a[0].aws_instance.web", true},
		{"module.a", `module.a["k"].aws_instance.web[1]`, true},
		{"module.a", "module.a.module.b.res.name", true},
		{"module.a", "module.ab.aws_instance.web", false},
		{"module.a[0]", "module.a[0].aws_instance.web", true},
		{"module.a[0]", "module.a[1].aws_instance.web", false},
		{"module.a[0]", "module.a.aws_instance.web", false},
		{"module.a.module.b", "module.a.module.b.res.name", true},
		{"module.a.module.b", "module.a[0].module.b[1].res.name[2]", true},
		{`module.a.module.b.res.name["k"]`, `module.a.module.b.res.name["k"]`, true},
		{`module.a.module.b.res.name["k"]`, `module.a.module.b.res.name["other"]`, false},
		{"module.a.module.b.res.name", `module.a.module.b.res.name["k"]`, true},
		{"module.a.module.b.res.name", `module.a[0].module.b.res.name["k"]`, true},
		{"module.instances.terraform_data.leaf", "module.instances[0].terraform_data.leaf", true},
		{"module.instances.terraform_data.leaf", "module.instances[1].terraform_data.uses_leaf", false},
		{"terraform_data.counted", "terraform_data.counted[2]", true},
		{"terraform_data.counted[0]", "terraform_data.counted", false},
	}
	for _, tc := range cases {
		p, err := Compile(tc.pat)
		if err != nil {
			t.Fatalf("compile %s: %v", tc.pat, err)
		}
		if got := p.Match(tc.addr); got != tc.ok {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pat, tc.addr, got, tc.ok)
		}
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pat  string
		addr string
		ok   bool
	}{
		{"aws_instance.web[*]", "aws_instance.web[0]", true},
		{"aws_instance.web[*]", "aws_instance.web[10]", true},
		{`aws_instance.web[*]`, `aws_instance.web["k"]`, true},
		{`aws_instance.web[*]`, `aws_instance.web["a.b"]`, true},
		{"aws_instance.web[*]", "aws_instance.web", false},
		{"aws_instance.web[*]", "aws_instance.web2[0]", false},
		{"module.legacy.*", "module.legacy.aws_instance.db", true},
		{"module.legacy.*", "module.legacy.module.child.aws_instance.db", true},
		{"module.legacy.*", "module.legacy[0].aws_instance.db", true},
		{"module.legacy.*", `module.legacy["a.b"].aws_instance.db[1]`, true},
		{"module.legacy.*", "module.legacy2.aws_instance.db", false},
		{"module.legacy.*", "module.other.aws_instance.db", false},
		{"module.a.module.b.*", "module.a[0].module.b[1].res.name[2]", true},
		{"*", "aws_instance.web[0]", true},
		{"terraform_data.*", "terraform_data.pool[3]", true},
		{"terraform_data.*", "module.a.terraform_data.pool", false},
		{"*aws_instance.web", "module.x.aws_instance.web", true},
		{"*aws_instance.web", "aws_instance.web", true},
		{"*aws_instance.web", "aws_instance.web[0]", false},
		{"nope*", "aws_instance.web", false},
		{"module.app.aws_instance.web[*]", "module.app.aws_instance.web[0]", true},
		{"module.app.aws_instance.web[*]", "module.app[0].aws_instance.web[1]", true},
		{"module.app.aws_instance.web[*]", "module.app.aws_instance.web", false},
	}
	for _, tc := range cases {
		p, err := Compile(tc.pat)
		if err != nil {
			t.Fatalf("compile %s: %v", tc.pat, err)
		}
		if !p.IsGlob() {
			t.Fatalf("%s should be a glob", tc.pat)
		}
		if got := p.Match(tc.addr); got != tc.ok {
			t.Errorf("glob Match(%q, %q) = %v, want %v", tc.pat, tc.addr, got, tc.ok)
		}
	}
}

func TestGraphResourceFilter(t *testing.T) {
	if !IsGraphResource("terraform_data.a") || !IsGraphResource("module.child.terraform_data.leaf") {
		t.Fatal("managed resources should be graph resources")
	}
	for _, n := range []string{"local.b", "var.prefix", "module.child.var.prefix", "module.child.output.id", "output.x", "module.child"} {
		if IsGraphResource(n) {
			t.Fatalf("%s should not be a managed graph resource", n)
		}
	}
}
