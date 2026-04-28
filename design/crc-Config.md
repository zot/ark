# Config
**Requirements:** R8, R9, R10, R11, R12, R13, R14, R22, R23, R24, R25, R26, R27, R143, R144, R145, R146, R148, R149, R150, R151, R157, R158, R159, R194, R195, R200, R201, R203, R205, R206, R207, R208, R209, R340, R341, R396, R397, R624, R625, R631, R632, R633, R634, R635, R646, R853, R854, R855, R856, R938, R943, R947, R948, R949, R950, R951, R952, R953, R954, R955, R956, R957, R958, R959, R960, R1012, R1274, R1588, R1589, R1590, R1591, R1592, R1919, R1920, R1921, R1922, R1938

Parses, validates, and mutates ark.toml. Provides the effective pattern
sets for each source directory. Explains pattern resolution for any file.
Resolves glob sources into concrete directories. Maps file patterns to
chunking strategies.

## Knows
- dotfiles: bool — whether * matches dotfiles (default true)
- globalInclude: []string — global include patterns
- globalExclude: []string — global exclude patterns
- sources: []Source — directory entries with optional per-source strategies and pattern overrides
- strategies: map[string]string — file glob pattern → strategy name
- chunkers: []ChunkerConfig — language definitions from `[[chunker]]` entries
- sessionTTL: time.Duration — session cache TTL (default 30s, from `session_ttl` in ark.toml)
- searchExclude: []string — glob patterns excluded from search results by default (R938)
- scheduleTags: map[string]string — tag name → default duration from `[schedule]` (R853, R854)
- scheduleFilterFiles: []string — glob patterns restricting schedule scanning (R953)
- scheduleExcludeFiles: []string — glob patterns excluding files from schedule scanning (R954)
- lifecycleInclude: []string — glob patterns for tags that get full lifecycle. Default `["*"]` (R957)
- lifecycleExclude: []string — glob patterns for tags excluded from lifecycle (R958)
- tagModel: string — GGUF embedding model filename, relative to dbPath (R1274)
- embedTiers: []EmbedTier — ctx/parallel pairs for chunk embedding, sorted by byte limit ascending (R1588, R1590)
- aboutCentroidFilter: bool — enable file-centroid pre-filtering for "about" queries; default false (R1919, R1921, R1922)
- aboutCentroidThreshold: float64 — cosine similarity gate for the centroid filter; default 0.3, consulted only when aboutCentroidFilter is true (R1920, R1921)
- aboutFilterTopK: int — default chunk count retained per about-mode filter row; default 200 (R1938)
- errors: []string — validation errors (identical include/exclude)

## Does
- Load(path): parse ark.toml, validate, return Config
- WriteDefault(path): write initial ark.toml with default excludes
- Save(path): write current Config state to ark.toml
- Validate(): check for identical include/exclude strings, report errors
- EffectivePatterns(source): return combined global + per-source patterns
- HasErrors(): true if validation errors exist (reported every operation)
- AddSource(dir): add a new [[source]] entry. If dir contains glob chars, store as glob source (skip os.Stat). Otherwise validate dir exists.
- RemoveSource(dir): remove a source entry by directory path. Error if source is managed by a glob. Error if dir is the ark database directory (~/.ark) — hardcoded source.
- IsGlob(dir): true if dir contains *, ?, or [ characters
- ResolveGlobs(): expand all glob source dirs, return list of resolved concrete dirs with their glob origin and strategy
- StrategyForFile(path, sourceStrategies): merge sourceStrategies over global strategies, find longest matching pattern. Returns strategy name or "lines" as default
- AddInclude(pattern, sourceDir): add include pattern — global if sourceDir empty, per-source otherwise. Validate pattern syntax.
- AddExclude(pattern, sourceDir): add exclude pattern — global if sourceDir empty, per-source otherwise. Validate pattern syntax.
- RemovePattern(pattern, sourceDir): remove pattern from include or exclude lists — global if sourceDir empty, per-source otherwise. No-op with message if not found.
- ExpandTilde(path string) string: expand `~` to home dir, `~user`
  to user's home (os/user.Lookup first, ~/../user fallback). R947-R952.
  Called at config load and CLI flag parsing boundaries.
- ExpandTildeSlice(paths []string) []string: expand tilde in each element.
- ShowWhy(filePath): explain why a file is included/excluded/unresolved — returns the matching pattern(s), source (global, per-source, .gitignore, .arkignore), and whether include-wins-conflicts applied. Reads ignore files at query time.
- IsScheduleTag(tag string) (defaultDur string, ok bool): check if a tag is declared as a schedule tag. Returns default duration and whether it's a schedule tag. (R853, R855)
- ScheduleTags() map[string]string: return the full schedule tag map (R853)
- IsLifecycleTag(tag string) bool: check if a schedule tag participates in the lifecycle (matched by lifecycle_include, not excluded by lifecycle_exclude). (R957, R958, R960)
- MatchesScheduleFilter(path string) bool: check if a file path passes schedule filter_files/exclude_files. (R953, R954, R955, R956)

## Collaborators
- Matcher: uses patterns from Config for classification and ShowWhy resolution

## Sequences
- seq-add.md
- seq-config-mutate.md
