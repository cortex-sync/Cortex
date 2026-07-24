package keychain

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// forceUnboundMachineID makes machineID() report bound=false for the duration of
// the test by clearing the env override and pointing the machine-id lookup at a
// path that does not exist, so the resolver falls through to the hostname.
func forceUnboundMachineID(t *testing.T) {
	t.Helper()
	t.Setenv(machineIDEnvVar, "")
	saved := machineIDPaths
	machineIDPaths = []string{filepath.Join(t.TempDir(), "no-such-machine-id")}
	t.Cleanup(func() { machineIDPaths = saved })
}

func newTestFileStore(t *testing.T) *fileStore {
	t.Helper()
	return &fileStore{path: filepath.Join(t.TempDir(), "credentials.enc")}
}

func TestFileStoreRoundTrip(t *testing.T) {
	fs := newTestFileStore(t)

	if fs.kind() != "file" {
		t.Fatalf("kind() = %q, want file", fs.kind())
	}

	if err := fs.Set("gitlab.com", "alice", "token-aaa"); err != nil {
		t.Fatalf("Set gitlab: %v", err)
	}
	if err := fs.Set("github.com", "bob", "token-bbb"); err != nil {
		t.Fatalf("Set github: %v", err)
	}

	user, token, err := fs.Get("gitlab.com")
	if err != nil {
		t.Fatalf("Get gitlab: %v", err)
	}
	if user != "alice" || token != "token-aaa" {
		t.Fatalf("got (%q, %q), want (alice, token-aaa)", user, token)
	}

	// Second host is preserved across writes.
	user, token, err = fs.Get("github.com")
	if err != nil {
		t.Fatalf("Get github: %v", err)
	}
	if user != "bob" || token != "token-bbb" {
		t.Fatalf("got (%q, %q), want (bob, token-bbb)", user, token)
	}

	if err := fs.Delete("gitlab.com"); err != nil {
		t.Fatalf("Delete gitlab: %v", err)
	}
	if _, _, err := fs.Get("gitlab.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete, Get err = %v, want ErrNotFound", err)
	}
	// Unrelated host survives the delete.
	if _, _, err := fs.Get("github.com"); err != nil {
		t.Fatalf("Get github after deleting gitlab: %v", err)
	}
}

func TestFileStoreMissingHost(t *testing.T) {
	fs := newTestFileStore(t)
	if _, _, err := fs.Get("never.set"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty store err = %v, want ErrNotFound", err)
	}
}

// TestFileStoreEncryptedAtRest ensures the token never appears as plaintext in
// the file on disk.
func TestFileStoreEncryptedAtRest(t *testing.T) {
	fs := newTestFileStore(t)
	const secret = "dummy-token-value-xyz"
	if err := fs.Set("gitlab.com", "alice", secret); err != nil {
		t.Fatalf("Set: %v", err)
	}

	blob, err := os.ReadFile(fs.path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatal("token found in plaintext on disk")
	}
	if strings.Contains(string(blob), "alice") {
		t.Fatal("username found in plaintext on disk")
	}

	info, err := os.Stat(fs.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perms = %o, want 600", perm)
	}
}

// TestDecryptRejectsTamperedData ensures GCM authentication catches corruption.
func TestDecryptRejectsTamperedData(t *testing.T) {
	blob, err := encrypt([]byte(`{"gitlab.com":{"username":"a","token":"b"}}`))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Flip a byte in the ciphertext body.
	blob[len(blob)-1] ^= 0xff
	if _, err := decrypt(blob); err == nil {
		t.Fatal("decrypt accepted tampered ciphertext")
	}
}

func TestDecryptRejectsShortInput(t *testing.T) {
	if _, err := decrypt([]byte{0x01, 0x02}); err == nil {
		t.Fatal("decrypt accepted too-short input")
	}
}

// TestMachineIDEnvOverride verifies CORTEX_MACHINE_ID takes precedence and is
// treated as a genuine (bound) machine identifier.
func TestMachineIDEnvOverride(t *testing.T) {
	t.Setenv(machineIDEnvVar, "deadbeef-stable-id")
	id, bound := machineID()
	if id != "deadbeef-stable-id" || !bound {
		t.Fatalf("machineID() = (%q, %v), want (deadbeef-stable-id, true)", id, bound)
	}
}

// TestFileStoreKeyBindsToMachineID proves the encrypted store is keyed on the
// machine identifier (the M9 finding): a file written under one identifier must
// not decrypt under another. CORTEX_MACHINE_ID is the controllable input.
func TestFileStoreKeyBindsToMachineID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.enc")

	t.Setenv(machineIDEnvVar, "machine-one")
	fsOne := &fileStore{path: path}
	if err := fsOne.Set("gitlab.com", "alice", "token-aaa"); err != nil {
		t.Fatalf("Set under machine-one: %v", err)
	}

	t.Setenv(machineIDEnvVar, "machine-two")
	fsTwo := &fileStore{path: path}
	if _, _, err := fsTwo.Get("gitlab.com"); err == nil {
		t.Fatal("store decrypted under a different machine id; key is not bound to the machine")
	}
}

// TestMachineIDUnboundFallsBack verifies that with no env override and no
// readable machine-id file, machineID() falls back to a non-bound identifier
// (the M9 unbound path) rather than reporting a false machine binding.
func TestMachineIDUnboundFallsBack(t *testing.T) {
	forceUnboundMachineID(t)

	id, bound := machineID()
	if bound {
		t.Fatalf("machineID() = (%q, true), want bound=false with no machine-id source", id)
	}
	if id == "" {
		t.Fatal("machineID() returned an empty identifier")
	}
}

// TestFileStoreWarnsOnceWhenUnbound proves the M9 fix: when the key cannot be
// bound to the machine, deriveKey emits the portability warning exactly once per
// process (not silently, and not on every call), and the store still functions.
func TestFileStoreWarnsOnceWhenUnbound(t *testing.T) {
	forceUnboundMachineID(t)

	var buf bytes.Buffer
	savedWriter := weakKeyWarnWriter
	weakKeyWarnWriter = &buf
	weakKeyWarnOnce = sync.Once{}
	t.Cleanup(func() {
		weakKeyWarnWriter = savedWriter
		weakKeyWarnOnce = sync.Once{}
	})

	fs := &fileStore{path: filepath.Join(t.TempDir(), "credentials.enc")}
	if err := fs.Set("gitlab.com", "alice", "token-aaa"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// A second key derivation (Get) must not produce a second warning.
	if _, _, err := fs.Get("gitlab.com"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "PORTABLE") || !strings.Contains(out, machineIDEnvVar) {
		t.Fatalf("warning did not name the portability risk or the override var: %q", out)
	}
	if n := strings.Count(out, "WARNING"); n != 1 {
		t.Fatalf("warning emitted %d times across two derivations, want exactly 1", n)
	}
}

// TestFileStorePathUsesConfigDir checks the primary path: os.UserConfigDir()
// succeeds (as it does in any normal environment with $HOME set), so
// fileStorePath must use it rather than falling back.
func TestFileStorePathUsesConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	want := filepath.Join(home, ".config", "cortex", "credentials.enc")
	if got := fileStorePath(); got != want {
		t.Fatalf("fileStorePath() = %q, want %q", got, want)
	}
}

// TestFileStorePathFallsBackWhenHomeUnavailable covers the fallback chain:
// with both $XDG_CONFIG_HOME and $HOME empty, os.UserConfigDir() fails (it has
// nothing to derive a path from), and the fallback to os.UserHomeDir() fails
// for the same reason, landing on the documented last resort ("."). Both env
// vars must be cleared together - os.UserConfigDir() on unix falls back to
// $HOME/.config internally, so it only fails when $HOME is unavailable too.
func TestFileStorePathFallsBackWhenHomeUnavailable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	want := filepath.Join(".", ".config", "cortex", "credentials.enc")
	if got := fileStorePath(); got != want {
		t.Fatalf("fileStorePath() = %q, want %q (the documented dot-dir fallback)", got, want)
	}
}
