// Package git wraps go-git operations used by the Cortex MCP server.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/cortex-sync/Cortex/mcp/git-server/internal/secretscan"
)

// defaultBranch is the branch a new profile repo is initialised with. Set
// explicitly so new repos use "main" rather than go-git's legacy "master".
const defaultBranch = "main"

// Status returns a human-readable summary of changed files in the repo.
func Status(repoPath string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", fmt.Errorf("opening repo at %s: %w", repoPath, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("getting worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}
	if status.IsClean() {
		return "nothing to commit, working tree clean", nil
	}
	var buf bytes.Buffer
	for path, s := range status {
		fmt.Fprintf(&buf, "%c%c %s\n", s.Staging, s.Worktree, path)
	}
	return buf.String(), nil
}

// CommitAndPush stages all changes, commits with the given message, and pushes
// to the origin remote. The push honours ctx, so a hung network operation can be
// cancelled or timed out by the caller.
func CommitAndPush(ctx context.Context, repoPath, message, username, token string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("getting worktree: %w", err)
	}

	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return "", fmt.Errorf("staging changes: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}
	if status.IsClean() {
		// No file changes to commit, but the branch may still be ahead of origin
		// (e.g. a previous push failed, leaving a committed-but-unpushed change).
		// Flush any pending commits rather than short-circuiting: a stranded
		// commit plus a last-write-wins Pull on another device would lose work.
		if _, err := repo.Remote("origin"); err != nil {
			// No remote configured - there is genuinely nothing to push.
			return "nothing to commit, working tree clean", nil
		}
		auth := &http.BasicAuth{Username: username, Password: token}
		err := repo.PushContext(ctx, &gogit.PushOptions{Auth: auth})
		if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
			return "nothing to commit, already up to date", nil
		}
		if err != nil {
			return "", fmt.Errorf("pushing pending commits: %w", err)
		}
		return "no file changes; pushed pending local commit(s)", nil
	}

	// Server-side secret gate: scan the content of every changed file before
	// committing. This is the last line of defence behind the skill-level
	// filename gate - it catches a credential pasted into a file's body, which a
	// filename check cannot, and runs even if the skill gate is bypassed. It is
	// tuned for accidental pastes into text rather than adversarial obfuscation:
	// binary blobs are skipped and oversized files are scanned only up to the
	// limit (see secretscan), with the profile .gitignore and filename gate
	// covering that residue.
	paths := make([]string, 0, len(status))
	for path := range status {
		paths = append(paths, path)
	}
	findings, err := secretscan.ScanFiles(repoPath, paths)
	if err != nil {
		return "", fmt.Errorf("scanning changes for secrets: %w", err)
	}
	if len(findings) > 0 {
		return "", &secretscan.BlockedError{Findings: findings}
	}

	commit, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Cortex",
			Email: "cortex@local",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("committing: %w", err)
	}

	auth := &http.BasicAuth{Username: username, Password: token}
	if err := repo.PushContext(ctx, &gogit.PushOptions{Auth: auth}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("pushing: %w", err)
	}

	return fmt.Sprintf("committed and pushed: %s", commit.String()), nil
}

// Pull force-updates the current branch to origin's tip: last-write-wins. The
// remote always wins - any diverging local commits AND any uncommitted local
// changes are discarded. This is deliberate for a profile sync (the source of
// truth is the remote), but it is destructive, so callers must treat it as such.
//
// It cannot use go-git's PullContext: in go-git v5, PullOptions.Force only
// affects the fetch, while PullContext still rejects a non-fast-forward update
// (ErrNonFastForwardUpdate) and aborts on a dirty worktree - which is exactly
// the diverged two-device case this is meant to resolve. So we fetch the branch
// into a remote-tracking ref and hard-reset onto it. The fetch honours ctx, so a
// hung network operation can be cancelled or timed out by the caller.
func Pull(ctx context.Context, repoPath, username, token string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("resolving HEAD: %w", err)
	}
	if !head.Name().IsBranch() {
		return "", fmt.Errorf("HEAD is not on a branch (%s); cannot pull", head.Name())
	}
	branch := head.Name().Short()

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("getting worktree: %w", err)
	}

	// Fetch origin's branch into a remote-tracking ref. The explicit refspec
	// keeps this working even for a repo created by InitAndPush (whose origin may
	// carry no fetch refspec); Force lets the tracking ref move non-fast-forward.
	auth := &http.BasicAuth{Username: username, Password: token}
	trackingRef := plumbing.NewRemoteReferenceName("origin", branch)
	refSpec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:%s", branch, trackingRef))
	err = repo.FetchContext(ctx, &gogit.FetchOptions{
		Auth:     auth,
		RefSpecs: []config.RefSpec{refSpec},
		Force:    true,
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("fetching: %w", err)
	}

	remoteRef, err := repo.Reference(trackingRef, true)
	if err != nil {
		return "", fmt.Errorf("resolving origin/%s after fetch: %w", branch, err)
	}
	if remoteRef.Hash() == head.Hash() {
		return "already up to date", nil
	}

	// Hard reset moves the current branch to origin's tip and overwrites the
	// index and worktree (see setHEADCommit + HardReset in go-git): remote wins.
	if err := wt.Reset(&gogit.ResetOptions{Mode: gogit.HardReset, Commit: remoteRef.Hash()}); err != nil {
		return "", fmt.Errorf("resetting to origin/%s: %w", branch, err)
	}
	return fmt.Sprintf("pulled latest changes (reset to origin/%s)", branch), nil
}

// Clone clones a remote repo to the given local path. The clone honours ctx, so
// a hung network operation can be cancelled or timed out by the caller.
func Clone(ctx context.Context, remoteURL, localPath, username, token string) (string, error) {
	auth := &http.BasicAuth{Username: username, Password: token}
	_, err := gogit.PlainCloneContext(ctx, localPath, false, &gogit.CloneOptions{
		URL:  remoteURL,
		Auth: auth,
	})
	if err != nil {
		return "", fmt.Errorf("cloning %s: %w", remoteURL, err)
	}
	return fmt.Sprintf("cloned %s to %s", remoteURL, localPath), nil
}

// InitAndPush initialises a new repo at localPath, sets origin to remoteURL,
// commits the files already present, and pushes to the (pre-created, empty)
// remote.
//
// go-git's PlainClone cannot clone an empty remote (it returns
// ErrEmptyRemoteRepository), so first-run setup initialises locally and pushes
// instead. The caller must write the profile files into localPath *before*
// calling this. Re-running on an existing repo/remote is tolerated. The push
// honours ctx, so a hung network operation can be cancelled or timed out.
func InitAndPush(ctx context.Context, localPath, remoteURL, message, username, token string) (string, error) {
	repo, err := gogit.PlainInitWithOptions(localPath, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName(defaultBranch),
		},
		Bare: false,
	})
	if err != nil {
		if !errors.Is(err, gogit.ErrRepositoryAlreadyExists) {
			return "", fmt.Errorf("initialising repo at %s: %w", localPath, err)
		}
		// Already a repo - reuse it so setup is idempotent.
		if repo, err = gogit.PlainOpen(localPath); err != nil {
			return "", fmt.Errorf("opening existing repo: %w", err)
		}
	}

	// Configure origin, tolerating a pre-existing remote - but never trust one
	// blindly. CreateRemote is a silent no-op when origin already exists, and the
	// caller-supplied remoteURL (which gitInitHandler validated with RequireHTTPS
	// and resolved the PAT for) is ignored in that case. Since go-git then pushes
	// to whatever origin records, a reused repo whose origin points at another -
	// or an http - host would receive the credential resolved for remoteURL's
	// host. When the remote already exists, require it to be https and to resolve
	// to remoteURL's host before pushing; a mismatch fails closed.
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	}); err != nil {
		if !errors.Is(err, gogit.ErrRemoteExists) {
			return "", fmt.Errorf("adding origin remote: %w", err)
		}
		if err := requireOriginMatches(repo, remoteURL); err != nil {
			return "", err
		}
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("getting worktree: %w", err)
	}
	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return "", fmt.Errorf("staging files: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}
	if status.IsClean() {
		return "", fmt.Errorf("nothing to commit - write the profile files into %s before initialising", localPath)
	}

	commit, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Cortex",
			Email: "cortex@local",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("committing: %w", err)
	}

	// Push the default branch explicitly - a fresh remote has no refs to match.
	refSpec := config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", defaultBranch, defaultBranch))
	auth := &http.BasicAuth{Username: username, Password: token}
	if err := repo.PushContext(ctx, &gogit.PushOptions{
		Auth:     auth,
		RefSpecs: []config.RefSpec{refSpec},
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("pushing initial commit: %w", err)
	}

	return fmt.Sprintf("initialised %s, pushed %s to %s (branch %s)", localPath, commit.String(), remoteURL, defaultBranch), nil
}

// RemoteHost returns the hostname from the repo's origin remote URL, enforcing
// that origin is an https URL with no embedded credentials.
//
// This is the credential-resolution gate for the push and pull paths, which send
// the PAT to whatever origin is recorded in .git/config. Validating with
// RequireHTTPS (not a bare host parse) fails closed: a repo whose origin is
// http://, a non-https transport, or carries userinfo is rejected before any
// credential is read or transmitted, so the PAT can never travel over cleartext.
// This mirrors the RequireHTTPS check the clone and init paths apply to the
// caller-supplied URL.
func RemoteHost(repoPath string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return "", fmt.Errorf("getting origin remote: %w", err)
	}
	return validateOriginURLs(remote.Config().URLs)
}

// validateOriginURLs enforces that every URL configured on an origin remote is a
// credential-safe https URL and that they all resolve to the same host, then
// returns that host.
//
// Validating a single URL is not enough: go-git fetches from URLs[0] but pushes
// to URLs[len-1], so an origin such as
// ["https://gitlab.com/u/r.git", "http://attacker/u/r.git"] would pass a check
// of the first URL alone and then send the PAT to the attacker host in cleartext
// on push. Profile repos never need a multi-URL remote, so a URL that is not
// https, carries userinfo, or resolves to a different host than the first is
// rejected - the credential can only ever travel to one validated https host.
func validateOriginURLs(urls []string) (string, error) {
	if len(urls) == 0 {
		return "", fmt.Errorf("no URLs configured for origin")
	}
	host, err := RequireHTTPS(urls[0])
	if err != nil {
		return "", err
	}
	for _, u := range urls[1:] {
		h, err := RequireHTTPS(u)
		if err != nil {
			return "", err
		}
		if h != host {
			return "", fmt.Errorf("origin has multiple hosts (%s and %s); refusing - the PAT could be pushed to either", host, h)
		}
	}
	return host, nil
}

// requireOriginMatches fails closed unless the repo's existing origin remote is
// safe to receive the credential the caller resolved for remoteURL: every
// configured origin URL must be https, carry no userinfo, and resolve to the
// same host as remoteURL. It guards the reused-repo path of InitAndPush, where
// CreateRemote is a no-op and the pre-existing origin - not remoteURL - is what
// go-git pushes to.
func requireOriginMatches(repo *gogit.Repository, remoteURL string) error {
	wantHost, err := RequireHTTPS(remoteURL)
	if err != nil {
		return err
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("reading existing origin remote: %w", err)
	}
	gotHost, err := validateOriginURLs(remote.Config().URLs)
	if err != nil {
		return fmt.Errorf("existing origin remote is not usable: %w", err)
	}
	if gotHost != wantHost {
		return fmt.Errorf("existing origin host %q does not match requested remote host %q; refusing to push (the credential for %q would be sent to %q)", gotHost, wantHost, wantHost, gotHost)
	}
	return nil
}

// RequireHTTPS validates that remoteURL uses the https scheme and has a host,
// returning the hostname. Cortex is HTTPS + PAT only (see CONTRIBUTING.md):
// permitting http, file, git, or ssh URLs would let a PAT travel over cleartext
// or an unexpected transport. It fails closed - a URL that does not parse,
// carries no scheme, or has no host is rejected. Returning the host lets callers
// validate and resolve the credential key in a single parse.
func RequireHTTPS(remoteURL string) (string, error) {
	u, err := url.Parse(remoteURL)
	if err != nil {
		return "", fmt.Errorf("parsing remote URL %q: %w", remoteURL, err)
	}
	if u.Scheme != "https" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "none"
		}
		return "", fmt.Errorf("remote URL %q must use https, got scheme %q: Cortex is HTTPS + PAT only", remoteURL, scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("remote URL %q has no host", remoteURL)
	}
	// Reject a credential embedded in the URL (https://user:token@host/...).
	// Cortex passes the PAT via BasicAuth, never in the URL - and a userinfo URL
	// would be persisted verbatim into .git/config by clone/init, leaking it.
	if u.User != nil {
		return "", fmt.Errorf("remote URL must not embed credentials (userinfo) - Cortex supplies the PAT separately, not in the URL")
	}
	return u.Hostname(), nil
}
