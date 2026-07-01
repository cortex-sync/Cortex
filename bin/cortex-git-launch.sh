#!/bin/sh
# Cortex MCP server launcher.
#
# This script is the command Claude Code runs for the `cortex-git` MCP server
# (see .mcp.json). It is always present in the installed plugin, so the MCP
# server's command exists at startup regardless of hook timing. On first run it
# downloads the correct prebuilt `cortex-git-server` binary for this platform
# from the GitHub release named in bin/VERSION, verifies its SHA-256 against the
# release checksums.txt (fail-closed), caches it under ${CLAUDE_PLUGIN_DATA}
# (which survives plugin updates), then exec's it. Subsequent runs exec the
# cached binary directly.
#
# Pass --prefetch to ensure the binary is present without launching the server
# (used by the SessionStart warm-up hook).
#
# Requires: curl, tar, and sha256sum or shasum. On Windows, run under WSL.
set -eu

repo="cortex-sync/Cortex"

prefetch=0
if [ "${1:-}" = "--prefetch" ]; then
	prefetch=1
	shift
fi

# Plugin root: provided by Claude Code when installed; otherwise resolved from
# this script's location so the launcher also works from a local checkout.
root="${CLAUDE_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"

# Developer mode: prefer a locally-built binary if one exists (make build).
local_bin="$root/mcp/git-server/bin/cortex-git-server"
if [ -x "$local_bin" ]; then
	[ "$prefetch" -eq 1 ] && exit 0
	exec "$local_bin" "$@"
fi

tag="$(tr -d '[:space:]' <"$root/bin/VERSION")"
version="${tag#v}" # release archive names drop the leading "v"

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
Linux) goos=linux ;;
Darwin) goos=darwin ;;
*)
	echo "cortex-git launcher: unsupported OS '$os' (on Windows, run under WSL)" >&2
	exit 1
	;;
esac
case "$arch" in
x86_64 | amd64) goarch=amd64 ;;
arm64 | aarch64) goarch=arm64 ;;
*)
	echo "cortex-git launcher: unsupported architecture '$arch'" >&2
	exit 1
	;;
esac

data_dir="${CLAUDE_PLUGIN_DATA:-$HOME/.cache/cortex}"
bin_dir="$data_dir/bin"
bin="$bin_dir/cortex-git-server-$version-$goos-$goarch"

if [ ! -x "$bin" ]; then
	# Release download base. Overridable via CORTEX_GIT_RELEASE_BASE for a mirror
	# or for offline testing (the test suite points it at a local fixture over
	# file://); defaults to the GitHub release. The SHA-256 check below is the
	# integrity guarantee regardless of where the bytes come from.
	base="${CORTEX_GIT_RELEASE_BASE:-https://github.com/$repo/releases/download/$tag}"
	archive="cortex-git-server_${version}_${goos}_${goarch}.tar.gz"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT

	echo "cortex-git launcher: fetching $archive ($tag)..." >&2
	curl -fsSL "$base/$archive" -o "$tmp/$archive" || {
		echo "cortex-git launcher: could not fetch the MCP server binary for $tag ($base/$archive)." >&2
		echo "cortex-git launcher: that release asset may not be published yet - without it the cortex-git tools cannot start." >&2
		echo "cortex-git launcher: from a source checkout, build it locally instead:" >&2
		echo "cortex-git launcher:     (cd \"$root/mcp/git-server\" && make build)   # needs Go" >&2
		echo "cortex-git launcher: then reload the plugin. Details: docs/usage.md > Troubleshooting." >&2
		exit 1
	}
	curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" ||
		{ echo "cortex-git launcher: checksums download failed: $base/checksums.txt" >&2; exit 1; }

	expected="$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")"
	[ -n "$expected" ] ||
		{ echo "cortex-git launcher: no checksum entry for $archive" >&2; exit 1; }
	if command -v sha256sum >/dev/null 2>&1; then
		actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
	else
		actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
	fi
	[ "$actual" = "$expected" ] ||
		{ echo "cortex-git launcher: SHA-256 mismatch for $archive (refusing to run)" >&2; exit 1; }

	tar -xzf "$tmp/$archive" -C "$tmp"
	mkdir -p "$bin_dir"
	mv "$tmp/cortex-git-server" "$bin.tmp"
	chmod +x "$bin.tmp"
	mv "$bin.tmp" "$bin"
	rm -rf "$tmp"
	trap - EXIT
fi

if [ "$prefetch" -eq 1 ]; then
	echo "cortex-git launcher: binary ready ($bin)" >&2
	exit 0
fi

exec "$bin" "$@"
