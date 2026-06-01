# Config — `ark.toml`

Canonical reference for every TOML key ark reads from
`ark.toml`. This is a **summary spec**: it doesn't introduce
behavior, it indexes the configuration surface that the
per-feature specs define along a single cross-cutting axis.
Per-feature specs in `specs/*.md` own the contract; this
document lists every key, its type, default, one-line meaning,
and the owning spec.

Like `specs/cli-commands.md`, `specs/record-formats.md`,
`specs/lua-api.md`, and `specs/features.md`, this is a
**mirror** spec. Mini-spec's per-feature anchoring won't catch
config changes on its own — when a per-feature spec adds,
renames, or retires an ark.toml key, update this file
explicitly. When the two disagree, the per-feature spec wins
and this file gets corrected to match.

Language: Go (TOML decoding via BurntSushi/toml). Environment:
ark CLI binary at `~/.ark/ark`; the config file lives at
`<db>/ark.toml`.

## Top-level keys

| Key                       | Type       | Default              | Meaning                                                                                                              | Owner                                                                       |
|---------------------------|------------|----------------------|----------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------|
| `dotfiles`                | bool       | `false`              | Index files whose names begin with `.`. Off by default.                                                              | `source-monitoring.md`                                                      |
| `case_insensitive`        | bool       | `false`              | Treat path globs as case-insensitive.                                                                                | `source-monitoring.md`                                                      |
| `embed_cmd`               | string     | `""`                 | External embedding command (legacy path). Empty falls back to the in-process embed pipeline.                         | `chunk-embeddings.md`                                                       |
| `query_cmd`               | string     | `""`                 | External query-embedding command. Pairs with `embed_cmd`.                                                            | `chunk-embeddings.md`                                                       |
| `default_include`         | []string   | (built-in)           | Glob patterns applied to every source unless overridden.                                                             | `source-monitoring.md`                                                      |
| `default_exclude`         | []string   | (built-in)           | Glob patterns that exclude paths from every source unless overridden.                                                | `source-monitoring.md`                                                      |
| `strategies`              | map<str,str> | `{}`               | Glob-pattern → chunker-name map applied across all sources.                                                          | `chunker-strategies.md`                                                     |
| `session_ttl`             | string     | `"30s"`              | Lifetime of named session caches before eviction. Go duration string.                                                | `session-status.md`                                                         |
| `search_exclude`          | []string   | `[]`                 | Glob patterns excluded from search results by default.                                                               | `search.md`                                                                 |
| `tag_model`               | string     | `""`                 | Filename of the GGUF embedding model under the ark dir. Empty disables vector-EC.                                    | `chunk-embeddings.md`, `embed-subcommands.md`                               |
| `auto_compact`            | bool       | `false`              | Run `mdb_env_copy2` before `Open` on every server start.                                                             | `record-formats.md`                                                         |
| `about_centroid_filter`   | bool       | `false`              | Enable the EF centroid prefilter on `-about` search queries.                                                         | `search.md`                                                                 |
| `about_centroid_threshold`| float64    | (mode-dep.)          | Cosine threshold for the EF centroid prefilter.                                                                      | `search.md`                                                                 |
| `about_filter_top_k`      | int        | (mode-dep.)          | Top-K cap applied after the EF centroid prefilter.                                                                   | `search.md`                                                                 |
| `pdf_preview_zoom`        | float64    | `1.0`                | Zoom factor applied to PDF page previews in the content view.                                                        | `chunked-content-view.md`                                                   |

## `[[source]]` — repeated table

| Key          | Type          | Default | Meaning                                                                                                  | Owner                  |
|--------------|---------------|---------|----------------------------------------------------------------------------------------------------------|------------------------|
| `dir`        | string        | (req.)  | Source root directory. Identifies the source.                                                            | `source-monitoring.md` |
| `strategies` | map<str,str>  | `{}`    | Per-source glob → chunker overrides (layered over the top-level `strategies` map).                       | `chunker-strategies.md`|
| `include`    | PatternSpec   | `{}`    | Include glob patterns. Either `include = [...]` to replace defaults, or `include.add = [...]` to extend. | `source-monitoring.md` |
| `exclude`    | PatternSpec   | `{}`    | Exclude glob patterns. Same Replace/Add forms as `include`.                                              | `source-monitoring.md` |
| `from_glob`  | string        | `""`    | Marks this source as derived from a glob expansion; tracked for sources-check reconciliation.            | `source-monitoring.md` |

## `[[chunker]]` — repeated table

| Key              | Type        | Default | Meaning                                                                                          | Owner                       |
|------------------|-------------|---------|--------------------------------------------------------------------------------------------------|-----------------------------|
| `name`           | string      | (req.)  | Chunker identifier (e.g. `python`, `go`).                                                        | `chunker-strategies.md`     |
| `type`           | string      | (req.)  | `bracket`, `bracket-full`, `indent`, or `indent-full`.                                           | `chunker-strategies.md`     |
| `tab_width`      | int         | `0`     | Logical tab width for indent chunkers.                                                           | `chunker-strategies.md`     |
| `line_comments`  | []string    | `[]`    | Line-comment prefixes the chunker should ignore when measuring structure.                        | `chunker-strategies.md`     |
| `block_comments` | [][]string  | `[]`    | Block-comment delimiter pairs (open, close).                                                     | `chunker-strategies.md`     |

## `embed_tiers` — array of inline tables

Tuned per-bucket context/parallel pairs for chunk embedding.
See `chunk-embeddings.md` for the four-bucket adaptive design.

| Key        | Type | Default | Meaning                                                                | Owner                  |
|------------|------|---------|------------------------------------------------------------------------|------------------------|
| `ctx`      | int  | (req.)  | llama.cpp `n_ctx` for this tier.                                       | `chunk-embeddings.md`  |
| `parallel` | int  | (req.)  | llama.cpp `n_parallel` (per-sequence ctx budget = `ctx / parallel`).   | `chunk-embeddings.md`  |

## `[recall]` — recall feature

Recall is a per-corpus property; `[recall]` gates both the
recall substrate's discussed-tag dedup and the simple-recall
ambient watcher.

| Key                | Type     | Default | Meaning                                                                                                                                                              | Owner                  |
|--------------------|----------|---------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------|
| `discussed_ttl`    | string   | `"24h"` | TTL on RD records before lazy expiry. `"0"` disables expiry. Go duration string.                                                                                     | `discussed-tags.md`    |
| `enabled`          | bool     | `false` | Master switch for the simple-recall watcher.                                                                                                                          | `simple-recall.md`     |
| `propose`          | bool     | `true`  | Pass `--propose` to the recall substrate so RC records accrue.                                                                                                       | `simple-recall.md`     |
| `min_similarity`   | float64  | `0.65`  | Per-section similarity gate. Sections whose top recalled chunk scores below this are dropped from the DM.                                                            | `simple-recall.md`     |
| `min_propose_similarity` | float64 | `0.70` | Chunk-EC ↔ tag-ED cosine floor for the propose pass. Tag candidates below this are dropped before the top-K cut and never written as RC records.                | `derived-tags.md`      |
| `activation_delay` | int      | `15`    | Seconds the watcher waits after a `turn_duration` record before firing. A user record arriving inside this window cancels the firing entirely.                       | `simple-recall.md`     |
| `chunks_per_dm`    | int      | `5`     | Per-input top-K cap. Each section in the DM body lists at most this many recalled chunks.                                                                            | `simple-recall.md`     |
| `sources`          | []string | `[]`    | Optional whitelist of source root directories. Empty means every `chat-jsonl` source qualifies.                                                                      | `simple-recall.md`     |
| `reject_propose_ceiling` | int | `0`     | Once a `(chunk, tag)` edge's Recall Judgment rejection magnitude (`-score`) reaches this, the propose pass stops surfacing it. `0` (unset) = infinite, safe default.   | `simple-recall.md`     |
| `reject_mention_ceiling` | int | `0`     | Once a `(chunk, tag)` edge's rejection magnitude (`-score`) reaches this, the assistant stops mentioning the count to the user. `0` = infinite.                       | `simple-recall.md`     |
| `surface_cooldown`  | string   | `"24h"` | Surface-cooldown window — a previously-surfaced `(session, chunk)` is suppressed within it. Doubles as the RM record's lazy-expiry TTL. Go duration string.            | `simple-recall.md`     |
| `context_turns`     | int      | `3`     | How many trailing conversation turns `recall next --session` injects into the curation doc so the per-session secretary judges with the live conversation. `0` = none. | `simple-recall.md`     |

## `[luhmann]` — Luhmann orchestrator

| Key                              | Type     | Default       | Meaning                                                                                                                                              | Owner          |
|----------------------------------|----------|---------------|------------------------------------------------------------------------------------------------------------------------------------------------------|----------------|
| `context_limit`                  | int      | `150000`      | Token ceiling passed to spawned subagents (used by the subagent's self-recycle check via `ark connections recall context`).                          | `luhmann.md`   |
| `crash_pause_after`              | int      | `3`           | Consecutive crash count at which the supervisor pauses a class (storm pause `crash-storm`) instead of respawning.                                    | `luhmann.md`   |
| `quit_early_pause_after`         | int      | `3`           | Consecutive quit-early count (independent counter) at which the supervisor pauses a class (storm pause `quit-early-storm`) instead of respawning.    | `luhmann.md`   |
| `backoff_seconds`                | []int    | `[1, 5, 30]`  | Seconds to wait between successive crash respawns. Final value applies to attempts beyond the list length, up to `crash_pause_after`.                | `luhmann.md`   |
| `class.<NAME>.enabled`           | bool     | `true`        | Whether the orchestrator should host this subagent class (e.g. `class.recall.enabled`).                                                              | `luhmann.md`   |

## `[schedule]` — scheduling feature

| Key                  | Type                              | Default | Meaning                                                                                          | Owner            |
|----------------------|-----------------------------------|---------|--------------------------------------------------------------------------------------------------|------------------|
| `tags`               | []string                          | `[]`    | Tags whose values are recognized as schedule entries.                                            | `scheduling.md`  |
| `defaults`           | map<str,str>                      | `{}`    | Default time-of-day per schedule tag (e.g. `standup = "09:00"`).                                 | `scheduling.md`  |
| `filter_files`       | []string                          | `[]`    | Restrict schedule scanning to matching files.                                                    | `scheduling.md`  |
| `exclude_files`      | []string                          | `[]`    | Exclude files from schedule scanning.                                                            | `scheduling.md`  |
| `lifecycle_include`  | []string                          | `["*"]` | Tags that participate in the full schedule lifecycle.                                            | `scheduling.md`  |
| `lifecycle_exclude`  | []string                          | `[]`    | Tags excluded from the schedule lifecycle.                                                       | `scheduling.md`  |
| `tag.<NAME>.filter_files`  | []string                    | `[]`    | Per-tag override of `filter_files`.                                                              | `scheduling.md`  |
| `tag.<NAME>.exclude_files` | []string                    | `[]`    | Per-tag override of `exclude_files`.                                                             | `scheduling.md`  |

## Notes

- All durations follow Go's `time.ParseDuration` grammar (`"24h"`,
  `"30m"`, `"30s"`, `"500ms"`). `"0"` is allowed where the
  per-feature spec assigns it special semantics.
- Tables documented as "repeated" use the TOML `[[...]]`
  array-of-tables syntax.
- "Owner" names the per-feature spec that defines the
  behavior. When a knob's contract changes, edit the owner
  spec first, then mirror to this index.
- Live reload: `ark serve` re-reads `ark.toml` on the existing
  config-reload path. A subset of changes are classified
  "deferred" (require restart) and produce an E record; the
  rest take effect on the next operation that consults them.
  See `config-tracking.md` for the classification.
