# Config
**Requirements:** R8-14, R22-27, R143-146, R148-151, R157-159, R194, R195, R200, R201, R203, R205-209, R340, R341, R396, R397, R624, R625, R631-635, R646, R853-856, R938, R943, R947-960, R1012, R1274, R1588-1592, R1919-1922, R1938, R2125, R2143-2150, R2728-2741, R2767, R2768, R2781, R2797-2800, R2811, R2822, R2830-2837, R2856, R2862, R2886, R2892, R2964-2966, R2968, R3041, R3135, R3171, R3195, R3198-3203, R3218, R3220, R3268, R3278, R3288-3290, R3292, R3329-3331, R3354, R3381, R3487, R3581-3583, R3589, R3673, R3674, R3676, R3678

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
- sources: []Source — directory entries with optional per-source strategies, pattern overrides, an `ext_mirror` dir that redirects the source's `@ext` mirror tree in-tree (R3171), and `logs_for` naming the source whose work this one records (R3288). `logs_for` is **declared, never derived**: the lookup path resolves it by name only, so no vendor's directory-naming scheme enters the query (R3289).
- strategies: map[string]string — file glob pattern → strategy name
- chunkers: []ChunkerConfig — language definitions from `[[chunker]]` entries. Carries easy-form `strings`/`brackets` flat pairs and full-form `string_defs`/`bracket_defs` structs; full-form bracket entries also accept optional `escape`, `allowed_inner`, and `allowed_parent` to mirror microfts2's BracketGroup model (R2147, R2148, R2149, R2150)
- sessionTTL: time.Duration — session cache TTL (default 30s, from `session_ttl` in ark.toml)
- searchExclude: *[]string — the noise filter (R938). nil means the key is absent and the built-in default applies; a pointer to an empty slice is an explicit `[]` (R3582, R3583). TOML key `search_exclude`, `omitempty`, so only nil is dropped on save.
- systemExclude: *[]string — the fence, same nil/empty convention (R3581, R3583). TOML key `system_exclude`.
- dbPath: string — unexported (`toml:"-"`); the directory `LoadConfig` read `ark.toml` from, reassigned by DB.Open. The fence default names the scheduler's log directory under it (R3581).
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
- Embedding: EmbeddingConfig — the `[embedding]` table (R2964, R2965, R2966, R3487, R2968):
  - Model: string — GGUF embedding model filename, relative to dbPath; renamed from top-level `tag_model` (R2964)
  - Tiers: []EmbedTier — ctx/parallel pairs for chunk embedding, sorted by byte limit ascending; renamed from top-level `embed_tiers` (R2965, R1590)
  - LibDir: string — dir of the runtime-`dlopen`ed llama.cpp libs; ResolveLibDir defaults it to `<dbPath>/lib` (R2966)
  - Backend: string — `auto|cpu|vulkan|cuda|metal|rocm` (R3487)
  - LlamaVersion: string — pinned llama.cpp build (R2968)
- aboutCentroidFilter: bool — enable file-centroid pre-filtering for "about" queries; default false (R1919, R1921, R1922)
- aboutCentroidThreshold: float64 — cosine similarity gate for the centroid filter; default 0.3, consulted only when aboutCentroidFilter is true (R1920, R1921)
- aboutFilterTopK: int — default chunk count retained per about-mode filter row; default 200 (R1938)
- autoCompact: bool — when true, `ark serve` runs database compaction on startup as if `-compact` had been passed. The CLI flag (when supplied) wins over this setting. Default false. (R2125)
- luhmann: LuhmannConfig — orchestrator restart-policy knobs (R2797, R2798, R2799, R2800, R2862). Carries `ContextLimit int` (default 150000, R2797), `CrashPauseAfter int` (default 3, R2798), `QuitEarlyPauseAfter int` (default 3, R2862 — consecutive-quit-early ceiling on the independent `quit_early` counter), `BackoffSeconds []int` (default `[1, 5, 30]`, R2799), and `Classes map[string]LuhmannClass` (per-class enable flag, R2800). Live reload picks up changes on the next supervisor decision (R2801). Also carries the **pty child command**: `Model string` (default `opus`, R3329) and `Command []string` (unset by default, R3330) — the argv the managed-pty launch runs, which is config rather than a literal because the model is a spend decision and the override is what lets a throwaway stand-in exercise the pty machinery.
- errors: []string — validation errors (identical include/exclude)

## Does
- PatternSpec.UnmarshalTOML: an array is the replace form, an `add` table
  the extend form; any other value is refused with an error naming both
  accepted forms, never coerced (R2146, R3678).
- Clone(): a deep copy sharing no slice, map or pointer with c at any depth:
  the struct copied by value, then every reference reachable through an
  exported field copied by reflection, and the one unexported reference
  (derivedStrategies) cloned explicitly. A Config is never written once
  published, so every mutator here runs on a Clone (R3673, R3674).
- Load(path): parse ark.toml, validate, return Config
- WriteDefault(path): write initial ark.toml with default excludes (editors' side files among them, R3676)
  and per-chime `[schedule.tag.chime-Nm]` blocks for each of the
  six standard cadences. All chimes default to `lifecycle = "disk"`
  with `log_cap = 1000`. (R2781, R2834)
- SearchExcludes() []string: the effective noise filter: the configured list, or `defaultSearchExclude` (`~/.claude/projects/**`, tilde-expanded) when nil. Every reader of the setting calls this, never the field (R3582, R3583).
- SystemExcludes() []string: the effective fence: the configured list, or `scheduleDirFor(dbPath)/**` when nil; empty when `dbPath` is unset (a zero-value Config in a test has no fence) (R3581, R3583).
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
- LuhmannConfig.EffectiveChildArgv(model): resolve the pty child's argv, and the **only** place the resolution rule lives — the host asks, it does not compose. Three levels, explicit parameter → `[luhmann] model` → the compiled-in `luhmannDefaultModel`, yielding `claude --model <resolved>` (R3329). A non-empty `[luhmann] command` short-circuits all of it and is returned verbatim (R3330). An explicit model *with* a command override returns an error rather than picking one (R3331) — both cannot be honored and silently dropping a typed parameter is the expensive kind of wrong. Pure and collaborator-free, so it is unit-testable against a zero-value config with no DB, server, or pty.
- HasErrors(): true if validation errors exist (reported every operation)
- AddSource(dir): add a new [[source]] entry. If dir contains glob chars, store as glob source (skip os.Stat). Otherwise validate dir exists.
- RemoveSource(dir): remove a source entry by directory path. Error if source is managed by a glob. Error if dir is the ark database directory (~/.ark) — hardcoded source.
- SourceName(src): the source's short name — the last segment of its `dir`
  (R3292). A label for a source ark already holds, not a reconstruction of
  another program's directory choice, which is what R3289 forbids.
- ResolveSourceName(name): the source `name` denotes — full `dir` match first,
  last-segment match second, so `dir` stays the identity and the short form is a
  convenience (R3292). More than one last-segment match is an error naming every
  candidate rather than a silent pick. Backs `--source`, `--for`, and `logs_for`
  validation.
- SourcesLoggingFor(name): the sources whose `logs_for` resolves to the same
  source `name` does — comparison over declared values, no path mangling
  (R3288, R3289, R3292). Backs `ark chats list --for`.
- ProposeLogsFor(): for each source carrying no `logs_for`, propose one by
  applying Claude Code's cwd-mangling heuristic to each *other* source's dir and
  matching the result against this source's basename. Returns proposals for
  review; **writes nothing** (R3290). This is the single place the vendor scheme
  appears, and it is a populator rather than a rule — deleting it costs the
  convenience of `--auto`, never the pairing itself. A proposal naming no
  existing source is returned flagged so the caller reports and skips it,
  because a `logs_for` pointing at nothing is worse than an absent one.
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
- **A source `dir` that is itself a symlink is refused (R3354).** Two doors,
  because there are two: `ValidateSourceDir` rejects it as written, and the
  glob expansion lstats each match so an expanded `~/work/*` cannot auto-add
  one either. `os.Stat` was the wrong call there and looked right — it follows
  the link, so a symlinked match passes `IsDir()` and becomes a concrete
  source.
  - A source root that is a link asks ark to *follow* one, which is the single
    thing this design does not do, and both ways of granting it cost more than
    refusing. Resolving to the target makes the user's own configured path a
    name ark cannot answer, since a scan starts *below* the root and so never
    meets the link that would make it resolvable. Walking the target while
    recording the configured path makes a stored name depend on config, so a
    later `dir` edit silently renames every file in the source.
  - The check is skipped for glob *patterns*, which name no file to lstat;
    their expansions are checked instead, which is where a real link can
    actually appear.
  - Unchecked, this is not an error but a convincing success: `filepath.WalkDir`
    lstats its root, so a symlinked one yields the link node as a single *file*
    entry and none of the contents. The scan reports one file having indexed
    nothing — worse than a zero to disbelieve, because nothing looks anomalous.
  - The supported way to gather scattered directories under one root is a
    **real** directory of links, which puts them inside a source and hands them
    to the predicate below. (Bill's call, 2026-08-02, replacing an earlier
    "resolve at config load".)
- **The track/propose predicate is re-derived at config load (R3381).**
  `LinkTable.Rebuild` already *was* the re-derivation and had no caller;
  `DB.ReloadConfig` is the trigger it was missing. `SetConfig` runs first and
  is not optional — the table's config **is** the source membership the
  predicate tests, so rebuilding against the previous one classifies links
  correctly for a configuration nobody is running.
  - **The demotion direction works; the promotion direction is not built yet.**
    Rebuild re-Observes the paths it is given, and the only paths available
    to give it are the *tracked* ones, so a link whose source was removed
    correctly drops to proposed. A link that was **proposed** is not in the
    table at all (`Observe` records only the tracked branch), and its identity
    lives in a symlink-reason U record whose `symlink` field is still dropped
    on the floor (O197). So "add the link first, add the source later, and the
    reload promotes it" — the half R3381 exists for — waits on U-record
    storage, which is the next item on this feature's list. Gap O202.
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
