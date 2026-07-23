package main

import (
	"strings"
	"testing"

	"github.com/cortex-sync/Cortex/mcp/git-server/internal/keychain"
)

// setEnvCreds points the CORTEX_GIT_* variables at the given host for the
// duration of the test.
func setEnvCreds(t *testing.T, host, username, token string) {
	t.Helper()
	t.Setenv("CORTEX_GIT_HOST", host)
	t.Setenv("CORTEX_GIT_USERNAME", username)
	t.Setenv("CORTEX_GIT_TOKEN", token)
}

func TestEnvCredentialsMatchingHost(t *testing.T) {
	setEnvCreds(t, "gitlab.com", "lsymons", "glpat-test")
	username, token, ok := envCredentials("gitlab.com")
	if !ok {
		t.Fatal("expected env credentials to apply to the matching host")
	}
	if username != "lsymons" || token != "glpat-test" {
		t.Fatalf("unexpected credentials: user %q token %q", username, token)
	}
}

func TestEnvCredentialsHostMatchIsCaseInsensitive(t *testing.T) {
	setEnvCreds(t, "GitLab.com", "lsymons", "glpat-test")
	if _, _, ok := envCredentials("gitlab.com"); !ok {
		t.Fatal("expected case-insensitive host match")
	}
}

func TestEnvCredentialsHostMatchIgnoresTrailingDot(t *testing.T) {
	// A repo remote may resolve to an FQDN root form ("gitlab.com."); the env
	// token scoped to "gitlab.com" should still apply.
	setEnvCreds(t, "gitlab.com", "lsymons", "glpat-test")
	if _, _, ok := envCredentials("gitlab.com."); !ok {
		t.Fatal("expected trailing-dot host to match the env-scoped host")
	}
	// And the reverse: env host with the trailing dot, bare lookup host.
	setEnvCreds(t, "gitlab.com.", "lsymons", "glpat-test")
	if _, _, ok := envCredentials("gitlab.com"); !ok {
		t.Fatal("expected env host with trailing dot to match a bare lookup host")
	}
}

func TestEnvCredentialsHostMatchIgnoresPort(t *testing.T) {
	// A repo remote on a non-default port resolves to a bare hostname (see
	// RequireHTTPS/hostcanon.Canonicalize); an env token scoped without a port
	// must still apply, and vice versa.
	setEnvCreds(t, "git.example.com", "lsymons", "glpat-test")
	if _, _, ok := envCredentials("git.example.com:8443"); !ok {
		t.Fatal("expected a ported lookup host to match an env host with no port")
	}

	setEnvCreds(t, "git.example.com:8443", "lsymons", "glpat-test")
	if _, _, ok := envCredentials("git.example.com"); !ok {
		t.Fatal("expected an env host with a port to match a bare lookup host")
	}
}

// TestEnvCredentialsHostMatchFailsClosedOnUnnormalisableHost covers hostsEqual's
// fail-closed branch: a host either side cannot IDNA-normalise must never be
// treated as matching anything, in either direction.
func TestEnvCredentialsHostMatchFailsClosedOnUnnormalisableHost(t *testing.T) {
	const invalid = "-invalid-.com"

	setEnvCreds(t, invalid, "lsymons", "glpat-test")
	if _, _, ok := envCredentials("gitlab.com"); ok {
		t.Fatal("an unnormalisable env host must not match any lookup host")
	}

	setEnvCreds(t, "gitlab.com", "lsymons", "glpat-test")
	if _, _, ok := envCredentials(invalid); ok {
		t.Fatal("an unnormalisable lookup host must not match any env host")
	}
}

func TestEnvCredentialsUsernameDefaults(t *testing.T) {
	t.Setenv("CORTEX_GIT_HOST", "gitlab.com")
	t.Setenv("CORTEX_GIT_TOKEN", "glpat-test")
	username, _, ok := envCredentials("gitlab.com")
	if !ok {
		t.Fatal("expected env credentials to apply")
	}
	if username != envUsernameDefault {
		t.Fatalf("expected default username %q, got %q", envUsernameDefault, username)
	}
}

func TestEnvCredentialsScopedToHost(t *testing.T) {
	setEnvCreds(t, "gitlab.com", "lsymons", "glpat-test")
	if _, _, ok := envCredentials("github.com"); ok {
		t.Fatal("env token for gitlab.com must not be offered to github.com")
	}
}

func TestEnvCredentialsTokenWithoutHostIgnored(t *testing.T) {
	t.Setenv("CORTEX_GIT_TOKEN", "glpat-test")
	if _, _, ok := envCredentials("gitlab.com"); ok {
		t.Fatal("a token without CORTEX_GIT_HOST must be ignored")
	}
}

func TestResolveCredsPrefersEnvOverStore(t *testing.T) {
	if err := keychain.SetCredentials("gitlab.com", "stored-user", "stored-token"); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	t.Cleanup(func() { _ = keychain.DeleteCredentials("gitlab.com") })

	setEnvCreds(t, "gitlab.com", "env-user", "env-token")
	username, token, errResult := resolveCreds("gitlab.com")
	if errResult != nil {
		t.Fatalf("unexpected error result: %s", resultText(t, errResult))
	}
	if username != "env-user" || token != "env-token" {
		t.Fatalf("expected env credentials to win, got user %q token %q", username, token)
	}
}

func TestResolveCredsFallsBackToStoreForOtherHosts(t *testing.T) {
	if err := keychain.SetCredentials("github.com", "stored-user", "stored-token"); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	t.Cleanup(func() { _ = keychain.DeleteCredentials("github.com") })

	setEnvCreds(t, "gitlab.com", "env-user", "env-token")
	username, token, errResult := resolveCreds("github.com")
	if errResult != nil {
		t.Fatalf("unexpected error result: %s", resultText(t, errResult))
	}
	if username != "stored-user" || token != "stored-token" {
		t.Fatalf("expected stored credentials for the non-env host, got user %q token %q", username, token)
	}
}

func TestGetAuthStatusReportsEnvSource(t *testing.T) {
	setEnvCreds(t, "gitlab.com", "env-user", "env-token")
	res := call(t, getAuthStatusHandler, map[string]interface{}{"host": "gitlab.com"})
	text := resultText(t, res)
	if !strings.Contains(text, "source: env") || !strings.Contains(text, "env-user") {
		t.Fatalf("expected env source in auth status, got %q", text)
	}
	if strings.Contains(text, "env-token") {
		t.Fatalf("auth status must never echo the token, got %q", text)
	}
}

func TestSetCredentialsNotesActiveEnvOverride(t *testing.T) {
	setEnvCreds(t, "gitlab.com", "env-user", "env-token")
	t.Cleanup(func() { _ = keychain.DeleteCredentials("gitlab.com") })
	res := call(t, setCredentialsHandler, map[string]interface{}{
		"host": "gitlab.com", "username": "stored-user", "token": "stored-token",
	})
	text := resultText(t, res)
	if !strings.Contains(text, "credentials stored for gitlab.com") || !strings.Contains(text, "takes precedence") {
		t.Fatalf("expected store confirmation with env-override note, got %q", text)
	}
}

func TestDeleteCredentialsNotesActiveEnvOverride(t *testing.T) {
	setEnvCreds(t, "gitlab.com", "env-user", "env-token")
	res := call(t, deleteCredentialsHandler, map[string]interface{}{"host": "gitlab.com"})
	text := resultText(t, res)
	if !strings.Contains(text, "credentials removed for gitlab.com") || !strings.Contains(text, "remains active") {
		t.Fatalf("expected delete confirmation with env-override note, got %q", text)
	}
}
