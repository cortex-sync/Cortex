package main

// Environment-injected credentials.
//
// A host environment that cannot reach the OS keychain interactively (the
// Cowork local-MCP config, a .mcpb user_config, CI) can supply the PAT via
// environment variables instead of the credential store:
//
//	CORTEX_GIT_HOST     - git host the token is for (e.g. gitlab.com); required
//	CORTEX_GIT_TOKEN    - the Personal Access Token; required
//	CORTEX_GIT_USERNAME - username for HTTPS basic auth; defaults to "git"
//
// When set, they take precedence over the keychain/file store for that host
// only - the token is never offered to any other host. set_credentials and
// delete_credentials keep operating on the underlying store.

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// envUsernameDefault is used when CORTEX_GIT_USERNAME is unset. GitHub and
// GitLab both accept any non-empty username with a PAT over HTTPS.
const envUsernameDefault = "git"

// envHostWarnOnce dedupes the misconfiguration warning so a token without a
// host is reported once per process, not once per credential lookup.
var envHostWarnOnce sync.Once

// envCredentials returns credentials injected via the CORTEX_GIT_* environment
// variables, with ok reporting whether they apply to host. A token without
// CORTEX_GIT_HOST is ignored (with a warning on stderr): the host scopes the
// token, and offering it to whatever host a repo remote happens to point at
// would leak it.
func envCredentials(host string) (username, token string, ok bool) {
	token = os.Getenv("CORTEX_GIT_TOKEN")
	if token == "" {
		return "", "", false
	}
	envHost := strings.TrimSpace(os.Getenv("CORTEX_GIT_HOST"))
	if envHost == "" {
		envHostWarnOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "cortex-git: CORTEX_GIT_TOKEN is set but CORTEX_GIT_HOST is not; ignoring the environment token")
		})
		return "", "", false
	}
	if !hostsEqual(envHost, host) {
		return "", "", false
	}
	username = os.Getenv("CORTEX_GIT_USERNAME")
	if username == "" {
		username = envUsernameDefault
	}
	return username, token, true
}

// hostsEqual compares two hostnames for credential scoping: case-insensitive,
// ignoring surrounding whitespace and a trailing FQDN root dot (so
// "gitlab.com." matches "gitlab.com"). Matching is exact otherwise - the env
// token is only ever offered to its named host.
func hostsEqual(a, b string) bool {
	canon := func(h string) string {
		return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
	}
	return canon(a) == canon(b)
}
