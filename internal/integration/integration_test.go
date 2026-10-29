//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var tfExcludeBin string

func TestMain(m *testing.M) {
	root := moduleRoot()
	dir, err := os.MkdirTemp("", "tf-exclude-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "tf-exclude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build tf-exclude: %v\n%s", err, out)
		os.Exit(1)
	}
	tfExcludeBin = bin
	os.Exit(m.Run())
}

func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func requireTool(t *testing.T, name, requireEnv string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv(requireEnv) == "1" {
			t.Fatalf("%s is required ( %s=1 ) but was not found on PATH", name, requireEnv)
		}
		t.Skipf("%s not on PATH", name)
	}
	return p
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func toolEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TF_CLI_ARGS") || strings.HasPrefix(e, "TF_WORKSPACE=") || strings.HasPrefix(e, "TF_EXCLUDE_") {
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"CHECKPOINT_DISABLE=1",
		"TF_IN_AUTOMATION=1",
	)
	return env
}

func run(t *testing.T, dir, bin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = toolEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s %v: %v\n%s", bin, args, err, stderr.String())
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func must(t *testing.T, dir, bin string, args ...string) string {
	t.Helper()
	stdout, stderr, code := run(t, dir, bin, args...)
	if code != 0 {
		t.Fatalf("%s %v failed (%d)\nstdout:\n%s\nstderr:\n%s", bin, args, code, stdout, stderr)
	}
	return stdout
}

func initTerraform(t *testing.T, dir, bin string) {
	t.Helper()
	must(t, dir, bin, "init", "-input=false")
}

type changeSet map[string]string

func showChanges(t *testing.T, dir, bin, planPath string) changeSet {
	t.Helper()
	stdout := must(t, dir, bin, "show", "-json", planPath)
	var payload struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Mode    string `json:"mode"`
			Change  struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("show json: %v\n%s", err, stdout[:min(400, len(stdout))])
	}
	out := changeSet{}
	for _, rc := range payload.ResourceChanges {
		if rc.Mode != "" && rc.Mode != "managed" {
			continue
		}
		out[rc.Address] = strings.Join(rc.Change.Actions, ",")
	}
	return out
}

func changing(in changeSet) changeSet {
	out := changeSet{}
	for k, v := range in {
		if v == "no-op" {
			continue
		}
		out[k] = v
	}
	return out
}

func diffSets(got, want changeSet) string {
	keys := map[string]struct{}{}
	for k := range got {
		keys[k] = struct{}{}
	}
	for k := range want {
		keys[k] = struct{}{}
	}
	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	sort.Strings(list)
	var b strings.Builder
	for _, k := range list {
		g, gok := got[k]
		w, wok := want[k]
		if gok && wok && g == w {
			continue
		}
		fmt.Fprintf(&b, "  %s: tf-exclude %q (present %v), tofu %q (present %v)\n", k, g, gok, w, wok)
	}
	return b.String()
}

func fixture(name string) string {
	return filepath.Join(moduleRoot(), "testdata", "fixtures", name)
}

func TestParityWithOpenTofu(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	tofu := requireTool(t, "tofu", "TF_EXCLUDE_REQUIRE_TOFU")

	patterns := []string{
		"terraform_data.b",
		"terraform_data.d",
		"terraform_data.counted[0]",
		"terraform_data.counted",
		`terraform_data.each["x"]`,
		"module.child",
		"module.instances[0]",
		"module.instances",
		`module.foreach["red"]`,
		"module.child.terraform_data.leaf",
		"module.wrap.module.inner.terraform_data.deep",
		"terraform_data.via_local",
		"module.instances.terraform_data.leaf",
	}

	tfDir := copyTree(t, fixture("parity"))
	tofuDir := copyTree(t, fixture("parity"))
	initTerraform(t, tfDir, terraform)
	initTerraform(t, tofuDir, tofu)

	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			must(t, tfDir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-out=cmp.tfplan", "-exclude="+pattern)
			got := showChanges(t, tfDir, terraform, "cmp.tfplan")
			must(t, tofuDir, tofu, "plan", "-input=false", "-lock=false", "-out=cmp.tfplan", "-exclude="+pattern)
			want := showChanges(t, tofuDir, tofu, "cmp.tfplan")
			if diff := diffSets(changing(got), changing(want)); diff != "" {
				t.Fatalf("changing resource_changes differ for -exclude %s\n%s", pattern, diff)
			}
			if diff := diffSets(got, want); diff != "" {
				t.Fatalf("full resource_changes differ for -exclude %s\n%s", pattern, diff)
			}
		})
	}
}

func TestDestroyKeepsExcludedResource(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")

	t.Run("keeps-a", func(t *testing.T) {
		dir := copyTree(t, fixture("chain"))
		initTerraform(t, dir, terraform)
		must(t, dir, terraform, "apply", "-auto-approve", "-input=false", "-lock=false")
		must(t, dir, tfExcludeBin, "destroy", "-exclude=terraform_data.a", "-auto-approve", "-input=false", "-lock=false")
		state := must(t, dir, terraform, "state", "list")
		if strings.TrimSpace(state) != "terraform_data.a" {
			t.Fatalf("state after destroy -exclude a:\n%s", state)
		}
	})

	t.Run("keeps-dependency-of-c", func(t *testing.T) {
		dir := copyTree(t, fixture("chain"))
		initTerraform(t, dir, terraform)
		must(t, dir, terraform, "apply", "-auto-approve", "-input=false", "-lock=false")
		must(t, dir, tfExcludeBin, "destroy", "-exclude=terraform_data.c", "-auto-approve", "-input=false", "-lock=false")
		state := strings.Fields(must(t, dir, terraform, "state", "list"))
		sort.Strings(state)
		if strings.Join(state, ",") != "terraform_data.a,terraform_data.c" {
			t.Fatalf("state after destroy -exclude c: %v", state)
		}
	})
}

func TestDestroyParityAndState(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	tofu := requireTool(t, "tofu", "TF_EXCLUDE_REQUIRE_TOFU")

	// Plan parity for destroy -exclude, then a real destroy that must leave
	// the excluded resource in state.
	for _, pattern := range []string{"terraform_data.a", "terraform_data.c", "terraform_data.explicit"} {
		t.Run("plan/"+pattern, func(t *testing.T) {
			tfDir := copyTree(t, fixture("chain"))
			tofuDir := copyTree(t, fixture("chain"))
			initTerraform(t, tfDir, terraform)
			initTerraform(t, tofuDir, tofu)
			must(t, tfDir, terraform, "apply", "-auto-approve", "-input=false", "-lock=false")
			must(t, tofuDir, tofu, "apply", "-auto-approve", "-input=false", "-lock=false")

			_, stderr, code := run(t, tfDir, tfExcludeBin, "plan", "-destroy", "-input=false", "-lock=false", "-out=cmp.tfplan", "-exclude="+pattern)
			if code != 0 {
				// Excluding explicit on destroy pulls in a and c as well, which
				// empties the destroy. That refusal is asserted below; other
				// patterns must plan.
				if pattern == "terraform_data.explicit" && strings.Contains(stderr, "every resource") {
					_, tofuErr, tofuCode := run(t, tofuDir, tofu, "plan", "-destroy", "-input=false", "-lock=false", "-out=cmp.tfplan", "-exclude="+pattern)
					if tofuCode != 0 {
						t.Fatalf("tofu destroy plan failed: %s", tofuErr)
					}
					want := showChanges(t, tofuDir, tofu, "cmp.tfplan")
					if len(changing(want)) != 0 {
						t.Fatalf("refused an empty destroy but tofu still destroys %#v", want)
					}
					return
				}
				t.Fatalf("tf-exclude plan -destroy failed (%d): %s", code, stderr)
			}
			got := showChanges(t, tfDir, terraform, "cmp.tfplan")
			must(t, tofuDir, tofu, "plan", "-destroy", "-input=false", "-lock=false", "-out=cmp.tfplan", "-exclude="+pattern)
			want := showChanges(t, tofuDir, tofu, "cmp.tfplan")
			if diff := diffSets(changing(got), changing(want)); diff != "" {
				t.Fatalf("destroy changes differ\n%s", diff)
			}
		})
	}
}

func TestGlobTargetAndCreate(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	dir := copyTree(t, fixture("parity"))
	initTerraform(t, dir, terraform)

	must(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-out=glob.tfplan", "-target=terraform_data.counted[*]")
	got := changing(showChanges(t, dir, terraform, "glob.tfplan"))
	for _, addr := range []string{"terraform_data.counted[0]", "terraform_data.counted[1]", "terraform_data.counted[2]"} {
		if got[addr] != "create" {
			t.Fatalf("%s = %q, want create; full %#v", addr, got[addr], got)
		}
	}
	if _, ok := got["terraform_data.indexed_dep[0]"]; ok {
		t.Fatal("positive -target of counted must not include indexed_dep")
	}

	// A resource that does not exist yet is part of the candidate set.
	must(t, dir, terraform, "apply", "-auto-approve", "-input=false", "-lock=false")
	main := filepath.Join(dir, "main.tf")
	b, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("\nresource \"terraform_data\" \"brand_new\" {}\n")...)
	if err := os.WriteFile(main, b, 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-out=new.tfplan", "-exclude=terraform_data.d")
	got = showChanges(t, dir, terraform, "new.tfplan")
	if got["terraform_data.brand_new"] != "create" {
		t.Fatalf("brand_new = %q, full changing %#v", got["terraform_data.brand_new"], changing(got))
	}
	if _, ok := got["terraform_data.d"]; ok && got["terraform_data.d"] != "no-op" {
		// d is excluded, so it must not be a planned change. It also should
		// not appear as a no-op inside a targeted plan.
		t.Fatalf("excluded d still in plan as %q", got["terraform_data.d"])
	}
	if _, ok := got["terraform_data.d"]; ok {
		t.Fatalf("excluded terraform_data.d is in the plan: %q", got["terraform_data.d"])
	}
}

func TestRefusalsAgainstRealTerraform(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	dir := copyTree(t, fixture("chain"))
	initTerraform(t, dir, terraform)

	_, stderr, code := run(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-exclude=terraform_data.missing")
	if code != 1 || !strings.Contains(stderr, "matched no resource") {
		t.Fatalf("missing address code=%d stderr=%s", code, stderr)
	}
	_, stderr, code = run(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-exclude=nope*")
	if code != 1 || !strings.Contains(stderr, "matched no resource") {
		t.Fatalf("glob code=%d stderr=%s", code, stderr)
	}
	_, stderr, code = run(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-exclude=terraform_data.a")
	if code != 1 || !strings.Contains(stderr, "every resource") {
		t.Fatalf("empty code=%d stderr=%s", code, stderr)
	}
}

func TestWorkspaceIsPassedThrough(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	dir := copyTree(t, fixture("chain"))
	initTerraform(t, dir, terraform)
	must(t, dir, terraform, "apply", "-auto-approve", "-input=false", "-lock=false")
	must(t, dir, terraform, "workspace", "new", "other")

	must(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-out=ws.tfplan", "-exclude=terraform_data.explicit")
	got := changing(showChanges(t, dir, terraform, "ws.tfplan"))
	if got["terraform_data.a"] != "create" || got["terraform_data.c"] != "create" {
		t.Fatalf("workspace other should plan creates, got %#v", got)
	}
	if _, ok := got["terraform_data.explicit"]; ok {
		t.Fatal("excluded resource was planned in the other workspace")
	}
	// Default workspace state is untouched.
	must(t, dir, terraform, "workspace", "select", "default")
	state := strings.Fields(must(t, dir, terraform, "state", "list"))
	sort.Strings(state)
	if strings.Join(state, ",") != "terraform_data.a,terraform_data.c,terraform_data.explicit" {
		t.Fatalf("default workspace state changed: %v", state)
	}
}

func TestScale2000(t *testing.T) {
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	dir := copyTree(t, fixture("scale2000"))
	initTerraform(t, dir, terraform)

	stdout := must(t, dir, tfExcludeBin, "targets", "--json", "-input=false", "-lock=false", "-exclude=terraform_data.skip")
	var payload struct {
		Resources int      `json:"resources"`
		Excluded  []string `json:"excluded"`
		Targets   []string `json:"targets"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("targets json: %v\n%s", err, stdout)
	}
	if payload.Resources != 2000 || len(payload.Excluded) != 1 || len(payload.Targets) != 1999 {
		t.Fatalf("counts resources=%d excluded=%d targets=%d", payload.Resources, len(payload.Excluded), len(payload.Targets))
	}
	if payload.Excluded[0] != "terraform_data.skip" {
		t.Fatalf("excluded %v", payload.Excluded)
	}

	_, stderr, code := run(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-out=scale.tfplan", "-exclude=terraform_data.skip")
	if code != 0 && strings.Contains(stderr, "refusing -out") {
		_, stderr, code = run(t, dir, tfExcludeBin, "plan", "-input=false", "-lock=false", "-exclude=terraform_data.skip")
		if code != 0 {
			t.Fatalf("batched plan failed (%d)\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "invocations") {
			t.Fatalf("expected batching notice, stderr:\n%s", stderr)
		}
		adds := sumPlanAdds(stderr)
		if adds != 1999 {
			t.Fatalf("batched plan adds = %d, want 1999\n%s", adds, stderr)
		}
		return
	}
	if code != 0 {
		t.Fatalf("plan failed (%d)\n%s", code, stderr)
	}
	got := changing(showChanges(t, dir, terraform, "scale.tfplan"))
	if len(got) != 1999 {
		t.Fatalf("plan has %d changes, want 1999", len(got))
	}
	if _, ok := got["terraform_data.skip"]; ok {
		t.Fatal("skip was included")
	}
}

func TestForcedBatchStillPlans(t *testing.T) {
	requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	dir := t.TempDir()
	body := []byte(`
resource "terraform_data" "keep" {
  count = 12
  input = count.index
}
resource "terraform_data" "skip" {}
`)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	terraform := requireTool(t, "terraform", "TF_EXCLUDE_REQUIRE_TERRAFORM")
	initTerraform(t, dir, terraform)

	cmd := exec.Command(tfExcludeBin, "plan", "-input=false", "-lock=false", "-exclude=terraform_data.skip")
	cmd.Dir = dir
	env := toolEnv()
	env = append(env, "TF_EXCLUDE_ARG_LIMIT=350")
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("batched plan: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invocations") {
		t.Fatalf("expected multiple invocations, stderr:\n%s", stderr.String())
	}
	if got := sumPlanAdds(stderr.String()); got != 12 {
		t.Fatalf("adds = %d, want 12\n%s", got, stderr.String())
	}
}

func sumPlanAdds(s string) int {
	total := 0
	for _, line := range strings.Split(s, "\n") {
		line = stripANSI(strings.TrimSpace(line))
		if !strings.HasPrefix(line, "Plan:") {
			continue
		}
		var add, change, destroy int
		if _, err := fmt.Sscanf(line, "Plan: %d to add, %d to change, %d to destroy.", &add, &change, &destroy); err == nil {
			total += add
		}
	}
	return total
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
