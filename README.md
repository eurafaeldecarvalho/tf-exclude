# tf-exclude

Plan, apply, or destroy every Terraform resource except the ones you name.

On a 200-resource local configuration, excluding `terraform_data.db` (and the one resource that depends on it) takes **198** hand-written `-target` flags, **7,630** characters. `tf-exclude` is **1** flag. A naive `state list` minus that one address is 199 flags, and that plan still contains `terraform_data.db`, because targeting the dependent pulls it back in.

```sh
tf-exclude plan -exclude terraform_data.db
```

The clip below is a real run of `targets` and `plan -exclude` on [`testdata/demo`](testdata/demo) (5 resources: `db`, `app` which depends on it, `api`, and `web` with `count = 2`). It is not the 200-resource measurement.

![tf-exclude targets and plan -exclude on testdata/demo](docs/demo.gif)

Reproduce the numbers with `./scripts/prova.sh` (Terraform ≥ 1.4, OpenTofu ≥ 1.9, no cloud credentials). The run that produced the figures below was hyperfine 1.19.0, 1 warmup and 5 runs, on Terraform v1.17.1 and OpenTofu v1.13.2.

```text
$ tf-exclude plan -exclude terraform_data.db -input=false -lock=false
tf-exclude: planning the full configuration to compute targets...
tf-exclude: 200 resources, 2 excluded
tf-exclude: matched: terraform_data.db
tf-exclude: dependents also excluded: terraform_data.app
tf-exclude: 198 -target flags

No changes. Your infrastructure matches the configuration.

Warning: Resource targeting is in effect
You are creating a plan with the -target option, which means that the
result of this plan may not represent all of the changes requested by the
current configuration.
The -target option is not for routine use, and is provided only for
exceptional situations such as recovering from errors or mistakes, or when
Terraform specifically suggests to use it as part of an error message.
```

Refresh lines from that run are omitted above. The warning is Terraform's; tf-exclude does not hide it.

The hand-built command starts `terraform plan -target=module.one.terraform_data.item[0] -target=module.one.terraform_data.item[10] …` and is 7,630 characters for 198 flags. `./scripts/prova.sh` prints that length again.

| | flags | what the plan contains |
|---|---:|---|
| Hand-built `-target` list, dependents removed | 198 | same `resource_changes` as tf-exclude and as `tofu plan -exclude` |
| `state list` without `terraform_data.db` | 199 | still includes `terraform_data.db` |
| `tf-exclude plan -exclude terraform_data.db` | 1 | same set as the 198-flag plan |

| command | median |
|---|---:|
| `terraform plan` | 0.0796 s |
| `tf-exclude plan -exclude terraform_data.db` | 0.2768 s |
| difference | 0.1972 s |

The wrapper is slower because it plans the whole configuration once to learn addresses and the graph, then plans again with `-target`. On this fixture that is about 3.5× the plain plan (0.2768 / 0.0796). Both commands ran against an already-applied local state, so the time is not a cloud refresh.

## What it does

`tf-exclude` is a single binary. It runs the `terraform` already on your `PATH` (or `TF_EXCLUDE_BIN`). It does not fork Terraform, ship a provider, or edit state.

```sh
tf-exclude plan    -exclude 'aws_db_instance.main' -exclude 'module.legacy.*'
tf-exclude apply   -exclude 'aws_db_instance.main'          # Terraform asks before it applies
tf-exclude destroy -exclude 'aws_db_instance.main'          # the usual "keep the database" case
tf-exclude plan    -target  'module.app.aws_instance.web[*]' # -target with a glob
tf-exclude targets -exclude 'aws_db_instance.main' --json
tf-exclude plan    -exclude 'aws_db_instance.main' --dry-run
```

`-exclude` may be repeated. Without `*` it follows [OpenTofu's address rules](https://opentofu.org/docs/cli/commands/plan/#resource-targeting): a module address selects that module and its children, a resource address selects every instance, and an instance key selects that instance. `*` matches any text, and `[*]` matches one instance key. A `.` in a glob also allows an instance key in that position, so `module.legacy.*` matches `module.legacy[0].aws_instance.db` as well as `module.legacy.aws_instance.db`.

Candidate addresses are every managed resource in a full plan (`terraform plan -out` and `terraform show -json`): already created, not yet created, and `count` / `for_each` instances, including modules. The dependency edges come from `terraform graph -type=plan` (or `-type=plan-destroy` for destroy).

## Why

Terraform has `-target` and no inverse. [hashicorp/terraform#2253](https://github.com/hashicorp/terraform/issues/2253) has been open since 2015 and had 1,539 👍 on 2026-10-29. The shortcut people use, `terraform state list | grep -v …`, misses resources that are not in state yet, gets module and `count` / `for_each` addresses wrong, and, as the table above shows, pulls an excluded resource back in when a dependent is targeted.

OpenTofu has had native `-exclude` since 1.9. Terraform 1.17.1 (latest release on 2026-10-28) still does not, and neither does the 1.18 changelog on main as of that date. If you can move to OpenTofu, use its flag. This tool is for configurations that stay on Terraform.

## Quickstart

No account. `terraform` must be on your `PATH`. On 2026-10-29, `go install github.com/eurafaeldecarvalho/tf-exclude@latest` installed the latest commit on `main` from the public module.

```sh
go install github.com/eurafaeldecarvalho/tf-exclude@latest
tf-exclude targets -exclude 'aws_db_instance.main'
tf-exclude plan -exclude 'aws_db_instance.main'
```

`tf-exclude version` prints `dev` until a tagged release is published. To build a checkout instead: `go build -o tf-exclude .`

`apply` and `destroy` do not add `-auto-approve`. Terraform asks, unless you passed `-auto-approve` yourself. `--dry-run` prints the `terraform` command and does not run that final step. The preliminary plan still runs; it is read-only.

Other flags (`-var`, `-var-file`, `-out`, `-chdir`, `-lock`, `-refresh`, `-destroy` on `plan`, and so on) are passed through. The current workspace is left alone.

## How it compares

**OpenTofu `-exclude`.** Same idea, implemented in the engine: one process, `-exclude-file`, no second plan. Use it if you can. tf-exclude checks its `resource_changes` against `tofu plan -exclude` on the fixtures in this repo (OpenTofu 1.13.2, Terraform 1.5.7, 1.9.8, and 1.17.1).

**`terraform state list | grep -v`.** Only addresses already in state. On the 200-resource fixture, dropping `terraform_data.db` and targeting the other 199 still plans `terraform_data.db`, because `terraform_data.app` depends on it.

**[floj/tfrs](https://github.com/floj/tfrs).** Interactive picker that fills in `-target`. It does not compute "everything except these, plus dependents."

**[minebul/tfile](https://github.com/minebul/tfile).** Targets the resources declared in selected `.tf` files. That is file scope, not an exclude list.

## Parity with OpenTofu

Where the integration tests compare plans, the set of managed `resource_changes` (address and actions) matches `tofu plan -exclude` / `tofu plan -destroy -exclude`.

Differences, on purpose:

- A pattern that matches nothing is an error. OpenTofu currently plans the full configuration when `-exclude` names an address that does not exist. A typo on `destroy` would otherwise destroy everything.
- An exclude set that removes every resource is an error. OpenTofu writes an empty plan and exits 0. On `destroy -exclude terraform_data.explicit` in `testdata/fixtures/chain`, OpenTofu's plan destroys nothing and tf-exclude refuses.
- `-exclude` and `-target` together are refused. OpenTofu refuses that too.
- Globs (`*`, `[*]`) are only in tf-exclude. OpenTofu has no glob.
- When the `-target` list does not fit in one command line, tf-exclude runs several `terraform` invocations. OpenTofu accepts the exclude list in one process (`-exclude-file` exists there; Terraform has no equivalent). `-out` is refused in that case, because one plan file cannot hold several batches. `TF_EXCLUDE_ARG_LIMIT` overrides the byte budget (default 30,000 on Windows, 200,000 elsewhere).
- Terraform prints its targeting warning. The plan is a `-target` plan.

Destroy follows the destroy graph, whose edges are the reverse of a normal plan. `destroy -exclude A` keeps A and every resource A depends on (destroying those would also destroy A) and still destroys resources that depend on A. That is what OpenTofu does. A normal `plan`/`apply` exclude keeps the dependents as well, because targeting a dependent would pull the excluded resource back in.

`terraform graph` connects every instance of a dependent resource to the whole dependency, including across `count` and `for_each` module instances. Excluding `terraform_data.counted[0]` also excludes every `terraform_data.indexed_dep` instance, not only `[0]`. OpenTofu's `-exclude` does the same.

## Limitations

- This is not a native `-exclude`. Underneath it is `-target`, with Terraform's warning not to use targeting in normal work. The full plan is computed first. On the 200-resource fixture that added 0.1972 s (median) to a 0.0796 s plan.
- Data sources, `moved`, `import`, `removed`, and outputs follow Terraform's `-target` rules, which are not identical to OpenTofu's notes on data sources and outputs. The tests here use only `terraform_data`.
- Backends that execute remotely (`remote`, `cloud`) are refused. A remote state backend that still runs Terraform on your machine (S3, for example) is not refused, and it is not covered by the tests.
- Very large target lists become several Terraform invocations. Each `apply` or `destroy` batch uses Terraform's own confirmation. A failure in a later batch leaves the earlier batches applied.
- `TF_CLI_ARGS` and `TF_CLI_ARGS_<command>` are passed through. Extra `-target` or `-exclude` values there are not interpreted by tf-exclude.
- OpenTofu 1.13 cannot `show -json` a state written by Terraform 1.17 (`terraform_data` attribute `store`). `scripts/prova.sh` applies the fixture once with each binary before comparing plans.
- If Terraform grows a native `-exclude`, this binary becomes a way to keep the same command while moving. Prefer the native flag once it exists.

## Tests

```sh
go test ./...
go test -tags=integration -count=1 ./...   # needs terraform; tofu for the parity cases
```

The module still says Go 1.22. CI compiles with Go 1.23 because current macOS refuses binaries built by Go 1.22 (`dyld: missing LC_UUID load command`). A release binary for macOS needs that newer toolchain too.

Integration tests run real `terraform` and, when `tofu` is on `PATH`, real OpenTofu. They use `terraform_data` only. Set `TF_EXCLUDE_REQUIRE_TERRAFORM=1` or `TF_EXCLUDE_REQUIRE_TOFU=1` to fail instead of skip when a binary is missing. CI runs Terraform 1.5.7, 1.9.8, and 1.17.1, plus OpenTofu 1.13.2, and a 2,000-resource plan that has to survive the command-line limit.

## Project

Questions: [Discussions](https://github.com/eurafaeldecarvalho/tf-exclude/discussions). Bugs and features: the issue templates. Maintainer: Rafael de Carvalho. [Security](SECURITY.md). [Support](SUPPORT.md) is best-effort; there is no guaranteed support.

An AI coding agent wrote the initial implementation. It is covered by the test suite in this repository. The maintainer has not published a separate hands-on test log.

Social preview image: [docs/social-preview.png](docs/social-preview.png) (1280×640).

License: MIT.
