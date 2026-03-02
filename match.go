package ark

// CRC: crc-Matcher.md

import (
	"path/filepath"
	"strings"
)

// Classification is the result of matching a path against include/exclude patterns.
type Classification int

const (
	Included   Classification = iota
	Excluded
	Unresolved
)

// Matcher evaluates the four-form pattern language against file paths.
type Matcher struct {
	Dotfiles bool // whether * matches dotfiles (default true)
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
func (m *Matcher) Match(pattern, path string, isDir bool) bool {
	form := patternForm(pattern)

	switch form {
	case formFile:
		// name — matches a file named "name" (not a directory)
		if isDir {
			return false
		}
		return m.matchGlob(pattern, path)

	case formDir:
		// name/ — matches a directory named "name"
		if !isDir {
			return false
		}
		dirPat := strings.TrimSuffix(pattern, "/")
		return m.matchGlob(dirPat, path)

	case formChildren:
		// name/* — matches immediate children of "name"
		dirPat := strings.TrimSuffix(pattern, "/*")
		dir := filepath.Dir(path)
		return m.matchGlob(dirPat, dir)

	case formDescendants:
		// name/** — matches any descendant of "name" at any depth
		dirPat := strings.TrimSuffix(pattern, "/**")
		return m.matchDescendant(dirPat, path)
	}
	return false
}

type patternFormType int

const (
	formFile patternFormType = iota
	formDir
	formChildren
	formDescendants
)

func patternForm(pattern string) patternFormType {
	if strings.HasSuffix(pattern, "/**") {
		return formDescendants
	}
	if strings.HasSuffix(pattern, "/*") {
		return formChildren
	}
	if strings.HasSuffix(pattern, "/") {
		return formDir
	}
	return formFile
}

// matchGlob matches a glob pattern against a path, handling anchoring
// and dotfiles. Unanchored patterns match at any depth.
func (m *Matcher) matchGlob(pattern, path string) bool {
	anchored := strings.HasPrefix(pattern, "/")
	if anchored {
		pattern = pattern[1:]
		return m.globMatch(pattern, path)
	}
	// Unanchored: try matching against path and every suffix after /
	if m.globMatch(pattern, path) {
		return true
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if m.globMatch(pattern, path[i+1:]) {
				return true
			}
		}
	}
	return false
}

// matchDescendant checks if path is under a directory matching dirPat.
func (m *Matcher) matchDescendant(dirPat, path string) bool {
	anchored := strings.HasPrefix(dirPat, "/")
	if anchored {
		dirPat = dirPat[1:]
	}
	// Split path into components, check if any prefix matches dirPat
	parts := strings.Split(path, "/")
	for i := 1; i <= len(parts)-1; i++ {
		prefix := strings.Join(parts[:i], "/")
		if m.globMatch(dirPat, prefix) {
			return true
		}
		if !anchored && m.globMatch(dirPat, parts[i-1]) {
			return true
		}
	}
	return false
}

// globMatch performs glob matching with dotfile awareness.
// Uses filepath.Match semantics but overrides dotfile behavior.
func (m *Matcher) globMatch(pattern, name string) bool {
	// Handle backslash escapes for literal wildcards
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	if !matched {
		return false
	}
	// If dotfiles disabled, check if * matched a dotfile component
	if !m.Dotfiles && !strings.HasPrefix(pattern, ".") {
		base := filepath.Base(name)
		if strings.HasPrefix(base, ".") {
			return false
		}
	}
	return true
}
