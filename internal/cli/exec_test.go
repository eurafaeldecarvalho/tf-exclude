package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var fakeTerraform string

func TestMain(m *testing.M) {
	root := moduleRoot()
	bin := filepath.Join(os.TempDir(), "tf-exclude-fake-terraform")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./internal/tftestbin")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fake terraform: %v\n%s", err, out)
		os.Exit(1)
	}
	fakeTerraform = bin
	os.Exit(m.Run())
}

func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "log")
	t.Setenv("TF_EXCLUDE_BIN", fakeTerraform)
	t.Setenv("TF_FAKE_LOG", logPath)
	t.Setenv("TF_FAKE_VERSION", "")
	var out, errb bytes.Buffer
	code = mainWith(args, "test", &out, &errb)
	return out.String(), errb.String(), code
}

func fakeLog(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("TF_FAKE_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatalf("log line %s: %v", line, err)
		}
		calls = append(calls, args)
	}
	return calls
}

func TestCLIExcludeDropsDependents(t *testing.T) {
	stdout, stderr, code := runCLI(t, "plan", "-exclude=terraform_data.a", "-input=false", "-lock=false")
	if code != 0 {
		t.Fatalf("code %d\nstderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stderr, "2 excluded") {
		t.Fatalf("summary missing exclusion count:\n%s", stderr)
	}
	calls := fakeLog(t)
	var final []string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "plan" {
			final = c
		}
	}
	joined := strings.Join(final, " ")
	if strings.Contains(joined, "-target=terraform_data.a") || strings.Contains(joined, "-target=terraform_data.b") {
		t.Fatalf("final plan still targets excluded resources: %s", joined)
	}
	if !strings.Contains(joined, "-target=terraform_data.c") {
		t.Fatalf("final plan missing terraform_data.c: %s", joined)
	}
	if strings.Contains(joined, "-auto-approve") {
		t.Fatalf("plan injected -auto-approve: %s", joined)
	}
	for _, c := range calls {
		if len(c) > 0 && c[0] == "state" {
			t.Fatal("state subcommand was invoked")
		}
	}
	// The preliminary plan must not carry -target. The final one must.
	var plans [][]string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "plan" {
			plans = append(plans, c)
		}
	}
	if len(plans) != 2 {
		t.Fatalf("plans = %d, want discovery + final", len(plans))
	}
	if strings.Contains(strings.Join(plans[0], " "), "-target=") {
		t.Fatalf("discovery plan has -target: %v", plans[0])
	}
}

func TestCLIDoesNotInventAutoApprove(t *testing.T) {
	_, stderr, code := runCLI(t, "apply", "-exclude=terraform_data.c")
	if code != 0 {
		t.Fatalf("code %d stderr %s", code, stderr)
	}
	calls := fakeLog(t)
	var apply []string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "apply" {
			apply = c
		}
	}
	if strings.Contains(strings.Join(apply, " "), "-auto-approve") {
		t.Fatalf("auto-approve injected: %v", apply)
	}
	_, _, code = runCLI(t, "destroy", "-exclude=terraform_data.c", "-auto-approve")
	if code != 0 {
		t.Fatal(code)
	}
	found := false
	for _, c := range fakeLog(t) {
		if len(c) > 0 && c[0] == "destroy" && strings.Contains(strings.Join(c, " "), "-auto-approve") {
			found = true
		}
	}
	if !found {
		t.Fatal("user -auto-approve was dropped")
	}
}

func TestCLIRefusals(t *testing.T) {
	_, stderr, code := runCLI(t, "plan", "-exclude=missing*")
	if code != 1 || !strings.Contains(stderr, "matched no resource") {
		t.Fatalf("glob miss code=%d stderr=%s", code, stderr)
	}
	_, stderr, code = runCLI(t, "plan", "-exclude=terraform_data.a", "-exclude=terraform_data.c")
	if code != 1 || !strings.Contains(stderr, "every resource") {
		t.Fatalf("empty code=%d stderr=%s", code, stderr)
	}
	_, stderr, code = runCLI(t, "plan", "-exclude=a", "-target=b")
	if code != 2 || !strings.Contains(stderr, "cannot be combined") {
		t.Fatalf("both code=%d stderr=%s", code, stderr)
	}
}

func TestCLIOldTerraform(t *testing.T) {
	t.Setenv("TF_EXCLUDE_BIN", fakeTerraform)
	t.Setenv("TF_FAKE_VERSION", "0.11.15")
	t.Setenv("TF_FAKE_LOG", filepath.Join(t.TempDir(), "log"))
	var errb bytes.Buffer
	code := mainWith([]string{"plan", "-exclude=terraform_data.a"}, "test", io.Discard, &errb)
	if code != 1 || !strings.Contains(errb.String(), "0.12") {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

func TestCLITargetsJSON(t *testing.T) {
	stdout, stderr, code := runCLI(t, "targets", "--json", "-exclude=terraform_data.c")
	if code != 0 {
		t.Fatalf("code %d stderr %s", code, stderr)
	}
	var payload struct {
		Resources int      `json:"resources"`
		Excluded  []string `json:"excluded"`
		Targets   []string `json:"targets"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json %s: %v", stdout, err)
	}
	if payload.Resources != 3 || len(payload.Excluded) != 1 || len(payload.Targets) != 2 {
		t.Fatalf("%+v", payload)
	}
}

func TestCLIDryRunSkipsFinalCommand(t *testing.T) {
	stdout, stderr, code := runCLI(t, "--dry-run", "destroy", "-exclude=terraform_data.c")
	if code != 0 {
		t.Fatalf("code %d stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "destroy") || !strings.Contains(stdout, "-target=terraform_data.a") {
		t.Fatalf("dry-run stdout:\n%s", stdout)
	}
	for _, c := range fakeLog(t) {
		if len(c) > 0 && (c[0] == "destroy" || c[0] == "apply") {
			t.Fatalf("dry-run executed %v", c)
		}
	}
}

func TestCLISavedPlanPassthrough(t *testing.T) {
	_, stderr, code := runCLI(t, "apply", "saved.tfplan")
	if code != 0 {
		t.Fatalf("code %d stderr %s", code, stderr)
	}
	calls := fakeLog(t)
	if len(calls) != 1 || calls[0][0] != "apply" || !contains(calls[0], "saved.tfplan") {
		t.Fatalf("calls %+v", calls)
	}
}

func TestRemoteBackendRefused(t *testing.T) {
	dir := t.TempDir()
	meta := filepath.Join(dir, ".terraform")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"version":3,"backend":{"type":"cloud","config":{}}}`)
	if err := os.WriteFile(filepath.Join(meta, "terraform.tfstate"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkBackend(dir)
	if err == nil || !strings.Contains(err.Error(), "cloud") {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta, "terraform.tfstate"), []byte(`{"backend":{"type":"local"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkBackend(dir); err != nil {
		t.Fatal(err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
