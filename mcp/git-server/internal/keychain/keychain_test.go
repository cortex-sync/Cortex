package keychain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestKeyringStoreSetUsernameWriteFails covers the first-call failure branch
// of Set (distinct from the token-write branch above): nothing has been
// written yet, so there is nothing to clean up, and the error must name the
// username write specifically.
func TestKeyringStoreSetUsernameWriteFails(t *testing.T) {
	keyring.MockInit()

	original := keyringSet
	keyringSet = func(service, user, pass string) error {
		return errors.New("simulated username-write failure")
	}
	t.Cleanup(func() { keyringSet = original })

	err := (keyringStore{}).Set("username-write-fails.example", "user", "tok")
	if err == nil {
		t.Fatal("Set: want the simulated username-write error, got nil")
	}
	if !strings.Contains(err.Error(), "storing username") {
		t.Fatalf("error = %v, want it to mention 'storing username'", err)
	}
}

// TestKeyringStoreGetBackendErrors covers Get's two non-ErrNotFound backend-
// error branches (username lookup, token lookup) and the asymmetric case
// where the username exists but the token entry does not - all three paths
// go-keyring's mock alone cannot produce, hence the keyringGet indirection.
func TestKeyringStoreGetBackendErrors(t *testing.T) {
	keyring.MockInit()

	t.Run("username lookup backend error", func(t *testing.T) {
		original := keyringGet
		keyringGet = func(service, user string) (string, error) {
			return "", errors.New("simulated backend error")
		}
		t.Cleanup(func() { keyringGet = original })

		_, _, err := (keyringStore{}).Get("get-username-error.example")
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("Get: err = %v, want a wrapped backend error (not ErrNotFound, not nil)", err)
		}
		if !strings.Contains(err.Error(), "retrieving username") {
			t.Fatalf("error = %v, want it to mention 'retrieving username'", err)
		}
	})

	t.Run("token lookup not found (asymmetric state)", func(t *testing.T) {
		const host = "get-token-missing.example"
		if err := keyring.Set(service, hostUsernameKey(host), "user"); err != nil {
			t.Fatalf("seeding username entry: %v", err)
		}
		t.Cleanup(func() { _ = keyring.Delete(service, hostUsernameKey(host)) })

		_, _, err := (keyringStore{}).Get(host)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get with username present but token missing: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("token lookup backend error", func(t *testing.T) {
		const host = "get-token-error.example"
		if err := keyring.Set(service, hostUsernameKey(host), "user"); err != nil {
			t.Fatalf("seeding username entry: %v", err)
		}
		t.Cleanup(func() { _ = keyring.Delete(service, hostUsernameKey(host)) })

		original := keyringGet
		keyringGet = func(service, user string) (string, error) {
			if user == hostTokenKey(host) {
				return "", errors.New("simulated backend error")
			}
			return original(service, user)
		}
		t.Cleanup(func() { keyringGet = original })

		_, _, err := (keyringStore{}).Get(host)
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("Get: err = %v, want a wrapped backend error (not ErrNotFound, not nil)", err)
		}
		if !strings.Contains(err.Error(), "retrieving token") {
			t.Fatalf("error = %v, want it to mention 'retrieving token'", err)
		}
	})
}

// TestKeyringStoreDeleteBackendErrors covers Delete's two non-ErrNotFound
// backend-error branches. A genuine backend failure (locked keyring) must be
// surfaced, distinct from the already-tested idempotent "nothing to delete"
// (ErrNotFound) case.
func TestKeyringStoreDeleteBackendErrors(t *testing.T) {
	keyring.MockInit()

	t.Run("username delete backend error", func(t *testing.T) {
		original := keyringDelete
		keyringDelete = func(service, user string) error {
			return errors.New("simulated backend error")
		}
		t.Cleanup(func() { keyringDelete = original })

		err := (keyringStore{}).Delete("delete-username-error.example")
		if err == nil {
			t.Fatal("Delete: want the simulated backend error, got nil")
		}
		if !strings.Contains(err.Error(), "deleting username") {
			t.Fatalf("error = %v, want it to mention 'deleting username'", err)
		}
	})

	t.Run("token delete backend error", func(t *testing.T) {
		const host = "delete-token-error.example"
		original := keyringDelete
		keyringDelete = func(service, user string) error {
			if user == hostTokenKey(host) {
				return errors.New("simulated backend error")
			}
			return original(service, user)
		}
		t.Cleanup(func() { keyringDelete = original })

		err := (keyringStore{}).Delete(host)
		if err == nil {
			t.Fatal("Delete: want the simulated backend error, got nil")
		}
		if !strings.Contains(err.Error(), "deleting token") {
			t.Fatalf("error = %v, want it to mention 'deleting token'", err)
		}
	})
}
