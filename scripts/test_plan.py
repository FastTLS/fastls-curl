"""Conservatively select checks using the workspace testing policy."""
import argparse
import json
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parents[1]
TARGETS = [
    {"runner": "ubuntu-24.04", "target": "linux-amd64"},
    {"runner": "windows-2025", "target": "windows-amd64"},
    {"runner": "ubuntu-24.04-arm", "target": "linux-arm64"},
    {"runner": "macos-15-intel", "target": "darwin-amd64"},
    {"runner": "macos-15", "target": "darwin-arm64"},
]


def plan(paths):
    docs = all(p.endswith(".md") or p in {"LICENSE", "NOTICE", ".gitignore"} for p in paths)
    if paths and docs:
        return "docs", "Only documentation or repository ignore rules changed"
    production = [p for p in paths if not p.endswith("_test.go")]
    risks = ("internal/cli/", "cmd/", "go.mod", "go.sum", "scripts/package.py")
    if not paths or any(p.startswith(risks) for p in production):
        return "minor", "Initial acceptance or TLS/proxy/header/stream/dependency/packaging surface changed"
    known_tooling = (".github/", "scripts/check_docs.py", "scripts/test_plan.py", "scripts/test_test_plan.py")
    if all(p.endswith("_test.go") or p.endswith(".md") or p.startswith(known_tooling) for p in paths):
        return "patch", "Targeted tests or workflow changes"
    return "minor", "Unclassified input changes require conservative acceptance"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="")
    parser.add_argument("--tier", choices=["auto", "patch", "minor"], default="auto")
    parser.add_argument("--output")
    parser.add_argument("--github-output")
    args = parser.parse_args()
    base = args.base
    if not base or set(base) == {"0"}:
        paths = subprocess.check_output(["git", "ls-files"], cwd=ROOT, text=True).splitlines()
    else:
        # Missing history is an error, never silently downgraded to a smaller tier.
        paths = subprocess.check_output(["git", "diff", "--name-only", base, "HEAD"], cwd=ROOT, text=True).splitlines()
    tier, reason = plan(paths)
    rank = {"docs": 0, "patch": 1, "minor": 2}
    if args.tier != "auto" and rank[args.tier] > rank[tier]:
        tier, reason = args.tier, reason + "; raised by explicit input"
    evidence = None
    if tier != "docs" and args.tier == "auto" and (ROOT / "RELEASE_VALIDATION.json").exists():
        from release import metadata_equivalent, verify_baseline
        config = json.loads((ROOT / "RELEASE_VALIDATION.json").read_text())
        if metadata_equivalent("HEAD", config["runtime_head"]):
            # A successful five-platform + race/resource run must be verified,
            # not merely claimed in a local file, before narrowing this scope.
            evidence = verify_baseline(config)
            tier, reason = "identity", "Verified unchanged production/compiler inputs; run five native identity/install checks"
    matrix = [] if tier == "docs" else TARGETS[:2] if tier == "patch" else TARGETS
    result = {"tier": tier, "reason": reason, "base": base, "paths": paths,
              "matrix": {"include": matrix}, "resource": tier == "minor",
              "race": tier == "minor", "performance_certification": False,
              "reused_runtime_evidence": evidence}
    text = json.dumps(result, indent=2)
    print(text)
    if args.output:
        pathlib.Path(args.output).write_text(text + "\n", encoding="utf-8")
    if args.github_output:
        with open(args.github_output, "a", encoding="utf-8") as output:
            output.write(f"tier={tier}\nmatrix={json.dumps(result['matrix'])}\n")


if __name__ == "__main__":
    main()
