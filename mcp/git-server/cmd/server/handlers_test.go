package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"

	"github.com/cortex-sync/Cortex/mcp/git-server/internal/keychain"
)

// allowTempPaths points the path-confinement root at the temp-dir base for a
// test, so the throwaway repos these handler tests create (all under os.TempDir)
// pass confinement. Confinement itself is exercised in confine_test.go; here it
// must simply not get in the way of the credential/host/operation gates.
func allowTempPaths(t *testing.T) {
	t.Helper()
	t.Setenv("CORTEX_REPO_ROOT", os.TempDir())
}

// repoWithRemote initialises a throwaway git repo with an "origin" remote set to
// remoteURL, so RemoteHost resolves a host without any network access.
func repoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	}); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}
	return dir
}

// TestGitPushPullRemoteHostError checks that commit_push and pull fail cleanly
// when the path is not a git repo (or has no origin remote), reporting the
// host-resolution failure rather than attempting any network operation.
func TestGitPushPullRemoteHostError(t *testing.T) {
	allowTempPaths(t)
	cases := []struct {
		name string
		h    handler
	}{
		{"commit_push", gitCommitPushHandler},
		{"pull", gitPullHandler},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, tc.h, map[string]interface{}{
				"repo_path": filepath.Join(t.TempDir(), "not-a-repo"),
				"message":   "msg",
			})
			if !res.IsError {
				t.Fatalf("%s on non-repo: want IsError, got %q", tc.name, resultText(t, res))
			}
			if txt := resultText(t, res); !strings.Contains(txt, "could not determine remote host") {
				t.Fatalf("%s error = %q, want 'could not determine remote host'", tc.name, txt)
			}
		})
	}
}

// TestGitNetworkHandlersMissingCreds checks that all four network handlers stop
// at the credential gate - returning the "no credentials" error before any
// network call - when no PAT is stored for the target host. Each case uses a
// distinct host so the shared keyring mock has nothing stored for it.
func TestGitNetworkHandlersMissingCreds(t *testing.T) {
	allowTempPaths(t)
	cases := []struct {
		name string
		h    handler
		args map[string]interface{}
	}{
		{"commit_push", gitCommitPushHandler, map[string]interface{}{
			"repo_path": repoWithRemote(t, "https://push.nocreds.example/u/r.git"),
			"message":   "m",
		}},
		{"pull", gitPullHandler, map[string]interface{}{
			"repo_path": repoWithRemote(t, "https://pull.nocreds.example/u/r.git"),
		}},
		{"clone", gitCloneHandler, map[string]interface{}{
			"remote_url": "https://clone.nocreds.example/u/r.git",
			"local_path": t.TempDir(),
		}},
		{"init", gitInitHandler, map[string]interface{}{
			"remote_url": "https://init.nocreds.example/u/r.git",
			"local_path": t.TempDir(),
			"message":    "m",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, tc.h, tc.args)
			if !res.IsError {
				t.Fatalf("%s without creds: want IsError, got %q", tc.name, resultText(t, res))
			}
			if txt := resultText(t, res); !strings.Contains(txt, "no credentials found") {
				t.Fatalf("%s error = %q, want 'no credentials found'", tc.name, txt)
			}
		})
	}
}

// TestGitNetworkHandlersOperationError drives each network handler PAST the host
// and credential gates - a PAT is stored for the target host - and into the git
// operation itself, which then fails deterministically and without any network
// access. This covers the post-credential path (gitOpContext + the igit call +
// result handling) that the bad-host and missing-creds tests above stop short of.
//
// commit_push is deliberately not exercised here: a CLEAN repo always attempts
// a push (to flush unpushed commits), so it cannot fail before the network
// without a fragile setup - a happy-path round-trip is covered by the e2e test
// in CI instead. Its own operation-error path (a DIRTY repo blocked by the
// secret-scan gate, which fires before any push attempt) is covered separately
// by TestGitCommitPushHandlerBlockedBySecretScan below.
func TestGitNetworkHandlersOperationError(t *testing.T) {
	allowTempPaths(t)
	// A local path that is already a git repo makes PlainCloneContext fail with
	// "repository already exists" before it opens any network connection.
	existingRepo := repoWithRemote(t, "https://clone.op.example/u/r.git")

	cases := []struct {
		name    string
		h       handler
		host    string
		args    map[string]interface{}
		isError bool   // whether the handler should report IsError
		want    string // substring the result/error text must contain
	}{
		{
			name: "pull", h: gitPullHandler, host: "pull.op.example",
			args: map[string]interface{}{
				// Fresh repo has no HEAD, so Pull fails before it fetches.
				"repo_path": repoWithRemote(t, "https://pull.op.example/u/r.git"),
			},
			isError: true, want: "resolving HEAD",
		},
		{
			name: "clone", h: gitCloneHandler, host: "clone.op.example",
			args: map[string]interface{}{
				"remote_url": "https://clone.op.example/u/r.git",
				"local_path": existingRepo, // already a repo -> fails before network
			},
			isError: true, want: "already exists",
		},
		{
			name: "init", h: gitInitHandler, host: "init.op.example",
			args: map[string]interface{}{
				"remote_url": "https://init.op.example/u/r.git",
				"local_path": t.TempDir(), // empty -> nothing to commit, before push
				"message":    "m",
			},
			isError: true, want: "nothing to commit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := keychain.SetCredentials(tc.host, "user", "tok"); err != nil {
				t.Fatalf("SetCredentials: %v", err)
			}
			t.Cleanup(func() { _ = keychain.DeleteCredentials(tc.host) })

			res := call(t, tc.h, tc.args)
			if res.IsError != tc.isError {
				t.Fatalf("%s: IsError = %v, want %v (result %q)", tc.name, res.IsError, tc.isError, resultText(t, res))
			}
			txt := resultText(t, res)
			if !strings.Contains(txt, tc.want) {
				t.Fatalf("%s: result = %q, want it to contain %q", tc.name, txt, tc.want)
			}
			// Guard: prove we got past the early gates into the operation, so this
			// really exercises the post-credential path and not an earlier return.
			if strings.Contains(txt, "no credentials found") || strings.Contains(txt, "could not determine remote host") {
				t.Fatalf("%s: stopped at an early gate (%q); operation path not exercised", tc.name, txt)
			}
		})
	}
}

// TestGitCommitPushHandlerBlockedBySecretScan drives commit_push PAST the host
// and credential gates and into CommitAndPush itself, deterministically and
// without any network: the secret-scan gate inside CommitAndPush fires before
// staging/committing/pushing, so a file containing a credential-shaped string
// fails the operation before it ever attempts a push - unlike a clean repo
// (see TestGitNetworkHandlersOperationError's docstring), which always tries
// to push and so needs a reachable remote.
func TestGitCommitPushHandlerBlockedBySecretScan(t *testing.T) {
	allowTempPaths(t)
	const host = "blocked.example"
	if err := keychain.SetCredentials(host, "user", "tok"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	t.Cleanup(func() { _ = keychain.DeleteCredentials(host) })

	repo := repoWithRemote(t, "https://"+host+"/u/r.git")
	if err := os.WriteFile(filepath.Join(repo, "memory.md"), []byte("GL_TOKEN = glpat-ABCDEFGHIJ1234567890\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := call(t, gitCommitPushHandler, map[string]interface{}{
		"repo_path": repo,
		"message":   "cortex: update",
	})
	if !res.IsError {
		t.Fatalf("commit_push with a secret in a file: want IsError, got %q", resultText(t, res))
	}
	if txt := resultText(t, res); !strings.Contains(txt, "refusing to commit") {
		t.Fatalf("result = %q, want it to mention 'refusing to commit'", txt)
	}
}

// TestRequiredMessageArg checks that commit_push and init reject a missing
// commit message before touching credentials or the network. mcp-go v0.18.0's
// handleToolCall does not itself enforce mcp.Required() - without this check a
// caller omitting "message" would silently commit and push with an empty one.
func TestRequiredMessageArg(t *testing.T) {
	allowTempPaths(t)
	cases := []struct {
		name string
		h    handler
		args map[string]interface{}
	}{
		{"commit_push missing", gitCommitPushHandler, map[string]interface{}{
			// repo_path need not even be a real repo - the message check runs
			// before RemoteHost, so this must fail without reaching it.
			"repo_path": filepath.Join(t.TempDir(), "not-a-repo"),
		}},
		{"commit_push whitespace-only", gitCommitPushHandler, map[string]interface{}{
			"repo_path": filepath.Join(t.TempDir(), "not-a-repo"),
			"message":   "   \n\t",
		}},
		{"init missing", gitInitHandler, map[string]interface{}{
			"remote_url": "https://reqmsg.example/u/r.git",
			"local_path": t.TempDir(),
		}},
		{"init whitespace-only", gitInitHandler, map[string]interface{}{
			"remote_url": "https://reqmsg.example/u/r.git",
			"local_path": t.TempDir(),
			"message":    "   ",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, tc.h, tc.args)
			if !res.IsError {
				t.Fatalf("%s with no message: want IsError, got %q", tc.name, resultText(t, res))
			}
			if txt := resultText(t, res); !strings.Contains(txt, "message is required") {
				t.Fatalf("%s error = %q, want 'message is required'", tc.name, txt)
			}
			if strings.Contains(resultText(t, res), "could not determine remote host") {
				t.Fatalf("%s: reached RemoteHost - the message check should have stopped it first", tc.name)
			}
		})
	}
}

// TestSetCredentialsHandlerRequiresUsernameAndToken checks that a missing or
// empty username/token is rejected rather than silently stored. This is the
// concrete example from the mcp-go required-arg gap: set_credentials with no
// token would otherwise store an empty one with no error.
func TestSetCredentialsHandlerRequiresUsernameAndToken(t *testing.T) {
	const host = "reqcreds.example"
	t.Cleanup(func() { _ = keychain.DeleteCredentials(host) })

	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"missing username", map[string]interface{}{"host": host, "token": "tok"}, "username is required"},
		{"empty username", map[string]interface{}{"host": host, "username": "", "token": "tok"}, "username is required"},
		{"whitespace-only username", map[string]interface{}{"host": host, "username": "  \t", "token": "tok"}, "username is required"},
		{"missing token", map[string]interface{}{"host": host, "username": "user"}, "token is required"},
		{"empty token", map[string]interface{}{"host": host, "username": "user", "token": ""}, "token is required"},
		{"whitespace-only token", map[string]interface{}{"host": host, "username": "user", "token": "   "}, "token is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, setCredentialsHandler, tc.args)
			if !res.IsError {
				t.Fatalf("%s: want IsError, got %q", tc.name, resultText(t, res))
			}
			if txt := resultText(t, res); !strings.Contains(txt, tc.want) {
				t.Fatalf("%s: result = %q, want it to contain %q", tc.name, txt, tc.want)
			}
		})
	}

	// None of the rejected calls above should have stored anything.
	if _, _, errRes := resolveCreds(host); errRes == nil {
		t.Fatal("resolveCreds after rejected set_credentials calls: want an error result (nothing should be stored), got nil")
	}
}

// TestSetCredentialsHandlerRejectsEmptyTokenWithoutClobbering is the concrete
// clobber scenario: a working credential must survive a set_credentials call
// that omits the token, not be silently overwritten with an empty one.
func TestSetCredentialsHandlerRejectsEmptyTokenWithoutClobbering(t *testing.T) {
	// A missing token and a whitespace-only one must both be rejected: a fable-
	// agent adversarial review found that requireStringArg's original v == ""
	// check let " " through as "non-empty", re-opening this exact clobber with
	// one space. requireStringArg now checks strings.TrimSpace(v) == "".
	cases := []struct {
		name string
		args map[string]interface{}
	}{
		{"missing token", map[string]interface{}{}},
		{"whitespace-only token", map[string]interface{}{"token": "  \n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := "noclobber-" + strings.ReplaceAll(tc.name, " ", "-") + ".example"
			t.Cleanup(func() { _ = keychain.DeleteCredentials(host) })

			if err := keychain.SetCredentials(host, "good-user", "good-tok"); err != nil {
				t.Fatalf("SetCredentials: %v", err)
			}

			args := map[string]interface{}{"host": host, "username": "good-user"}
			for k, v := range tc.args {
				args[k] = v
			}
			res := call(t, setCredentialsHandler, args)
			if !res.IsError {
				t.Fatalf("set_credentials %s: want IsError, got %q", tc.name, resultText(t, res))
			}

			user, token, errRes := resolveCreds(host)
			if errRes != nil {
				t.Fatalf("resolveCreds after rejected set_credentials: unexpected error result %q", resultText(t, errRes))
			}
			if user != "good-user" || token != "good-tok" {
				t.Fatalf("resolved = (%q, %q), want the original (good-user, good-tok) untouched", user, token)
			}
		})
	}
}

// TestSetCredentialsHandlerStores covers the set_credentials success path (no
// environment override) and confirms the stored PAT round-trips through
// resolveCreds.
func TestSetCredentialsHandlerStores(t *testing.T) {
	const host = "setcreds.example"
	t.Cleanup(func() { _ = keychain.DeleteCredentials(host) })

	res := call(t, setCredentialsHandler, map[string]interface{}{
		"host":     host,
		"username": "dave",
		"token":    "tok-set",
	})
	if res.IsError {
		t.Fatalf("set_credentials: unexpected error %q", resultText(t, res))
	}
	if txt := resultText(t, res); !strings.Contains(txt, "credentials stored for "+host) {
		t.Fatalf("set result = %q, want 'credentials stored for %s'", txt, host)
	}

	user, token, errRes := resolveCreds(host)
	if errRes != nil {
		t.Fatalf("resolveCreds after set: %q", resultText(t, errRes))
	}
	if user != "dave" || token != "tok-set" {
		t.Fatalf("resolved = (%q, %q), want (dave, tok-set)", user, token)
	}
}
