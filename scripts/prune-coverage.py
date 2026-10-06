#!/usr/bin/env python3
"""Remove requirement IDs claimed by a `// Proves:` test comment from
spec/coverage-pending.txt. Run from the repo root after adding tests."""
import pathlib, re

root = pathlib.Path(__file__).resolve().parent.parent
proven = set()
for f in root.rglob("*_test.go"):
    if "spec" in f.parts:
        continue
    for line in f.read_text().splitlines():
        m = re.search(r"//\s*Proves:\s*(.+)$", line)
        if m:
            proven.update(re.findall(r"[A-Z][A-Z0-9]*-\d+[a-z]?", m.group(1)))
pending = root / "spec" / "coverage-pending.txt"
lines = pending.read_text().splitlines()
kept = [l for l in lines if l.startswith("#") or not l.strip() or l.split()[0] not in proven]
pending.write_text("\n".join(kept) + "\n")
ids = [l for l in kept if l.strip() and not l.startswith("#")]
print(f"pruned {len(lines) - len(kept)}, {len(ids)} pending")
