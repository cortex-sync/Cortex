#!/usr/bin/env python3
"""Verify the structure of one or more Cortex .mcpb bundles.

Catches regressions in mcpb/manifest.json or scripts/pack-mcpb.sh without
needing the (npm-only) `mcpb` CLI. For each bundle it asserts:

  - manifest.json sits at the archive root and parses as JSON;
  - the required manifest fields are present and well-formed;
  - server.entry_point names a file that is actually in the bundle;
  - a non-Windows entry_point carries the executable bit (S_IFREG + 0o111);
  - the icon referenced by the manifest is present;
  - every declared tool has a name.

Usage: python3 scripts/check-mcpb.py <bundle.mcpb | dir>...
Exits non-zero (with a clear message per failure) if any check fails.
Requires: python3 stdlib only.
"""
from __future__ import annotations

import glob
import json
import os
import sys
import zipfile

REQUIRED_TOP = ("manifest_version", "name", "version", "description", "server")


def _fail(bundle: str, msg: str) -> str:
    return f"{os.path.basename(bundle)}: {msg}"


def check_bundle(path: str) -> list[str]:
    """Return a list of problems (empty == the bundle is well-formed)."""
    errors: list[str] = []
    try:
        z = zipfile.ZipFile(path)
    except zipfile.BadZipFile:
        return [_fail(path, "not a valid zip/.mcpb archive")]

    names = set(z.namelist())
    if "manifest.json" not in names:
        return [_fail(path, "manifest.json is missing from the archive root")]

    try:
        m = json.loads(z.read("manifest.json"))
    except json.JSONDecodeError as exc:
        return [_fail(path, f"manifest.json is not valid JSON: {exc}")]

    for field in REQUIRED_TOP:
        if field not in m:
            errors.append(_fail(path, f"manifest missing required field '{field}'"))

    server = m.get("server", {})
    entry = server.get("entry_point")
    if not entry:
        errors.append(_fail(path, "server.entry_point is missing"))
    elif entry not in names:
        errors.append(_fail(path, f"server.entry_point '{entry}' is not in the bundle"))
    elif not entry.endswith(".exe"):
        # On Unix the binary must be executable, or the host can't launch it.
        mode = (z.getinfo(entry).external_attr >> 16) & 0o777
        is_reg = (z.getinfo(entry).external_attr >> 16) & 0o170000 == 0o100000
        if not is_reg:
            errors.append(_fail(path, f"'{entry}' is missing the regular-file (S_IFREG) flag"))
        if not mode & 0o111:
            errors.append(_fail(path, f"'{entry}' is not marked executable (mode {oct(mode)})"))

    icon = m.get("icon")
    if icon and icon not in names:
        errors.append(_fail(path, f"icon '{icon}' is referenced but not in the bundle"))

    for i, tool in enumerate(m.get("tools", [])):
        if not tool.get("name"):
            errors.append(_fail(path, f"tools[{i}] has no name"))

    return errors


def main() -> int:
    args = sys.argv[1:]
    if not args:
        print("usage: check-mcpb.py <bundle.mcpb | dir>...", file=sys.stderr)
        return 2

    bundles: list[str] = []
    for arg in args:
        if os.path.isdir(arg):
            bundles.extend(sorted(glob.glob(os.path.join(arg, "*.mcpb"))))
        else:
            bundles.append(arg)

    if not bundles:
        print("check-mcpb: no .mcpb bundles found", file=sys.stderr)
        return 1

    all_errors: list[str] = []
    for b in bundles:
        errs = check_bundle(b)
        if errs:
            all_errors.extend(errs)
        else:
            print(f"check-mcpb: OK  {os.path.basename(b)}")

    if all_errors:
        print("\ncheck-mcpb: FAILED", file=sys.stderr)
        for e in all_errors:
            print(f"  - {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
