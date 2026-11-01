"""Check repository-local Markdown links without building or accessing the network."""
import pathlib
import re
import sys
from urllib.parse import unquote

ROOT = pathlib.Path(__file__).resolve().parents[1]
errors = []
for path in ROOT.glob("*.md"):
    text = path.read_text(encoding="utf-8")
    for target in re.findall(r"\]\(([^)]+)\)", text):
        target = target.split("#", 1)[0]
        if not target or "://" in target or target.startswith("mailto:"):
            continue
        if not (path.parent / unquote(target)).exists():
            errors.append(f"{path.name}: missing link target {target}")
if errors:
    print("\n".join(errors), file=sys.stderr)
    sys.exit(1)
print("Repository Markdown links OK")
