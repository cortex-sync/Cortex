#!/bin/sh
# Cortex - OpenAI Codex CLI bootstrap.
#
# Codex has no plugin installer, so this script does the host-side bootstrap that
# `/plugin install` does on Claude. It supports the two Cortex-on-Codex tiers (see
# docs/usage.md), which are independently selectable and compose:
#
#   Tier 1  --profile-dir DIR
#       Place the Codex instruction file (AGENTS.md) from your profile repo's
#       adapter (adapters/codex.md, or adapters/generic.md as a fallback), so Codex
#       loads your persona, working style, and memory. Sync stays host-side (run
#       Cortex from Claude Code). No MCP server, no sandbox changes.
#
#   Tier 2  --with-mcp
#       Additionally register the cortex-git MCP server (via `codex mcp add`) and
#       make the four Cortex skills discoverable (symlinked into ~/.agents/skills/),
#       so /sync-profile etc. run natively in Codex. REQUIRES opening Codex's
#       sandbox network - see the note printed at the end.
#
# This script never clones the profile repo, mutates its git state, or touches
# credentials - those stay with the cortex-git credential store / Claude Code Cortex.
#
# POSIX sh. Safe to re-run (idempotent).
set -eu

skills="setup sync-profile restore-profile promote-lessons"
codex_home="${CODEX_HOME:-$HOME/.codex}"
agents_skills_dir="$HOME/.agents/skills"

# Cortex checkout root: this script lives in <root>/scripts/.
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
launcher="$root/bin/cortex-git-launch.sh"

profile_dir=""
with_mcp=0

usage() {
	cat <<EOF
Usage: install-codex.sh [--profile-dir DIR] [--with-mcp]

  --profile-dir DIR   Tier 1: copy DIR/adapters/codex.md (or generic.md) to
                      $codex_home/AGENTS.md so Codex loads your profile.
  --with-mcp          Tier 2: register the cortex-git MCP server and symlink the
                      Cortex skills into $agents_skills_dir.
  -h, --help          Show this help.

With no options nothing is changed. Tier 1 and Tier 2 can be combined.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--profile-dir)
		[ $# -ge 2 ] || { echo "install-codex: --profile-dir needs a directory" >&2; exit 2; }
		profile_dir="$2"
		shift 2
		;;
	--with-mcp)
		with_mcp=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "install-codex: unknown argument '$1'" >&2
		usage >&2
		exit 2
		;;
	esac
done

if [ -z "$profile_dir" ] && [ "$with_mcp" -eq 0 ]; then
	echo "install-codex: nothing to do - pass --profile-dir DIR and/or --with-mcp." >&2
	echo >&2
	usage >&2
	exit 2
fi

# --- Tier 1: place AGENTS.md --------------------------------------------------
if [ -n "$profile_dir" ]; then
	src="$profile_dir/adapters/codex.md"
	[ -f "$src" ] || src="$profile_dir/adapters/generic.md"
	if [ ! -f "$src" ]; then
		echo "install-codex: no adapters/codex.md or adapters/generic.md under '$profile_dir'" >&2
		exit 1
	fi
	mkdir -p "$codex_home"
	dest="$codex_home/AGENTS.md"
	if [ -e "$dest" ] && ! cmp -s "$src" "$dest"; then
		cp "$dest" "$dest.cortex-bak"
		echo "install-codex: backed up existing AGENTS.md to $dest.cortex-bak"
	fi
	cp "$src" "$dest"
	echo "install-codex: placed $(basename "$src") -> $dest"
fi

# --- Tier 2: skills + MCP server ----------------------------------------------
if [ "$with_mcp" -eq 1 ]; then
	# Skills: symlink each folder so Codex auto-discovers it (Codex follows symlinks).
	mkdir -p "$agents_skills_dir"
	for s in $skills; do
		target="$root/skills/$s"
		link="$agents_skills_dir/$s"
		if [ ! -d "$target" ]; then
			echo "install-codex: skill '$s' not found at $target - skipping" >&2
			continue
		fi
		if [ -e "$link" ] && [ ! -L "$link" ]; then
			echo "install-codex: $link exists and is not a symlink - skipping (rename it to wire Cortex's $s)" >&2
			continue
		fi
		ln -sfn "$target" "$link"
		echo "install-codex: linked skill $s -> $link"
	done

	# MCP server: prefer the Codex CLI, which writes the config.toml entry itself.
	if command -v codex >/dev/null 2>&1; then
		if codex mcp list 2>/dev/null | grep -q "cortex-git"; then
			echo "install-codex: MCP server 'cortex-git' already registered - leaving it"
		else
			codex mcp add cortex-git -- "$launcher"
			echo "install-codex: registered MCP server 'cortex-git' (command: $launcher)"
		fi
	else
		echo "install-codex: 'codex' not on PATH - add the server by hand to $codex_home/config.toml:" >&2
		echo >&2
		echo "  [mcp_servers.cortex-git]" >&2
		echo "  command = \"$launcher\"" >&2
		echo >&2
	fi
fi

# --- Next steps ---------------------------------------------------------------
echo
echo "Done. Next steps:"
if [ -n "$profile_dir" ]; then
	echo "  - Tier 1: start Codex; it now loads your profile from $codex_home/AGENTS.md."
	echo "    Keep it current by running sync-profile from Claude Code (sync stays host-side)."
fi
if [ "$with_mcp" -eq 1 ]; then
	echo "  - Tier 2 PREREQUISITE: the cortex-git server needs outbound network, which"
	echo "    Codex blocks by default under workspace-write. Enable it in $codex_home/config.toml:"
	echo
	echo "        [sandbox_workspace_write]"
	echo "        network_access = true"
	echo
	echo "    (and allowlist your Git host if you use features.network_proxy), or run Codex"
	echo "    with danger-full-access. Then restart Codex and run /restore-profile or /sync-profile."
fi
