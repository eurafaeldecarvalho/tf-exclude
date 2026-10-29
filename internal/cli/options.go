package cli

import (
	"fmt"
	"strings"
)

// Options is a parsed tf-exclude invocation. Passthrough is everything that
// belongs to terraform, with -exclude, -target, -chdir, and --dry-run removed.
type Options struct {
	Command     string
	Chdir       string
	Excludes    []string
	Targets     []string
	DryRun      bool
	JSON        bool
	Passthrough []string
}

func (o Options) destroyMode() bool {
	if o.Command == "destroy" {
		return true
	}
	for _, a := range o.Passthrough {
		if a == "-destroy" || a == "-destroy=true" {
			return true
		}
	}
	return false
}

func (o Options) planFile() string {
	args := tokenize(o.Passthrough)
	for _, a := range args {
		if a.positional {
			return a.raw
		}
	}
	return ""
}

// ParseArgs parses argv (without the program name).
func ParseArgs(argv []string) (Options, error) {
	var opt Options
	if len(argv) == 0 {
		opt.Command = "help"
		return opt, nil
	}
	i := 0
	// Flags that may appear before the subcommand.
	for i < len(argv) {
		a := argv[i]
		if a == "--" {
			return Options{}, fmt.Errorf("missing command before --")
		}
		if a == "-h" || a == "--help" || a == "help" {
			opt.Command = "help"
			return opt, nil
		}
		if !strings.HasPrefix(a, "-") {
			opt.Command = a
			i++
			break
		}
		name, val, hasEq := splitKV(a)
		switch canon(name) {
		case "-chdir":
			v, ni, err := take(argv, i, hasEq, val)
			if err != nil {
				return Options{}, err
			}
			if err := opt.setChdir(v); err != nil {
				return Options{}, err
			}
			i = ni
		case "-dry-run":
			opt.DryRun = true
			i++
		default:
			return Options{}, fmt.Errorf("unknown flag %s before the command", a)
		}
	}
	if opt.Command == "" {
		return Options{}, fmt.Errorf("missing command")
	}
	switch opt.Command {
	case "plan", "apply", "destroy", "targets", "version", "help":
	default:
		return Options{}, fmt.Errorf("unknown command %q (want plan, apply, destroy, targets, version)", opt.Command)
	}

	for i < len(argv) {
		a := argv[i]
		if a == "--" {
			opt.Passthrough = append(opt.Passthrough, argv[i+1:]...)
			break
		}
		if a == "-h" || a == "--help" {
			opt.Command = "help"
			return opt, nil
		}
		if !strings.HasPrefix(a, "-") {
			opt.Passthrough = append(opt.Passthrough, a)
			i++
			continue
		}
		name, val, hasEq := splitKV(a)
		switch canon(name) {
		case "-exclude":
			v, ni, err := take(argv, i, hasEq, val)
			if err != nil {
				return Options{}, fmt.Errorf("-exclude: %w", err)
			}
			opt.Excludes = append(opt.Excludes, v)
			i = ni
		case "-target":
			v, ni, err := take(argv, i, hasEq, val)
			if err != nil {
				return Options{}, fmt.Errorf("-target: %w", err)
			}
			opt.Targets = append(opt.Targets, v)
			i = ni
		case "-chdir":
			v, ni, err := take(argv, i, hasEq, val)
			if err != nil {
				return Options{}, err
			}
			if err := opt.setChdir(v); err != nil {
				return Options{}, err
			}
			i = ni
		case "-dry-run":
			opt.DryRun = true
			i++
		case "-json":
			if opt.Command == "targets" && (name == "-json" || name == "--json") {
				opt.JSON = true
				i++
				break
			}
			// Terraform's own UI flag. "--json" is ours only on targets.
			if name == "--json" {
				return Options{}, fmt.Errorf("--json is only for %q; pass -json to forward Terraform's JSON UI", "targets")
			}
			opt.Passthrough = append(opt.Passthrough, a)
			i++
		default:
			if !hasEq && valueFlag(canon(name)) {
				if i+1 >= len(argv) {
					return Options{}, fmt.Errorf("flag %s needs a value", name)
				}
				opt.Passthrough = append(opt.Passthrough, a, argv[i+1])
				i += 2
				continue
			}
			opt.Passthrough = append(opt.Passthrough, a)
			i++
		}
	}
	if len(opt.Excludes) > 0 && len(opt.Targets) > 0 {
		return Options{}, fmt.Errorf("-exclude and -target cannot be combined")
	}
	return opt, nil
}

func (o *Options) setChdir(v string) error {
	if v == "" {
		return fmt.Errorf("-chdir needs a directory")
	}
	if o.Chdir != "" && o.Chdir != v {
		return fmt.Errorf("-chdir given twice (%s and %s)", o.Chdir, v)
	}
	o.Chdir = v
	return nil
}

func canon(name string) string {
	switch name {
	case "--exclude":
		return "-exclude"
	case "--target":
		return "-target"
	case "--chdir":
		return "-chdir"
	case "--dry-run":
		return "-dry-run"
	case "--json":
		return "-json"
	default:
		return name
	}
}

func splitKV(arg string) (name, value string, hasEq bool) {
	if eq := strings.IndexByte(arg, '='); eq >= 0 {
		return arg[:eq], arg[eq+1:], true
	}
	return arg, "", false
}

func take(argv []string, i int, hasEq bool, val string) (string, int, error) {
	if hasEq {
		if val == "" {
			return "", 0, fmt.Errorf("flag %s needs a value", argv[i])
		}
		return val, i + 1, nil
	}
	if i+1 >= len(argv) {
		return "", 0, fmt.Errorf("flag %s needs a value", argv[i])
	}
	return argv[i+1], i + 2, nil
}

func valueFlag(name string) bool {
	switch name {
	case "-out", "-var", "-var-file", "-replace", "-state", "-parallelism",
		"-lock-timeout", "-lock", "-refresh", "-input", "-generate-config-out",
		"-backup", "-chdir":
		return true
	default:
		return false
	}
}

type token struct {
	raw        string
	name       string
	value      string
	positional bool
}

// tokenize groups passthrough flags that take a separate value so a plan file
// is not confused with -var's argument.
func tokenize(args []string) []token {
	var out []token
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			for _, rest := range args[i+1:] {
				out = append(out, token{raw: rest, positional: true})
			}
			break
		}
		if !strings.HasPrefix(a, "-") {
			out = append(out, token{raw: a, positional: true})
			continue
		}
		name, val, hasEq := splitKV(a)
		name = canon(name)
		if hasEq {
			out = append(out, token{raw: a, name: name, value: val})
			continue
		}
		if valueFlag(name) && i+1 < len(args) {
			out = append(out, token{raw: a, name: name, value: args[i+1]})
			i++
			continue
		}
		out = append(out, token{raw: a, name: name})
	}
	return out
}

// discoveryArgs are the user flags that must also be on the preliminary plan
// so the address set matches the final command. Output and approval flags are
// not included; the preliminary plan is discarded.
func discoveryArgs(passthrough []string, destroy bool) []string {
	var out []string
	seenDestroy := false
	for _, tok := range tokenize(passthrough) {
		if tok.positional {
			continue
		}
		switch tok.name {
		case "-var", "-var-file", "-state", "-parallelism", "-lock-timeout", "-lock", "-refresh", "-input":
			out = append(out, flagTokens(tok)...)
		case "-no-color", "-refresh-only":
			out = append(out, tok.raw)
		case "-replace":
			if !destroy {
				out = append(out, flagTokens(tok)...)
			}
		case "-destroy":
			seenDestroy = true
			out = append(out, "-destroy")
		}
	}
	if destroy && !seenDestroy {
		out = append(out, "-destroy")
	}
	if !hasFlag(out, "-input") {
		out = append(out, "-input=false")
	}
	return out
}

func flagTokens(tok token) []string {
	if strings.Contains(tok.raw, "=") || tok.value == "" {
		return []string{tok.raw}
	}
	return []string{tok.raw, tok.value}
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		n, _, _ := splitKV(a)
		if canon(n) == name {
			return true
		}
	}
	return false
}

func hasOut(passthrough []string) bool {
	for _, tok := range tokenize(passthrough) {
		if tok.name == "-out" {
			return true
		}
	}
	return false
}
