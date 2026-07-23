#!/usr/bin/env bash
# Regression test for install-codex.sh's AGENTS.md snapshot/drift handling.
#
# Covers the "skills data-loss" finding: a re-run used to decide whether to back
# up the existing AGENTS.md using two signals that both fail once the file has
# been managed before - "does .cortex-bak already exist" (Tier 1 install) and
# "does the marker line survive" (--uninstall) - neither of which detects a user
# editing the file (or restore-profile reconciling it) while leaving the marker
# line intact. The fix compares $dest against a $dest.cortex-last snapshot of
# exactly what Cortex wrote last time. No Codex binary or network needed.
#
# Run via `make test-install-codex`.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
script="$repo_root/scripts/install-codex.sh"
[ -x "$script" ] || { echo "test-install-codex: $script not found or not executable" >&2; exit 1; }

pass=0
fail=0

check() {
	local name="$1" ok="$2"
	if [ "$ok" -eq 1 ]; then
		echo "PASS: $name"
		pass=$((pass + 1))
	else
		echo "FAIL: $name"
		fail=$((fail + 1))
	fi
}

new_case() {
	work="$(mktemp -d)"
	profile_dir="$work/profile"
	codex_home="$work/.codex"
	mkdir -p "$profile_dir/adapters"
	printf 'persona line one\npersona line two\n' >"$profile_dir/adapters/codex.md"
}

run_install() {
	CODEX_HOME="$codex_home" HOME="$work/home" "$script" --profile-dir "$profile_dir" >"$work/out.log" 2>&1
}

run_uninstall() {
	CODEX_HOME="$codex_home" HOME="$work/home" "$script" --uninstall >"$work/out.log" 2>&1
}

dest() { printf '%s/AGENTS.md' "$codex_home"; }

# --- Case 1: fresh install, nothing pre-existing -----------------------------
new_case
run_install
d="$(dest)"
ok=1
[ -f "$d" ] || ok=0
[ -f "$d.cortex-last" ] || ok=0
cmp -s "$d" "$d.cortex-last" || ok=0
[ -e "$d.cortex-bak" ] && ok=0 # nothing to preserve - no original existed
check "fresh install creates AGENTS.md + cortex-last, no spurious cortex-bak" "$ok"
rm -rf "$work"

# --- Case 2: pre-existing user file gets backed up exactly once -------------
new_case
mkdir -p "$codex_home"
printf 'a real pre-existing user AGENTS.md\n' >"$(dest)"
run_install
d="$(dest)"
ok=1
[ -f "$d.cortex-bak" ] || ok=0
grep -qxF 'a real pre-existing user AGENTS.md' "$d.cortex-bak" 2>/dev/null || ok=0
grep -q 'persona line one' "$d" || ok=0
check "pre-existing user file backed up to cortex-bak before overwrite" "$ok"
rm -rf "$work"

# --- Case 3: unchanged re-run creates no drift file, bak untouched -----------
new_case
mkdir -p "$codex_home"
printf 'original user content\n' >"$(dest)"
run_install
d="$(dest)"
bak_sum_before="$(cksum <"$d.cortex-bak")"
run_install # re-run with the same profile_dir/src: dest matches cortex-last exactly
ok=1
drift_count=$(find "$codex_home" -maxdepth 1 -name 'AGENTS.md.cortex-drifted-*' | wc -l | tr -d ' ')
[ "$drift_count" -eq 0 ] || ok=0
bak_sum_after="$(cksum <"$d.cortex-bak")"
[ "$bak_sum_before" = "$bak_sum_after" ] || ok=0
check "re-run with no drift creates no cortex-drifted-* file, bak unchanged" "$ok"
rm -rf "$work"

# --- Case 4: THE regression - drift with the marker line still intact -------
new_case
mkdir -p "$codex_home"
printf 'original user content\n' >"$(dest)"
run_install
d="$(dest)"
# Simulate a user edit (or a restore-profile reconcile) that changes real content
# but happens to leave the Cortex marker line in place - the exact shape the old
# marker-only check could not distinguish from "unchanged".
printf '\nhand-added notes the user cares about\n' >>"$d"
grep -qxF '<!-- cortex:managed do-not-edit (managed by install-codex.sh) -->' "$d" || {
	echo "test-install-codex: marker line missing after simulated edit - test setup bug" >&2
	exit 1
}
run_install
ok=1
drift_count=$(find "$codex_home" -maxdepth 1 -name 'AGENTS.md.cortex-drifted-*' | wc -l | tr -d ' ')
[ "$drift_count" -eq 1 ] || ok=0
if [ "$drift_count" -eq 1 ]; then
	drift_file="$(find "$codex_home" -maxdepth 1 -name 'AGENTS.md.cortex-drifted-*')"
	grep -q 'hand-added notes the user cares about' "$drift_file" || ok=0
fi
# The original pre-Cortex backup must still be the ORIGINAL, not the drifted copy.
grep -qxF 'original user content' "$d.cortex-bak" || ok=0
check "drift with marker intact is still caught and preserved (the regression)" "$ok"
rm -rf "$work"

# --- Case 5: uninstall preserves drifted content before restoring -----------
new_case
mkdir -p "$codex_home"
printf 'original user content\n' >"$(dest)"
run_install
d="$(dest)"
printf '\nhand-added notes the user cares about\n' >>"$d"
run_uninstall
ok=1
[ -f "$d.cortex-pre-uninstall" ] || ok=0
grep -q 'hand-added notes the user cares about' "$d.cortex-pre-uninstall" 2>/dev/null || ok=0
grep -qxF 'original user content' "$d" || ok=0 # restored from cortex-bak
[ -e "$d.cortex-bak" ] && ok=0                 # consumed by the restore
[ -e "$d.cortex-last" ] && ok=0                # cleaned up
check "uninstall preserves drifted content to cortex-pre-uninstall before restoring" "$ok"
rm -rf "$work"

# --- Case 6: uninstall of an untouched managed file is clean (no false save) -
new_case
run_install # no pre-existing file - install creates a fresh Cortex-managed one
d="$(dest)"
run_uninstall
ok=1
[ -e "$d" ] && ok=0                      # no original to restore, so removed entirely
[ -e "$d.cortex-pre-uninstall" ] && ok=0 # nothing drifted - must not be created
check "uninstall of an untouched fresh install removes it with no false save" "$ok"
rm -rf "$work"

echo "----"
echo "test-install-codex: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
