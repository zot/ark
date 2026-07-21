# Matcher
**Requirements:** R9, R10, R15, R16, R17, R18, R19, R20, R147, R3195, R3196, R3198, R3199

Evaluates doublestar glob patterns against file paths with ark-level
semantic modifiers (trailing `/` for directory-only). Stateless —
receives patterns, paths, and the contextual root, returns
classification.

**The one matcher (R3195).** Every path glob in ark — CLI filter rows,
`ark.toml` keys, subscription filters, chunk-stats scoping — is evaluated
here. No surface implements its own glob matching and no command carries a
per-command anchoring rule. What a surface supplies is the *contextual root*
(the `sourceDir` argument); what the pattern's leading characters supply is
the anchoring form. Together they are the whole vocabulary.

Uses `github.com/bmatcuk/doublestar/v4` for glob matching. Ark adds:
- Trailing `/` modifier: pattern matches directories only
- No trailing `/`: pattern matches files only
- Three anchoring forms (R3196):
  - `/path` (single leading slash): filesystem-absolute — match
    against the file's absolute path on disk
  - `./path`: anchored to the contextual root — strip `./` and match
    against the path relative to that root
  - Bare (no leading slash): relative to the contextual root — prepend
    `**/` and match against the root-relative path
- Dotfile filtering: post-match check when dotfiles=false

**Three contexts, one matcher.** The caller's `sourceDir` argument *is* the
context (`specs/main.md` §Glob Patterns is canonical):

| context | `sourceDir` | bare `X` means | callers |
|---|---|---|---|
| CLI (R3197) | `""` — already anchored | `$PWD/X`, arrived as `/…` | filter-stack rows, anchored CLI-side before dispatch |
| source-scoped (R3198) | the source's dir | `SOURCE/**/X` | Scanner classify, `strategies` |
| rootless (R3199) | `""` | `**/X` (any depth, any source) | `search_exclude`, `[schedule]` keys, Lua/MCP opts |

CLI and rootless share `sourceDir == ""` and still behave differently,
because the CLI rewrote the pattern to the absolute form *before* it got
here. The anchoring pass is the CLI's (crc-CLI.md, crc-Searcher.md); this
card only reads the form it is handed. A rootless `./X` has no root to
anchor to and falls back to matching the absolute path — accepted and
documented, not fixed (R3199).

## Knows
- dotfiles: bool — whether * and ** match dotfiles

## Does
- Match(pattern, absPath, sourceDir, isDir): bool — does this
  pattern match this file?
  - Strips trailing `/` for directory patterns, rejects files
  - Rejects directories for non-`/` patterns
  - Anchoring form selects which path form to match:
    - `/...` → absPath (filesystem-absolute)
    - `./...` → rootRelative (sourceDir-stripped, after dropping `./`)
    - bare → `**/` + rootRelative (any depth below the contextual root)
  - With `sourceDir == ""` the root-relative form degrades to absPath, so
    a bare pattern reads `**/X` against the absolute path — the rootless
    context (R3199)
  - Delegates to doublestar.Match
  - Post-filters dotfiles when dotfiles=false
- **Absorbs the per-surface matchers (R3195).** `pathMatchesGlob` /
  `matchFilesGlob` (search.go), `matchBaseSet` / `matchPath` (cmd/ark),
  `ChunkStats`' inline globbing (crc-DB.md), `matchesFilterExclude`
  (crc-Config.md), and pubsub's `anchorGlob` / `matchFileFilters`
  (crc-PubSub.md) all route here instead. Retiring the search-side pair is
  a **bug fix, not just consolidation**: its basename-first test
  (`filepath.Match` on the base name, then full-path doublestar) is a
  hand-rolled `**/X` that agrees with the bare form for a no-slash pattern
  but matches *nothing* for a slash-bearing one — the same silent-empty
  failure as O160, in the rootless context rather than pubsub.
- **Tilde expansion is the caller's** (R950). Patterns reach Match already
  expanded; the entry points that accept user text (`matchFilesGlob`,
  config load) call `ExpandTilde` first. Expansion is universal, anchoring
  is per-context — the two are deliberately separate steps.
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
