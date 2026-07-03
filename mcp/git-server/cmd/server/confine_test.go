package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfinePathAccepts(t *testing.T) {
	root := t.TempDir()

	t.Run("existing path inside root", func(t *testing.T) {
		sub := filepath.Join(root, "profile")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := confinePath(root, sub)
		if err != nil {
			t.Fatalf("confinePath: %v", err)
		}
		want, _ := filepath.EvalSymlinks(sub)
		if got != want {
			t.Fatalf("confinePath = %q, want %q", got, want)
		}
	})

	t.Run("not-yet-existing leaf inside root", func(t *testing.T) {
		// git_clone / git_init targets do not exist yet - confinement must still
		// resolve and accept them.
		leaf := filepath.Join(root, "new", "clone-target")
		got, err := confinePath(root, leaf)
		if err != nil {
			t.Fatalf("confinePath: %v", err)
		}
		resolvedRoot, _ := filepath.EvalSymlinks(root)
		if !strings.HasPrefix(got, resolvedRoot) {
			t.Fatalf("confinePath = %q, want it under %q", got, resolvedRoot)
		}
	})

	t.Run("root itself is allowed", func(t *testing.T) {
		if _, err := confinePath(root, root); err != nil {
			t.Fatalf("confinePath(root, root): %v", err)
		}
	})
}

func TestConfinePathRejects(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		name string
		arg  string
	}{
		{"empty", ""},
		{"relative", "profile/memory"},
		{"absolute outside root", t.TempDir()}, // a sibling temp dir, not under root
		{"parent-traversal escape", filepath.Join(root, "..", "escape")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := confinePath(root, c.arg); err == nil {
				t.Fatalf("confinePath(%q) = %q, want error (must fail closed)", c.arg, got)
			}
		})
	}
}

// TestConfinePathRejectsSymlinkEscape confirms a symlink inside the root whose
// target leaves the root cannot be used to reach an outside path.
func TestConfinePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if got, err := confinePath(root, filepath.Join(link, "secret")); err == nil {
		t.Fatalf("confinePath via escaping symlink = %q, want error (must fail closed)", got)
	}
}

func TestRepoRoot(t *testing.T) {
	t.Run("prefers CORTEX_REPO_ROOT", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CORTEX_REPO_ROOT", dir)
		got, err := repoRoot()
		if err != nil {
			t.Fatalf("repoRoot: %v", err)
		}
		want, _ := filepath.Abs(dir)
		if got != want {
			t.Fatalf("repoRoot = %q, want %q", got, want)
		}
	})

	t.Run("falls back to home when unset", func(t *testing.T) {
		t.Setenv("CORTEX_REPO_ROOT", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory on this platform")
		}
		got, err := repoRoot()
		if err != nil {
			t.Fatalf("repoRoot: %v", err)
		}
		if got != home {
			t.Fatalf("repoRoot = %q, want home %q", got, home)
		}
	})
}

// TestGitStatusHandlerConfinesPath is the handler-level regression guard: a path
// argument outside the confinement root is rejected before any repo access.
func TestGitStatusHandlerConfinesPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CORTEX_REPO_ROOT", root)

	res := call(t, gitStatusHandler, map[string]interface{}{
		"repo_path": t.TempDir(), // sibling temp dir, outside root
	})
	if !res.IsError {
		t.Fatalf("git_status outside root: want IsError, got %q", resultText(t, res))
	}
	if txt := resultText(t, res); !strings.Contains(txt, "outside the permitted root") {
		t.Fatalf("error = %q, want it to mention 'outside the permitted root'", txt)
	}
}
