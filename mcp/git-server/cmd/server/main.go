package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	igit "github.com/cortex-sync/Cortex/mcp/git-server/internal/git"
	"github.com/cortex-sync/Cortex/mcp/git-server/internal/keychain"
)

const serverName = "cortex-git"

// Build metadata, injected at release time via -ldflags -X (see
// .goreleaser.yaml). The defaults apply to local and dev builds.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s %s (commit %s, built %s)\n", serverName, version, commit, date)
		return
	}

	s := server.NewMCPServer(serverName, version)

	// Register tools
	registerTools(s)

	log.Printf("cortex-git MCP server v%s starting", version)
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(s *server.MCPServer) {
	// git_status - list changed files in the profile repo
	s.AddTool(mcp.NewTool("git_status",
		mcp.WithDescription("List changed files in the Cortex profile repo"),
		mcp.WithString("repo_path", mcp.Required(), mcp.Description("Absolute path to the local profile repo")),
	), gitStatusHandler)

	// git_commit_push - commit all changes and push
	s.AddTool(mcp.NewTool("git_commit_push",
		mcp.WithDescription("Commit all changed files and push to the remote"),
		mcp.WithString("repo_path", mcp.Required(), mcp.Description("Absolute path to the local profile repo")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Commit message")),
	), gitCommitPushHandler)

	// git_pull - pull latest from remote (safe fast-forward by default; force = last-write-wins)
	s.AddTool(mcp.NewTool("git_pull",
		mcp.WithDescription("Pull the latest changes from the remote. Safe by default: it fast-forwards a clean repo and refuses if the pull would discard uncommitted changes or unpushed local commits. Set force to true to overwrite them (last-write-wins) - only when the user has chosen to discard local work."),
		mcp.WithString("repo_path", mcp.Required(), mcp.Description("Absolute path to the local profile repo")),
		mcp.WithBoolean("force", mcp.Description("Discard local uncommitted changes and diverging commits, resetting to origin (last-write-wins). Default false: a pull that would lose local work is refused instead.")),
	), gitPullHandler)

	// git_clone - clone a remote repo to a local path
	s.AddTool(mcp.NewTool("git_clone",
		mcp.WithDescription("Clone the Cortex profile repo to a local path"),
		mcp.WithString("remote_url", mcp.Required(), mcp.Description("HTTPS remote URL of the profile repo")),
		mcp.WithString("local_path", mcp.Required(), mcp.Description("Local path to clone into")),
	), gitCloneHandler)

	// git_init - initialise a new profile repo and push to an empty remote
	s.AddTool(mcp.NewTool("git_init",
		mcp.WithDescription("Initialise a new profile repo locally, add the remote, commit the files already in local_path, and push. Use for first-run setup against a freshly created EMPTY remote repo (go-git cannot clone an empty remote). Write the profile files into local_path before calling."),
		mcp.WithString("local_path", mcp.Required(), mcp.Description("Local path containing the profile files to commit")),
		mcp.WithString("remote_url", mcp.Required(), mcp.Description("HTTPS remote URL of the pre-created empty repo")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Initial commit message")),
	), gitInitHandler)

	// get_auth_status - check if credentials are configured
	s.AddTool(mcp.NewTool("get_auth_status",
		mcp.WithDescription("Check whether a PAT is available for the given host, and report its source: the environment (CORTEX_GIT_* variables) or the keychain/file credential store"),
		mcp.WithString("host", mcp.Required(), mcp.Description("Git host (e.g. gitlab.com, github.com)")),
	), getAuthStatusHandler)

	// set_credentials - store a PAT in the credential store
	s.AddTool(mcp.NewTool("set_credentials",
		mcp.WithDescription("Store a Personal Access Token for the given host (OS keychain, or encrypted-file fallback on headless platforms)"),
		mcp.WithString("host", mcp.Required(), mcp.Description("Git host (e.g. gitlab.com, github.com)")),
		mcp.WithString("username", mcp.Required(), mcp.Description("Git username")),
		mcp.WithString("token", mcp.Required(), mcp.Description("Personal Access Token (write access to repo)")),
	), setCredentialsHandler)

	// delete_credentials - remove a stored PAT (e.g. to rotate a token)
	s.AddTool(mcp.NewTool("delete_credentials",
		mcp.WithDescription("Remove the stored PAT for the given host, e.g. to rotate a token. Succeeds even if none was stored."),
		mcp.WithString("host", mcp.Required(), mcp.Description("Git host (e.g. gitlab.com, github.com)")),
	), deleteCredentialsHandler)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// defaultGitTimeout bounds any single git network operation (clone, push, pull,
// and the fetch inside init). This is the one place to tune the limit: every
// network handler derives its per-operation deadline from gitOpContext. It is
// generous on purpose so a first clone/init of a large profile over a slow link
// does not trip it, while still stopping a wedged socket from hanging a tool
// call indefinitely. Override at runtime with CORTEX_GIT_TIMEOUT.
const defaultGitTimeout = 2 * time.Minute

// gitOpTimeout returns the configured git network timeout: CORTEX_GIT_TIMEOUT if
// it parses as a positive Go duration (e.g. "5m", "90s"), otherwise
// defaultGitTimeout. An unparseable or non-positive value is ignored with a note
// on stderr rather than failing the operation.
func gitOpTimeout() time.Duration {
	if v := os.Getenv("CORTEX_GIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "cortex-git: ignoring invalid CORTEX_GIT_TIMEOUT %q; using default %s\n", v, defaultGitTimeout)
	}
	return defaultGitTimeout
}

// gitOpContext derives a per-operation context from the request context, bounded
// by gitOpTimeout. The caller must call the returned cancel func when the
// operation completes (defer cancel()).
func gitOpContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, gitOpTimeout())
}

// stringArg returns the named string argument, or "" if absent. mcp-go
// v0.18.0's handleToolCall does not itself enforce mcp.Required() (verified) -
// a caller can omit a "required" argument and every handler still sees "" for
// it. Path arguments (confinedPathArg), remote_url (RequireHTTPS), and host
// (hostcanon.Canonicalize, reached via resolveCreds/keychain) already reject ""
// downstream, so plain stringArg is fine for those. Anything else that must
// not silently proceed on "" should go through requireStringArg instead.
func stringArg(req mcp.CallToolRequest, name string) string {
	v, _ := req.Params.Arguments[name].(string)
	return v
}

// requireStringArg returns the named string argument, or a ready-to-return MCP
// error result if it is absent or empty (mirroring confinedPathArg/
// resolveCreds). Use this for a required string arg that has no other
// downstream validation - e.g. a commit message, or set_credentials'
// username/token - where an empty value would otherwise silently proceed (an
// empty-message commit; storing an empty token that clobbers a working one)
// instead of failing loudly.
func requireStringArg(req mcp.CallToolRequest, name string) (string, *mcp.CallToolResult) {
	v := stringArg(req, name)
	// TrimSpace only for the emptiness check, not the returned value: a
	// whitespace-only message/username/token (" ", "\n") must not sail through
	// as "non-empty" - for set_credentials that would silently store a
	// whitespace credential, clobbering a working one with no error, exactly
	// the failure mode this helper exists to close. Returning v unmodified (not
	// the trimmed form) avoids rewriting real content that merely has
	// incidental leading/trailing whitespace as part of it.
	if strings.TrimSpace(v) == "" {
		return "", mcp.NewToolResultError(fmt.Sprintf("%s is required", name))
	}
	return v, nil
}

// boolArg returns the named boolean argument, or false if absent or not a bool.
func boolArg(req mcp.CallToolRequest, name string) bool {
	v, _ := req.Params.Arguments[name].(bool)
	return v
}

// resolveCreds looks up credentials for host: environment-injected
// credentials (CORTEX_GIT_*, see envcreds.go) take precedence, then the
// credential store. If none are found it returns a ready-to-return MCP error
// result; callers return it as-is. A backend failure (locked keyring,
// decryption error) is surfaced verbatim rather than being misreported as a
// missing PAT.
func resolveCreds(host string) (username, token string, errResult *mcp.CallToolResult) {
	if username, token, ok := envCredentials(host); ok {
		return username, token, nil
	}
	username, token, err := keychain.GetCredentials(host)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return "", "", mcp.NewToolResultError(
				fmt.Sprintf("no credentials found for %s - run set_credentials first, or set CORTEX_GIT_HOST/CORTEX_GIT_TOKEN in the server environment", host))
		}
		return "", "", mcp.NewToolResultError(
			fmt.Sprintf("could not read credentials for %s: %v", host, err))
	}
	return username, token, nil
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func gitStatusHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repoPath, errResult := confinedPathArg(req, "repo_path")
	if errResult != nil {
		return errResult, nil
	}
	status, err := igit.Status(repoPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(status), nil
}

func gitCommitPushHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repoPath, errResult := confinedPathArg(req, "repo_path")
	if errResult != nil {
		return errResult, nil
	}
	message, errResult := requireStringArg(req, "message")
	if errResult != nil {
		return errResult, nil
	}

	host, err := igit.RemoteHost(repoPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("could not determine remote host: %v", err)), nil
	}
	username, token, errResult := resolveCreds(host)
	if errResult != nil {
		return errResult, nil
	}

	ctx, cancel := gitOpContext(ctx)
	defer cancel()
	result, err := igit.CommitAndPush(ctx, repoPath, message, username, token)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(result), nil
}

func gitPullHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repoPath, errResult := confinedPathArg(req, "repo_path")
	if errResult != nil {
		return errResult, nil
	}

	host, err := igit.RemoteHost(repoPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("could not determine remote host: %v", err)), nil
	}
	username, token, errResult := resolveCreds(host)
	if errResult != nil {
		return errResult, nil
	}

	ctx, cancel := gitOpContext(ctx)
	defer cancel()
	result, err := igit.Pull(ctx, repoPath, username, token, boolArg(req, "force"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(result), nil
}

func gitCloneHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	remoteURL := stringArg(req, "remote_url")

	host, err := igit.RequireHTTPS(remoteURL)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	localPath, errResult := confinedPathArg(req, "local_path")
	if errResult != nil {
		return errResult, nil
	}
	username, token, errResult := resolveCreds(host)
	if errResult != nil {
		return errResult, nil
	}

	ctx, cancel := gitOpContext(ctx)
	defer cancel()
	result, err := igit.Clone(ctx, remoteURL, localPath, username, token)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(result), nil
}

func gitInitHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	remoteURL := stringArg(req, "remote_url")

	host, err := igit.RequireHTTPS(remoteURL)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	localPath, errResult := confinedPathArg(req, "local_path")
	if errResult != nil {
		return errResult, nil
	}
	message, errResult := requireStringArg(req, "message")
	if errResult != nil {
		return errResult, nil
	}
	username, token, errResult := resolveCreds(host)
	if errResult != nil {
		return errResult, nil
	}

	ctx, cancel := gitOpContext(ctx)
	defer cancel()
	result, err := igit.InitAndPush(ctx, localPath, remoteURL, message, username, token)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(result), nil
}

func getAuthStatusHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host := stringArg(req, "host")
	if username, _, ok := envCredentials(host); ok {
		return mcp.NewToolResultText(fmt.Sprintf("credentials found for %s (user: %s, source: env)", host, username)), nil
	}
	backend := keychain.Backend()
	username, _, err := keychain.GetCredentials(host)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return mcp.NewToolResultText(fmt.Sprintf("no credentials stored for %s (backend: %s)", host, backend)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("could not read credentials for %s (backend: %s): %v", host, backend, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("credentials found for %s (user: %s, backend: %s)", host, username, backend)), nil
}

func setCredentialsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host := stringArg(req, "host")
	username, errResult := requireStringArg(req, "username")
	if errResult != nil {
		return errResult, nil
	}
	token, errResult := requireStringArg(req, "token")
	if errResult != nil {
		return errResult, nil
	}
	if err := keychain.SetCredentials(host, username, token); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to store credentials: %v", err)), nil
	}
	msg := fmt.Sprintf("credentials stored for %s (backend: %s)", host, keychain.Backend())
	if _, _, ok := envCredentials(host); ok {
		msg += " - note: CORTEX_GIT_TOKEN is set in the server environment and takes precedence for this host"
	}
	return mcp.NewToolResultText(msg), nil
}

func deleteCredentialsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host := stringArg(req, "host")
	if err := keychain.DeleteCredentials(host); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete credentials: %v", err)), nil
	}
	msg := fmt.Sprintf("credentials removed for %s", host)
	if _, _, ok := envCredentials(host); ok {
		msg += " - note: CORTEX_GIT_TOKEN is still set in the server environment and remains active for this host"
	}
	return mcp.NewToolResultText(msg), nil
}
