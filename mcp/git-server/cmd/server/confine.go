package main

// Path confinement for the path-taking git tools.
//
// Every git tool (git_status/commit_push/pull/clone/init) takes a caller-supplied
// filesystem path. Those arguments are model-controlled, so a prompt injection
// could otherwise point an operation at an arbitrary path - /etc for disclosure
// via git_status, or an unrelated repo to commit and push with the profile PAT.
//
// confinePath binds every path argument beneath a configurable root: CORTEX_REPO_ROOT
// if set, otherwise the user's home directory. Every supported profile location
// lives under home (the CLI ~/.claude tree, ~/cortex-profile, a cloned profile,
// the Codex ~/.codex tree), so a home-rooted default confines the tools without
// breaking any of them while still rejecting an injected out-of-tree path.
// Deployments whose profile lives elsewhere (e.g. a mounted folder) set
// CORTEX_REPO_ROOT.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// repoRoot returns the directory that every repo/local path argument must resolve
// within: CORTEX_REPO_ROOT if set, otherwise the user's home directory. It
// returns an error only when neither is available (no CORTEX_REPO_ROOT and no
// resolvable home), so callers fail closed with an actionable message rather than
// silently disabling confinement.
func repoRoot() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CORTEX_REPO_ROOT")); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("resolving CORTEX_REPO_ROOT %q: %w", v, err)
		}
		return abs, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine a path-confinement root: set CORTEX_REPO_ROOT (home directory is unavailable: %w)", err)
	}
	return home, nil
}

// confinePath validates a caller-supplied path argument and returns the cleaned,
// symlink-resolved absolute path to hand to go-git. The path must be absolute and
// must resolve to a location within root; a relative path, a "../" escape, or a
// symlink whose target leaves root is rejected.
//
// The leaf need not exist (git_clone and git_init create it), so only the longest
// existing ancestor is symlink-resolved and the not-yet-created segments are
// rejoined; root itself is resolved too, so a symlinked root cannot be used to
// slip past the containment check.
func confinePath(root, arg string) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(arg) {
		return "", fmt.Errorf("path %q must be absolute", arg)
	}
	canonRoot, err := resolveExisting(root)
	if err != nil {
		return "", fmt.Errorf("resolving confinement root %q: %w", root, err)
	}
	resolved, err := resolveExisting(arg)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", arg, err)
	}
	rel, err := filepath.Rel(canonRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q resolves outside the permitted root %q; set CORTEX_REPO_ROOT to allow it", arg, canonRoot)
	}
	return resolved, nil
}

// resolveExisting returns path with symlinks resolved. filepath.EvalSymlinks
// fails on a path that does not exist yet (git_clone/git_init targets), so it
// resolves the longest existing ancestor and rejoins the remaining segments -
// anchoring a not-yet-created leaf to a real, symlink-free parent so it still
// cannot escape root through a symlinked component.
func resolveExisting(path string) (string, error) {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(path)
	if parent == path {
		// Reached the filesystem root without an existing ancestor - nothing to
		// anchor to. Treat as unresolvable rather than trusting the raw path.
		return "", fmt.Errorf("no existing ancestor for %q", path)
	}
	resolvedParent, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

// confinedPathArg resolves and confines the named path argument, returning a
// ready-to-return MCP error result on failure (mirroring resolveCreds). The
// returned path is absolute, cleaned, and guaranteed to sit within the
// confinement root.
func confinedPathArg(req mcp.CallToolRequest, name string) (string, *mcp.CallToolResult) {
	root, err := repoRoot()
	if err != nil {
		return "", mcp.NewToolResultError(err.Error())
	}
	path, err := confinePath(root, stringArg(req, name))
	if err != nil {
		return "", mcp.NewToolResultError(err.Error())
	}
	return path, nil
}
