package cli

import (
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	opt, err := ParseArgs([]string{
		"-chdir=envs/prod", "plan",
		"-exclude", "aws_db_instance.main",
		"-exclude=module.legacy.*",
		"-var", "region=us-east-1",
		"-out=tfplan",
		"-input=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opt.Command != "plan" || opt.Chdir != "envs/prod" {
		t.Fatalf("%+v", opt)
	}
	if strings.Join(opt.Excludes, ",") != "aws_db_instance.main,module.legacy.*" {
		t.Fatalf("excludes %v", opt.Excludes)
	}
	if strings.Join(opt.Passthrough, " ") != "-var region=us-east-1 -out=tfplan -input=false" {
		t.Fatalf("passthrough %q", opt.Passthrough)
	}
	if opt.planFile() != "" {
		t.Fatal("plan should not see a plan file")
	}
}

func TestParseRejectsBoth(t *testing.T) {
	_, err := ParseArgs([]string{"plan", "-exclude=a", "-target=b"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("got %v", err)
	}
}

func TestParseApplyPlanFile(t *testing.T) {
	opt, err := ParseArgs([]string{"apply", "-auto-approve", "saved.tfplan"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.planFile() != "saved.tfplan" {
		t.Fatalf("plan file %q", opt.planFile())
	}
	opt, err = ParseArgs([]string{"apply", "-var", "saved.tfplan", "-exclude=a"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.planFile() != "" {
		t.Fatalf("var value treated as plan file: %+v", opt)
	}
}

func TestDiscoveryArgsDestroy(t *testing.T) {
	got := discoveryArgs([]string{"-var", "x=1", "-auto-approve", "-out=tfplan", "-replace=aws_instance.web", "-lock=false"}, true)
	s := strings.Join(got, " ")
	if !strings.Contains(s, "-var x=1") || !strings.Contains(s, "-lock=false") || !strings.Contains(s, "-destroy") {
		t.Fatalf("discovery args %s", s)
	}
	if strings.Contains(s, "-auto-approve") || strings.Contains(s, "-out") || strings.Contains(s, "-replace") {
		t.Fatalf("discovery should not carry approval, -out, or -replace on destroy: %s", s)
	}
}

func TestTargetsJSONFlag(t *testing.T) {
	opt, err := ParseArgs([]string{"targets", "--json", "-exclude=a"})
	if err != nil {
		t.Fatal(err)
	}
	if !opt.JSON {
		t.Fatal("expected json")
	}
	_, err = ParseArgs([]string{"plan", "--json", "-exclude=a"})
	if err == nil {
		t.Fatal("expected error for --json on plan")
	}
}
