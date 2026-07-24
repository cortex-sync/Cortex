package keychain

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keyringStore stores credentials in the OS keychain via zalando/go-keyring.
// Each host occupies two entries: one for the username and one for the token.
type keyringStore struct{}

func (keyringStore) kind() string { return "keychain" }

// keyringSet, keyringGet, and keyringDelete are keyring.Set/Get/Delete,
// indirected so a test can simulate one call of a pair failing while the
// other succeeds - something go-keyring's own mock (MockInitWithError is
// all-or-nothing across every call) cannot produce.
var (
	keyringSet    = keyring.Set
	keyringGet    = keyring.Get
	keyringDelete = keyring.Delete
)

// Set stores the username and token for the given host. The two entries can't
// be written atomically (go-keyring has no multi-key transaction), so if the
// username write succeeds but the token write then fails, best-effort delete
// the username again rather than leave it dangling - otherwise a later Get
// would find the username entry but not the token, surface a confusing
// backend error instead of ErrNotFound, and leave sync blocked until a manual
// delete_credentials. The cleanup's own error is deliberately ignored: the
// original token-write error is what the caller needs to see.
func (keyringStore) Set(host, username, token string) error {
	if err := keyringSet(service, hostUsernameKey(host), username); err != nil {
		return fmt.Errorf("storing username: %w", err)
	}
	if err := keyringSet(service, hostTokenKey(host), token); err != nil {
		_ = keyringDelete(service, hostUsernameKey(host))
		return fmt.Errorf("storing token: %w", err)
	}
	return nil
}

// Get retrieves the username and token for the given host.
func (keyringStore) Get(host string) (username, token string, err error) {
	username, err = keyringGet(service, hostUsernameKey(host))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("retrieving username for %s: %w", host, err)
	}
	token, err = keyringGet(service, hostTokenKey(host))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("retrieving token for %s: %w", host, err)
	}
	return username, token, nil
}

// Delete removes the stored credentials for the given host.
func (keyringStore) Delete(host string) error {
	if err := keyringDelete(service, hostUsernameKey(host)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("deleting username: %w", err)
	}
	if err := keyringDelete(service, hostTokenKey(host)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("deleting token: %w", err)
	}
	return nil
}

func hostUsernameKey(host string) string { return host + ":username" }
func hostTokenKey(host string) string    { return host + ":token" }
