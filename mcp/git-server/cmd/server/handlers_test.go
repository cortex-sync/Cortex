package main

import (
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"

	"github.com/LucasSymons/Cortex/mcp/git-server/internal/keychain"
)

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
