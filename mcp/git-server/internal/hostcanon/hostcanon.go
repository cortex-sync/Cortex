// Package hostcanon provides the single hostname-normalisation rule shared by
// every place Cortex keys or compares a git host: the credential store
// (internal/keychain), the resolve-side host from a remote URL
// (internal/git.RequireHTTPS), and the env-credential host match
// (cmd/server/envcreds.go).
//
// Before this package existed, each of those three call sites normalised (or
// didn't) independently: the credential store keyed on the caller's raw
// string, RequireHTTPS returned url.Hostname() (port stripped, case and a
// trailing FQDN dot preserved), and envcreds.go had its own private
// lowercase+trim+trailing-dot canon. A host that only differed by case, a
// trailing root dot, an explicit port, or its Unicode-vs-punycode form could
// therefore be stored under one key and looked up under another, silently
// breaking credential resolution. Canonicalize is the one function all three
// now call, so the store key always equals the resolve key.
package hostcanon

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// Canonicalize normalises host for use as a credential-store key or as one
// side of a host comparison: it trims surrounding whitespace, strips a
// trailing ":port" (if present), strips a trailing FQDN root dot, and applies
// IDNA "Lookup" normalisation - ASCII case-folding plus Unicode/punycode
// normalisation, so "GitLab.com", "gitlab.com.", "gitlab.com:8443", and a
// Unicode-homoglyph or punycode form of the same host all canonicalise to the
// same string.
//
// It fails closed: an empty host, or one IDNA cannot normalise (malformed, or
// containing characters IDNA rejects as confusable), is an error rather than
// being passed through unnormalised - the caller must not fall back to
// treating an unnormalisable host as a valid, distinct key.
func Canonicalize(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("host is empty")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("host is empty after removing the trailing FQDN dot")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("host %q is not a valid hostname: %w", host, err)
	}
	return ascii, nil
}
