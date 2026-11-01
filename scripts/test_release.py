import hashlib
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch

import release


class ReleaseChecks(unittest.TestCase):
    def test_only_version_string_is_normalized(self):
        files = ["go.mod", "go.sum", "cmd/fcurl/main.go", "internal/cli/run.go"]
        values = {p: b"fixed\n" for p in files}
        def read(ref, path):
            if path == "internal/cli/run.go":
                return f'package cli\nvar Version = "{ref}"\nfunc unchanged() {{}}\n'.encode()
            return values[path]
        with patch.object(release, "git", return_value="\n".join(files)), patch.object(release, "source", side_effect=read):
            self.assertEqual(release.production("0.1.0-dev"), release.production("0.1.0"))
            original = release.production("0.1.0")
            values["go.sum"] = b"different dependency bytes"
            self.assertNotEqual(original, release.production("0.1.0"))

    def test_new_production_inputs_and_runtime_changes_invalidate_reuse(self):
        files = ["go.mod", "go.sum", "cmd/fcurl/main.go", "internal/cli/run.go"]
        def read(ref, path):
            return b'var Version = "0.1.0"\n' if path.endswith("run.go") else path.encode()
        with patch.object(release, "git", return_value="\n".join(files)), patch.object(release, "source", side_effect=read):
            before = release.production("HEAD")
            with patch.object(release, "git", return_value="\n".join(files + ["internal/embedded.json"])):
                self.assertNotEqual(before, release.production("HEAD"))
        def changed(ref, path):
            data = read(ref, path)
            return data + b"func changed() {}\n" if path.endswith("run.go") else data
        with patch.object(release, "git", return_value="\n".join(files)), patch.object(release, "source", side_effect=changed):
            self.assertNotEqual(before, release.production("HEAD"))

    def test_changed_compiler_flags_are_not_metadata(self):
        src = b'import subprocess, os\nenv=dict(os.environ, GOWORK="off", CGO_ENABLED="0")\nsubprocess.run(["go", "build", "-trimpath"], env=env, check=True)\n'
        with patch.object(release, "source", return_value=src):
            before = release.build_recipe("HEAD")
        with patch.object(release, "source", return_value=src.replace(b"-trimpath", b"-race")):
            self.assertNotEqual(before, release.build_recipe("HEAD"))

    def test_missing_or_failed_required_evidence_is_rejected(self):
        run = {"id": 1, "conclusion": "success", "status": "completed", "path": ".github/workflows/ci.yml", "repository": {"full_name": release.REPO}}
        jobs = [{"name": n, "conclusion": "success"} for n in release.NATIVE | {"plan", "required"}]
        with patch.object(release, "api", return_value={"jobs": jobs}):
            release.verify_run(run, False)
            with self.assertRaises(ValueError):
                release.verify_run(run, True)
            with self.assertRaises(ValueError):
                release.verify_run({**run, "conclusion": "failure"}, False)
            with self.assertRaises(ValueError):
                release.verify_run({**run, "repository": {"full_name": "other/repo"}}, False)

    def fixture(self, root):
        evidence = {"version": "0.1.0", "source_commit": "a" * 40, "source_tree": "b" * 40, "toolchain": "1.26.10"}
        for target in release.TARGETS:
            folder = root / ("fcurl-" + target)
            folder.mkdir()
            name = f"fcurl-0.1.0-{target}"
            archive = folder / (name + (".zip" if target.startswith("windows") else ".tar.gz"))
            archive.write_bytes(b"immutable archive " + target.encode())
            (folder / (archive.name + ".sha256")).write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + archive.name + "\n")
            binary = folder / ("fcurl.exe" if target.startswith("windows") else "fcurl")
            binary.write_bytes(b"candidate " + target.encode())
            manifest = {**evidence, "target": target, "source_dirty": False, "toolchain": f"go version go1.26.10 {target.replace('-', '/')}", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
            (folder / (name + ".BUILD.json")).write_text(json.dumps(manifest))
        return evidence

    def test_corrupted_archive_and_wrong_source_cannot_be_published(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            evidence = self.fixture(root)
            files, packages = release.collect(root, evidence)
            self.assertEqual(len(packages), 5)
            archive = files[0]
            original = archive.read_bytes()
            archive.write_bytes(b"corrupted")
            with self.assertRaises(ValueError):
                release.collect(root, evidence)
            archive.write_bytes(original)
            with self.assertRaises(ValueError):
                release.collect(root, {**evidence, "source_commit": "c" * 40})
            (root / "fcurl-linux-amd64/fcurl").write_bytes(b"other binary")
            with self.assertRaises(ValueError):
                release.collect(root, evidence)

    def test_api_does_not_send_credentials_to_unknown_hosts(self):
        with self.assertRaises(ValueError):
            release.api("https://unrelated.example/upload", "POST", binary=b"data")


if __name__ == "__main__":
    unittest.main()
