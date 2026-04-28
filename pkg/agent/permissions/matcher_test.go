// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import "testing"

func TestMatchPrefix(t *testing.T) {
	cases := []struct {
		cmd, prefix string
		want        bool
	}{
		// Exact match
		{"git", "git", true},
		{"git", "prefix:git", true}, // tolerate prefix: marker
		// Real prefix
		{"git status", "git", true},
		{"git push -f origin main", "git", true},
		{"git push", "git push", true},
		{"git push -f", "git push", true},
		// Different command sharing letters
		{"git-imerge", "git", false},
		{"github-cli", "git", false},
		// Empty / whitespace
		{"", "git", false},
		{"git", "", false},
		{"  git status  ", "git", true}, // trim ws
		// Anchoring at the start matters — substring matches don't
		{"echo git status", "git", false},
	}
	for _, tc := range cases {
		got := MatchPrefix(tc.cmd, tc.prefix)
		if got != tc.want {
			t.Errorf("MatchPrefix(%q, %q) = %v want %v", tc.cmd, tc.prefix, got, tc.want)
		}
	}
}

func TestMatchExact(t *testing.T) {
	if !MatchExact("foo", "foo") {
		t.Errorf("exact match failed")
	}
	if MatchExact("foo", "bar") {
		t.Errorf("non-match returned true")
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		path, pattern string
		want          bool
	}{
		// Bare ** under a directory
		{"/Users/me/repo", "/Users/me/repo/**", true},        // dir itself
		{"/Users/me/repo/a.go", "/Users/me/repo/**", true},
		{"/Users/me/repo/sub/dir/x", "/Users/me/repo/**", true},
		{"/Users/other/repo/a.go", "/Users/me/repo/**", false},

		// **/ at the start: matches at any depth
		{"/foo/bar/.env", "**/.env", true},
		{"/.env", "**/.env", true},
		{"/foo/.env.local", "**/.env", false}, // .env.local != .env
		{"/foo/bar/.env.local", "**/.env*", true},
		{"/foo/bar/credentials.json", "**/credentials*", true},
		{"/foo/bar/my-credentials", "**/credentials*", false}, // pattern is prefix-based here

		// Single * — non-/ chars only
		{"/foo/bar.go", "/foo/*.go", true},
		{"/foo/bar/baz.go", "/foo/*.go", false}, // single * doesn't span /
		{"/foo/bar/baz.go", "/foo/**/*.go", true},

		// ? — single non-/ char
		{"/foo/a.go", "/foo/?.go", true},
		{"/foo/ab.go", "/foo/?.go", false},
		{"/foo/a/b.go", "/foo/?.go", false},

		// Edge: empty path / empty pattern
		{"", "/foo/**", false},
		{"/foo", "", false},
	}
	for _, tc := range cases {
		got := MatchGlob(tc.path, tc.pattern)
		if got != tc.want {
			t.Errorf("MatchGlob(%q, %q) = %v want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

func TestMatchGlob_CacheReuse(t *testing.T) {
	// Hit the same pattern twice; second call should reuse the
	// compiled regex. Verifies the cache doesn't crash on repeats —
	// observable behavior is just "result is consistent."
	const pat = "/foo/**/*.go"
	got1 := MatchGlob("/foo/bar/baz.go", pat)
	got2 := MatchGlob("/foo/bar/baz.go", pat)
	if got1 != got2 {
		t.Errorf("cache made result inconsistent: %v vs %v", got1, got2)
	}
	if !got1 {
		t.Errorf("expected match")
	}
}
