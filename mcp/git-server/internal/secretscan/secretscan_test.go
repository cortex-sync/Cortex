package secretscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFilesDetectsSecrets(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantRule string
	}{
		{"aws access key", "deploy uses AKIAIOSFODNN7EXAMPLE for s3\n", "aws-access-key-id"},
		{"aws secret access key", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n", "aws-secret-access-key"},
		{"azure storage key", "AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uPiK6sHWBwQ==\n", "azure-storage-key"},
		{"private key block", "-----BEGIN OPENSSH PRIVATE KEY-----\nbody\n", "private-key-block"},
		{"encrypted private key block", "-----BEGIN ENCRYPTED PRIVATE KEY-----\nbody\n", "private-key-block"},
		{"gitlab pat", "token: glpat-ABCDEFGHIJ1234567890\n", "gitlab-pat"},
		{"github pat", "GH_TOKEN=ghp_0123456789abcdefghijklmnopqrstuvwxyz\n", "github-pat"},
		{"slack token", "slack = xoxb-1234567890-abcdef\n", "slack-token"},
		{"google api key", "key AIzaabcdefghijklmnopqrstuvwxyz012345678 used\n", "google-api-key"},
		{"jwt", "auth eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.dBjftJeZ4CVP\n", "jwt"},
		{"generic assignment quoted", "password = \"hunter2hunter2\"\n", "generic-secret-assignment"},
		{"generic assignment unquoted env", "DB_PASSWORD=hunter2supersecret\n", "generic-secret-assignment"},
		{"generic assignment unquoted yaml", "client_secret: s3cr3tval-unquoted\n", "generic-secret-assignment"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "memory.md", c.content)

			findings, err := ScanFiles(root, []string{"memory.md"})
			if err != nil {
				t.Fatalf("ScanFiles: %v", err)
			}
			if len(findings) != 1 {
				t.Fatalf("findings = %v, want exactly 1", findings)
			}
			if findings[0].Rule != c.wantRule {
				t.Fatalf("rule = %q, want %q", findings[0].Rule, c.wantRule)
			}
			if findings[0].Line != 1 {
				t.Fatalf("line = %d, want 1", findings[0].Line)
			}
			// The matched secret value must never leak into the finding.
			if strings.Contains(findings[0].String(), strings.TrimSpace(c.content)) {
				t.Fatalf("finding %q leaked the secret content", findings[0].String())
			}
		})
	}
}

func TestScanFilesCleanContent(t *testing.T) {
	root := t.TempDir()
	// Prose that name-drops the keywords with spaces (as English does) must not
	// trip the unquoted generic-assignment arm.
	writeFile(t, root, "notes.md", "# Notes\n\nNothing sensitive here. We discussed the api key rotation, "+
		"the client secret policy, and a password reset flow - all just prose about AWS and tokens.\n")

	findings, err := ScanFiles(root, []string{"notes.md"})
	if err != nil {
		t.Fatalf("ScanFiles: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
}

func TestScanFilesSkipsMissingAndDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A staged deletion and a directory carry no committed content, so both are
	// skipped without a finding.
	findings, err := ScanFiles(root, []string{"does-not-exist.md", "subdir"})
	if err != nil {
		t.Fatalf("ScanFiles: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none (missing/dir skipped)", findings)
	}
}

// TestScanFilesFailsClosedOnUnscannable is the regression guard for the M8
// finding: a file the gate cannot read in full must block the commit rather than
// pass unverified.
func TestScanFilesFailsClosedOnUnscannable(t *testing.T) {
	root := t.TempDir()
	// Binary: a NUL in the sniff window. A real secret hidden in binary would
	// otherwise slip through, so the whole file is flagged unscannable.
	writeFile(t, root, "blob.bin", "AKIAIOSFODNN7EXAMPLE\x00\x01\x02")
	// Oversized: larger than maxFileSize, so the tail cannot be read.
	writeFile(t, root, "big.dat", strings.Repeat("a", maxFileSize+1))

	findings, err := ScanFiles(root, []string{"blob.bin", "big.dat"})
	if err != nil {
		t.Fatalf("ScanFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range findings {
		got[f.Path] = f.Rule
	}
	if got["blob.bin"] != ruleUnscannableBinary {
		t.Fatalf("blob.bin rule = %q, want %q", got["blob.bin"], ruleUnscannableBinary)
	}
	if got["big.dat"] != ruleUnscannableLarge {
		t.Fatalf("big.dat rule = %q, want %q", got["big.dat"], ruleUnscannableLarge)
	}
}

// TestScanFilesScansTextDominatedFileWithLateNUL confirms a stray NUL beyond the
// binary sniff window does not cause a text file to be skipped - it is still
// scanned, and a secret after the NUL is caught.
func TestScanFilesScansTextDominatedFileWithLateNUL(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("a", binarySniffLen+1) + "\x00 slack = xoxb-1234567890-abcdef"
	writeFile(t, root, "mostly-text.md", content)

	findings, err := ScanFiles(root, []string{"mostly-text.md"})
	if err != nil {
		t.Fatalf("ScanFiles: %v", err)
	}
	if len(findings) != 1 || findings[0].Rule != "slack-token" {
		t.Fatalf("findings = %v, want one slack-token (file scanned despite late NUL)", findings)
	}
}

func TestScanFilesReportsLineAndPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "creds.md", "line one\nline two\nslack = xoxb-1234567890-abcdef\n")

	findings, err := ScanFiles(root, []string{"creds.md"})
	if err != nil {
		t.Fatalf("ScanFiles: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want 1", findings)
	}
	if got := findings[0]; got.Path != "creds.md" || got.Line != 3 {
		t.Fatalf("finding = %s, want creds.md:3", got)
	}
}

func TestBlockedErrorMessageIsActionableAndSafe(t *testing.T) {
	err := &BlockedError{Findings: []Finding{
		{Path: "memory.md", Rule: "gitlab-pat", Line: 4},
	}}
	msg := err.Error()
	for _, want := range []string{"refusing to commit", "memory.md:4", "gitlab-pat", ".gitignore"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q missing %q", msg, want)
		}
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
