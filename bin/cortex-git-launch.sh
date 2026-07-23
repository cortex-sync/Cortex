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
# (used by the SessionStart warm-up hook). Pass --selftest to additionally send
# a synthetic MCP `initialize` over stdio and confirm the binary responds - a
# quick way to tell "the binary/launcher is fine" apart from "Claude Code isn't
# spawning/exposing it", which is otherwise indistinguishable from here (see
# docs/usage.md > Troubleshooting).
#
# Claude Code can count this server as installed on plugin reload without its
# process ever actually running - a known Claude Code issue with plugin-root
# .mcp.json environment-variable expansion (anthropics/claude-code#9427) - so
# every message below goes to stderr AND to a small log file (see log(),
# below): a run Claude Code never surfaces still leaves a discoverable trail.
#
# Requires: curl, tar, and sha256sum or shasum. On Windows, run under WSL.
set -eu

repo="cortex-sync/Cortex"

# Best-effort: a log-file write failure must never abort the launcher itself.
log_file="${CLAUDE_PLUGIN_DATA:-$HOME/.cache/cortex}/launcher.log"
log() {
	msg="cortex-git launcher: $*"
	echo "$msg" >&2
	{
		mkdir -p "$(dirname -- "$log_file")" &&
			printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$msg" >>"$log_file" &&
			tail -n 500 "$log_file" >"$log_file.tmp" && mv "$log_file.tmp" "$log_file"
	} 2>/dev/null || true
}

prefetch=0
selftest=0
case "${1:-}" in
--prefetch)
	prefetch=1
	shift
	;;
--selftest)
	selftest=1
	shift
	;;
esac

log "starting (pid=$$, prefetch=$prefetch, selftest=$selftest)"

# Plugin root: provided by Claude Code when installed; otherwise resolved from
# this script's location so the launcher also works from a local checkout.
root="${CLAUDE_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"

# Developer mode: prefer a locally-built binary if one exists (make build).
local_bin="$root/mcp/git-server/bin/cortex-git-server"
if [ -x "$local_bin" ]; then
	log "using local dev binary $local_bin"
	bin="$local_bin"
else
	tag="$(tr -d '[:space:]' <"$root/bin/VERSION")"
	version="${tag#v}" # release archive names drop the leading "v"

	os="$(uname -s)"
	arch="$(uname -m)"
	case "$os" in
	Linux) goos=linux ;;
	Darwin) goos=darwin ;;
	*)
		log "unsupported OS '$os' (on Windows, run under WSL)"
		exit 1
		;;
	esac
	case "$arch" in
	x86_64 | amd64) goarch=amd64 ;;
	arm64 | aarch64) goarch=arm64 ;;
	*)
		log "unsupported architecture '$arch'"
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

		log "fetching $archive ($tag)..."
		curl -fsSL "$base/$archive" -o "$tmp/$archive" || {
			log "could not fetch the MCP server binary for $tag ($base/$archive)."
			log "that release asset may not be published yet - without it the cortex-git tools cannot start."
			log "from a source checkout, build it locally instead:"
			log "    (cd \"$root/mcp/git-server\" && make build)   # needs Go"
			log "then reload the plugin. Details: docs/usage.md > Troubleshooting."
			exit 1
		}
		curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" ||
			{ log "checksums download failed: $base/checksums.txt"; exit 1; }

		expected="$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")"
		[ -n "$expected" ] ||
			{ log "no checksum entry for $archive"; exit 1; }
		if command -v sha256sum >/dev/null 2>&1; then
			actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
		else
			actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
		fi
		[ "$actual" = "$expected" ] ||
			{ log "SHA-256 mismatch for $archive (refusing to run)"; exit 1; }

		tar -xzf "$tmp/$archive" -C "$tmp"
		mkdir -p "$bin_dir"
		mv "$tmp/cortex-git-server" "$bin.tmp"
		chmod +x "$bin.tmp"
		mv "$bin.tmp" "$bin"
		rm -rf "$tmp"
		trap - EXIT
	fi
fi

if [ "$prefetch" -eq 1 ]; then
	log "binary ready ($bin)"
	exit 0
fi

if [ "$selftest" -eq 1 ]; then
	log "self-test: sending a synthetic MCP initialize to $bin"
	init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-06-18","capabilities":{},"clientInfo":{"name":"cortex-selftest","version":"1"}}}'
	initialized_notif='{"jsonrpc":"2.0","method":"notifications/initialized"}'
	# Capture stdout and stderr separately - the server logs a startup line to
	# stderr (log.Printf writes there by default), which would otherwise land on
	# the same stream as the JSON-RPC response and break a naive first-line
	# check.
	stderr_tmp="$(mktemp)"
	trap 'rm -f "$stderr_tmp"' EXIT
	response="$(printf '%s\n%s\n' "$init_req" "$initialized_notif" | "$bin" 2>"$stderr_tmp" | head -n1)"
	case "$response" in
	*'"serverInfo"'*)
		log "self-test: PASS - $bin responded with serverInfo"
		printf '%s\n' "$response"
		exit 0
		;;
	*)
		log "self-test: FAIL - no serverInfo in the response from $bin"
		printf 'response: %s\n' "$response" >&2
		if [ -s "$stderr_tmp" ]; then
			log "self-test: $bin wrote to stderr:"
			while IFS= read -r line; do log "  $line"; done <"$stderr_tmp"
		fi
		exit 1
		;;
	esac
fi

log "exec'ing $bin (pid=$$)"
exec "$bin" "$@"
