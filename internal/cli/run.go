package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/eurafaeldecarvalho/tf-exclude/internal/addr"
	"github.com/eurafaeldecarvalho/tf-exclude/internal/graph"
	"github.com/eurafaeldecarvalho/tf-exclude/internal/limit"
)

// Main runs tf-exclude and returns the process exit code.
func Main(argv []string, version string) int {
	return mainWith(argv, version, os.Stdout, os.Stderr)
}

func mainWith(argv []string, version string, stdout, stderr io.Writer) int {
	opt, err := ParseArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n%s", err, shortUsage)
		return 2
	}
	if opt.Command == "help" {
		fmt.Fprint(stdout, helpText)
		return 0
	}
	if opt.Command == "version" {
		fmt.Fprintf(stdout, "tf-exclude %s\n", version)
		if bin, err := terraformBin(); err != nil {
			fmt.Fprintf(stdout, "terraform: not found on PATH\n")
		} else if v, err := readVersion(bin, opt.Chdir); err != nil {
			fmt.Fprintf(stdout, "terraform: %s\n", err)
		} else {
			fmt.Fprintf(stdout, "terraform %s\n", v)
		}
		return 0
	}
	if len(opt.Excludes) == 0 && len(opt.Targets) == 0 {
		if opt.Command == "apply" && opt.planFile() != "" {
			return applySavedPlan(opt, stdout, stderr)
		}
		fmt.Fprintf(stderr, "tf-exclude: pass at least one -exclude or -target\n%s", shortUsage)
		return 2
	}
	if opt.Command == "apply" && opt.planFile() != "" {
		fmt.Fprintf(stderr, "tf-exclude: a saved plan already contains its targets; run `tf-exclude apply %s` with no -exclude or -target\n", opt.planFile())
		return 2
	}

	bin, err := terraformBin()
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}
	ver, err := readVersion(bin, opt.Chdir)
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}
	if !ver.atLeast(0, 12) {
		fmt.Fprintf(stderr, "tf-exclude: terraform %s does not support `terraform show -json` (requires >= 0.12)\n", ver)
		return 1
	}
	if err := checkBackend(opt.Chdir); err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}

	patterns, err := compilePatterns(opt)
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}

	fmt.Fprintf(stderr, "tf-exclude: planning the full configuration to compute targets...\n")
	addresses, edges, err := discover(bin, opt)
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}

	var sel graph.Selection
	positive := len(opt.Targets) > 0
	if positive {
		sel, err = graph.ExpandTargets(addresses, patterns)
	} else {
		sel, err = graph.Exclude(addresses, edges, patterns)
	}
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}
	writeSummary(stderr, sel, positive)

	if opt.Command == "targets" {
		return writeTargets(stdout, stderr, sel, opt.JSON)
	}

	finalCmd := opt.Command
	base := prefixChdir(opt.Chdir, append([]string{finalCmd}, opt.Passthrough...))
	budget := limit.Default()
	batches, err := limit.Split(bin, base, sel.Targets, budget)
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}
	if opt.DryRun {
		fmt.Fprintf(stderr, "tf-exclude: dry-run; not running terraform %s\n", finalCmd)
		for i, batch := range batches {
			if len(batches) > 1 {
				fmt.Fprintf(stdout, "# batch %d/%d\n", i+1, len(batches))
			}
			fmt.Fprintln(stdout, formatCommand(bin, batch))
		}
		return 0
	}
	if len(batches) > 1 && hasOut(opt.Passthrough) {
		fmt.Fprintf(stderr, "tf-exclude: refusing -out because the %d -target flags do not fit in one command line (limit %d bytes). Run without -out, or set TF_EXCLUDE_ARG_LIMIT if the OS allows a longer command.\n", len(sel.Targets), budget)
		return 1
	}
	if len(batches) > 1 {
		fmt.Fprintf(stderr, "tf-exclude: %d -target flags do not fit in one command line (limit %d bytes); running %d terraform %s invocations\n", len(sel.Targets), budget, len(batches), finalCmd)
		if finalCmd == "apply" || finalCmd == "destroy" {
			fmt.Fprintf(stderr, "tf-exclude: each invocation uses Terraform's own confirmation (or -auto-approve if you passed it)\n")
		}
	}
	code := 0
	for i, batch := range batches {
		if len(batches) > 1 {
			fmt.Fprintf(stderr, "tf-exclude: batch %d/%d (%d targets)\n", i+1, len(batches), countTargets(batch))
		}
		c := runStream(bin, batch, stdout, stderr)
		if c == 1 || (c != 0 && c != 2) {
			return c
		}
		if c == 2 {
			code = 2
		}
	}
	return code
}

func applySavedPlan(opt Options, stdout, stderr io.Writer) int {
	bin, err := terraformBin()
	if err != nil {
		fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "tf-exclude: applying saved plan %s (targets were fixed when the plan was created)\n", opt.planFile())
	args := prefixChdir(opt.Chdir, append([]string{"apply"}, opt.Passthrough...))
	return runStream(bin, args, stdout, stderr)
}

func compilePatterns(opt Options) ([]addr.Pattern, error) {
	raw := opt.Excludes
	if len(opt.Targets) > 0 {
		raw = opt.Targets
	}
	out := make([]addr.Pattern, 0, len(raw))
	for _, r := range raw {
		p, err := addr.Compile(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func discover(bin string, opt Options) ([]string, []graph.Edge, error) {
	destroy := opt.destroyMode()
	dir, err := os.MkdirTemp("", "tf-exclude-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	planPath := filepath.Join(dir, "discovery.tfplan")

	planArgs := prefixChdir(opt.Chdir, append([]string{"plan", "-out=" + planPath}, discoveryArgs(opt.Passthrough, destroy)...))
	stdout, stderr, code := runCapture(bin, planArgs, nil)
	if code != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		return nil, nil, fmt.Errorf("preliminary terraform plan failed (exit %d)\n%s", code, msg)
	}
	if os.Getenv("TF_EXCLUDE_DEBUG") != "" && strings.TrimSpace(stderr) != "" {
		fmt.Fprintf(os.Stderr, "tf-exclude: preliminary plan stderr:\n%s\n", stderr)
	}

	showArgs := prefixChdir(opt.Chdir, []string{"show", "-json", planPath})
	showOut, showErr, code := runCapture(bin, showArgs, nil)
	if code != 0 {
		return nil, nil, fmt.Errorf("terraform show -json failed (exit %d)\n%s", code, strings.TrimSpace(showErr))
	}
	addresses, err := managedAddresses([]byte(showOut))
	if err != nil {
		return nil, nil, fmt.Errorf("read plan JSON: %w", err)
	}

	graphType := "plan"
	if destroy {
		graphType = "plan-destroy"
	}
	graphArgs := prefixChdir(opt.Chdir, []string{"graph", "-type=" + graphType})
	dot, graphErr, code := runCapture(bin, graphArgs, nil)
	if code != 0 {
		return nil, nil, fmt.Errorf("terraform graph -type=%s failed (exit %d)\n%s", graphType, code, strings.TrimSpace(graphErr))
	}
	return addresses, graph.ParseDOT(dot), nil
}

func writeSummary(w io.Writer, sel graph.Selection, positive bool) {
	if positive {
		fmt.Fprintf(w, "tf-exclude: %d resources, %d -target flags after glob expansion\n", len(sel.Resources), len(sel.Targets))
		return
	}
	direct := map[string]struct{}{}
	for _, a := range sel.Matched {
		direct[a] = struct{}{}
	}
	var deps []string
	for _, a := range sel.Excluded {
		if _, ok := direct[a]; !ok {
			deps = append(deps, a)
		}
	}
	fmt.Fprintf(w, "tf-exclude: %d resources, %d excluded\n", len(sel.Resources), len(sel.Excluded))
	fmt.Fprintf(w, "tf-exclude: matched: %s\n", formatList(sel.Matched))
	if len(deps) > 0 {
		fmt.Fprintf(w, "tf-exclude: dependents also excluded: %s\n", formatList(deps))
	}
	fmt.Fprintf(w, "tf-exclude: %d -target flags\n", len(sel.Targets))
}

func formatList(items []string) string {
	const max = 40
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", … (+%d more)", len(items)-max)
}

func writeTargets(stdout, stderr io.Writer, sel graph.Selection, asJSON bool) int {
	if asJSON {
		matched, excluded, targets := sel.Matched, sel.Excluded, sel.Targets
		if matched == nil {
			matched = []string{}
		}
		if excluded == nil {
			excluded = []string{}
		}
		if targets == nil {
			targets = []string{}
		}
		payload := struct {
			Resources int      `json:"resources"`
			Matched   []string `json:"matched"`
			Excluded  []string `json:"excluded"`
			Targets   []string `json:"targets"`
		}{
			Resources: len(sel.Resources),
			Matched:   matched,
			Excluded:  excluded,
			Targets:   targets,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintf(stderr, "tf-exclude: %s\n", err)
			return 1
		}
		return 0
	}
	for _, t := range sel.Targets {
		fmt.Fprintf(stdout, "-target=%s\n", t)
	}
	return 0
}

func countTargets(batch []string) int {
	n := 0
	for _, a := range batch {
		if strings.HasPrefix(a, "-target=") {
			n++
		}
	}
	return n
}

func formatCommand(bin string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, quoteArg(bin))
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

const shortUsage = "usage: tf-exclude <plan|apply|destroy|targets> [-exclude ADDR]... [terraform args]\n"

const helpText = `tf-exclude — plan, apply, or destroy every resource except the ones you name

tf-exclude wraps the terraform binary on your PATH. It does not read or write
state itself. apply and destroy keep Terraform's own confirmation unless you
pass -auto-approve.

Usage:
  tf-exclude plan    -exclude ADDR [-exclude ADDR]... [terraform plan args]
  tf-exclude apply   -exclude ADDR [-exclude ADDR]... [terraform apply args]
  tf-exclude destroy -exclude ADDR [-exclude ADDR]... [terraform destroy args]
  tf-exclude targets -exclude ADDR [-exclude ADDR]... [--json]
  tf-exclude plan    -target  GLOB [-target GLOB]...  [terraform plan args]

-exclude may be repeated. An address without * follows OpenTofu -exclude:
a module address selects that module and its children, a resource address
selects every instance, and an instance key selects that instance. "*" is a
glob and "[*]" matches one instance key (module.legacy.* , aws_instance.web[*]).

The resource is excluded together with everything that would pull it back into
the operation. On plan/apply that is the transitive dependents. On destroy,
terraform graph reverses the edges, so the resources it depends on are kept
as well and the dependents are still destroyed. This matches OpenTofu.

-target with a glob expands to concrete addresses and passes them through.
-exclude and -target together are refused. A pattern that matches nothing is
refused. An -exclude that removes every resource is refused.

  --dry-run   print the terraform command instead of running the final step
  --json      on targets, print JSON instead of -target lines
  -chdir=DIR  same as terraform -chdir

Exit codes are Terraform's when terraform runs (including 2 for
-detailed-exitcode). Usage errors exit 2. Refusals exit 1.

Environment:
  TF_EXCLUDE_BIN        terraform executable (default: terraform on PATH)
  TF_EXCLUDE_ARG_LIMIT  command-line budget in bytes, for tests
  TF_EXCLUDE_DEBUG=1    show the preliminary plan's stderr
`
