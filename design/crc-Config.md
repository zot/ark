# Config
**Requirements:** R8, R9, R10, R11, R12, R13, R14, R22, R23, R24, R25, R26, R27

Parses and validates ark.toml. Provides the effective pattern sets
for each source directory.

## Knows
- dotfiles: bool — whether * matches dotfiles (default true)
- globalInclude: []string — global include patterns
- globalExclude: []string — global exclude patterns
- sources: []Source — directory entries with strategy and optional overrides
- errors: []string — validation errors (identical include/exclude)

## Does
- Load(path): parse ark.toml, validate, return Config
- WriteDefault(path): write initial ark.toml with default excludes
- Validate(): check for identical include/exclude strings, report errors
- EffectivePatterns(source): return combined global + per-source patterns
- HasErrors(): true if validation errors exist (reported every operation)

## Collaborators
- Matcher: uses patterns from Config for classification

## Sequences
- seq-add.md
