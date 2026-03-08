# Matcher
**Requirements:** R9, R10, R15, R16, R17, R18, R19, R20, R21, R147

Evaluates doublestar glob patterns against file paths with ark-level
semantic modifiers (trailing `/` for directory-only). Stateless —
receives patterns and paths, returns classification.

Uses `github.com/bmatcuk/doublestar/v4` for glob matching. Ark adds:
- Trailing `/` modifier: pattern matches directories only
- No trailing `/`: pattern matches files only
- Unanchored patterns: prepend `**/` to match at any depth
- Dotfile filtering: post-match check when dotfiles=false

## Knows
- dotfiles: bool — whether * and ** match dotfiles

## Does
- Match(pattern, path, isDir): bool — does this pattern match this path?
  - Strips trailing `/` for directory patterns, rejects files
  - Rejects directories for non-`/` patterns
  - Prepends `**/` for unanchored patterns
  - Delegates to doublestar.Match
  - Post-filters dotfiles when dotfiles=false
- Classify(includes, excludes, path, isDir): included | excluded | unresolved
  - If any include matches → included (include wins)
  - If any exclude matches and no include → excluded
  - If nothing matches → unresolved

## Collaborators
- None (stateless utility)

## Sequences
- seq-add.md
- seq-config-mutate.md
