//go:build e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestE2ESyncRoundTrip drives the real cortex-git binary through the full
// profile-sync lifecycle against a live Gitea instance over HTTPS with PAT
// auth. It is gated behind the `e2e` build tag and the e2e/run.sh harness,
// which provisions Gitea and exports the environment below. Run via `make e2e`.
//
// Unlike the git package's TestSyncRoundTrip (which uses go-git's local
// filesystem transport), this exercises the real HTTPS + BasicAuth network
// path and the RequireHTTPS guard, end to end through the MCP stdio protocol.
//
// Environment (set by e2e/run.sh):
//
//	E2E_REMOTE_URL  https clone URL of a freshly created EMPTY repo
//	E2E_HOST        host key credentials are stored under (the origin hostname,
//	                as resolved by RequireHTTPS/url.Hostname())
//	E2E_USERNAME    Gitea username
//	E2E_TOKEN       Gitea personal access token
//	SSL_CERT_FILE   path to the self-signed cert, trusted by the subprocess
func TestE2ESyncRoundTrip(t *testing.T) {
	remoteURL := os.Getenv("E2E_REMOTE_URL")
	host := os.Getenv("E2E_HOST")
	username := os.Getenv("E2E_USERNAME")
	token := os.Getenv("E2E_TOKEN")
	caFile := os.Getenv("SSL_CERT_FILE")
	if remoteURL == "" || host == "" || username == "" || token == "" || caFile == "" {
		t.Skip("e2e env not set; run via e2e/run.sh (make e2e)")
	}

	bin := buildServer(t)
	cfgDir := t.TempDir()
	// All device working trees live under one root so the subprocess can confine
	// path arguments to it via CORTEX_REPO_ROOT (the default home-dir root would
	// reject these temp paths).
	workRoot := t.TempDir()

	// The subprocess trusts the self-signed cert via SSL_CERT_FILE and pins its
	// credential store to an isolated dir via CORTEX_CONFIG_DIR, which forces
	// the file backend on every platform (XDG_CONFIG_HOME isolation only worked
	// on Linux, and a usable OS keychain would have won anywhere else).
	c, err := client.NewStdioMCPClient(bin, []string{
		"SSL_CERT_FILE=" + caFile,
		"CORTEX_CONFIG_DIR=" + cfgDir,
		"CORTEX_REPO_ROOT=" + workRoot,
	})
	if err != nil {
		t.Fatalf("starting stdio client: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "cortex-e2e", Version: "test"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	callTool := func(name string, args map[string]interface{}) *mcp.CallToolResult {
		t.Helper()
		var req mcp.CallToolRequest
		req.Params.Name = name
		req.Params.Arguments = args
		res, err := c.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("CallTool %s: transport error: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("CallTool %s: tool error: %s", name, resultText(t, res))
		}
		return res
	}

	// 1. Store the PAT for the host.
	callTool("set_credentials", map[string]interface{}{
		"host": host, "username": username, "token": token,
	})

	// 2. get_auth_status should now report the credentials present.
	status := resultText(t, callTool("get_auth_status", map[string]interface{}{"host": host}))
	if !strings.Contains(status, "credentials found") {
		t.Fatalf("get_auth_status = %q, want 'credentials found'", status)
	}

	// 3. Device A: write a profile file, init the repo, push to the empty remote.
	deviceA := filepath.Join(workRoot, "deviceA")
	if err := os.MkdirAll(deviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deviceA, "CLAUDE.md"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	callTool("git_init", map[string]interface{}{
		"local_path": deviceA, "remote_url": remoteURL, "message": "cortex: initial",
	})

	// 4. Device B: clone and confirm the file arrived over HTTPS.
	deviceB := filepath.Join(workRoot, "deviceB")
	callTool("git_clone", map[string]interface{}{
		"remote_url": remoteURL, "local_path": deviceB,
	})
	if got := readE2EFile(t, deviceB, "CLAUDE.md"); got != "v1\n" {
		t.Fatalf("cloned CLAUDE.md = %q, want v1", got)
	}

	// 5. Device B: change the file, commit and push.
	if err := os.WriteFile(filepath.Join(deviceB, "CLAUDE.md"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	callTool("git_commit_push", map[string]interface{}{
		"repo_path": deviceB, "message": "cortex: update",
	})

	// 6. Device A: pull and confirm it now sees the update.
	callTool("git_pull", map[string]interface{}{"repo_path": deviceA})
	if got := readE2EFile(t, deviceA, "CLAUDE.md"); got != "v2\n" {
		t.Fatalf("after pull CLAUDE.md = %q, want v2", got)
	}
}

func readE2EFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
