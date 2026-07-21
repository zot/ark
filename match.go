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

// Match tests whether a single pattern matches a file.
//
// Three anchoring forms by leading slashes (R3196):
//
//   - "/X":  filesystem-absolute. Matches against absPath as-is.
//   - "./X": anchored to the contextual root. Strips "./", matches
//     against the path relative to that root.
//   - "X":   bare, i.e. relative to the contextual root. Prepends "**/"
//     and matches against the root-relative path.
//
// sourceDir *is* the context. Non-empty selects the source-scoped reading,
// where bare X means SOURCE/**/X (R3198). Empty selects the rootless
// reading, where the root-relative form degrades to absPath, so bare X
// means **/X — any depth, any source (R3199) — and "./X" has nothing to
// anchor to and falls back to the absolute path. The CLI is a third
// context (R3197), but it resolves *before* this call: AnchorGlobToDir
// rewrites a bare CLI glob to $PWD/X, so what arrives here is the "/X"
// form. The pattern's shape carries the context.
//
// Trailing "/" on the pattern means directory-only.
//
// CRC: crc-Matcher.md | R16, R17, R18, R19, R20, R3196, R3198, R3199
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

// MatchPathFilters is the one path-glob filter test in ark: does this path
// pass an (include, exclude) glob pair? Every surface that scopes a file set
// by path calls it — the CLI filter stack, chunk stats, subscription
// delivery, and the rootless ark.toml keys — so a glob means the same thing
// wherever it is written. Empty lists pass everything; a non-empty include
// list requires a hit; any exclude hit rejects.
//
// The contextual root is empty here, which is the **rootless** reading: a
// bare pattern means `**/X` (any depth, any source). CLI-supplied globs have
// already been rewritten to the absolute form by AnchorGlobToDir, so they
// take the `/X` branch instead — the pattern's own shape carries the
// context, which is why one call site serves both.
//
// CRC: crc-Matcher.md | R3195, R3196, R3199
func MatchPathFilters(path string, include, exclude []string) bool {
	if len(include) == 0 && len(exclude) == 0 {
		return true
	}
	m := &Matcher{Dotfiles: true}
	if len(include) > 0 {
		matched := false
		for _, pat := range include {
			if m.Match(ExpandTilde(pat), path, "", false) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pat := range exclude {
		if m.Match(ExpandTilde(pat), path, "", false) {
			return false
		}
	}
	return true
}

// FilterPaths returns the subset of paths passing MatchPathFilters.
// CRC: crc-Matcher.md | R3195
func FilterPaths(paths []string, include, exclude []string) []string {
	if len(include) == 0 && len(exclude) == 0 {
		return paths
	}
	var out []string
	for _, p := range paths {
		if MatchPathFilters(p, include, exclude) {
			out = append(out, p)
		}
	}
	return out
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
