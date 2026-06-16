package keychain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
