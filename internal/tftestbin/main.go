// Command tftestbin is a fake terraform used by unit tests. It is not a
// Terraform implementation.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	args := stripChdir(os.Args[1:])
	appendLog(args)
	if len(args) == 0 {
		os.Exit(0)
	}
	switch args[0] {
	case "version":
		ver := os.Getenv("TF_FAKE_VERSION")
		if ver == "" {
			ver = "1.5.7"
		}
		if len(args) > 1 && args[1] == "-json" {
			fmt.Printf("{\"terraform_version\":%q}\n", ver)
			return
		}
		fmt.Printf("Terraform v%s\n", ver)
	case "plan":
		out := flagValue(args, "-out")
		if out != "" {
			if err := os.WriteFile(out, []byte("plan"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	case "show":
		fmt.Print(planJSON)
	case "graph":
		fmt.Print(graphDOT)
	case "apply", "destroy":
	case "state":
		fmt.Fprintln(os.Stderr, "state must not be invoked")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unexpected subcommand %s\n", args[0])
		os.Exit(1)
	}
}

func stripChdir(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-chdir" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-chdir=") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

func appendLog(args []string) {
	path := os.Getenv("TF_FAKE_LOG")
	if path == "" {
		return
	}
	b, _ := json.Marshal(args)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	fmt.Fprintln(f, string(b))
}

const planJSON = `{
  "format_version": "1.2",
  "terraform_version": "1.5.7",
  "resource_changes": [
    {"address": "terraform_data.a", "mode": "managed", "type": "terraform_data", "name": "a", "change": {"actions": ["no-op"]}},
    {"address": "terraform_data.b", "mode": "managed", "type": "terraform_data", "name": "b", "change": {"actions": ["no-op"]}},
    {"address": "terraform_data.c", "mode": "managed", "type": "terraform_data", "name": "c", "change": {"actions": ["no-op"]}}
  ]
}
`

const graphDOT = `digraph {
  "[root] terraform_data.b (expand)" -> "[root] terraform_data.a (expand)"
  "[root] terraform_data.a (expand)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
  "[root] terraform_data.c (expand)" -> "[root] provider[\"terraform.io/builtin/terraform\"]"
}
`
