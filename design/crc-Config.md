# Config
**Requirements:** R8, R9, R10, R11, R12, R13, R14, R22, R23, R24, R25, R26, R27, R143, R144, R145, R146, R148, R149, R150, R151, R157, R158, R159, R194, R195, R200, R201, R203, R205, R206, R207, R208, R209, R340, R341

Parses, validates, and mutates ark.toml. Provides the effective pattern
sets for each source directory. Explains pattern resolution for any file.
Resolves glob sources into concrete directories. Maps file patterns to
chunking strategies.

## Knows
- dotfiles: bool — whether * matches dotfiles (default true)
- globalInclude: []string — global include patterns
- globalExclude: []string — global exclude patterns
- sources: []Source — directory entries with strategy and optional overrides
- strategies: map[string]string — file glob pattern → strategy name
- errors: []string — validation errors (identical include/exclude)

## Does
- Load(path): parse ark.toml, validate, return Config
- WriteDefault(path): write initial ark.toml with default excludes
- Save(path): write current Config state to ark.toml
- Validate(): check for identical include/exclude strings, report errors
- EffectivePatterns(source): return combined global + per-source patterns
- HasErrors(): true if validation errors exist (reported every operation)
- AddSource(dir, strategy): add a new [[source]] entry. If dir contains glob chars, store as glob source (skip os.Stat). Otherwise validate dir exists.
- RemoveSource(dir): remove a source entry by directory path. Error if source is managed by a glob. Error if dir is the ark database directory (~/.ark) — hardcoded source.
- IsGlob(dir): true if dir contains *, ?, or [ characters
- ResolveGlobs(): expand all glob source dirs, return list of resolved concrete dirs with their glob origin and strategy
- StrategyForFile(path, fallback): check strategies map (longest match wins), return strategy name or fallback
- AddInclude(pattern, sourceDir): add include pattern — global if sourceDir empty, per-source otherwise. Validate pattern syntax.
- AddExclude(pattern, sourceDir): add exclude pattern — global if sourceDir empty, per-source otherwise. Validate pattern syntax.
- RemovePattern(pattern, sourceDir): remove pattern from include or exclude lists — global if sourceDir empty, per-source otherwise. No-op with message if not found.
- ShowWhy(filePath): explain why a file is included/excluded/unresolved — returns the matching pattern(s), source (global, per-source, .gitignore, .arkignore), and whether include-wins-conflicts applied. Reads ignore files at query time.

## Collaborators
- Matcher: uses patterns from Config for classification and ShowWhy resolution

## Sequences
- seq-add.md
- seq-config-mutate.md
