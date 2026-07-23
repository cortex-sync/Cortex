package hostcanon

import "testing"

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
	}{
		{"already canonical", "gitlab.com", "gitlab.com"},
		{"mixed case", "GitLab.com", "gitlab.com"},
		{"all caps", "GITLAB.COM", "gitlab.com"},
		{"trailing FQDN dot", "gitlab.com.", "gitlab.com"},
		{"explicit port", "gitlab.com:8443", "gitlab.com"},
		{"port and case together", "GitLab.com:443", "gitlab.com"},
		{"surrounding whitespace", "  gitlab.com  ", "gitlab.com"},
		{"port and trailing dot together", "gitlab.com.:8443", "gitlab.com"},
		{"bare hostname, no dots", "localhost", "localhost"},
		{"bare hostname with port", "localhost:1234", "localhost"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Canonicalize(c.host)
			if err != nil {
				t.Fatalf("Canonicalize(%q): unexpected error: %v", c.host, err)
			}
			if got != c.want {
				t.Fatalf("Canonicalize(%q) = %q, want %q", c.host, got, c.want)
			}
		})
	}
}

// TestCanonicalizeIDNAEquivalence covers the IDNA half of normalisation: a
// Unicode domain and its punycode encoding are two representations of the
// exact same DNS name, so they must canonicalise identically - a credential
// stored via one form must resolve when the remote is later read back in the
// other.
func TestCanonicalizeIDNAEquivalence(t *testing.T) {
	unicode := "münchen.de"
	punycode := "xn--mnchen-3ya.de"

	gotUnicode, err := Canonicalize(unicode)
	if err != nil {
		t.Fatalf("Canonicalize(%q): unexpected error: %v", unicode, err)
	}
	gotPunycode, err := Canonicalize(punycode)
	if err != nil {
		t.Fatalf("Canonicalize(%q): unexpected error: %v", punycode, err)
	}
	if gotUnicode != gotPunycode {
		t.Fatalf("Canonicalize(%q) = %q, Canonicalize(%q) = %q, want them equal", unicode, gotUnicode, punycode, gotPunycode)
	}
	if gotUnicode != punycode {
		t.Fatalf("Canonicalize(%q) = %q, want the punycode form %q", unicode, gotUnicode, punycode)
	}
}

// TestCanonicalizeHomoglyphIsDistinct is a security regression guard: a
// Cyrillic homoglyph of a Latin domain is a genuinely different DNS name, not
// an alternate spelling of the real one, so it must canonicalise to a
// different (punycode) string - never collide with the real domain's
// canonical form. Otherwise credentials scoped to the real host could be
// offered to a lookalike.
func TestCanonicalizeHomoglyphIsDistinct(t *testing.T) {
	real := "apple.com"
	homoglyph := "аpple.com" // Cyrillic 'а' (U+0430) standing in for 'a'

	gotReal, err := Canonicalize(real)
	if err != nil {
		t.Fatalf("Canonicalize(%q): unexpected error: %v", real, err)
	}
	gotHomoglyph, err := Canonicalize(homoglyph)
	if err != nil {
		t.Fatalf("Canonicalize(%q): unexpected error: %v", homoglyph, err)
	}
	if gotReal == gotHomoglyph {
		t.Fatalf("Canonicalize(%q) = Canonicalize(%q) = %q, want distinct canonical forms (must not collide)", real, homoglyph, gotReal)
	}
}

func TestCanonicalizeRejects(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"dot only", "."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := Canonicalize(c.host); err == nil {
				t.Fatalf("Canonicalize(%q) = %q, want error", c.host, got)
			}
		})
	}
}
