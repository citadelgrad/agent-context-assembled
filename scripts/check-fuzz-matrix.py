#!/usr/bin/env python3
"""Fail when CI's explicit fuzz matrix drifts from Go fuzz targets."""

from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "ci.yml"
FUZZ_FUNCTION = re.compile(r"^func (Fuzz[A-Za-z0-9_]+)\(f \*testing\.F\)", re.MULTILINE)
MATRIX_ENTRY = re.compile(
    r"^\s*- package: (\./[^\s]+)\n\s+target: (Fuzz[A-Za-z0-9_]+)\s*$",
    re.MULTILINE,
)


def package_for(path: pathlib.Path) -> str:
    return "./" + path.parent.relative_to(ROOT).as_posix()


def main() -> int:
    discovered: set[tuple[str, str]] = set()
    for path in ROOT.rglob("*_test.go"):
        if ".git" in path.parts:
            continue
        text = path.read_text(encoding="utf-8")
        discovered.update((package_for(path), name) for name in FUZZ_FUNCTION.findall(text))

    configured = set(MATRIX_ENTRY.findall(WORKFLOW.read_text(encoding="utf-8")))
    missing = sorted(discovered - configured)
    stale = sorted(configured - discovered)
    if not missing and not stale:
        print(f"fuzz matrix covers all {len(discovered)} targets")
        return 0
    for package, target in missing:
        print(f"missing from CI fuzz matrix: {package} / {target}", file=sys.stderr)
    for package, target in stale:
        print(f"stale CI fuzz matrix entry: {package} / {target}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
