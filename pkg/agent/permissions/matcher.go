// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"regexp"
	"strings"
	"sync"
)

// MatchExact reports whether content equals pattern. Trivial; exists so
// the call site reads consistently with the other matchers.
func MatchExact(content, pattern string) bool {
	return content == pattern
}

// MatchPrefix implements shell-command prefix matching. A pattern of
// `prefix:git` matches "git", "git status", "git push -f", but NOT
// "git-imerge" (because `git-imerge` is a different command, not git
// with an argument). The matching is purely textual — we don't try to
// expand aliases or resolve PATH; a determined user can always tweak
// rules to taste.
//
// Patterns may be passed with or without the leading "prefix:". Both
// forms behave the same; the engine strips the marker before calling.
func MatchPrefix(cmd, prefix string) bool {
	prefix = strings.TrimPrefix(prefix, "prefix:")
	cmd = strings.TrimSpace(cmd)
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	if cmd == prefix {
		return true
	}
	return strings.HasPrefix(cmd, prefix+" ")
}

// MatchGlob implements gitignore-style globbing for absolute paths.
// Supported syntax:
//
//   - `*` — any sequence of non-`/` chars (single segment)
//   - `?` — any single non-`/` char
//   - `**` — any sequence including `/` (zero or more segments)
//   - everything else is literal
//
// Examples:
//
//	"/Users/me/repo/**"     matches /Users/me/repo, /Users/me/repo/a/b
//	"**/*.go"               matches any .go file
//	"**/.env"               matches a .env file at any depth
//	"**/credentials*"       matches credentials, credentials.json, etc.
//	"!**/secrets/**"        — negation NOT supported here; deny rules
//	                          handle that case at the rule level.
//
// Patterns are compiled once and cached; subsequent calls reuse the
// regex. The cache is bounded (see globCacheMaxEntries) to prevent
// unbounded growth from a misbehaving caller — rules normally come
// from settings.json so the set is small, but we don't trust that.
func MatchGlob(path, pattern string) bool {
	if pattern == "" {
		return false
	}
	re, err := compileGlob(pattern)
	if err != nil {
		// Malformed glob — treat as no match. Settings load already
		// validates patterns; this is just defense in depth.
		return false
	}
	return re.MatchString(path)
}

// globCacheMaxEntries caps how many compiled regexes we hold. Rules
// from settings + builtins typically number in the dozens; 1024 is
// generous. On overflow we drop the entire map and rebuild — simpler
// than LRU bookkeeping and matches the access pattern (small stable
// working set, occasional churn from session rules).
const globCacheMaxEntries = 1024

var (
	globCacheMu sync.RWMutex
	globCache   = make(map[string]*regexp.Regexp)
)

func compileGlob(pattern string) (*regexp.Regexp, error) {
	globCacheMu.RLock()
	if re, ok := globCache[pattern]; ok {
		globCacheMu.RUnlock()
		return re, nil
	}
	globCacheMu.RUnlock()

	re, err := globToRegex(pattern)
	if err != nil {
		return nil, err
	}
	globCacheMu.Lock()
	if len(globCache) >= globCacheMaxEntries {
		// Drop everything rather than evict one entry. The next round
		// of decisions will repopulate the working set; the cost of a
		// thousand small regex compilations is sub-millisecond.
		globCache = make(map[string]*regexp.Regexp, globCacheMaxEntries)
	}
	globCache[pattern] = re
	globCacheMu.Unlock()
	return re, nil
}

// globToRegex converts a gitignore-ish glob to an anchored regex. The
// rules:
//
//   - `**` consumes any chars (including `/`).
//   - `*` consumes any chars except `/`.
//   - `?` consumes one char except `/`.
//   - regex metacharacters are escaped except where we're emitting them.
//
// We special-case `/**` so that `dir/**` matches both `dir` itself
// and any descendant (matches gitignore semantics). The special-case
// fires both for trailing `/**` (e.g. `/Users/me/repo/**`) and for
// `/**/` in the middle (e.g. `/repo/**/main.go` matches `/repo/main.go`).
func globToRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')

	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// `**` — match any chars including `/`. Two
				// special cases when preceded by a literal `/`:
				//
				//   "/**/"  → ".*/?" then continue past the trailing `/`
				//   "/**$"  → "(?:/.*)?" (trailing-** matches dir itself)
				//
				// In both cases we strip the `/` we just emitted and
				// re-emit a wrapper that makes the whole `/...` segment
				// optional.
				prevIsSlash := b.Len() > 0 && b.String()[b.Len()-1] == '/'
				atEnd := i+2 == len(pattern)
				followedBySlash := i+2 < len(pattern) && pattern[i+2] == '/'

				if prevIsSlash && (atEnd || followedBySlash) {
					cur := b.String()
					b.Reset()
					b.WriteString(strings.TrimSuffix(cur, "/"))
					if atEnd {
						b.WriteString(`(?:/.*)?`)
						i += 2
					} else {
						// followedBySlash: emit `(?:/.*)?` and skip
						// the `/` after `**` too — the wrapper above
						// already covers it.
						b.WriteString(`(?:/.*)?`)
						i += 3
					}
					continue
				}
				b.WriteString(`.*`)
				i += 2
				continue
			}
			// Single `*` — non-`/` chars.
			b.WriteString(`[^/]*`)
			i++
		case '?':
			b.WriteString(`[^/]`)
			i++
		case '\\':
			// Escape sequence: include literal next char if present.
			if i+1 < len(pattern) {
				b.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
				i += 2
				continue
			}
			b.WriteString(`\\`)
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteByte('$')

	return regexp.Compile(b.String())
}
