package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zalando/go-keyring"

	"github.com/cortex-sync/Cortex/mcp/git-server/internal/keychain"
)

// TestMain routes every credential operation in this package through
// go-keyring's in-memory mock, so the suite never touches a real OS keychain or
// the on-disk encrypted store. The subprocess stdio test launches a separately
// compiled binary that cannot see this mock; it is isolated via
// CORTEX_CONFIG_DIR instead, which pins the file backend deterministically on
// every platform (see TestStdioServerSmoke).
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

type handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

// call invokes a handler with the given string arguments and returns the
// result, failing the test on a transport-level error or a nil result.
func call(t *testing.T, h handler, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if res == nil {
		t.Fatal("handler returned nil result")
	}
	return res
}

// resultText concatenates the text content of a tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestResolveCredsMissingVsFound checks the branch added so a genuinely missing
// PAT is reported distinctly, and a stored credential resolves cleanly.
func TestResolveCredsMissingVsFound(t *testing.T) {
	const host = "resolve.example"

	_, _, errRes := resolveCreds(host)
	if errRes == nil {
		t.Fatal("resolveCreds on unset host: want an error result, got nil")
	}
	if txt := resultText(t, errRes); !strings.Contains(txt, "run set_credentials first") {
		t.Fatalf("missing-creds message = %q, want it to mention set_credentials", txt)
	}

	if err := keychain.SetCredentials(host, "alice", "tok-123"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	t.Cleanup(func() { _ = keychain.DeleteCredentials(host) })

	user, token, errRes := resolveCreds(host)
	if errRes != nil {
		t.Fatalf("resolveCreds after set: unexpected error result %q", resultText(t, errRes))
	}
	if user != "alice" || token != "tok-123" {
		t.Fatalf("resolveCreds = (%q, %q), want (alice, tok-123)", user, token)
	}
}

// TestGetAuthStatusHandler checks both branches of the status handler.
func TestGetAuthStatusHandler(t *testing.T) {
	res := call(t, getAuthStatusHandler, map[string]interface{}{"host": "noauth.example"})
	if txt := resultText(t, res); !strings.Contains(txt, "no credentials stored") {
		t.Fatalf("unset-host status = %q, want 'no credentials stored'", txt)
	}

	const host = "auth.example"
	if err := keychain.SetCredentials(host, "bob", "tok-xyz"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	t.Cleanup(func() { _ = keychain.DeleteCredentials(host) })

	res = call(t, getAuthStatusHandler, map[string]interface{}{"host": host})
	txt := resultText(t, res)
	if !strings.Contains(txt, "credentials found") || !strings.Contains(txt, "bob") {
		t.Fatalf("set-host status = %q, want 'credentials found' and user 'bob'", txt)
	}
}

// TestDeleteCredentialsHandler checks the delete tool removes a stored PAT and
// is idempotent (deleting an absent host still succeeds, as rotation needs).
func TestDeleteCredentialsHandler(t *testing.T) {
	const host = "delete.example"
	if err := keychain.SetCredentials(host, "carol", "tok-del"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}

	res := call(t, deleteCredentialsHandler, map[string]interface{}{"host": host})
	if res.IsError {
		t.Fatalf("delete_credentials: unexpected error %q", resultText(t, res))
	}
	if txt := resultText(t, res); !strings.Contains(txt, "removed") {
		t.Fatalf("delete result = %q, want 'removed'", txt)
	}

	status := resultText(t, call(t, getAuthStatusHandler, map[string]interface{}{"host": host}))
	if !strings.Contains(status, "no credentials stored") {
		t.Fatalf("after delete, status = %q, want 'no credentials stored'", status)
	}

	// Idempotent: deleting an already-absent host still succeeds.
	if res := call(t, deleteCredentialsHandler, map[string]interface{}{"host": host}); res.IsError {
		t.Fatalf("second delete: unexpected error %q", resultText(t, res))
	}
}

// TestGitOpTimeout checks the single tunable git timeout: the default applies
// with no override, a valid CORTEX_GIT_TIMEOUT wins, and an invalid or
// non-positive value is ignored in favour of the default.
func TestGitOpTimeout(t *testing.T) {
	if got := gitOpTimeout(); got != defaultGitTimeout {
		t.Fatalf("gitOpTimeout() with no override = %s, want default %s", got, defaultGitTimeout)
	}

	t.Setenv("CORTEX_GIT_TIMEOUT", "45s")
	if got := gitOpTimeout(); got != 45*time.Second {
		t.Fatalf("gitOpTimeout() = %s, want 45s", got)
	}

	for _, bad := range []string{"not-a-duration", "0", "-5m"} {
		t.Setenv("CORTEX_GIT_TIMEOUT", bad)
		if got := gitOpTimeout(); got != defaultGitTimeout {
			t.Fatalf("gitOpTimeout() with %q = %s, want default %s", bad, got, defaultGitTimeout)
		}
	}
}

// TestGitOpContextHasDeadline confirms gitOpContext bounds the request context
// with a deadline derived from the configured timeout.
func TestGitOpContextHasDeadline(t *testing.T) {
	ctx, cancel := gitOpContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("gitOpContext returned a context with no deadline")
	}
}

// TestHTTPSGuardRejectsInsecureURLs confirms the clone and init handlers reject
// a non-https remote before any network or credential access.
func TestHTTPSGuardRejectsInsecureURLs(t *testing.T) {
	cases := []struct {
		name string
		h    handler
		args map[string]interface{}
	}{
		{"clone", gitCloneHandler, map[string]interface{}{
			"remote_url": "http://gitlab.com/u/r.git",
			"local_path": t.TempDir(),
		}},
		{"init", gitInitHandler, map[string]interface{}{
			"local_path": t.TempDir(),
			"remote_url": "http://gitlab.com/u/r.git",
			"message":    "initial",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, tc.h, tc.args)
			if !res.IsError {
				t.Fatalf("%s with http URL: want IsError, got success: %q", tc.name, resultText(t, res))
			}
			if txt := resultText(t, res); !strings.Contains(txt, "https") {
				t.Fatalf("%s rejection = %q, want it to mention https", tc.name, txt)
			}
		})
	}
}

// TestGitStatusHandler exercises the handler -> git package wiring and its
// error mapping, without re-testing git logic covered in the git package.
func TestGitStatusHandler(t *testing.T) {
	allowTempPaths(t)
	res := call(t, gitStatusHandler, map[string]interface{}{
		"repo_path": filepath.Join(t.TempDir(), "not-a-repo"),
	})
	if !res.IsError {
		t.Fatalf("git_status on non-repo: want IsError, got %q", resultText(t, res))
	}

	repo := t.TempDir()
	if _, err := gogit.PlainInit(repo, false); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	res = call(t, gitStatusHandler, map[string]interface{}{"repo_path": repo})
	if res.IsError {
		t.Fatalf("git_status on clean repo: unexpected error %q", resultText(t, res))
	}
	if txt := resultText(t, res); !strings.Contains(txt, "nothing to commit") {
		t.Fatalf("clean-repo status = %q, want 'nothing to commit'", txt)
	}
}

// buildServer compiles the cortex-git binary to a temp path and returns it,
// skipping the test if no Go toolchain is available. Shared by the stdio smoke
// test and the e2e suite.
func buildServer(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not found; skipping")
	}
	bin := filepath.Join(t.TempDir(), "cortex-git")
	build := exec.Command(goBin, "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building server: %v\n%s", err, out)
	}
	return bin
}

// TestStdioServerSmoke compiles the server and drives it over a real stdio
// MCP session: it confirms tool registration and that an error travels back
// correctly through the protocol. This covers the cmd/server wiring that the
// in-process handler tests above cannot (registration + stdio transport).
func TestStdioServerSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio smoke test in -short mode")
	}
	bin := buildServer(t)

	// Pin the credential store to a temp dir via CORTEX_CONFIG_DIR so the
	// subprocess writes nothing to the developer's real config location or OS
	// keychain - the override forces the file backend on every platform.
	cfgDir := t.TempDir()
	c, err := client.NewStdioMCPClient(bin, []string{"CORTEX_CONFIG_DIR=" + cfgDir})
	if err != nil {
		t.Fatalf("starting stdio client: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "cortex-test", Version: "test"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"git_status": false, "git_commit_push": false, "git_pull": false,
		"git_clone": false, "git_init": false, "get_auth_status": false,
		"set_credentials": false, "delete_credentials": false,
	}
	for _, tl := range tools.Tools {
		if _, ok := want[tl.Name]; ok {
			want[tl.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q not registered over stdio", name)
		}
	}

	var callReq mcp.CallToolRequest
	callReq.Params.Name = "git_clone"
	callReq.Params.Arguments = map[string]interface{}{
		"remote_url": "http://gitlab.com/u/r.git",
		"local_path": filepath.Join(t.TempDir(), "clone"),
	}
	res, err := c.CallTool(ctx, callReq)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("git_clone http URL over stdio: want IsError, got %q", resultText(t, res))
	}
	if txt := resultText(t, res); !strings.Contains(txt, "https") {
		t.Fatalf("stdio rejection = %q, want mention of https", txt)
	}
}
