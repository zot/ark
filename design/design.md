# Ark Design

Orchestration layer over microfts2 and microvec. Digital zettelkasten
with hybrid search.

## Intent

Ark coordinates two search engines (trigram + vector) through a single
interface. Files on disk are the source of truth; the database is just
an index. The CLI and HTTP API expose identical operations. A
long-running server keeps the embedding model warm; the CLI proxies to
it or falls back to cold-start.

V2 adds model-free recall: chunk/file content retrieval (JSONL), @tag
tracking with vocabulary, and JSONL chunking for conversation logs.
Tags + FTS give fully functional recall on any hardware.

## Cross-cutting Concerns

### LMDB Lifecycle
microfts2 owns the LMDB environment. Ark opens microfts2 first
(which creates the env), passes the env to microvec, then opens its
own subdatabase. All three share one env with MaxDBs=8. Closing
follows reverse order.

### File Identity
microfts2 allocates fileids. microvec and ark's subdatabase both
reference files by fileid. microfts2 is the single source of truth
for path→fileid mapping.

### Pattern Matching
Doublestar glob patterns (`github.com/bmatcuk/doublestar/v4`) with
ark-level semantic modifiers: trailing `/` for directory-only, no
trailing `/` for file-only. Unanchored patterns match at any depth
(prepend `**/`). Dotfile filtering is a post-match check. Used
throughout: source config, CLI remedy commands, scan classification,
strategy mapping.

### Error Reporting
Config errors (identical include/exclude) are reported on every
operation until resolved. Missing/unresolved files are persisted
and surfaced through status and listing commands.

### Tag Tracking
@tags (format: `@word:`) are extracted from file content during
add and refresh. Stored in the ark subdatabase with T (global count)
and F (per-file count) prefix keys. Tag names are the portion between
@ and :, stored lowercase. The tag vocabulary file (`~/.ark/tags.md`)
documents definitions using `@tag: name -- description` format.

### Chunk/File Retrieval
Search results can include content via `--chunks` (JSONL per chunk)
or `--files` (JSONL per file, deduplicated). This enables the
permission end-run: indexed content emitted without per-file prompts.
Content is read from disk at query time using offsets from microfts2.

### Embedded UI Engine
Frictionless runs in-process via the `flib` facade package
(`github.com/zot/frictionless/flib`), which wraps the ui-engine.
`cfg.Server.Dir` points to `~/.ark/`, where UI assets (html/, lua/,
viewdefs/, apps/) coexist with ark data (data.mdb, ark.toml,
ark.sock). Two listeners: the unix socket serves both ark API and
Frictionless `/api/*` routes (via `flib.RegisterAPI`); the HTTP port
(written to `ui-port`) serves the browser UI. If the ui-engine fails
to start, the ark API server continues — UI is optional. On shutdown,
`flib.Shutdown()` runs before the LMDB env closes.

### Sessions
Named closure actors that carry state (currently a ChunkCache)
across commands. Server-side only. Each session has a TTL
(configurable via `session_ttl` in ark.toml, default 30s) — the
cache is evicted on TTL expiry or when a search query diverges
from the previous query (not a prefix). Sessions are autocreated
on first use by any source (CLI `--session`, HTTP `session` field,
Lua `session` opt). The session is the Closure Actor pattern
applied to cross-query caching.

### Temporary Documents
Ephemeral, in-memory documents with `tmp://` path prefix. Indexed
alongside persistent files via microfts2's in-memory overlay.
Search includes them by default; `--no-tmp` / `WithNoTmp()` opts out.
CLI detects tmp:// prefix on add/remove and proxies to server.
Search without `--session` uses `onlyIfTmp` to avoid proxying when
no tmp docs exist (HTTP 204 = proceed locally). Tags extracted
from tmp content using the same regex as persistent files.
Lifetime = server lifetime.

## Artifacts

### CRC Cards
- [x] crc-DB.md → `db.go`
- [x] crc-Config.md → `config.go`
- [x] crc-Matcher.md → `match.go`
- [x] crc-Store.md → `store.go`
- [x] crc-Scanner.md → `scanner.go`
- [x] crc-Indexer.md → `indexer.go`
- [x] crc-Searcher.md → `search.go`
- [x] crc-Server.md → `server.go`, `watcher.go`
- [x] crc-CLI.md → `cmd/ark/main.go`, `cmd/ark/vecbench.go`
- [x] crc-TagBlock.md → `tagblock.go`
- [x] crc-Session.md → `session.go`
- [x] crc-SearchCmd.md → `server.go`, `session.go`

### Sequences
- [x] seq-add.md → `scanner.go`, `indexer.go`, `store.go`
- [x] seq-search.md → `search.go`
- [x] seq-server-startup.md → `server.go`, `scanner.go`, `indexer.go`
- [x] seq-cli-dispatch.md → `cmd/ark/main.go`, `server.go`
- [x] seq-config-mutate.md → `config.go`, `cmd/ark/main.go`, `server.go`
- [x] seq-sources-check.md → `config.go`, `db.go`, `cmd/ark/main.go`, `server.go`
- [x] seq-install.md → `cmd/ark/main.go`
- [x] seq-reconcile.md → `server.go`
- [x] seq-parallel-refresh.md → `indexer.go`
- [x] seq-file-change.md → `server.go`, `watcher.go`, `indexer.go`, `search.go`, `store.go`
- [x] seq-message.md → `cmd/ark/main.go`, `tagblock.go`
- [x] seq-session-search.md → `session.go`, `server.go`, `search.go`, `cmd/ark/main.go`
- [x] seq-tmp-documents.md → `db.go`, `server.go`, `cmd/ark/main.go`, `search.go`

### Test Designs
- [x] test-Config.md → `config_test.go`
- [x] test-Matcher.md → `match_test.go`
- [x] test-Searcher.md → `search_test.go`
- [x] test-Store.md → `store_test.go`
- [x] test-Tags.md → `indexer_test.go`, `store_test.go`
- [x] test-ChunkRetrieval.md → `search_test.go`
- [x] test-TagBlock.md → `tagblock_test.go`

## Gaps

- [x] O1: Test files not yet written: config_test.go, match_test.go, search_test.go, store_test.go, tags_test.go
- [ ] O2: serverClient TOCTOU race — probe can succeed but actual request fails if server dies between. Acceptable for v1
- [ ] A1: IndexBuilt field removed from StatusInfo during simplification — spec still mentions it, update spec
- [ ] A2: MissingRecord.FileID always serializes as 0 in stored JSON (populated from LMDB key on read)
- [ ] O3: Integration tests need live microfts2+microvec: merge/intersect (test-Searcher), FillChunks/FillFiles (test-ChunkRetrieval)
- [ ] O4: Fetch uses O(n) StaleFiles scan — add direct path lookup to microfts2 when performance matters
- [ ] O5: ark stop does not verify process is ark (PID rollover could kill wrong process) — check /proc/PID/cmdline on Linux
- [ ] A3: R187 (vector search) deferred to V4 — no design artifact needed until then
- [ ] A4: R214 (negative requirement — no separate lock file) — verified by absence, no design artifact needed
- [ ] O6: JSONL chunks flood search results — single conversation file produces hundreds of score-1.0 chunks, burying small .md files. Mitigated by --filter-files/--exclude-files filtering.
- [ ] A5: microfts2 WithOnly/WithExcept — implemented in microfts2 dependency, used by Searcher.ResolveFilters
- [ ] A6: R231 (no backward compatibility for --source/--not-source) — verified by removal, no design artifact needed
- [ ] A7: R235 (test for per-source add-include round-trip) — covered in test-Config.md
- [ ] O7: Shutdown race: signal handler calls db.Close() while reconcileLoop and watchLoop goroutines may still be running. watcher.Close() stops watchLoop, but reconcileLoop can still be mid-Scan/Refresh against the LMDB env. Need to close reconcileCh and wait for goroutine to drain before db.Close().
- [x] D1: R338-R339 (EnsureArkSource) — designed in crc-Server.md but not implemented. Server should ensure ~/.ark is a source on every startup, before reconciliation.
- [x] D2: R340 (RemoveSource guard for ~/.ark) — designed in crc-Config.md line 25 but not implemented. Config.RemoveSource should reject the ark database directory.
- [x] O8: ark install UI asset extraction not yet implemented — R276-R281 designed, bundle commands (R297-R318) implemented, install needs to call ExtractBundle
- [x] O9: ~/.ark/mcp shell script (R283) not yet created — needs adaptation of .ui/mcp pattern for ~/.ark/ paths
- [ ] A8: R294 (zip-graft allows layering without recompilation) — build process property, no code artifact
- [ ] A9: R296 (re-export CreateBundle/ExtractBundle from ui-engine) — upstream change in ui-engine/cli/exports.go, done
- [ ] A10: R303 (bundle is build-time command) — inferred, covered by R297 implementation
- [ ] A11: R319-R322 (Makefile asset pipeline) — Makefile infrastructure, not Go code
- [x] O10: Self-triggered ark.toml events — configMutate saves ark.toml, watcher fires, watchLoop reloads config + triggers a second reconcile. Harmless (idempotent) but wasteful. Could suppress with a short debounce or write-flag.
- [ ] O11: No tests for watcher/throttle — the throttle state machine is testable (inject clock, mock watcher channels) but untested
- [x] D3: Phase C (R360-R369, append detection) blocked on microfts2 — needs FileLength in N record, AppendChunks API, chunker offset support. Requests in ~/work/microfts2/UPDATES.md items 2-4
- [ ] O12: Append detection assumes clean chunk boundaries — all current strategies (lines, chat-jsonl) produce clean boundaries. For markdown strategy, derive boundary cleanliness from last chunk end vs file length. When unclean, implement back-seek from last chunk to find match point and WithReplaceFrom in microfts2.AppendChunks
- [ ] O13: AppendFile reads full file twice (once in DetectAppend for prefix hash, once in AppendFile for new bytes + full hash). Acceptable because savings come from avoiding re-chunking/re-indexing old content. Could optimize with hash state passing if profiling shows it matters.
- [x] A12: R376-R381, R384 (markdown chunker) — implemented in microfts2 as MarkdownChunkFunc
- [x] D4: R415-R416, R541-R546 RegisterLuaFunctions — mcp:indexing(), mcp:search_grouped(), mcp:open() all registered. HTTP endpoints removed.
- [ ] A13: R423-R428 (MCP event pulse indicator) — pure Lua/CSS in Frictionless status bar, no ark Go code needed
- [ ] A14: R418 (browser reconnect on reload) — handled by ui-engine WebSocket reconnect logic, no ark code
- [ ] A15: R421-R422 (second tab detection) — ui-engine/Frictionless concern, not ark Go code
- [x] D5: R420 (preferred port on restart) — needs flib.Config.Port field in Frictionless upstream
- [ ] D6: R438-R439 (browser count) — flib.Runtime doesn't expose WebSocket connection count. UIStatus reports running/port/indexing but not browser count. Needs flib API addition.
- [ ] O14: gollama v0.1.8 SIGILL on Zen 2 (Steam Deck) — llama.cpp compute graph uses unsupported instructions. vec bench loads model but crashes on GetEmbeddings. Needs gollama rebuild with -march=znver2 or compatible flags.
- [ ] O15: No unit tests for SearchMulti — needs test with mock microfts2 DB or integration test
- [ ] O16: QueryTrigramCounts + BM25Func open two separate LMDB Views for overlapping data — could be one transaction with a BM25FuncFromQuery helper in microfts2
- [ ] O17: --proximity only works with --multi currently — spec says it composes with any search mode (R597)
- [ ] O18: No unit tests for Session actor — testable with injected clock/mock FTS
- [ ] A16: SearchCmd has no separate code file — command object is implicit in session closures (server.go, session.go). CRC card documents the concept.
- [ ] O19: No unit tests for tmp:// operations — AddTmpFile, RemoveTmpFile, onlyIfTmp probe, --no-tmp flag
- [x] O20: ark files does not yet list tmp:// documents (R671 — needs handleFiles update)
- [x] O21: ark status does not yet report tmp:// document count (R676)
- [ ] A17: R703 (QueryBigramCounts return type) — microfts2 API detail, no ark design artifact needed
- [ ] A18: R704-R705 (bigrams on by default, rebuild needed) — microfts2 default, no ark code change
- [ ] A19: R706 (ark rebuild recreates v3 format) — rebuild already works by recreating the DB, v3 is automatic
- [ ] A20: R707 (index size impact) — informational, no design artifact needed