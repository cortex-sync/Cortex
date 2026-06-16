package keychain

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
)

// keyDomain is mixed into the derived key so the stored file cannot be decrypted
// by other tools deriving keys from the same machine identifier.
const keyDomain = "cortex-credential-store-v1"

// machineIDEnvVar lets a deployment supply a stable, secret machine-bound
// identifier when no OS machine-id file is available (some containers, minimal
// images). When set it takes precedence and restores genuine non-portability of
// the encrypted store - see machineID.
const machineIDEnvVar = "CORTEX_MACHINE_ID"

// weakKeyWarnOnce guards the no-machine-binding warning so it is emitted at most
// once per process rather than on every encrypt/decrypt call.
var weakKeyWarnOnce sync.Once

// credEntry is a single host's stored credentials.
type credEntry struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}

// fileStore persists credentials in an AES-256-GCM encrypted file.
//
// SECURITY NOTE: the encryption key is derived from a machine-bound identifier
// (see deriveKey/machineID) so the file is not portable and cannot be read as
// plaintext, is not synced into the profile repo, and survives casual inspection
// or cloud backup. It is NOT protected by a user passphrase, so any process
// running as the same user on the same machine can decrypt it. This is
// obfuscation-at-rest comparable to (and stronger than) plaintext stores like
// git-credential-store or the gh CLI; it is a deliberate fallback for platforms
// without an OS Secret Service, not a replacement for hardware-backed keychains.
//
// The machine binding depends on /etc/machine-id (or CORTEX_MACHINE_ID). In an
// environment with neither - some containers/minimal images - the key falls back
// to a guessable value, the store becomes portable, and a one-time warning is
// emitted (see warnWeakKey). Set CORTEX_MACHINE_ID there to restore binding.
type fileStore struct {
	path string
	mu   sync.Mutex
}

func newFileStore() *fileStore {
	return &fileStore{path: fileStorePath()}
}

// newFileStoreAt pins the encrypted file directly inside dir, bypassing the
// default user-config-dir resolution. Used by the CORTEX_CONFIG_DIR override
// (see selectStore) so tests and headless deployments control the exact
// location.
func newFileStoreAt(dir string) *fileStore {
	return &fileStore{path: filepath.Join(dir, "credentials.enc")}
}

func (*fileStore) kind() string { return "file" }

// fileStorePath returns the location of the encrypted credentials file,
// honouring the OS user config directory (XDG_CONFIG_HOME on Linux).
func fileStorePath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		// Fall back to a dot-dir in the home directory.
		home, herr := os.UserHomeDir()
		if herr != nil || home == "" {
			home = "."
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "cortex", "credentials.enc")
}

// Set stores the username and token for the given host.
func (f *fileStore) Set(host, username, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	creds, err := f.load()
	if err != nil {
		return err
	}
	creds[host] = credEntry{Username: username, Token: token}
	return f.save(creds)
}

// Get retrieves the username and token for the given host.
func (f *fileStore) Get(host string) (username, token string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	creds, err := f.load()
	if err != nil {
		return "", "", err
	}
	entry, ok := creds[host]
	if !ok {
		return "", "", ErrNotFound
	}
	return entry.Username, entry.Token, nil
}

// Delete removes the stored credentials for the given host.
func (f *fileStore) Delete(host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	creds, err := f.load()
	if err != nil {
		return err
	}
	delete(creds, host)
	return f.save(creds)
}

// load reads and decrypts the credentials file. A missing file yields an empty
// map and no error.
func (f *fileStore) load() (map[string]credEntry, error) {
	// #nosec G304 -- f.path is derived solely from os.UserConfigDir/UserHomeDir
	// plus fixed constants in fileStorePath(); no caller/network input reaches it.
	blob, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return map[string]credEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}
	if len(blob) == 0 {
		return map[string]credEntry{}, nil
	}

	plaintext, err := decrypt(blob)
	if err != nil {
		// The file backend's key is machine-bound, so a decrypt failure usually
		// means the file was written on another machine (or is corrupt). Because
		// Set and Delete also load() first, this otherwise blocks storing new
		// credentials too - surface the recovery path in the error.
		return nil, fmt.Errorf("decrypting credentials file %s (it may be from another machine or corrupt; delete it and re-run set_credentials to recover): %w", f.path, err)
	}

	creds := map[string]credEntry{}
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}
	return creds, nil
}

// save encrypts and atomically writes the credentials file with 0600 perms.
func (f *fileStore) save(creds map[string]credEntry) error {
	plaintext, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	blob, err := encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypting credentials: %w", err)
	}

	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating credentials dir: %w", err)
	}

	// A unique temp file (0600 by default) rather than a fixed path+".tmp": the
	// in-process mutex does not guard against another cortex-git process saving
	// concurrently, and two writers sharing one temp path could rename a
	// half-written blob into place.
	tmp, err := os.CreateTemp(dir, "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp credentials file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing credentials file: %w", err)
	}
	if err := os.Rename(tmpPath, f.path); err != nil {
		_ = os.Remove(tmpPath) // best-effort cleanup; the rename error is what matters
		return fmt.Errorf("finalising credentials file: %w", err)
	}
	return nil
}

// encrypt seals plaintext with AES-256-GCM, prefixing the random nonce.
func encrypt(plaintext []byte) ([]byte, error) {
	gcm, err := newGCM()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt opens a blob produced by encrypt.
func decrypt(blob []byte) ([]byte, error) {
	gcm, err := newGCM()
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := blob[:ns], blob[ns:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("authentication failed (wrong machine key or corrupt file): %w", err)
	}
	return plaintext, nil
}

// newGCM builds an AES-256-GCM cipher from the machine-bound key.
func newGCM() (cipher.AEAD, error) {
	key := deriveKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return gcm, nil
}

// deriveKey produces a 32-byte key bound to this machine and user. It mixes a
// fixed domain string, the machine identifier, and the current user's id so the
// file is non-portable and scoped per user. When no genuine machine-bound
// identifier is available it warns once, loudly, rather than silently producing
// a portable key (see machineID).
func deriveKey() [32]byte {
	id, bound := machineID()
	if !bound {
		weakKeyWarnOnce.Do(warnWeakKey)
	}
	h := sha256.New()
	h.Write([]byte(keyDomain))
	h.Write([]byte(id))
	if u, err := user.Current(); err == nil {
		h.Write([]byte(u.Uid))
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

// machineID returns a stable identifier for key derivation and reports whether
// it is genuinely machine-bound. CORTEX_MACHINE_ID (if set) and the OS machine-id
// files are treated as machine-bound; the hostname and the final constant are
// not - they are guessable or public, so a file keyed on them is portable.
//
// SECURITY NOTE: when bound is false the derived key provides neither
// non-portability nor real at-rest protection (the hostname is guessable and the
// constant is public in this source). Callers warn in that case. Set
// CORTEX_MACHINE_ID to a stable secret, use an OS keychain, or adopt the planned
// passphrase mode (docs/TODO.md) for genuine protection.
func machineID() (id string, bound bool) {
	if v := strings.TrimSpace(os.Getenv(machineIDEnvVar)); v != "" {
		return v, true
	}
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		// #nosec G304 -- p is a fixed internal list of machine-id paths, not user input
		if b, err := os.ReadFile(p); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s, true
			}
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h, false
	}
	return "cortex-default-machine", false
}

// warnWeakKey reports, once per process, that the encrypted store is running
// without a machine-bound key and is therefore portable. Written to stderr so it
// never corrupts the stdio MCP protocol channel on stdout.
func warnWeakKey() {
	fmt.Fprintf(os.Stderr,
		"cortex-git: WARNING: no machine-bound identifier found (no /etc/machine-id "+
			"and %s unset); the encrypted credential store is keyed on a guessable "+
			"value and is therefore PORTABLE, not machine-bound. Set %s to a stable "+
			"secret, or use an OS keychain, for at-rest protection. See SECURITY.md.\n",
		machineIDEnvVar, machineIDEnvVar)
}
