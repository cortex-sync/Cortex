package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestRequireHTTPS(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https", "https://gitlab.com/user/repo.git", false},
		{"https with port", "https://git.example.com:8443/u/r.git", false},
		{"http rejected", "http://gitlab.com/user/repo.git", true},
		{"ssh scheme rejected", "ssh://git@gitlab.com/user/repo.git", true},
		{"git scheme rejected", "git://gitlab.com/user/repo.git", true},
		{"file scheme rejected", "file:///tmp/repo.git", true},
		{"scp-style rejected", "git@gitlab.com:user/repo.git", true},
		{"bare path rejected", "/local/path/repo", true},
		{"empty rejected", "", true},
		{"userinfo with token rejected", "https://user:glpat-secret@gitlab.com/u/r.git", true},
		{"userinfo username-only rejected", "https://user@gitlab.com/u/r.git", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RequireHTTPS(c.url)
			if c.wantErr && err == nil {
				t.Fatalf("RequireHTTPS(%q) = nil, want error", c.url)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("RequireHTTPS(%q) unexpected error: %v", c.url, err)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	dir := t.TempDir()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}

	// A fresh repo with no files is clean.
	got, err := Status(dir)
	if err != nil {
		t.Fatalf("Status (clean): %v", err)
	}
	if !strings.Contains(got, "clean") {
		t.Fatalf("Status (clean) = %q, want it to mention clean", got)
	}

	// An untracked file shows up.
	if err := os.WriteFile(filepath.Join(dir, "active.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Status(dir)
	if err != nil {
		t.Fatalf("Status (dirty): %v", err)
	}
	if !strings.Contains(got, "active.md") {
		t.Fatalf("Status (dirty) = %q, want it to list active.md", got)
	}
}

func TestStatusNonRepo(t *testing.T) {
	if _, err := Status(t.TempDir()); err == nil {
		t.Fatal("Status on a non-repo dir should error")
	}
}

func TestRemoteHost(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// No remote configured yet.
	if _, err := RemoteHost(dir); err == nil {
		t.Fatal("RemoteHost with no origin should error")
	}

	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://gitlab.com/u/r.git"},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	host, err := RemoteHost(dir)
	if err != nil {
		t.Fatalf("RemoteHost: %v", err)
	}
	if host != "gitlab.com" {
		t.Fatalf("RemoteHost = %q, want gitlab.com", host)
	}
}

// TestRemoteHostRejectsInsecureOrigin is the regression guard for the C1 finding:
// the push and pull paths resolve credentials via RemoteHost, so RemoteHost must
// fail closed on any origin that would leak the PAT - an http:// origin (cleartext
// transport) or one with embedded userinfo - exactly as the clone/init paths do.
func TestRemoteHostRejectsInsecureOrigin(t *testing.T) {
	cases := []struct {
		name      string
		originURL string
	}{
		{"http origin sends PAT in cleartext", "http://gitlab.com/u/r.git"},
		{"ssh origin is an unexpected transport", "ssh://git@gitlab.com/u/r.git"},
		{"userinfo origin leaks the token", "https://user:glpat-secret@gitlab.com/u/r.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			repo, err := gogit.PlainInit(dir, false)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if _, err := repo.CreateRemote(&config.RemoteConfig{
				Name: "origin",
				URLs: []string{c.originURL},
			}); err != nil {
				t.Fatalf("create remote: %v", err)
			}
			if host, err := RemoteHost(dir); err == nil {
				t.Fatalf("RemoteHost(%q) = %q, want error (must fail closed)", c.originURL, host)
			}
		})
	}
}

// TestRemoteHostRejectsMultiURLOrigin is the regression guard for the multi-URL
// origin finding: go-git pushes to the LAST configured origin URL (URLs[len-1])
// while fetching from the first, so validating only URLs[0] would let an origin
// whose second URL is an attacker/http host pass the gate and then leak the PAT
// on push. RemoteHost must reject any origin whose URLs are not all the same
// https host.
func TestRemoteHostRejectsMultiURLOrigin(t *testing.T) {
	cases := []struct {
		name string
		urls []string
	}{
		{"http push URL behind an https fetch URL", []string{"https://gitlab.com/u/r.git", "http://attacker/u/r.git"}},
		{"divergent https hosts", []string{"https://gitlab.com/u/r.git", "https://attacker.com/u/r.git"}},
		{"userinfo in the second URL", []string{"https://gitlab.com/u/r.git", "https://user:glpat-secret@gitlab.com/u/r.git"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			repo, err := gogit.PlainInit(dir, false)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if _, err := repo.CreateRemote(&config.RemoteConfig{
				Name: "origin",
				URLs: c.urls,
			}); err != nil {
				t.Fatalf("create remote: %v", err)
			}
			if host, err := RemoteHost(dir); err == nil {
				t.Fatalf("RemoteHost(%v) = %q, want error (must fail closed)", c.urls, host)
			}
		})
	}

	// A multi-URL origin that resolves to a single https host is legitimate and
	// must still be accepted, so the guard above is not over-strict.
	t.Run("same https host twice is accepted", func(t *testing.T) {
		dir := t.TempDir()
		repo, err := gogit.PlainInit(dir, false)
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, err := repo.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{"https://gitlab.com/u/r.git", "https://gitlab.com/mirror/r.git"},
		}); err != nil {
			t.Fatalf("create remote: %v", err)
		}
		host, err := RemoteHost(dir)
		if err != nil {
			t.Fatalf("RemoteHost: %v", err)
		}
		if host != "gitlab.com" {
			t.Fatalf("RemoteHost = %q, want gitlab.com", host)
		}
	})
}

// TestInitAndPushRejectsMismatchedExistingOrigin is the regression guard for the
// git_init misdirection finding: InitAndPush reuses a pre-existing repo, and
// CreateRemote is a no-op when origin already exists, so the caller-supplied
// remoteURL is ignored and go-git pushes to the recorded origin. If that origin
// points at another (or an http) host, the PAT resolved for remoteURL's host
// would be sent there. InitAndPush must refuse before committing or pushing.
func TestInitAndPushRejectsMismatchedExistingOrigin(t *testing.T) {
	cases := []struct {
		name           string
		existingOrigin string
	}{
		{"http origin would leak the PAT in cleartext", "http://attacker.invalid/u/r.git"},
		{"different https host would misdirect the PAT", "https://attacker.invalid/u/r.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			repo, err := gogit.PlainInit(dir, false)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if _, err := repo.CreateRemote(&config.RemoteConfig{
				Name: "origin",
				URLs: []string{c.existingOrigin},
			}); err != nil {
				t.Fatalf("create remote: %v", err)
			}
			writeFile(t, dir, "CLAUDE.md", "v1\n")

			// Unreachable request host: if the gate failed open the push would
			// fail on the network, so a synchronous "refusing" error proves the
			// origin mismatch was caught before any credential left the process.
			_, err = InitAndPush(context.Background(), dir, "https://gitlab.com/u/r.git", "cortex: initial", "u", "t")
			if err == nil {
				t.Fatalf("InitAndPush with existing origin %q = nil, want error (must fail closed)", c.existingOrigin)
			}
			if !strings.Contains(err.Error(), "refusing") && !strings.Contains(err.Error(), "not usable") {
				t.Fatalf("error = %v, want it to explain the origin mismatch", err)
			}
		})
	}
}

// TestInitAndPushNothingToCommit verifies the guard fires before any network
// access when local_path has no files to commit.
func TestInitAndPushNothingToCommit(t *testing.T) {
	_, err := InitAndPush(context.Background(), t.TempDir(), "https://example.invalid/repo.git", "init", "user", "token")
	if err == nil {
		t.Fatal("expected error for empty repo, got nil")
	}
	if !strings.Contains(err.Error(), "nothing to commit") {
		t.Fatalf("error = %v, want it to mention 'nothing to commit'", err)
	}
}

func TestCommitAndPushNothingToCommit(t *testing.T) {
	dir := t.TempDir()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Clean tree returns a message without error and never reaches the network.
	got, err := CommitAndPush(context.Background(), dir, "msg", "user", "token")
	if err != nil {
		t.Fatalf("CommitAndPush (clean): %v", err)
	}
	if !strings.Contains(got, "nothing to commit") {
		t.Fatalf("result = %q, want it to mention 'nothing to commit'", got)
	}
}

// TestCommitAndPushBlocksSecret confirms the server-side content gate refuses to
// commit when a changed file contains a secret, and never reaches the network.
func TestCommitAndPushBlocksSecret(t *testing.T) {
	dir := t.TempDir()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeFile(t, dir, "memory.md", "notes\nGL_TOKEN = glpat-ABCDEFGHIJ1234567890\n")

	// Unreachable remote: if the gate fails open, the push would error on the
	// network instead, so a clean "refusing to commit" proves it blocked first.
	_, err := CommitAndPush(context.Background(), dir, "cortex: update", "u", "t")
	if err == nil {
		t.Fatal("expected commit to be blocked, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to commit") {
		t.Fatalf("error = %v, want it to mention 'refusing to commit'", err)
	}
	if !strings.Contains(err.Error(), "memory.md") {
		t.Fatalf("error = %v, want it to name the offending file", err)
	}
}

// TestSyncRoundTrip exercises the full lifecycle against a local bare remote:
// init+push on "device A", clone on "device B", commit+push from B, pull on A.
// This drives every exported network operation without leaving the machine.
// It relies on go-git's local transport (git is present in CI and dev images).
func TestSyncRoundTrip(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	bare, err := gogit.PlainInit(remote, true)
	if err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	// Point the bare remote's HEAD at main, as a real host does when an empty
	// repo first receives a main branch. Otherwise HEAD dangles on master and
	// clone cannot resolve a default branch.
	headToMain := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))
	if err := bare.Storer.SetReference(headToMain); err != nil {
		t.Fatalf("set remote HEAD: %v", err)
	}

	// Device A: create profile files, init, push.
	deviceA := filepath.Join(root, "a")
	if err := os.MkdirAll(deviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, deviceA, "CLAUDE.md", "v1\n")
	if _, err := InitAndPush(context.Background(), deviceA, remote, "cortex: initial", "u", "t"); err != nil {
		t.Fatalf("InitAndPush: %v", err)
	}

	// Device B: clone and confirm the file arrived.
	deviceB := filepath.Join(root, "b")
	if _, err := Clone(context.Background(), remote, deviceB, "u", "t"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if got := readFile(t, deviceB, "CLAUDE.md"); got != "v1\n" {
		t.Fatalf("cloned CLAUDE.md = %q, want v1", got)
	}

	// Device B: change a file, commit, push.
	writeFile(t, deviceB, "CLAUDE.md", "v2\n")
	if _, err := CommitAndPush(context.Background(), deviceB, "cortex: update", "u", "t"); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	// Device A: pull and confirm it now sees the update.
	if _, err := Pull(context.Background(), deviceA, "u", "t"); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got := readFile(t, deviceA, "CLAUDE.md"); got != "v2\n" {
		t.Fatalf("after pull CLAUDE.md = %q, want v2", got)
	}
}

// TestPullLastWriteWinsOnDivergence is the real test of the documented
// "last-write-wins" semantics: device A has BOTH a diverging local commit and a
// dirty (uncommitted) worktree, while origin has advanced independently. A plain
// go-git PullContext would return ErrNonFastForwardUpdate (or ErrUnstagedChanges
// on the dirty tree); Pull must instead discard A's local state and converge on
// origin's tip.
func TestPullLastWriteWinsOnDivergence(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	bare, err := gogit.PlainInit(remote, true)
	if err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	headToMain := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))
	if err := bare.Storer.SetReference(headToMain); err != nil {
		t.Fatalf("set remote HEAD: %v", err)
	}

	// Device A: create and push v1.
	deviceA := filepath.Join(root, "a")
	if err := os.MkdirAll(deviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, deviceA, "CLAUDE.md", "v1\n")
	if _, err := InitAndPush(context.Background(), deviceA, remote, "cortex: initial", "u", "t"); err != nil {
		t.Fatalf("InitAndPush: %v", err)
	}

	// Device B: clone, change the file, push - origin now advances past v1.
	deviceB := filepath.Join(root, "b")
	if _, err := Clone(context.Background(), remote, deviceB, "u", "t"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	writeFile(t, deviceB, "CLAUDE.md", "remote-wins\n")
	if _, err := CommitAndPush(context.Background(), deviceB, "cortex: B update", "u", "t"); err != nil {
		t.Fatalf("CommitAndPush B: %v", err)
	}

	// Device A: make a DIVERGING local commit (committed directly, never pushed),
	// then leave a further uncommitted edit on top - so A is both non-fast-forward
	// against origin and has a dirty worktree.
	writeFile(t, deviceA, "CLAUDE.md", "local-divergent\n")
	repoA, err := gogit.PlainOpen(deviceA)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	wtA, err := repoA.Worktree()
	if err != nil {
		t.Fatalf("worktree A: %v", err)
	}
	if err := wtA.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		t.Fatalf("stage A: %v", err)
	}
	if _, err := wtA.Commit("local only", &gogit.CommitOptions{
		Author: &object.Signature{Name: "A", Email: "a@local", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	writeFile(t, deviceA, "CLAUDE.md", "dirty-uncommitted\n") // unstaged change on top

	// Pull must converge on origin despite the divergence and the dirty worktree.
	if _, err := Pull(context.Background(), deviceA, "u", "t"); err != nil {
		t.Fatalf("Pull on diverged+dirty history: %v", err)
	}
	if got := readFile(t, deviceA, "CLAUDE.md"); got != "remote-wins\n" {
		t.Fatalf("after last-write-wins pull, CLAUDE.md = %q, want origin's 'remote-wins'", got)
	}
}

// commitLocally stages everything in dir and commits directly without pushing,
// leaving the branch ahead of origin - used to simulate a commit whose push
// failed, or a diverging local commit.
func commitLocally(t *testing.T, dir, message string) {
	t.Helper()
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree %s: %v", dir, err)
	}
	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		t.Fatalf("stage %s: %v", dir, err)
	}
	if _, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@local", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit %s: %v", dir, err)
	}
}

// TestCommitAndPushFlushesUnpushedCommits guards the stranding bug: a commit
// whose push failed leaves the branch ahead of origin with a clean worktree.
// CommitAndPush must still push it rather than short-circuit on "nothing to
// commit" - otherwise the commit is stranded, and a subsequent last-write-wins
// Pull on another device would silently destroy it.
func TestCommitAndPushFlushesUnpushedCommits(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	bare, err := gogit.PlainInit(remote, true)
	if err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	headToMain := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))
	if err := bare.Storer.SetReference(headToMain); err != nil {
		t.Fatalf("set remote HEAD: %v", err)
	}

	deviceA := filepath.Join(root, "a")
	if err := os.MkdirAll(deviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, deviceA, "CLAUDE.md", "v1\n")
	if _, err := InitAndPush(context.Background(), deviceA, remote, "cortex: initial", "u", "t"); err != nil {
		t.Fatalf("InitAndPush: %v", err)
	}

	// Commit a change locally WITHOUT pushing (as if the push had failed): the
	// worktree is now clean but the branch is one commit ahead of origin.
	writeFile(t, deviceA, "CLAUDE.md", "v2\n")
	commitLocally(t, deviceA, "cortex: unpushed change")

	if _, err := CommitAndPush(context.Background(), deviceA, "cortex: sync", "u", "t"); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	// Origin must now hold v2 - clone it fresh and confirm the commit was flushed.
	deviceC := filepath.Join(root, "c")
	if _, err := Clone(context.Background(), remote, deviceC, "u", "t"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if got := readFile(t, deviceC, "CLAUDE.md"); got != "v2\n" {
		t.Fatalf("origin CLAUDE.md = %q, want v2 (unpushed commit should have been flushed)", got)
	}
}

// TestPullDiscardsUnpushedLocalCommits pins the (deliberately destructive)
// last-write-wins contract: pulling when the local branch is AHEAD of an
// unchanged origin discards the unpushed local commits and resets to origin -
// the remote is the source of truth. This test exists so any change to that
// contract is a conscious one. In normal use, sync runs CommitAndPush first,
// which flushes pending commits before a pull (see TestCommitAndPush...).
func TestPullDiscardsUnpushedLocalCommits(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	bare, err := gogit.PlainInit(remote, true)
	if err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	headToMain := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))
	if err := bare.Storer.SetReference(headToMain); err != nil {
		t.Fatalf("set remote HEAD: %v", err)
	}

	deviceA := filepath.Join(root, "a")
	if err := os.MkdirAll(deviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, deviceA, "CLAUDE.md", "origin-v1\n")
	if _, err := InitAndPush(context.Background(), deviceA, remote, "cortex: initial", "u", "t"); err != nil {
		t.Fatalf("InitAndPush: %v", err)
	}

	// A local commit that is never pushed; origin stays at origin-v1.
	writeFile(t, deviceA, "CLAUDE.md", "local-ahead\n")
	commitLocally(t, deviceA, "cortex: local only")

	// Pull resets to origin, discarding the unpushed local commit.
	if _, err := Pull(context.Background(), deviceA, "u", "t"); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got := readFile(t, deviceA, "CLAUDE.md"); got != "origin-v1\n" {
		t.Fatalf("after pull, CLAUDE.md = %q, want origin-v1 (unpushed local commit should be discarded)", got)
	}
}

// TestNetworkOpsHonourCanceledContext confirms the network operations thread the
// caller's context through to go-git: an already-canceled context aborts Clone,
// CommitAndPush, and Pull instead of letting a network operation run unbounded.
// It uses the local transport (a bare remote on disk), so it stays offline like
// TestSyncRoundTrip while still exercising the context wiring.
func TestNetworkOpsHonourCanceledContext(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	bare, err := gogit.PlainInit(remote, true)
	if err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	headToMain := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))
	if err := bare.Storer.SetReference(headToMain); err != nil {
		t.Fatalf("set remote HEAD: %v", err)
	}

	// Seed the remote with one commit using a live context.
	deviceA := filepath.Join(root, "a")
	if err := os.MkdirAll(deviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, deviceA, "CLAUDE.md", "v1\n")
	if _, err := InitAndPush(context.Background(), deviceA, remote, "cortex: initial", "u", "t"); err != nil {
		t.Fatalf("seed InitAndPush: %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel() // canceled before any operation runs

	// Clone with a canceled context must fail rather than fetch.
	if _, err := Clone(canceled, remote, filepath.Join(root, "clone"), "u", "t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Clone with canceled ctx: got %v, want context.Canceled", err)
	}

	// A staged change pushed under a canceled context must fail at the push.
	writeFile(t, deviceA, "CLAUDE.md", "v2\n")
	if _, err := CommitAndPush(canceled, deviceA, "cortex: update", "u", "t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitAndPush with canceled ctx: got %v, want context.Canceled", err)
	}

	// Pull under a canceled context must fail at the fetch.
	if _, err := Pull(canceled, deviceA, "u", "t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pull with canceled ctx: got %v, want context.Canceled", err)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
