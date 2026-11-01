"""Verify evidence, promote immutable CI candidates and publish a resumable Release."""
import argparse
import ast
import hashlib
import json
import os
import pathlib
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
REPO = "FastTLS/fastls-curl"
TARGETS = ["linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"]
NATIVE = {"Native / " + target for target in TARGETS}


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()


def source(ref, path):
    return subprocess.check_output(["git", "show", f"{ref}:{path}"], cwd=ROOT)


def production(ref):
    files = git("ls-tree", "-r", "--name-only", ref).splitlines()
    result = {}
    for path in files:
        if path in {"go.mod", "go.sum"} or path.startswith(("cmd/", "internal/")) and not path.endswith("_test.go"):
            data = source(ref, path)
            if path == "internal/cli/run.go":
                data, count = re.subn(rb'(?m)^var Version = "[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?"\r?$', b'var Version = "<version>"', data)
                if count != 1:
                    raise ValueError("cannot identify exactly one source version declaration")
            result[path] = hashlib.sha256(data.replace(b"\r\n", b"\n")).hexdigest()
    if not {"go.mod", "go.sum", "cmd/fcurl/main.go", "internal/cli/run.go"} <= result.keys():
        raise ValueError("production input set is incomplete")
    return result


def build_recipe(ref):
    tree = ast.parse(source(ref, "scripts/package.py"))
    builds, environments = [], []
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr == "run" and node.args:
            values = node.args[0]
            if isinstance(values, ast.List) and len(values.elts) > 1 and isinstance(values.elts[0], ast.Constant) and values.elts[0].value == "go":
                builds.append(ast.dump(node, include_attributes=False))
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "dict" and any(k.arg == "CGO_ENABLED" for k in node.keywords):
            environments.append(ast.dump(node, include_attributes=False))
    if len(builds) != 1 or len(environments) != 1:
        raise ValueError("build recipe is not uniquely identifiable")
    return builds + environments


def metadata_equivalent(current, baseline):
    return production(current) == production(baseline) and build_recipe(current) == build_recipe(baseline)


def api(path, method="GET", body=None, binary=None, content_type=None, allow_missing=False):
    if not path.startswith("https://"):
        path = f"https://api.github.com/repos/{REPO}/" + path.lstrip("/")
    host = urllib.parse.urlsplit(path).hostname
    if host not in {"api.github.com", "uploads.github.com"}:
        raise ValueError("unexpected GitHub API host")
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "fcurl-release", "X-GitHub-Api-Version": "2022-11-28"}
    token = os.environ.get("GH_TOKEN")
    if token:
        headers["Authorization"] = "Bearer " + token
    data = binary if binary is not None else json.dumps(body).encode() if body is not None else None
    if data is not None:
        headers["Content-Type"] = content_type or "application/json"
    request = urllib.request.Request(path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.load(response) if response.status != 204 else {}
    except urllib.error.HTTPError as error:
        if error.code == 404 and allow_missing:
            return None
        raise RuntimeError(f"GitHub API {method} failed: HTTP {error.code}") from None


def verify_run(run, require_runtime):
    if run.get("conclusion") != "success" or run.get("status") != "completed" or run.get("path") != ".github/workflows/ci.yml" or run.get("repository", {}).get("full_name") != REPO:
        raise ValueError("validation run identity or result is invalid")
    jobs = api(f"actions/runs/{run['id']}/jobs?per_page=100")["jobs"]
    needed = NATIVE | {"plan", "required"}
    if require_runtime:
        needed.add("race-and-resource")
    successful = {j["name"] for j in jobs if j["conclusion"] == "success"}
    if not needed <= successful:
        raise ValueError("validation run is missing selected successful checks")
    return jobs


def verify_baseline(config=None):
    config = config or json.loads((ROOT / "RELEASE_VALIDATION.json").read_text())
    baseline = config["runtime_head"]
    if not metadata_equivalent("HEAD", baseline):
        raise ValueError("production or compiler inputs changed; complete fresh runtime acceptance")
    run = api(f"actions/runs/{config['runtime_run_id']}")
    if run["head_sha"] != baseline:
        raise ValueError("runtime evidence does not match its pinned source")
    verify_run(run, True)
    versions = re.findall(r"go-version:\s*['\"]?([0-9.]+)", source(baseline, ".github/workflows/ci.yml").decode())
    if not versions or set(versions) != {config["toolchain"]}:
        raise ValueError("baseline toolchain cannot be verified")
    if f"toolchain go{config['toolchain']}" not in source("HEAD", "go.mod").decode():
        raise ValueError("candidate toolchain differs from runtime acceptance")
    return {"runtime_run_id": run["id"], "runtime_head": baseline, "runtime_url": run["html_url"], "toolchain": config["toolchain"], "production_inputs": production("HEAD"), "build_recipe_unchanged": True}


def version_tag(tag):
    version = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version) or tag != "v" + version:
        raise ValueError("tag must match the stable VERSION file")
    if git("rev-parse", tag + "^{commit}") != git("rev-parse", "HEAD"):
        raise ValueError("tag does not identify this exact checkout")
    if git("status", "--porcelain", "--untracked-files=normal"):
        raise ValueError("release source must be clean")
    return version


def verify_remote_tag(tag, expected):
    obj = api("git/ref/tags/" + urllib.parse.quote(tag, safe=""))["object"]
    for _ in range(5):
        if obj["type"] == "commit":
            if obj["sha"] != expected:
                raise ValueError("remote release tag changed its source identity")
            return
        if obj["type"] != "tag":
            break
        obj = api("git/tags/" + obj["sha"])["object"]
    raise ValueError("remote release tag does not resolve to a commit")


def preflight(args):
    version = version_tag(args.tag)
    subprocess.run(["git", "merge-base", "--is-ancestor", "HEAD", "origin/main"], cwd=ROOT, check=True)
    evidence = verify_baseline()
    head = git("rev-parse", "HEAD")
    verify_remote_tag(args.tag, head)
    runs = api(f"actions/runs?head_sha={head}&event=push&per_page=30")["workflow_runs"]
    candidates = [r for r in runs if r["path"] == ".github/workflows/ci.yml" and r["head_sha"] == head and r["conclusion"] == "success"]
    if not candidates:
        raise ValueError("no successful candidate CI for this exact release commit")
    candidate = max(candidates, key=lambda r: r["id"])
    verify_run(candidate, False)
    artifacts = api(f"actions/runs/{candidate['id']}/artifacts?per_page=100")["artifacts"]
    by_name = {a["name"]: a for a in artifacts if not a["expired"]}
    if not {"fcurl-" + t for t in TARGETS} <= by_name.keys():
        raise ValueError("candidate platform artifacts are missing or expired")
    evidence.update({"version": version, "tag": args.tag, "source_commit": head, "source_tree": git("rev-parse", "HEAD^{tree}"), "candidate_run_id": candidate["id"], "candidate_url": candidate["html_url"], "candidate_artifacts": [{"name": by_name["fcurl-"+t]["name"], "id": by_name["fcurl-"+t]["id"], "digest": by_name["fcurl-"+t].get("digest")} for t in TARGETS]})
    path = ROOT / "dist/SOURCE_VERIFICATION.json"
    path.parent.mkdir(exist_ok=True)
    path.write_text(json.dumps(evidence, indent=2) + "\n")
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"version={version}\ncandidate_run_id={candidate['id']}\n")
    print(json.dumps({"version": version, "candidate_run_id": candidate["id"], "runtime_run_id": evidence["runtime_run_id"]}))


def collect(directory, evidence):
    files, packages = [], []
    for target in TARGETS:
        folder = directory / ("fcurl-" + target)
        suffix = ".zip" if target.startswith("windows") else ".tar.gz"
        name = f"fcurl-{evidence['version']}-{target}"
        archive = folder / (name + suffix)
        metadata = folder / (name + ".BUILD.json")
        manifest = json.loads(metadata.read_text())
        if manifest["version"] != evidence["version"] or manifest["target"] != target or manifest["source_dirty"] or manifest["source_commit"] != evidence["source_commit"] or manifest["source_tree"] != evidence["source_tree"]:
            raise ValueError(f"candidate identity mismatch: {target}")
        if manifest["toolchain"] != f"go version go{evidence['toolchain']} {target.replace('-', '/')}":
            raise ValueError(f"candidate toolchain mismatch: {target}")
        for name in ["go.mod", "go.sum"]:
            if manifest[name.replace('.', '_') + "_sha256"] != evidence["production_inputs"][name]:
                raise ValueError(f"candidate dependency identity mismatch: {target}")
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        if (folder / (archive.name + ".sha256")).read_text().split() != [digest, archive.name]:
            raise ValueError(f"candidate archive digest mismatch: {target}")
        # The already-installed candidate is preserved; promotion never rebuilds.
        binary = folder / ("fcurl.exe" if target.startswith("windows") else "fcurl")
        if hashlib.sha256(binary.read_bytes()).hexdigest() != manifest["binary_sha256"]:
            raise ValueError(f"candidate executable digest mismatch: {target}")
        packages.append({"target": target, "archive": archive.name, "sha256": digest, "binary_sha256": manifest["binary_sha256"]})
        files += [archive, metadata]
    return files, packages


def publish(args):
    version_tag(args.tag)
    evidence_path = ROOT / "dist/SOURCE_VERIFICATION.json"
    evidence = json.loads(evidence_path.read_text())
    if evidence["source_commit"] != git("rev-parse", "HEAD") or evidence["tag"] != args.tag:
        raise ValueError("source evidence belongs to another release")
    verify_remote_tag(args.tag, evidence["source_commit"])
    files, packages = collect(pathlib.Path(args.artifacts), evidence)
    sums = ROOT / "dist/SHA256SUMS"
    sums.write_text("".join(f"{p['sha256']}  {p['archive']}\n" for p in packages))
    record = ROOT / "dist/RELEASE.json"
    record.write_text(json.dumps({**evidence, "release_run_id": os.environ.get("GITHUB_RUN_ID"), "packages": packages}, indent=2) + "\n")
    files += [sums, record, evidence_path]
    release = api("releases/tags/" + args.tag, allow_missing=True)
    expected = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    if release is None:
        release = api("releases", "POST", {"tag_name": args.tag, "name": "fcurl " + args.tag, "draft": True, "prerelease": False, "body": (ROOT / "RELEASE_NOTES.md").read_text()})
    existing = {a["name"]: a for a in api(f"releases/{release['id']}/assets?per_page=100")}
    for path in files:
        if path.name in existing:
            if existing[path.name].get("digest") != "sha256:" + expected[path.name]:
                raise ValueError("refusing to overwrite a different release asset: " + path.name)
            continue
        if not release["draft"]:
            raise ValueError("published release is incomplete; refusing to mutate its asset set")
        url = release["upload_url"].split("{", 1)[0] + "?name=" + urllib.parse.quote(path.name)
        uploaded = api(url, "POST", binary=path.read_bytes(), content_type="application/octet-stream")
        if uploaded.get("digest") != "sha256:" + expected[path.name]:
            raise ValueError("uploaded asset digest mismatch: " + path.name)
    actual = {a["name"]: a for a in api(f"releases/{release['id']}/assets?per_page=100")}
    if set(actual) != set(expected) or any(actual[n].get("digest") != "sha256:" + sha for n, sha in expected.items()):
        raise ValueError("release asset set does not match the verified candidate")
    verify_remote_tag(args.tag, evidence["source_commit"])
    if release["draft"]:
        release = api(f"releases/{release['id']}", "PATCH", {"draft": False, "make_latest": "true"})
    published = api("releases/tags/" + args.tag)
    if published["draft"] or published["tag_name"] != args.tag:
        raise ValueError("release publication did not complete")
    print("Published and verified:", published["html_url"])


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("baseline")
    pre = sub.add_parser("preflight"); pre.add_argument("--tag", required=True)
    pub = sub.add_parser("publish"); pub.add_argument("--tag", required=True); pub.add_argument("--artifacts", required=True)
    args = parser.parse_args()
    if args.command == "baseline":
        print(json.dumps(verify_baseline()))
    elif args.command == "preflight":
        preflight(args)
    else:
        publish(args)


if __name__ == "__main__":
    main()
