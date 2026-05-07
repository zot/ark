# Matcher
**Requirements:** R9, R10, R15, R16, R17, R18, R19, R20, R147, R2133

Evaluates doublestar glob patterns against file paths with ark-level
semantic modifiers (trailing `/` for directory-only). Stateless —
receives patterns, paths, and the source directory, returns
classification.

Uses `github.com/bmatcuk/doublestar/v4` for glob matching. Ark adds:
- Trailing `/` modifier: pattern matches directories only
- No trailing `/`: pattern matches files only
- Three anchoring forms (R2133):
  - Bare (no leading slash): match at any depth in the source —
    prepend `**/` and match against the source-relative path
  - `/path` (single leading slash): filesystem-absolute — match
    against the file's absolute path on disk
  - `./path`: source-root-anchored — strip `./` and match against
    the source-relative path
- Dotfile filtering: post-match check when dotfiles=false

## Knows
- dotfiles: bool — whether * and ** match dotfiles

## Does
- Match(pattern, absPath, sourceDir, isDir): bool — does this
  pattern match this file?
  - Strips trailing `/` for directory patterns, rejects files
  - Rejects directories for non-`/` patterns
  - Anchoring form selects which path form to match:
    - `/...` → absPath (filesystem-absolute)
    - `./...` → sourceRelative (sourceDir-stripped, after dropping `./`)
    - bare → `**/` + sourceRelative (any-depth in source)
  - Delegates to doublestar.Match
  - Post-filters dotfiles when dotfiles=false
- Classify(includes, excludes, absPath, sourceDir, isDir): included | excluded | unresolved
  - For each pattern, calls Match with the same absPath/sourceDir pair
  - If any include matches → included (include wins)
  - If any exclude matches and no include → excluded
  - If nothing matches → unresolved

## Collaborators
- None (stateless utility)

## Sequences
- seq-add.md
- seq-config-mutate.md
