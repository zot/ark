# Config
**Requirements:** R8, R9, R10, R11, R12, R13, R14, R22, R23, R24, R25, R26, R27, R143, R144, R145, R146, R148, R149, R150, R151, R157, R158, R159, R194, R195, R200, R201, R203, R205, R206, R207, R208, R209, R340, R341, R396, R397, R624, R625, R631, R632, R633, R634, R635, R646, R853, R854, R855, R856, R938, R943, R947, R948, R949, R950, R951, R952, R953, R954, R955, R956, R957, R958, R959, R960, R1012, R1274, R1588, R1589, R1590, R1591, R1592, R1919, R1920, R1921, R1922, R1938, R2125, R2143, R2144, R2145, R2146, R2147, R2148, R2149, R2150, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2737, R2738, R2739, R2740, R2741, R2767, R2768, R2797, R2798, R2799, R2800, R2781, R2811, R2822, R2830, R2831, R2832, R2833, R2834, R2835, R2836, R2837, R2856, R2862, R2886, R2892, R2964, R2965, R2966, R2967, R2968, R3041, R3135, R3171, R3195, R3198, R3199, R3200, R3201, R3202, R3203, R3218, R3220

Parses, validates, and mutates ark.toml. Provides the effective pattern
sets for each source directory. Explains pattern resolution for any file.
Resolves glob sources into concrete directories. Maps file patterns to
chunking strategies.

**Two anchoring contexts live in this file** (`specs/main.md` §Glob Patterns
is canonical; crc-Matcher.md owns the matching). Every glob key here is one
or the other, and the difference is only what a *bare* pattern means:

- **Source-scoped (R3198)** — `default_include`, `default_exclude`,
  `[[source]].include`, `[[source]].exclude`, `strategies`,
  `[[source]].strategies`. Contextual root is the source's own directory;
  bare `X` means `SOURCE/**/X`. Config passes that dir to Matcher.
- **Rootless (R3199)** — `search_exclude`, `[schedule] filter_files`,
  `[schedule] exclude_files`, `[schedule.tag.<NAME>]` overrides. There is no
  current directory in a config file, so bare `X` means `**/X` (any depth,
  any source) and Config passes an empty root. Consequence, accepted and
  documented rather than fixed: `./X` has nothing to anchor to and degrades
  to matching the absolute path, so naming "the `specs` dir at the root of
  source X" from `[schedule]` requires an absolute path.

`[[source]].dir` is neither — it is expansion, not filtering (R3200).

## Knows
- dotfiles: bool — whether * matches dotfiles (default true)
- defaultInclude: []string — default include patterns; per-source `include`, when set, replaces this for that source (R2143, R2144). TOML key `default_include`.
- defaultExclude: []string — default exclude patterns; per-source `exclude`, when set, replaces this for that source (R2143, R2144). TOML key `default_exclude`.
- sources: []Source — directory entries with optional per-source strategies, pattern overrides, and an `ext_mirror` dir that redirects the source's `@ext` mirror tree in-tree (R3171)
- strategies: map[string]string — file glob pattern → strategy name
- chunkers: []ChunkerConfig — language definitions from `[[chunker]]` entries. Carries easy-form `strings`/`brackets` flat pairs and full-form `string_defs`/`bracket_defs` structs; full-form bracket entries also accept optional `escape`, `allowed_inner`, and `allowed_parent` to mirror microfts2's BracketGroup model (R2147, R2148, R2149, R2150)
- sessionTTL: time.Duration — session cache TTL (default 30s, from `session_ttl` in ark.toml)
- searchExclude: []string — glob patterns excluded from search results by default (R938)
- scheduleTags: map[string]ScheduleTagConfig — declared schedule tags by name. Populated from `[schedule.tag.X]` blocks in `ark.toml` (block presence = declaration). (R2830, R2833)
- scheduleFilterFiles: []string — glob patterns restricting schedule scanning, from top-level `[schedule] filter_files` (R953)
- scheduleExcludeFiles: []string — glob patterns excluding files from schedule scanning, from top-level `[schedule] exclude_files` (R954)

### ScheduleTagConfig

Per-tag config block parsed from `[schedule.tag.X]`. (R2830, R2831)

- Lifecycle: string — `"disk"` (default), `"tmp"`, or `"none"`.
  Controls audit destination. (R2822, R2823, R2824, R2825)
- LogCap: int — fired-entry cap per chunk; default 1000. (R2827)
- DefaultDuration: string — replaces `[schedule.defaults]` entries.
  (R2831)
- FilterFiles: []string — per-tag override of the global filter.
- ExcludeFiles: []string — per-tag override of the global exclude.
- Suppress: bool — when true, EnsureUpcoming is a no-op for the
  tag; queue drains on config reload. Default false. (R2835, R2836)
- Embedding: EmbeddingConfig — the `[embedding]` table (R2964, R2965, R2966, R2967, R2968):
  - Model: string — GGUF embedding model filename, relative to dbPath; renamed from top-level `tag_model` (R2964)
  - Tiers: []EmbedTier — ctx/parallel pairs for chunk embedding, sorted by byte limit ascending; renamed from top-level `embed_tiers` (R2965, R1590)
  - LibDir: string — dir of the runtime-`dlopen`ed llama.cpp libs; ResolveLibDir defaults it to `<dbPath>/lib` (R2966)
  - Backend: string — `auto|cpu|vulkan|cuda|metal|rocm` (R2967)
  - LlamaVersion: string — pinned llama.cpp build (R2968)
- aboutCentroidFilter: bool — enable file-centroid pre-filtering for "about" queries; default false (R1919, R1921, R1922)
- aboutCentroidThreshold: float64 — cosine similarity gate for the centroid filter; default 0.3, consulted only when aboutCentroidFilter is true (R1920, R1921)
- aboutFilterTopK: int — default chunk count retained per about-mode filter row; default 200 (R1938)
- autoCompact: bool — when true, `ark serve` runs database compaction on startup as if `-compact` had been passed. The CLI flag (when supplied) wins over this setting. Default false. (R2125)
- luhmann: LuhmannConfig — orchestrator restart-policy knobs (R2797, R2798, R2799, R2800, R2862). Carries `ContextLimit int` (default 150000, R2797), `CrashPauseAfter int` (default 3, R2798), `QuitEarlyPauseAfter int` (default 3, R2862 — consecutive-quit-early ceiling on the independent `quit_early` counter), `BackoffSeconds []int` (default `[1, 5, 30]`, R2799), and `Classes map[string]LuhmannClass` (per-class enable flag, R2800). Live reload picks up changes on the next supervisor decision (R2801).
- errors: []string — validation errors (identical include/exclude)

## Does
- Load(path): parse ark.toml, validate, return Config
- WriteDefault(path): write initial ark.toml with default excludes
  and per-chime `[schedule.tag.chime-Nm]` blocks for each of the
  six standard cadences. All chimes default to `lifecycle = "disk"`
  with `log_cap = 1000`. (R2781, R2834)
- EnsureArkSource(): add a synthetic `~/.ark` source entry to the
  Sources list if not already present. Uses the
  `arkSourceIncludePatterns` constant — top-level standard files
  (`ark.toml`, `chimes.md`, `tags.md`) plus per-extension
  whitelists under each content directory
  (`schedule/**/*.md`; `apps/**/*.{lua,js,html,css,md}`;
  `storage/**/*.{md,pdf}`; `external/**/*.md`;
  `skills/**/*.md` for ark-managed agent skill files, so sealed
  subagents can `ark fetch` their bootstrap skill — R2856) — as
  the source's Replace-form include list. Listing the standard files
  explicitly means ark-managed content is indexed regardless of
  the user's `[[source]]` configuration; the per-extension
  whitelist keeps binary artifacts under `apps/` and `storage/`
  (Fossil checkouts, `*.docx`, undo-tree dumps) out of the
  indexer. (R961, R962, R2393, R2811)
- EnsureLuhmannSource(): add a synthetic `chat-jsonl` source for the
  orchestrator's Claude Code project directory (`claudeProjectDir(luhmannCwd())`,
  `~/.claude/projects/<cwd-encoded>` for cwd `~/.ark/luhmann`) if not already
  present, Replace-form include `*.jsonl` — so the ark-managed Luhmann session's
  own log is indexed for recall and the watcher tap without user config, the same
  auto-index principle as EnsureArkSource. The `*.jsonl` global strategy gives it
  chat-jsonl; the `~/.claude/projects/**` search exclusion keeps it out of
  ordinary search. Called alongside EnsureArkSource at server start and config
  reload. (R3135)
- Save(path): write current Config state to ark.toml
- Validate(): check for identical include/exclude strings, report errors
- EffectivePatterns(source): for each of include/exclude, return the per-source patterns when set, else the corresponding default (R2143, R2144). Per-source replaces, not merges.
- HasErrors(): true if validation errors exist (reported every operation)
- AddSource(dir): add a new [[source]] entry. If dir contains glob chars, store as glob source (skip os.Stat). Otherwise validate dir exists.
- RemoveSource(dir): remove a source entry by directory path. Error if source is managed by a glob. Error if dir is the ark database directory (~/.ark) — hardcoded source.
- IsGlob(dir): true if dir contains *, ?, or [ characters — so `**` already
  registers as a glob and reaches the check below
- ResolveGlobs(): expand all glob source dirs, return list of resolved
  concrete dirs with their glob origin and strategy. `[[source]].dir` is
  filesystem **expansion**, not path filtering, so the three anchoring
  forms do not apply to it and `~` expansion is the only rewriting done
  (R3200). A `dir` containing `**` is **rejected with an error** naming `*`
  as the single-level alternative (R3201) — a recursive source glob makes
  every subdirectory its own source root, so one file change fires a
  watcher event per ancestor level, and the fault is *latent* (harmless
  until someone creates a nested directory), which is exactly what
  validating the expansion at load time cannot catch. Non-nested multiples
  (`~/work/*`) and the `from_glob` / `sources check` reconciliation
  (R196-R203) are untouched.
- StrategyForFile(path, sourceStrategies): merge sourceStrategies over
  global strategies, find the longest matching pattern. Returns the
  strategy name, or `lines` as default. Matching is `Matcher` under the
  **source-scoped** context (R3202, R3198), replacing the
  `filepath.Match`-on-source-relative-path-plus-basename-fallback pair,
  under which `*.md` worked at any depth (via the fallback) but `**/*.md`
  matched one level only — `**` had no effect. Near behavior-preserving:
  for a no-slash pattern the basename fallback and the bare→`**/` rule
  agree; what changes is that `**` starts working.
- AddInclude(pattern, sourceDir): add include pattern — global if sourceDir empty, per-source otherwise. Validate pattern syntax.
- AddExclude(pattern, sourceDir): add exclude pattern — global if sourceDir empty, per-source otherwise. Validate pattern syntax.
- validatePattern(pattern): syntax-check with `doublestar.ValidatePattern`
  rather than `filepath.Match`, so the validator and the matcher agree on
  what is legal (R3203). Nothing legal today is rejected by the change —
  it closes the gap where a `**` pattern passed a `filepath.Match`
  syntax check that could not express it.
- RemovePattern(pattern, sourceDir): remove pattern from include or exclude lists — global if sourceDir empty, per-source otherwise. No-op with message if not found.
- ExpandTilde(path string) string: expand `~` to home dir, `~user`
  to user's home (os/user.Lookup first, ~/../user fallback). R947-R952.
  Called at config load and CLI flag parsing boundaries.
- ExpandTildeSlice(paths []string) []string: expand tilde in each element.
- ShowWhy(filePath): explain why a file is included/excluded/unresolved — returns the matching pattern(s), source (global, per-source, .gitignore, .arkignore), and whether include-wins-conflicts applied. Reads ignore files at query time.
- IsScheduleTag(tag string) bool: true when a `[schedule.tag.X]`
  block exists for `tag`. (R2833)
- ScheduleTags() map[string]ScheduleTagConfig: full per-tag config
  map. Block enumeration. (R2833)
- Lifecycle(tag string) string: returns the tag's lifecycle value
  (`"disk"`, `"tmp"`, or `"false"`). Default `"disk"` when block
  exists with no `lifecycle` key set. (R2822)
- LogCap(tag string) int: per-tag cap, default 1000. (R2827)
- DefaultDuration(tag string) string: per-tag default duration.
  Replaces R854's `[schedule.defaults]` lookup. (R2831)
- IsSuppressed(tag string) bool: true when `[schedule.tag.X]
  suppress = true`. (R2835)
- SetSuppressed(tag string, v bool): mutate `ark.toml` to set or
  clear `suppress`; persists through the standard config-mutation
  path. Errors if the tag has no `[schedule.tag.X]` block — suppress
  modifies an existing declaration, never creates one. (R2840, R2841)
- MatchesScheduleFilter(path string) bool: check if a file path
  passes top-level `[schedule] filter_files`/`exclude_files`.
  (R953, R954, R955, R956). The underlying `matchesFilterExclude` tests via
  Matcher with an empty root — the rootless context (R3199, R3195) — rather
  than its own `**/`-prefixing pass.

## Collaborators
- Matcher: uses patterns from Config for classification and ShowWhy resolution

## Sequences
- seq-add.md
- seq-config-mutate.md
