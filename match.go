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
	Included Classification = iota
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
// CRC: crc-Matcher.md | R9, R10, R15, R2133
func (m *Matcher) Classify(includes, excludes []string, absPath, sourceDir string, isDir bool) Classification {
	included := false
	excluded := false
	for _, p := range includes {
		if m.Match(p, absPath, sourceDir, isDir) {
			included = true
			break
		}
	}
	for _, p := range excludes {
		if m.Match(p, absPath, sourceDir, isDir) {
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

// Match tests whether a single pattern matches a file. R2133.
//
// Three anchoring forms by leading slashes:
//
//   - "./X": source-root-anchored. Strips "./", matches against the
//     source-relative path (absPath relative to sourceDir).
//   - "/X":  filesystem-absolute. Matches against absPath as-is.
//   - "X":   bare. Prepends "**/", matches at any depth in source
//     (or against absPath when sourceDir is empty).
//
// Trailing "/" on the pattern means directory-only.
//
// sourceDir may be empty for ad-hoc filtering callers (search filters,
// `ark remove` patterns, etc.) — in that case, "./" and bare forms fall
// back to matching against absPath directly.
//
// CRC: crc-Matcher.md | R16, R17, R18, R19, R20, R2133
func (m *Matcher) Match(pattern, absPath, sourceDir string, isDir bool) bool {
	dirPattern := strings.HasSuffix(pattern, "/")
	if dirPattern != isDir {
		return false
	}
	if dirPattern {
		pattern = strings.TrimSuffix(pattern, "/")
	}

	var matchPath string
	switch {
	case strings.HasPrefix(pattern, "./"):
		pattern = pattern[2:]
		matchPath = sourceRelative(absPath, sourceDir)
		if matchPath == "" {
			return false
		}
	case strings.HasPrefix(pattern, "/"):
		matchPath = absPath
	default:
		pattern = "**/" + pattern
		matchPath = sourceRelative(absPath, sourceDir)
		if matchPath == "" {
			return false
		}
	}

	matched, err := doublestar.Match(pattern, matchPath)
	if err != nil || !matched {
		return false
	}

	if !m.Dotfiles {
		base := filepath.Base(matchPath)
		if strings.HasPrefix(base, ".") && !hasDotPrefix(pattern) {
			return false
		}
	}
	return true
}

// sourceRelative returns the source-relative form of absPath when
// sourceDir is non-empty and absPath is under it. Falls back to
// absPath when sourceDir is empty (ad-hoc Match callers). Returns
// empty string when absPath is not under sourceDir.
func sourceRelative(absPath, sourceDir string) string {
	if sourceDir == "" {
		return absPath
	}
	rel, err := filepath.Rel(sourceDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "" {
		return ""
	}
	return rel
}

// hasDotPrefix checks if the pattern explicitly names a dotfile
// (so the dotfile filter should not reject the match).
func hasDotPrefix(pattern string) bool {
	i := strings.LastIndex(pattern, "/")
	if i >= 0 {
		return strings.HasPrefix(pattern[i+1:], ".")
	}
	return strings.HasPrefix(pattern, ".")
}
