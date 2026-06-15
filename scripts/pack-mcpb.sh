#!/usr/bin/env bash
# Pack a Cortex .mcpb desktop-extension bundle for one platform.
#
# A .mcpb is a zip of `manifest.json` + the cortex-git-server binary (the Snyk
# desktop-extension model). Claude Desktop / Cowork install it from the
# Connectors page and run the binary host-side as a local MCP server, injecting
# the PAT via the user_config -> CORTEX_GIT_* env vars declared in the manifest.
#
# One bundle targets one OS/arch: the binary bytes inside decide the arch, and
# the manifest's win32 platform_override adds the .exe extension. We therefore
# build a separate bundle per release target rather than baking every binary
# into one fat archive.
#
# Usage:
#   scripts/pack-mcpb.sh [GOOS] [GOARCH]
# Defaults to the host platform (via `go env`). Examples:
#   scripts/pack-mcpb.sh                 # host os/arch
#   scripts/pack-mcpb.sh darwin arm64
#   scripts/pack-mcpb.sh windows amd64
#
# Output: dist/cortex-git_<version>_<goos>_<goarch>.mcpb
# Requires: go, python3. Archiver: mcpb (preferred), else zip, else python3.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$repo_root"

manifest="mcpb/manifest.json"
server_dir="mcp/git-server"
out_dir="dist"

command -v go >/dev/null 2>&1 || { echo "pack-mcpb: 'go' is required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "pack-mcpb: 'python3' is required" >&2; exit 1; }
[ -f "$manifest" ] || { echo "pack-mcpb: $manifest not found" >&2; exit 1; }

goos="${1:-$(go env GOOS)}"
goarch="${2:-$(go env GOARCH)}"

# The manifest is the single source of truth for name + version. Python validates
# both are non-empty strings (rejecting null/missing/non-string), and prints them
# one per line; command substitution (not process substitution) makes a python
# failure propagate as a non-zero exit the `||` catches, failing closed before we
# build. Reading line-by-line preserves any internal whitespace verbatim.
manifest_meta="$(python3 -c '
import json, sys
m = json.load(open(sys.argv[1]))
for key in ("name", "version"):
    v = m.get(key)
    if not isinstance(v, str) or not v.strip():
        sys.exit("manifest %s must be a non-empty string" % key)
print(m["name"])
print(m["version"])
' "$manifest")" || { echo "pack-mcpb: $manifest is invalid (bad JSON or missing/empty name/version)" >&2; exit 1; }
{ read -r name; read -r version; } <<<"$manifest_meta"

bin_name="cortex-git-server"
[ "$goos" = "windows" ] && bin_name="cortex-git-server.exe"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

echo "pack-mcpb: building $bin_name for $goos/$goarch (v$version)..." >&2
# Mirror the release ldflags so `--version` reports the real version. Date is
# fixed/empty here to keep the build reproducible; goreleaser stamps the real
# date for published releases.
( cd "$server_dir" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" \
    -o "$stage/$bin_name" ./cmd/server )

cp "$manifest" "$stage/manifest.json"
# Optional icon: include it only if present so the bundle stays minimal.
[ -f "mcpb/icon.png" ] && cp "mcpb/icon.png" "$stage/icon.png"

# Each bundle is single-platform, so point server.entry_point at the binary
# that is actually inside it. platform_overrides covers mcp_config.command but
# not entry_point, so the Windows bundle would otherwise name a (.exe-less)
# file it does not contain.
staged="$stage/manifest.json" bin_name="$bin_name" python3 - <<'PY'
import json, os
path = os.environ["staged"]
with open(path) as f:
    m = json.load(f)
m["server"]["entry_point"] = os.environ["bin_name"]
with open(path, "w") as f:
    json.dump(m, f, indent=2)
    f.write("\n")
PY

mkdir -p "$out_dir"
out="$out_dir/${name}_${version}_${goos}_${goarch}.mcpb"
rm -f "$out"
out_abs="$repo_root/$out"

# Pack from inside the staging dir so entries sit at the archive root.
# Preserving the binary's executable bit matters on macOS/Linux.
if command -v mcpb >/dev/null 2>&1; then
	( cd "$stage" && mcpb pack . "$out_abs" )
elif command -v zip >/dev/null 2>&1; then
	( cd "$stage" && zip -qr -X "$out_abs" . )
elif command -v python3 >/dev/null 2>&1; then
	echo "pack-mcpb: mcpb/zip not found, using python3 (preserving exec bits)" >&2
	stage="$stage" out_abs="$out_abs" bin_name="$bin_name" python3 - <<'PY'
import os, zipfile
stage = os.environ["stage"]
out = os.environ["out_abs"]
binname = os.environ["bin_name"]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(stage):
        for f in sorted(files):
            full = os.path.join(root, f)
            arc = os.path.relpath(full, stage)
            zi = zipfile.ZipInfo.from_file(full, arc)
            # rwxr-xr-x for the binary, rw-r--r-- for everything else.
            # 0o100000 = S_IFREG: keep the regular-file flag so unzip tools
            # honour the permission bits (and the binary's exec bit).
            mode = 0o755 if arc == binname else 0o644
            zi.external_attr = (mode | 0o100000) << 16
            with open(full, "rb") as fh:
                z.writestr(zi, fh.read())
PY
else
	echo "pack-mcpb: need one of mcpb, zip, or python3 to create the archive" >&2
	exit 1
fi

echo "pack-mcpb: wrote $out" >&2
