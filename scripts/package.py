"""Build one native candidate, archive it, and smoke-test those exact installed bytes."""
import argparse
import hashlib
import http.server
import json
import os
import pathlib
import re
import shutil
import subprocess
import tarfile
import tempfile
import threading
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]


def command(*args, **kwargs):
    return subprocess.check_output(args, cwd=ROOT, text=True, **kwargs).strip()


def modules():
    used = set(command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./cmd/fcurl").splitlines())
    raw = command("go", "list", "-m", "-json", "all")
    decoder = json.JSONDecoder()
    result = []
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip())
        if item["Path"] in used:
            result.append(item)
        raw = raw.lstrip()[end:]
    return result


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Length", "15")
        self.end_headers()
        self.wfile.write(b"fcurl-installed")

    def log_message(self, *args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", default=(ROOT / "VERSION").read_text().strip())
    parser.add_argument("--target", help="Expected native GOOS-GOARCH")
    parser.add_argument("--release", action="store_true", help="Require a clean tagged-version candidate")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?", args.version):
        parser.error("invalid version")
    if args.release:
        if command("git", "status", "--porcelain", "--untracked-files=normal"):
            raise SystemExit("release candidates require a clean source tree")
        if args.version != (ROOT / "VERSION").read_text().strip():
            raise SystemExit("release version must match VERSION")
        source = (ROOT / "internal/cli/run.go").read_text()
        if f'var Version = "{args.version}"' not in source:
            raise SystemExit("source version must match the release candidate")
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="0")
    target_info = json.loads(command("go", "env", "-json", "GOOS", "GOARCH", env=env))
    goos, goarch = target_info["GOOS"], target_info["GOARCH"]
    target = f"{goos}-{goarch}"
    if args.target and target != args.target:
        raise SystemExit(f"runner identity {target} differs from expected {args.target}")
    dist = ROOT / "dist"
    dist.mkdir(exist_ok=True)
    name = f"fcurl-{args.version}-{target}"
    binary_name = "fcurl.exe" if goos == "windows" else "fcurl"
    with tempfile.TemporaryDirectory(prefix="fcurl-package-") as temporary:
        staging = pathlib.Path(temporary) / name
        staging.mkdir()
        binary = staging / binary_name
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=true", "-ldflags",
                        f"-X github.com/FastTLS/fastls-curl/internal/cli.Version={args.version}",
                        "-o", str(binary), "./cmd/fcurl"], cwd=ROOT, env=env, check=True)
        for file in ["LICENSE", "NOTICE", "README.md", "COMPATIBILITY.md", "ROADMAP.md", "VALIDATION.md", "INVOCATION_PLAN.md", "RELEASE.md", "VERSION", "DEVELOPMENT_V0.2.md"]:
            shutil.copy2(ROOT / file, staging / file)
        deps = modules()
        for module in deps:
            if module.get("Main"):
                continue
            if "Replace" in module:
                raise SystemExit("replacement modules cannot be packaged")
            directory = pathlib.Path(module["Dir"])
            license_files = []
            for file in directory.rglob("*"):
                if not file.is_file():
                    continue
                base = file.name.upper()
                if not (base.startswith(("LICENSE", "NOTICE", "COPYING", "AUTHORS")) or base == "THIRD_PARTY_NOTICES.MD"):
                    continue
                relative = file.relative_to(directory)
                destination = staging / "third-party" / (module["Path"] + "@" + module["Version"]) / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(file, destination)
                license_files.append(str(relative).replace("\\", "/"))
            if not license_files:
                raise SystemExit(f"missing license materials: {module['Path']}")
            module["PackagedNotices"] = license_files
        sha = hashlib.sha256(binary.read_bytes()).hexdigest()
        manifest = {
            "version": args.version, "target": target, "binary_sha256": sha,
            "source_commit": command("git", "rev-parse", "HEAD"),
            "source_tree": command("git", "rev-parse", "HEAD^{tree}"),
            "source_dirty": bool(command("git", "status", "--porcelain", "--untracked-files=normal")),
            "toolchain": command("go", "version"),
            "go_mod_sha256": hashlib.sha256((ROOT / "go.mod").read_bytes()).hexdigest(),
            "go_sum_sha256": hashlib.sha256((ROOT / "go.sum").read_bytes()).hexdigest(),
            "build_info": command("go", "version", "-m", str(binary)),
            "modules": [{k: v for k, v in item.items() if k not in {"Dir", "GoMod"}} for item in deps],
        }
        (staging / "BUILD.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        if goos == "windows":
            archive = dist / (name + ".zip")
            with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as z:
                for file in staging.rglob("*"):
                    if file.is_file():
                        z.write(file, file.relative_to(staging.parent))
        else:
            archive = dist / (name + ".tar.gz")
            with tarfile.open(archive, "w:gz") as tar:
                tar.add(staging, arcname=name)
        # Verify the archive, not the temporary pre-packaging executable.
        installed = pathlib.Path(temporary) / "installed"
        installed.mkdir()
        if goos == "windows":
            with zipfile.ZipFile(archive) as z:
                z.extractall(installed)
        else:
            with tarfile.open(archive) as tar:
                tar.extractall(installed, filter="data")
        executable = installed / name / binary_name
        if hashlib.sha256(executable.read_bytes()).hexdigest() != sha:
            raise SystemExit("installed binary digest mismatch")
        identity = command(str(executable), "--version")
        if not identity.startswith("fcurl " + args.version + " "):
            raise SystemExit("installed version identity mismatch")
        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                result = command(str(executable), "--max-time", "5", f"http://127.0.0.1:{server.server_port}/")
                if result != "fcurl-installed":
                    raise SystemExit("installed HTTP smoke test failed")
            finally:
                server.shutdown()
                thread.join()
        # Keep a directly runnable copy for developers, using the same bytes.
        shutil.copy2(executable, dist / binary_name)
        (dist / (name + ".BUILD.json")).write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        archive_sha = hashlib.sha256(archive.read_bytes()).hexdigest()
        (dist / (archive.name + ".sha256")).write_text(f"{archive_sha}  {archive.name}\n", encoding="utf-8")
        print(f"Verified {archive.name}: {archive_sha}")
        print(identity)


if __name__ == "__main__":
    main()
