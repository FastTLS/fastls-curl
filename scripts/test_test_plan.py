import unittest
from test_plan import plan


class TestScope(unittest.TestCase):
    def test_docs_only(self):
        self.assertEqual(plan(["README.md", "COMPATIBILITY.md", "LICENSE"])[0], "docs")

    def test_risk_cannot_be_hidden_by_docs_or_tests(self):
        for path in ["internal/cli/run.go", "internal/cli/options.go", "cmd/fcurl/main.go", "go.mod", "go.sum", "scripts/package.py"]:
            with self.subTest(path=path):
                self.assertEqual(plan(["README.md", path, "internal/cli/run_test.go"])[0], "minor")

    def test_targeted_tooling(self):
        self.assertEqual(plan(["internal/cli/run_test.go", ".github/workflows/ci.yml"])[0], "patch")

    def test_unknown_or_missing_baseline(self):
        self.assertEqual(plan([])[0], "minor")
        self.assertEqual(plan(["new-input.json"])[0], "minor")


if __name__ == "__main__":
    unittest.main()
