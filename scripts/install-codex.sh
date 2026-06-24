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
# Machine marker flagging an AGENTS.md as Cortex-managed (for backup/uninstall
# decisions) - distinct from the human-readable '## Cortex configuration' heading,
# which a user might legitimately type themselves.
cortex_marker='<!-- cortex:managed do-not-edit (managed by install-codex.sh) -->'

# Cortex checkout root: this script lives in <root>/scripts/.
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
launcher="$root/bin/cortex-git-launch.sh"

profile_dir=""
with_mcp=0
uninstall=0

usage() {
	cat <<EOF
Usage: install-codex.sh [--profile-dir DIR] [--with-mcp] [--uninstall]

  --profile-dir DIR   Tier 1: copy DIR/adapters/codex.md (or generic.md) to
                      $codex_home/AGENTS.md (with a '## Cortex configuration'
                      block) so Codex loads your profile.
  --with-mcp          Tier 2: register the cortex-git MCP server and symlink the
                      Cortex skills into ~/.agents/skills.
  --uninstall         Reverse what this script created: remove the Cortex skill
                      symlinks and the cortex-git MCP entry, and restore AGENTS.md
                      from backup if one exists.
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
	--uninstall)
		uninstall=1
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

# --- Uninstall (exclusive) ----------------------------------------------------
if [ "$uninstall" -eq 1 ]; then
	if [ -n "$profile_dir" ] || [ "$with_mcp" -eq 1 ]; then
		echo "install-codex: --uninstall ignores --profile-dir/--with-mcp" >&2
	fi
	agents_skills_dir="${HOME:?install-codex: HOME must be set}/.agents/skills"
	for s in $skills; do
		link="$agents_skills_dir/$s"
		# Only remove a symlink that points into THIS checkout's skills/.
		if [ -L "$link" ] && [ "$(readlink "$link" 2>/dev/null)" = "$root/skills/$s" ]; then
			rm -f "$link"
			echo "install-codex: removed skill symlink $link"
		fi
	done
	if command -v codex >/dev/null 2>&1; then
		if codex mcp remove cortex-git >/dev/null 2>&1; then
			echo "install-codex: removed MCP server 'cortex-git'"
		else
			echo "install-codex: no 'cortex-git' MCP server to remove"
		fi
	fi
	dest="$codex_home/AGENTS.md"
	if [ -e "$dest.cortex-bak" ]; then
		# A genuine original exists. If the user has since replaced the managed file
		# with their own content, preserve that before restoring the backup.
		if [ -e "$dest" ] && ! grep -qxF "$cortex_marker" "$dest" 2>/dev/null; then
			cp "$dest" "$dest.cortex-pre-uninstall"
			echo "install-codex: current AGENTS.md is not Cortex-managed; saved it to $dest.cortex-pre-uninstall before restoring" >&2
		fi
		mv "$dest.cortex-bak" "$dest"
		echo "install-codex: restored original AGENTS.md from backup"
	elif [ -e "$dest" ] && grep -qxF "$cortex_marker" "$dest" 2>/dev/null; then
		rm -f "$dest"
		echo "install-codex: removed Cortex-managed AGENTS.md (no prior user file to restore)"
	elif [ -e "$dest" ]; then
		echo "install-codex: left $dest in place (not Cortex-managed) - remove it by hand if unwanted" >&2
	fi
	echo "install-codex: uninstall done."
	exit 0
fi

if [ -z "$profile_dir" ] && [ "$with_mcp" -eq 0 ]; then
	echo "install-codex: nothing to do - pass --profile-dir DIR, --with-mcp, or --uninstall." >&2
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
	if [ -d "$dest" ]; then
		echo "install-codex: $dest is a directory - refusing to overwrite" >&2
		exit 1
	fi
	# Back up a genuine user-authored AGENTS.md exactly once: skip if a backup
	# already exists, or if the file is already Cortex-managed (matched on our exact
	# whole-line marker, so prose merely mentioning the marker can't misclassify a
	# real user file), so a re-run with a changed adapter can't clobber the backup.
	backup_existing() {
		[ -e "$dest.cortex-bak" ] && return 0
		grep -qxF "$cortex_marker" "$1" 2>/dev/null && return 0
		cp "$1" "$dest.cortex-bak"
		echo "install-codex: backed up existing AGENTS.md to $dest.cortex-bak"
	}
	if [ -L "$dest" ]; then
		# Replace the symlink itself - never write through it and clobber an
		# unrelated target outside $codex_home. Back up the target's content first.
		echo "install-codex: $dest is a symlink - replacing the link, not its target" >&2
		[ -e "$dest" ] && backup_existing "$dest"
		rm -f "$dest"
	elif [ -e "$dest" ]; then
		backup_existing "$dest"
	fi
	cp "$src" "$dest"
	# Append the Cortex configuration block so sync-profile and the AGENTS.md memory
	# pointer can resolve the profile repo. It is per-machine (a local path), so it
	# lives in the on-disk AGENTS.md, not in the committed adapter.
	if ! grep -qxF "$cortex_marker" "$dest"; then
		abs_profile="$(CDPATH= cd -- "$profile_dir" && pwd)"
		remote="$(git -C "$profile_dir" remote get-url origin 2>/dev/null || true)"
		{
			printf '\n%s\n## Cortex configuration\n\n- Profile repo path: %s\n' "$cortex_marker" "$abs_profile"
			# Record Remote/Host only for an https origin - the only form Cortex
			# supports (the server's RequireHTTPS enforces it). For anything else
			# (ssh/scp/file/local path) skip both lines: it carries no info Cortex
			# uses and could embed a credential we must never write to AGENTS.md.
			# No origin remote -> record only the profile path above.
			if [ -n "$remote" ]; then
				scheme="$(printf '%s' "$remote" | sed -e 's#:.*##' | tr '[:upper:]' '[:lower:]')"
				if [ "$scheme" = "https" ]; then
					# Parse the authority with shell expansion (no fragile URL regex):
					# strip the scheme, take up to the first '/', drop any userinfo
					# (greedy to the last '@', so an '@' in a password is removed too)
					# and any ':port'. Rebuild the remote without the userinfo.
					rest="${remote#*://}"
					authority="${rest%%/*}"
					path="${rest#"$authority"}"
					host="${authority##*@}"
					host="${host%%:*}"
					printf -- '- Remote: https://%s%s\n' "$host" "$path"
					if [ -n "$host" ]; then
						printf -- '- Host: %s\n' "$host"
					fi
				else
					printf -- '- Remote: (non-https origin; not recorded - Cortex is HTTPS-only)\n'
				fi
			fi
		} >>"$dest"
	fi
	echo "install-codex: placed $(basename "$src") -> $dest (+ Cortex configuration block)"
fi

# --- Tier 2: skills + MCP server ----------------------------------------------
if [ "$with_mcp" -eq 1 ]; then
	# Skills: symlink each folder so Codex auto-discovers it (Codex follows symlinks).
	agents_skills_dir="${HOME:?install-codex: HOME must be set for --with-mcp}/.agents/skills"
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
		# Match cortex-git as a whitespace-delimited field (so 'cortex-git-staging'
		# does not false-match). Exact `codex mcp list` format is unverified - see the
		# open checks in docs/TODO.md.
		if codex mcp list 2>/dev/null | grep -qE '(^|[[:space:]])cortex-git([[:space:]]|$)'; then
			echo "install-codex: MCP server 'cortex-git' already registered - leaving it"
		else
			codex mcp add cortex-git -- "$launcher"
			echo "install-codex: registered MCP server 'cortex-git' (command: $launcher)"
		fi
	else
		echo "install-codex: 'codex' not on PATH - add the server by hand to $codex_home/config.toml" >&2
		echo "  (merge into any existing config; only add if [mcp_servers.cortex-git] is absent):" >&2
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
	echo "    (allowlist just your Git host if you use features.network_proxy). This is the"
	echo "    least-privilege option; danger-full-access also works but disables the WHOLE"
	echo "    sandbox - use it only as a session-scoped last resort. Then restart Codex and"
	echo "    run /restore-profile or /sync-profile."
fi
