// Package keychain stores Git credentials for the Cortex MCP server.
//
// Credentials are kept per-host under the "cortex" service name. Two backends
// are supported, selected automatically at runtime:
//
//   - keychain: the OS keychain via zalando/go-keyring (macOS Keychain,
//     Windows Credential Manager, or a Linux Secret Service such as
//     gnome-keyring/KWallet). Preferred whenever a working Secret Service is
//     present.
//   - file: an AES-256-GCM encrypted file under the user config directory,
//     used as a fallback when no Secret Service is available (headless Linux,
//     WSL2, containers, CI). See file_store.go for the security trade-offs.
//
// Tokens are never written to disk in plaintext under either backend.
package keychain

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"

	"github.com/cortex-sync/Cortex/mcp/git-server/internal/hostcanon"
)

// service is the keychain service name all Cortex secrets live under.
const service = "cortex"

// probeKey is a sentinel entry used to detect whether a usable OS keychain is
// present. It is only ever read, never written.
const probeKey = "__cortex_probe__"

// ErrNotFound is returned when no credentials are stored for a host. Callers
// errors.Is against it to tell a genuinely missing PAT apart from a backend
// failure (a locked keyring, a decryption error), which must surface verbatim
// rather than being misreported as "no credentials stored".
var ErrNotFound = errors.New("no credentials stored for host")

// store is the backend abstraction shared by the keychain and file backends.
type store interface {
	Set(host, username, token string) error
	Get(host string) (username, token string, err error)
	Delete(host string) error
	// kind returns a short identifier ("keychain" or "file") for diagnostics.
	kind() string
}

var (
	storeMu     sync.Mutex
	activeStore store
)

// backend returns the active credential store, selecting one on first use.
// Selection is cached for the lifetime of the process.
func backend() store {
	storeMu.Lock()
	defer storeMu.Unlock()
	if activeStore == nil {
		activeStore = selectStore()
	}
	return activeStore
}

// selectStore picks the credential backend. CORTEX_CONFIG_DIR, when set, pins
// the encrypted-file backend at that directory regardless of whether an OS
// keychain is present - a deterministic override for E2E tests and headless
// deployments (the OS-keychain probe never runs, so selection is identical on
// every platform). Otherwise the OS keychain is preferred when usable, with
// the encrypted file backend as the fallback. Selection is cached for the
// process lifetime (see backend), so the variable is read once at first use.
func selectStore() store {
	if dir := strings.TrimSpace(os.Getenv("CORTEX_CONFIG_DIR")); dir != "" {
		return newFileStoreAt(dir)
	}
	if keyringAvailable() {
		return keyringStore{}
	}
	return newFileStore()
}

// keyringAvailable reports whether a usable OS Secret Service is present. It
// reads a sentinel key: a missing key (ErrNotFound) means the keychain works
// but is empty; any other error means no Secret Service is available.
func keyringAvailable() bool {
	_, err := keyring.Get(service, probeKey)
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

// Backend returns the identifier of the active credential backend, either
// "keychain" or "file". Useful for surfacing which store is in use.
func Backend() string {
	return backend().kind()
}

// SetCredentials stores a username and PAT for the given host. host is
// canonicalised (see hostcanon.Canonicalize) before it becomes the store key,
// so the same effective host always resolves to the same entry regardless of
// case, a trailing FQDN dot, an explicit port, or its Unicode/punycode form -
// matching how GetCredentials and DeleteCredentials key their lookups.
func SetCredentials(host, username, token string) error {
	key, err := hostcanon.Canonicalize(host)
	if err != nil {
		return fmt.Errorf("cannot store credentials: %w", err)
	}
	return backend().Set(key, username, token)
}

// GetCredentials retrieves the username and PAT for the given host, keyed on
// its canonical form (see hostcanon.Canonicalize).
func GetCredentials(host string) (username, token string, err error) {
	key, err := hostcanon.Canonicalize(host)
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve credentials: %w", err)
	}
	return backend().Get(key)
}

// DeleteCredentials removes stored credentials for the given host, keyed on
// its canonical form (see hostcanon.Canonicalize).
func DeleteCredentials(host string) error {
	key, err := hostcanon.Canonicalize(host)
	if err != nil {
		return fmt.Errorf("cannot delete credentials: %w", err)
	}
	return backend().Delete(key)
}
