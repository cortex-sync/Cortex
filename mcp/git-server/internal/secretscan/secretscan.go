// Package secretscan provides a lightweight, dependency-free content scan for
// high-signal secrets. It backs the server-side commit gate: changed files are
// scanned before a commit is created so a credential pasted into a memory
// file's body never reaches the remote.
//
// The ruleset is deliberately curated for low false positives rather than
// exhaustive coverage - the threat model is an accidental paste of a real
// credential into a profile/memory file, not adversarial obfuscation. Users who
// want a comprehensive ruleset can layer an optional gitleaks pre-commit hook on
// top (see the repository SECURITY.md); this in-server gate is the always-on
// backstop that needs no external tooling.
package secretscan

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	// maxFileSize caps how much of any single file is read. Profile and memory
	// files are small; a file larger than this cannot be fully scanned, so the
	// gate fails closed and blocks it rather than waving the unread tail through.
	maxFileSize = 5 << 20 // 5 MiB
	// maxLineLen bounds the scanner's per-line buffer. Base64-encoded keys can be
	// long, so allow generous lines without risking unbounded memory.
	maxLineLen = 1 << 20 // 1 MiB
	// binarySniffLen is how much of a file's head is inspected for a NUL byte when
	// deciding if it is binary, mirroring git's own heuristic. A NUL within this
	// window marks the file binary; a stray NUL further in does not, so a
	// text-dominated file is still scanned rather than skipped.
	binarySniffLen = 8000
)

// Synthetic rule names for files the gate cannot content-scan. They are reported
// like any other finding so an unscannable file fails the commit closed rather
// than slipping through unverified.
const (
	ruleUnscannableBinary = "unscannable-binary"
	ruleUnscannableLarge  = "unscannable-too-large"
)

// rule pairs a stable, human-readable name with a compiled detection pattern.
// The name is reported in findings; the matched text never is.
type rule struct {
	name    string
	pattern *regexp.Regexp
}

// rules is the curated high-signal ruleset. Each pattern targets a credential
// shape distinctive enough to keep false positives low on prose and config.
var rules = []rule{
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	// The 40-char AWS secret access key has no distinctive prefix, so it is keyed
	// on the conventional assignment context to stay high-signal. The key ID rule
	// above covers the other half of the pair.
	{"aws-secret-access-key", regexp.MustCompile(`(?i)aws_?secret_?access_?key["']?\s*[:=]\s*["']?[A-Za-z0-9/+]{40}`)},
	// Azure storage / Service Bus keys: a base64 value assigned to AccountKey or
	// SharedAccessKey, as it appears in a connection string.
	{"azure-storage-key", regexp.MustCompile(`(?i)(?:Account|SharedAccess)Key\s*=\s*["']?[A-Za-z0-9/+]{40,}={0,2}`)},
	{"private-key-block", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY-----`)},
	{"gitlab-pat", regexp.MustCompile(`\bglpat-[0-9A-Za-z_-]{20,}`)},
	{"github-pat", regexp.MustCompile(`\b(?:gh[pousr]_[0-9A-Za-z]{36,}|github_pat_[0-9A-Za-z_]{22,})`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	// Quoted OR unquoted value: the unquoted arm catches the dominant .env / shell
	// / YAML shapes (FOO_SECRET=..., client_secret: ...). The keyword set uses
	// compound identifiers (api_key, client_secret), which prose writes with
	// spaces, keeping false positives on memory text low.
	{"generic-secret-assignment", regexp.MustCompile(`(?i)(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)["']?\s*[:=]\s*(?:["'][^"'\n]{8,}["']|[^\s"'\n]{8,})`)},
}

// Finding records one rule match within a file. It deliberately omits the
// matched text so the secret is never echoed back through logs or tool output -
// only its location and the rule that flagged it.
type Finding struct {
	Path string // path relative to the scan root
	Rule string // name of the rule that matched
	Line int    // 1-based line number of the match
}

// String renders a finding as "path:line (rule)", or "path (rule)" for a
// whole-file finding with no line (an unscannable file).
func (f Finding) String() string {
	if f.Line <= 0 {
		return fmt.Sprintf("%s (%s)", f.Path, f.Rule)
	}
	return fmt.Sprintf("%s:%d (%s)", f.Path, f.Line, f.Rule)
}

// BlockedError is returned by callers when a commit is refused because changed
// files contain likely secrets or could not be scanned. Its message is
// actionable and never contains a secret value, so it is safe to surface
// directly to the user.
type BlockedError struct {
	Findings []Finding
}

// Error renders an actionable, secret-free summary of every finding.
func (e *BlockedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to commit: %d secret-scan finding(s). "+
		"Remove the secret, or add the path to .gitignore if it is intentional, then retry:", len(e.Findings))
	for _, f := range e.Findings {
		fmt.Fprintf(&b, "\n  - %s", f.String())
	}
	return b.String()
}

// ScanFiles scans each of paths (relative to root) for secrets and returns all
// findings, ordered by path then line for stable output. Missing files (e.g.
// staged deletions) and directories carry no committed content and are skipped.
// Files that cannot be content-scanned - oversized or binary - fail closed: they
// yield a whole-file finding so the commit is blocked rather than letting unread
// content through. An error is returned only for unexpected I/O failures.
func ScanFiles(root string, paths []string) ([]Finding, error) {
	// Confine all file access beneath root with os.Root: any path that tries to
	// escape the scan root - via a "../" segment or a symlink pointing outside -
	// is rejected by the kernel rather than silently followed. Defence in depth
	// for a security-sensitive scanner reading caller-supplied relative paths.
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("opening scan root %s: %w", root, err)
	}
	defer func() { _ = r.Close() }() // read-only root; close error is immaterial

	var findings []Finding
	for _, p := range paths {
		fileFindings, err := scanFile(r, p)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// scanFile scans a single file, reporting at most one finding per rule (the
// first match) to keep output concise. Files that do not exist (staged
// deletions) or are directories yield no findings and no error. Files that
// cannot be scanned - larger than maxFileSize, or binary (a NUL byte in the
// first binarySniffLen bytes) - fail closed, returning a single whole-file
// finding so the caller blocks the commit.
func scanFile(root *os.Root, relPath string) ([]Finding, error) {
	info, err := root.Stat(relPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // deleted in the worktree; nothing to scan
		}
		return nil, fmt.Errorf("stat %s: %w", relPath, err)
	}
	if info.IsDir() {
		return nil, nil // a directory carries no content of its own
	}
	if info.Size() > maxFileSize {
		// Too large to read in full: a secret could hide beyond the scan window,
		// so block rather than scan a prefix and wave the rest through.
		return []Finding{{Path: relPath, Rule: ruleUnscannableLarge}}, nil
	}

	f, err := root.Open(relPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", relPath, err)
	}
	defer func() { _ = f.Close() }() // read-only file; close error is immaterial

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relPath, err)
	}
	sniff := data
	if len(sniff) > binarySniffLen {
		sniff = sniff[:binarySniffLen]
	}
	if bytes.IndexByte(sniff, 0) != -1 {
		// Binary content cannot be meaningfully scanned for text secrets: block.
		return []Finding{{Path: relPath, Rule: ruleUnscannableBinary}}, nil
	}

	var findings []Finding
	seen := make(map[string]bool, len(rules))
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxLineLen)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		for _, r := range rules {
			if seen[r.name] {
				continue
			}
			if r.pattern.Match(line) {
				findings = append(findings, Finding{Path: relPath, Rule: r.name, Line: lineNo})
				seen[r.name] = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", relPath, err)
	}
	return findings, nil
}
