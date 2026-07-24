package keychain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// resetBackend clears the cached backend so the next call re-selects one.
func resetBackend() {
	storeMu.Lock()
	activeStore = nil
	storeMu.Unlock()
}

// TestKeyringBackendRoundTrip exercises the public API against the in-memory
// keyring mock, mirroring how the keychain backend behaves in CI.
func TestKeyringBackendRoundTrip(t *testing.T) {
	keyring.MockInit()
	resetBackend()
	t.Cleanup(resetBackend)

	if got := Backend(); got != "keychain" {
		t.Fatalf("Backend() = %q, want keychain", got)
	}

	const host = "gitlab.com"
	if err := SetCredentials(host, "alice", "token-aaa"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}

	user, token, err := GetCredentials(host)
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if user != "alice" || token != "token-aaa" {
		t.Fatalf("got (%q, %q), want (alice, token-aaa)", user, token)
	}

	if err := DeleteCredentials(host); err != nil {
		t.Fatalf("DeleteCredentials: %v", err)
	}
	if _, _, err := GetCredentials(host); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete, GetCredentials err = %v, want ErrNotFound", err)
	}
}

// TestCredentialsKeyIsCanonicalised is the regression guard for the
// store/resolve host-key mismatch: a credential set under one form of a host
// (mixed case, a trailing FQDN dot, or an explicit port) must be found,
// overwritten, and deleted under any other canonically-equivalent form -
// exactly as RequireHTTPS's resolve-side host normalises, per
// hostcanon.Canonicalize.
func TestCredentialsKeyIsCanonicalised(t *testing.T) {
	keyring.MockInit()
	resetBackend()
	t.Cleanup(resetBackend)

	if err := SetCredentials("GitLab.com", "alice", "token-aaa"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}

	lookups := []string{"gitlab.com", "GITLAB.COM", "gitlab.com.", "gitlab.com:443"}
	for _, host := range lookups {
		t.Run(host, func(t *testing.T) {
			user, token, err := GetCredentials(host)
			if err != nil {
				t.Fatalf("GetCredentials(%q): %v", host, err)
			}
			if user != "alice" || token != "token-aaa" {
				t.Fatalf("GetCredentials(%q) = (%q, %q), want (alice, token-aaa)", host, user, token)
			}
		})
	}

	// A differently-cased, ported form must overwrite the same entry, not
	// create a second one.
	if err := SetCredentials("gitlab.com:8443", "bob", "token-bbb"); err != nil {
		t.Fatalf("SetCredentials (overwrite via different form): %v", err)
	}
	user, token, err := GetCredentials("GitLab.com")
	if err != nil {
		t.Fatalf("GetCredentials after overwrite: %v", err)
	}
	if user != "bob" || token != "token-bbb" {
		t.Fatalf("GetCredentials after overwrite = (%q, %q), want (bob, token-bbb) - overwrite must hit the same canonical key", user, token)
	}

	// Delete via yet another equivalent form must remove the one entry.
	if err := DeleteCredentials("GITLAB.COM."); err != nil {
		t.Fatalf("DeleteCredentials: %v", err)
	}
	if _, _, err := GetCredentials("gitlab.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete via an equivalent form, GetCredentials err = %v, want ErrNotFound", err)
	}
}

// TestCredentialsRejectUnnormalisableHost covers the fail-closed branch all
// three entry points share: a host hostcanon.Canonicalize cannot normalise
// (empty, or one IDNA rejects) must return an error instead of silently
// falling back to an unnormalised store key.
func TestCredentialsRejectUnnormalisableHost(t *testing.T) {
	keyring.MockInit()
	resetBackend()
	t.Cleanup(resetBackend)

	for _, host := range []string{"", "-invalid-.com"} {
		if err := SetCredentials(host, "alice", "token-aaa"); err == nil {
			t.Fatalf("SetCredentials(%q) = nil, want error", host)
		}
		if _, _, err := GetCredentials(host); err == nil {
			t.Fatalf("GetCredentials(%q) = nil error, want error", host)
		}
		if err := DeleteCredentials(host); err == nil {
			t.Fatalf("DeleteCredentials(%q) = nil, want error", host)
		}
	}
}

// TestConfigDirOverrideForcesFileBackend verifies that CORTEX_CONFIG_DIR pins
// the encrypted-file backend at the given directory even when a working OS
// keyring is present, and that the keyring is never written to.
func TestConfigDirOverrideForcesFileBackend(t *testing.T) {
	keyring.MockInit() // a working keyring that the override must ignore
	dir := t.TempDir()
	t.Setenv("CORTEX_CONFIG_DIR", dir)
	resetBackend()
	t.Cleanup(resetBackend)

	if got := Backend(); got != "file" {
		t.Fatalf("Backend() = %q, want file", got)
	}

	const host = "gitlab.com"
	if err := SetCredentials(host, "alice", "token-aaa"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.enc")); err != nil {
		t.Fatalf("credentials file not created in CORTEX_CONFIG_DIR: %v", err)
	}
	if _, err := keyring.Get(service, host); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("keyring was touched despite the override (err = %v)", err)
	}

	user, token, err := GetCredentials(host)
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if user != "alice" || token != "token-aaa" {
		t.Fatalf("got (%q, %q), want (alice, token-aaa)", user, token)
	}

	if err := DeleteCredentials(host); err != nil {
		t.Fatalf("DeleteCredentials: %v", err)
	}
	if _, _, err := GetCredentials(host); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete, GetCredentials err = %v, want ErrNotFound", err)
	}
}

// TestConfigDirBlankIsIgnored verifies that a whitespace-only CORTEX_CONFIG_DIR
// does not force the file backend.
func TestConfigDirBlankIsIgnored(t *testing.T) {
	keyring.MockInit()
	t.Setenv("CORTEX_CONFIG_DIR", "   ")
	resetBackend()
	t.Cleanup(resetBackend)

	if got := Backend(); got != "keychain" {
		t.Fatalf("Backend() = %q, want keychain", got)
	}
}

// TestKeyringUnavailableSelectsFile verifies that a Secret Service failure
// causes the file backend to be selected.
func TestKeyringUnavailableSelectsFile(t *testing.T) {
	keyring.MockInitWithError(errors.New("org.freedesktop.secrets not available"))
	resetBackend()
	t.Cleanup(func() {
		keyring.MockInit()
		resetBackend()
	})

	if got := Backend(); got != "file" {
		t.Fatalf("Backend() = %q, want file", got)
	}
}

// TestKeyringStoreSetCleansUpUsernameOnTokenFailure covers keyringStore.Set's
// partial-failure guard: go-keyring's own mock can only fail every call or
// none, so keyringSet is swapped for a stub that fails only on the second
// (token) write - the exact shape that used to leave a dangling username
// entry behind, making a later Get return a confusing backend error instead
// of ErrNotFound.
func TestKeyringStoreSetCleansUpUsernameOnTokenFailure(t *testing.T) {
	keyring.MockInit()
	const host = "partial-failure.example"

	calls := 0
	original := keyringSet
	keyringSet = func(service, user, pass string) error {
		calls++
		if calls == 2 { // the token write
			return errors.New("simulated token-write failure")
		}
		return original(service, user, pass)
	}
	t.Cleanup(func() { keyringSet = original })

	if err := (keyringStore{}).Set(host, "user", "tok"); err == nil {
		t.Fatal("Set: want the simulated token-write error, got nil")
	}

	// Check the username entry directly, not via keyringStore.Get: Get looks up
	// username then token in sequence and would return ErrNotFound anyway just
	// because the token entry is missing, regardless of whether the username
	// entry was actually cleaned up - that would make this test pass even
	// without the fix. Query the username key on its own to prove it's gone.
	if _, err := keyring.Get(service, hostUsernameKey(host)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("username entry after a partial failure: err = %v, want keyring.ErrNotFound (it should have been cleaned up, not left dangling)", err)
	}
}
