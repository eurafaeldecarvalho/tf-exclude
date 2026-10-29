#!/usr/bin/env bash
# Measure the proof number for tf-exclude.
#
# Reproducible without cloud credentials. Needs:
#   go, python3, terraform >= 1.4 (builtin terraform_data), tofu >= 1.9
# Optional: hyperfine (used for the timing median when it is on PATH).
#
# From the repo root: ./scripts/prova.sh
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

if ! command -v terraform >/dev/null; then
  echo "terraform is not on PATH" >&2
  exit 1
fi
if ! command -v tofu >/dev/null; then
  echo "tofu (OpenTofu >= 1.9) is not on PATH" >&2
  exit 1
fi
if ! command -v python3 >/dev/null; then
  echo "python3 is not on PATH" >&2
  exit 1
fi

export CHECKPOINT_DISABLE=1
export TF_IN_AUTOMATION=1
unset TF_CLI_ARGS TF_CLI_ARGS_plan TF_CLI_ARGS_apply TF_WORKSPACE TF_EXCLUDE_BIN TF_EXCLUDE_ARG_LIMIT || true

mkdir -p "$root/bin"
go build -o "$root/bin/tf-exclude" .
export PATH="$root/bin:$PATH"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/tf" "$work/tofu"
cp -a "$root/testdata/bench200/." "$work/tf/"
cp -a "$root/testdata/bench200/." "$work/tofu/"

# Terraform 1.17 writes a terraform_data "store" attribute that OpenTofu 1.13
# cannot read. Apply the same configuration once with each binary.
( cd "$work/tf" && terraform init -input=false >/dev/null && terraform apply -auto-approve -input=false -lock=false >/dev/null )
( cd "$work/tofu" && tofu init -input=false >/dev/null && tofu apply -auto-approve -input=false -lock=false >/dev/null )

cd "$work/tf"
python3 - "$work/tofu" <<'PY'
import json, statistics, subprocess, sys, time
from shutil import which

tofu_dir = sys.argv[1]

def run(args, cwd=None, check=True):
    p = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and p.returncode != 0:
        raise SystemExit(f"{args[0]} failed ({p.returncode})\n{p.stderr}\n{p.stdout}")
    return p

targets = json.loads(run(["tf-exclude", "targets", "--json", "-input=false", "-lock=false", "-exclude=terraform_data.db"]).stdout)
n = len(targets["targets"])
excluded = targets["excluded"]
resources = targets["resources"]

state = run(["terraform", "state", "list"]).stdout.split()
naive = [a for a in state if a != "terraform_data.db"]

def changes(show_bin, plan, cwd=None):
    raw = json.loads(run([show_bin, "show", "-json", plan], cwd=cwd).stdout)
    out = {}
    for rc in raw.get("resource_changes") or []:
        if rc.get("mode") not in (None, "managed"):
            continue
        out[rc["address"]] = ",".join(rc["change"]["actions"])
    return out

run(["tf-exclude", "plan", "-input=false", "-lock=false", "-out=ex.tfplan", "-exclude=terraform_data.db"])
got = changes("terraform", "ex.tfplan")

hand_args = ["terraform", "plan", "-input=false", "-lock=false", "-out=hand.tfplan"]
hand_args += ["-target=" + a for a in targets["targets"]]
run(hand_args)
hand = changes("terraform", "hand.tfplan")

naive_args = ["terraform", "plan", "-input=false", "-lock=false", "-out=naive.tfplan"]
naive_args += ["-target=" + a for a in naive]
run(naive_args)
naive_plan = changes("terraform", "naive.tfplan")

run(["tofu", "plan", "-input=false", "-lock=false", "-out=tofu.tfplan", "-exclude=terraform_data.db"], cwd=tofu_dir)
tofu = changes("tofu", "tofu.tfplan", cwd=tofu_dir)

equal = got == hand and got == tofu
naive_has_db = "terraform_data.db" in naive_plan

hand_line = "terraform plan " + " ".join("-target=" + a for a in targets["targets"])
naive_line = "terraform plan " + " ".join("-target=" + a for a in naive)

def hyperfine_medians():
    if which("hyperfine") is None:
        return None
    p = subprocess.run([
        "hyperfine", "--warmup", "1", "--runs", "5", "--export-json", "hf.json",
        "terraform plan -input=false -lock=false",
        "tf-exclude plan -exclude terraform_data.db -input=false -lock=false",
    ], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode != 0:
        raise SystemExit(p.stderr)
    data = json.loads(open("hf.json").read())
    return [r["median"] for r in data["results"]], "hyperfine-5"

def python_medians():
    def once(args):
        t = time.perf_counter()
        subprocess.run(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
        return time.perf_counter() - t
    cmds = [
        ["terraform", "plan", "-input=false", "-lock=false"],
        ["tf-exclude", "plan", "-exclude", "terraform_data.db", "-input=false", "-lock=false"],
    ]
    for args in cmds:
        once(args)
    med = []
    for args in cmds:
        samples = [once(args) for _ in range(5)]
        med.append(statistics.median(samples))
    return med, "python-median-5"

med, method = hyperfine_medians() or python_medians()
tf_s, ex_s = med
overhead = ex_s - tf_s

def ver(cmd):
    return subprocess.check_output([cmd, "version"], text=True).splitlines()[0]

print(f"PROOF_RESOURCES={resources}")
print(f"PROOF_EXCLUDED={len(excluded)}")
print(f"PROOF_EXCLUDED_LIST={','.join(excluded)}")
print(f"PROOF_N={n}")
print(f"PROOF_EXCLUDE_FLAGS=1")
print(f"PROOF_NAIVE_TARGETS={len(naive)}")
print(f"PROOF_BEFORE_CHARS={len(hand_line)}")
print(f"PROOF_NAIVE_CHARS={len(naive_line)}")
print(f"PROOF_SET_EQUAL={str(equal).lower()}")
print(f"PROOF_NAIVE_STILL_HAS_DB={str(naive_has_db).lower()}")
print(f"PROOF_TERRAFORM_PLAN_S={tf_s:.4f}")
print(f"PROOF_TF_EXCLUDE_PLAN_S={ex_s:.4f}")
print(f"PROOF_OVERHEAD_S={overhead:.4f}")
print(f"PROOF_TIMING={method}")
print(f"PROOF_TERRAFORM={ver('terraform')}")
print(f"PROOF_TOFU={ver('tofu')}")
if not equal:
    only_got = sorted(set(got) - set(tofu))
    only_tofu = sorted(set(tofu) - set(got))
    only_hand = sorted(set(hand) - set(got))
    raise SystemExit(
        "plan sets are not equal\n"
        f"tf-exclude vs hand extra {only_hand}\n"
        f"only tf-exclude {only_got[:20]} count={len(only_got)}\n"
        f"only tofu {only_tofu[:20]} count={len(only_tofu)}"
    )
if not naive_has_db:
    raise SystemExit("naive target list did not pull terraform_data.db back in; the fixture no longer shows the difference")
print("PROOF_OK=true")
PY
