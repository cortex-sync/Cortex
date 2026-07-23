#!/usr/bin/env bash
# Test the launcher's SHA-256 integrity gate (fail-closed) without network.
#
# cortex-git-launch.sh downloads the server binary and verifies its SHA-256
# against the release checksums.txt before caching/running it. This is the
# supply-chain integrity control, so it must fail closed: a tampered archive or a
# missing checksum entry must refuse to install. CORTEX_GIT_RELEASE_BASE points
# the launcher at a local fixture release over file://, so no network is needed.
#
# Requires: bash, curl, tar, sha256sum (or shasum) - the same tools the launcher
# itself needs. Run via `make test-launcher`.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
launcher="$repo_root/bin/cortex-git-launch.sh"
[ -x "$launcher" ] || { echo "test-launcher: $launcher not found or not executable" >&2; exit 1; }

# Match the launcher's own GOOS/GOARCH mapping so the fixture archive name lines up.
case "$(uname -s)" in
Linux) goos=linux ;;
Darwin) goos=darwin ;;
*) echo "test-launcher: unsupported OS for this test" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) goarch=amd64 ;;
arm64 | aarch64) goarch=arm64 ;;
*) echo "test-launcher: unsupported arch for this test" >&2; exit 1 ;;
esac

version="9.9.9"
archive="cortex-git-server_${version}_${goos}_${goarch}.tar.gz"
pass=0
fail=0

# run_case <name> <ok|refuse> <mutate-fn>: builds a fresh fixture release, applies
# the mutation, runs the launcher --prefetch with an isolated plugin root/data,
# and checks the exit status and whether the binary was cached.
run_case() {
	local name="$1" expect="$2" mutate="$3"
	local work root data release build
	work="$(mktemp -d)"
	root="$work/root"; data="$work/data"; release="$work/release"; build="$work/build"
	mkdir -p "$root/bin" "$release" "$build"
	printf 'v%s\n' "$version" >"$root/bin/VERSION"

	# Fake binary -> archive -> checksums.txt (correct hash for the bytes as built).
	printf '#!/bin/sh\necho fake-cortex\n' >"$build/cortex-git-server"
	chmod +x "$build/cortex-git-server"
	tar -czf "$release/$archive" -C "$build" cortex-git-server
	(cd "$release" && sha256sum "$archive" >checksums.txt)

	"$mutate" "$release" # case-specific tampering, applied after the checksum is recorded

	set +e
	local out code
	out="$(CLAUDE_PLUGIN_ROOT="$root" CLAUDE_PLUGIN_DATA="$data" \
		CORTEX_GIT_RELEASE_BASE="file://$release" \
		"$launcher" --prefetch 2>&1)"
	code=$?
	set -e

	local cached="$data/bin/cortex-git-server-${version}-${goos}-${goarch}"
	local ok=1
	if [ "$expect" = "ok" ]; then
		{ [ "$code" -eq 0 ] && [ -x "$cached" ]; } || ok=0
	else # refuse: must exit non-zero AND leave nothing cached
		{ [ "$code" -ne 0 ] && [ ! -e "$cached" ]; } || ok=0
	fi

	if [ "$ok" -eq 1 ]; then
		echo "PASS: $name"
		pass=$((pass + 1))
	else
		echo "FAIL: $name (expect=$expect exit=$code cached=$([ -e "$cached" ] && echo yes || echo no))"
		echo "  output: $out"
		fail=$((fail + 1))
	fi
	rm -rf "$work"
}

noop() { :; }
tamper_archive() { printf 'corruption' >>"$1/$archive"; } # bytes diverge from the recorded checksum
break_checksums() { : >"$1/checksums.txt"; }               # no entry for the archive

run_case "valid checksum installs" ok noop
run_case "tampered archive is refused" refuse tamper_archive
run_case "missing checksum entry is refused" refuse break_checksums

# --- --selftest: the launcher's own MCP-initialize health check ---

write_good_fixture() {
	cat >"$1" <<'FIXTURE'
#!/bin/sh
IFS= read -r _line
echo "some startup log noise" >&2
echo '{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"fake","version":"9.9.9"}}}'
FIXTURE
	chmod +x "$1"
}

write_bad_fixture() {
	cat >"$1" <<'FIXTURE'
#!/bin/sh
echo "not json" >&2
echo "not json either"
FIXTURE
	chmod +x "$1"
}

# selftest_case <name> <ok|refuse> <fixture-writer>: drops the fixture binary in
# as the dev-mode local binary (simpler than the release/checksum fixture
# above - --selftest doesn't care how the binary was found) and checks the
# launcher's exit code and whether the response contains serverInfo.
selftest_case() {
	local name="$1" expect="$2" writer="$3"
	local work root
	work="$(mktemp -d)"
	root="$work/root"
	mkdir -p "$root/mcp/git-server/bin"
	"$writer" "$root/mcp/git-server/bin/cortex-git-server"

	set +e
	local out code
	out="$(CLAUDE_PLUGIN_ROOT="$root" CLAUDE_PLUGIN_DATA="$work/data" "$launcher" --selftest 2>&1)"
	code=$?
	set -e

	local ok=1
	if [ "$expect" = "ok" ]; then
		{ [ "$code" -eq 0 ] && printf '%s' "$out" | grep -q '"serverInfo"'; } || ok=0
	else
		[ "$code" -ne 0 ] || ok=0
	fi

	if [ "$ok" -eq 1 ]; then
		echo "PASS: $name"
		pass=$((pass + 1))
	else
		echo "FAIL: $name (expect=$expect exit=$code)"
		echo "  output: $out"
		fail=$((fail + 1))
	fi
	rm -rf "$work"
}

selftest_case "selftest passes against a well-behaved fixture" ok write_good_fixture
selftest_case "selftest fails against a broken fixture" refuse write_bad_fixture

# --- launcher.log: every run leaves a discoverable trail ---

log_work="$(mktemp -d)"
log_root="$log_work/root"
mkdir -p "$log_root/mcp/git-server/bin"
printf '#!/bin/sh\nexit 0\n' >"$log_root/mcp/git-server/bin/cortex-git-server"
chmod +x "$log_root/mcp/git-server/bin/cortex-git-server"
CLAUDE_PLUGIN_ROOT="$log_root" CLAUDE_PLUGIN_DATA="$log_work/data" "$launcher" --prefetch >/dev/null 2>&1
log_out="$log_work/data/launcher.log"
if [ -f "$log_out" ] && grep -q "starting" "$log_out" && grep -q "binary ready" "$log_out"; then
	echo "PASS: launcher writes a discoverable log file"
	pass=$((pass + 1))
else
	echo "FAIL: launcher writes a discoverable log file (log_out=$log_out)"
	[ -f "$log_out" ] && cat "$log_out"
	fail=$((fail + 1))
fi
rm -rf "$log_work"

echo "----"
echo "test-launcher: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
