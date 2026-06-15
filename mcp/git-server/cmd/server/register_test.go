package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// TestServerRegistersAllTools drives a real tools/list request through the
// server in-process (no subprocess) and asserts every Cortex tool is
// advertised. This exercises registerTools and the list path directly, rather
// than only via the out-of-process stdio smoke test.
func TestServerRegistersAllTools(t *testing.T) {
	s := server.NewMCPServer(serverName, version)
	registerTools(s)

	req := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp := s.HandleMessage(context.Background(), req)
	if resp == nil {
		t.Fatal("tools/list returned a nil response")
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshalling tools/list response: %v", err)
	}
	got := string(b)

	for _, name := range []string{
		"git_status", "git_commit_push", "git_pull", "git_clone", "git_init",
		"get_auth_status", "set_credentials", "delete_credentials",
	} {
		if !strings.Contains(got, `"`+name+`"`) {
			t.Errorf("tools/list response is missing tool %q", name)
		}
	}
}
