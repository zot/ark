package ark

// CRC: crc-Matcher.md

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Classification is the result of matching a path against include/exclude patterns.
type Classification int

const (
	Included   Classification = iota
	Excluded
	Unresolved
)

// Matcher evaluates doublestar glob patterns against file paths with
// ark-level semantic modifiers (trailing / for directory-only).
type Matcher struct {
	Dotfiles bool // whether * and ** match dotfiles (default true)
}

// Classify determines whether a path is included, excluded, or unresolved
// given a set of include and exclude patterns. Include wins conflicts.
func (m *Matcher) Classify(includes, excludes []string, path string, isDir bool) Classification {
	included := false
	excluded := false
	for _, p := range includes {
		if m.Match(p, path, isDir) {
			included = true
			break
		}
	}
	for _, p := range excludes {
		if m.Match(p, path, isDir) {
			excluded = true
			break
		}
	}
	if included {
		return Included
	}
	if excluded {
		return Excluded
	}
	return Unresolved
}

// Match tests whether a single pattern matches a path.
// isDir indicates whether the path is a directory.
// Trailing / on the pattern means directory-only; otherwise file-only.
func (m *Matcher) Match(pattern, path string, isDir bool) bool {
	dirPattern := strings.HasSuffix(pattern, "/")
	if dirPattern != isDir {
		return false
	}
	if dirPattern {
		pattern = strings.TrimSuffix(pattern, "/")
	}

	anchored := strings.HasPrefix(pattern, "/")
	if anchored {
		pattern = pattern[1:]
		path = strings.TrimPrefix(path, "/")
	} else {
		// Unanchored patterns match at any depth — prepend **/
		pattern = "**/" + pattern
	}

	matched, err := doublestar.Match(pattern, path)
	if err != nil {
		return false
	}
	if !matched {
		return false
	}

	if !m.Dotfiles {
		// Check if a wildcard matched a dotfile component
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") && !hasDotPrefix(pattern) {
			return false
		}
	}
	return true
}

// hasDotPrefix checks if the pattern explicitly names a dotfile
// (so the dotfile filter should not reject the match).
func hasDotPrefix(pattern string) bool {
	// Check if the last path component of the pattern starts with .
	i := strings.LastIndex(pattern, "/")
	if i >= 0 {
		return strings.HasPrefix(pattern[i+1:], ".")
	}
	return strings.HasPrefix(pattern, ".")
}
