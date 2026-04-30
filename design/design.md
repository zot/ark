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

### DB Concurrency (Closure Actor)
All DB operations are serialized through a closure actor (ChanSvc)
on `ark.DB`. Three concurrent accessors — watcher goroutine, HTTP
handler goroutines, Lua/UI goroutine — send closures to the actor
instead of calling DB methods directly. LMDB handles its own
concurrency via MVCC; the actor protects the Go-side caches above
it (pathCache, pathToID, frecordCache). Watcher mutations use
fire-and-forget (Svc). HTTP/CLI operations use synchronous calls
(SvcSync). The former reconcileLoop merges into the actor.
Call direction is always session → DB, never the reverse.

**Read/Write Separation:** Reads execute directly in the actor and
return immediately (LMDB MVCC provides consistent snapshots). Writes
are queued and processed one at a time in a goroutine: Copy() creates
a cache-less DB copy, the goroutine indexes off the actor, then sends
a reconcile closure back. The actor invalidates caches, commits, and
dequeues the next write. Config files (ark.toml) bypass the queue and
index synchronously in the actor. See seq-write-actor.md.

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

### Host API Boundary (TypeScript — Markdown Editor)
All ark communication flows through the HostAPI interface (R1326–R1328).
The viewer imports nothing from ark or Frictionless. Every CRC card
that needs ark data receives it via HostAPI injection at construction.

### Packaging (R1329)
Built assets (JS bundle, CSS) are placed in ~/.ark/html/ by the
build process. No npm runtime dependency. This is a build/install
concern, not a runtime component — no CRC card needed.

### Read/Edit Mode (TypeScript — Markdown Editor)
A shared EditorState field tracks the current mode (R1348–R1349). All
extensions that produce interactive widgets check this field —
widgets are active in read mode, standard CM6 editing in edit mode.

## Artifacts

### CRC Cards
- [x] crc-DB.md → `db.go`
- [x] crc-Config.md → `config.go`
- [x] crc-Matcher.md → `match.go`
- [x] crc-Store.md → `store.go`
- [x] crc-Scanner.md → `scanner.go`
- [x] crc-Indexer.md → `indexer.go`, `ext.go`
- [x] crc-Searcher.md → `search.go`
- [x] crc-Server.md → `server.go`, `watcher.go`
- [x] crc-CLI.md → `cmd/ark/main.go`
- [x] crc-TagBlock.md → `tagblock.go`
- [x] crc-Session.md → `session.go`
- [x] crc-SearchCmd.md → `server.go`, `session.go`
- [x] crc-PubSub.md → `pubsub.go`
- [x] crc-EventScheduler.md → `scheduler.go`
- [x] crc-TmpTagStore.md → `tmp_tag_store.go`
- [x] crc-TvidMap.md → `tvid_map.go`

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
- [x] seq-pubsub.md → `pubsub.go`, `scheduler.go`, `server.go`, `indexer.go`, `cmd/ark/main.go`
- [x] seq-scheduling.md → `scheduler.go`, `store.go`, `indexer.go`, `server.go`, `config.go`, `cmd/ark/main.go`
- [x] seq-write-actor.md → `db.go`, `svc.go`, `indexer.go`, `server.go`
- [x] seq-editor-endpoints.md → `server.go`, `search.go`
- [x] seq-tag-value-index.md → `store.go`, `indexer.go`, `server.go`
- [x] seq-content-fetching.md → `server.go`
- [x] seq-filter-stack.md → `cmd/ark/main.go`, `server.go`, `search.go`
- [x] crc-Librarian.md → `librarian.go`
- [x] crc-PDFChunker.md → `pdfchunker.go`
- [x] seq-spectral-expand.md → `librarian.go`, `server.go`
- [x] seq-tag-embed.md → `librarian.go`, `store.go`, `server.go`
- [x] seq-chunk-embed.md → `librarian.go`, `store.go`, `server.go`, `config.go`
- [x] seq-pdf-chunk.md → `pdfchunker.go`
- [x] seq-empty-file-skip.md → `scanner.go`, `db.go`, `emptyfiles.go`
- [x] seq-pdf-chunk-retrieval.md → `pdfchunker.go`, `store.go`
- [x] seq-embed-validate.md → `cmd/ark/main.go`, `store.go`
- [x] seq-tmp-tag-overlay.md → `db.go`, `store.go`, `tmp_tag_store.go`, `indexer.go`
- [x] seq-tvid-overlay.md → `tvid_map.go`, `store.go`, `tmp_tag_store.go`, `db.go`

### CRC Cards (TypeScript — Ark Search Component)
- [x] crc-SearchAPI.md → `ark-search/src/search-api.ts`
- [x] crc-ArkSearchElement.md → `ark-search/src/ark-search-element.ts`
- [x] crc-ArkTagElement.md → `install/html/content-markdown.html`, `install/html/content-plain.html`
- [x] crc-PdfChunkElement.md → `pdf-chunk/src/pdf-chunk-element.ts`

### Sequences (TypeScript — PDF Chunk Element)
- [x] seq-pdf-chunk-render.md → `pdf-chunk/src/pdf-chunk-element.ts`, `ark-search/src/ark-search-element.ts`
- [x] seq-pdf-slice.md → `pdf-chunk/src/pdf-chunk-element.ts`, `ark-search/src/ark-search-element.ts`

### CRC Cards (TypeScript — Markdown Editor)
- [x] crc-ArkTagParser.md → `markdown-editor/src/ark-tag-parser.ts`
- [x] crc-TagWidget.md → `markdown-editor/src/tag-widget.ts`
- [x] crc-TagCompletion.md → `markdown-editor/src/tag-completion.ts`
- [x] crc-ArkSearchBlock.md → `markdown-editor/src/ark-search-block.ts`
- [x] crc-SearchResultView.md → `markdown-editor/src/search-result-view.ts`
- [x] crc-HostAPI.md → `markdown-editor/src/host-api.ts`
- [x] crc-ModeToggle.md → `markdown-editor/src/mode-toggle.ts`
- [x] crc-HighlightExtension.md → `markdown-editor/src/highlight-extension.ts`

### Sequences (TypeScript — Markdown Editor)
- [x] seq-tag-click.md → `markdown-editor/src/tag-widget.ts`, `markdown-editor/src/search-result-view.ts`, `ark-search/src/ark-search-element.ts`
- [x] seq-tag-completion.md → `markdown-editor/src/tag-completion.ts`
- [x] seq-ark-search-render.md → `markdown-editor/src/ark-search-block.ts`, `markdown-editor/src/search-result-view.ts`
- [x] seq-mode-toggle.md → `markdown-editor/src/mode-toggle.ts`, `markdown-editor/src/ark-search-block.ts`
- [x] seq-save.md → `markdown-editor/src/mode-toggle.ts`
- [x] seq-ark-tag-click.md → `install/html/content-markdown.html`, `install/html/content-plain.html`

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
- [x] A17: R703 (QueryBigramCounts return type) — superseded, bigrams removed from microfts2
- [x] A18: R704-R705 (bigrams on by default, rebuild needed) — superseded, bigrams removed
- [x] A19: R706 (ark rebuild recreates v3 format) — superseded, DB reverted to v2
- [x] A20: R707 (index size impact) — superseded, no bigram index
- [ ] A21: R717 (--unmatched implies request-only) — inferred, behavior falls out of the filter logic naturally
- [x] O22: R737: flib.Config needs Verbosity field to propagate ark verbosity to ui-engine cfg.Logging.Verbosity — requires cross-project change to frictionless/flib
- [ ] O23: No unit tests for SearchFuzzy — testable with mock microfts2 DB
- [x] C1: search_grouped mode dispatch — added "fuzzy" case (opts.Fuzzy=true, query stays). UI replaces "about" button with "fuzzy".

- [ ] A22: R753 (tagPattern/tagblock regexes unchanged) — verified by absence, no design artifact needed
- [ ] O24: Pubsub: SessionID on ScheduledEvent not used in fire() delivery — events fire to all sessions, not per-subscriber. Wire per-session filtering when needed.
- [ ] O25: Pubsub: writerID always empty string — self-notification exclusion (R798) cannot trigger. Wire actual session ID through indexer when session-aware indexing exists.
- [x] O26: Pubsub: ExtractTagValues only matches first tag per line on compound tags — subsequent tags in compound lines won't fire subscriptions. Needs RE2-compatible non-greedy or two-pass approach.
- [ ] O27: Pubsub: ErrorReporter uses nil — tmp:// append now available (R909), wire TmpErrorReporter to use it.
- [x] O28: Pubsub: Watchdog results not persisted — Watchdog() returns results but no caller writes them to tmp:// yet. Wire when tmp:// append lands.
- [ ] O29: Pubsub: No unit tests for PubSub, EventScheduler, Watchdog, or CLI commands
- [x] O30: Lua dead code: Source:makeNode() no longer called after mcp.listSource migration. Source._missingPaths also unused. Safe to remove.
- [ ] O31: Inherited new() on ui-engine prototypes captures root prototype in closure — creates Object type instead of target type. Workaround: use rawget to detect type-specific new(), fall back to session:create. See R846-R848.
- [ ] O32: No unit tests for mcp.listSource — testable with mock Config and DB.Missing()
- [ ] O33: No unit tests for scheduling: ParseDateValue, day-bucket Store ops, log file read/write, EnsureUpcoming, ScanScheduleLogs, crankForward
- [ ] O34: Store.WriteDayBuckets/QueryDayBuckets not yet called from indexer — wiring deferred until calendar UI (Lua API mcp:scheduled)
- [ ] O35: Lua APIs not yet registered: mcp:scheduled, mcp:reschedule, mcp:tagComplete, mcp:fileStatus, mcp:subscribe (R893-R898)
- [ ] O36: Event payload does not yet carry IsScheduledFire flag (R878) — publisher delivers same Event type for both scheduled fires and tag changes
- [ ] O37: Gap detection (R890-R892) not implemented — comparing @ark-event-fired: in log vs @ack: in source file
- [ ] O38: No unit tests for: ParseAcks, AckCoversDate, WriteDayBucketsForFile, handleScheduleSearch, handleScheduleChange, CheckScheduleConfig, cmdSchedule
- [ ] O39: handleScheduleChange uses strings.Replace matching trimmed value in untrimmed line — fragile for lines with unusual leading content
- [ ] A23: R980 (calendar virtual items from recurrence specs) — deferred to Lua/UI work
- [ ] O40: No unit tests for write actor: enqueueWrite, startNextWrite, ScanAsync, RefreshAsync
- [ ] O41: R1066: deferred-schedule pattern (pendingSchedule/DrainSchedule) not yet removed — schedule I/O still deferred rather than running in write goroutine
- [ ] O42: No unit tests for editor endpoints: handleSearchGrouped, handleTagComplete, handleTagValues, handleSave, handleSetTags
- [x] O43: handleTagValues reads files to extract values — O(files) I/O on each completion request. Store tag values in LMDB during indexing for O(1) lookup when this becomes slow.
- [ ] O44: handleSave allows writing to any indexed file — no authorization check. Acceptable for local use; revisit if ark ever serves untrusted clients.
- [ ] A24: R1098 (CORS) — same-origin, no explicit headers needed for localhost. Revisit if editor loaded from file:// origin.
- [ ] O45: No unit tests for V record Store methods: UpdateTagValues, AppendTagValues, RemoveTagValues, QueryTagValues, TagValueFiles
- [ ] O46: RemoveTagValues scans all V keys to find one fileid — O(total V records). Add reverse index (VF prefix) if profiling shows this is slow.
- [ ] A25: R1107 (V records rebuilt by ark rebuild) — rebuild already regenerates T/F/D; V follows same pattern, no separate design artifact needed
- [ ] A26: R1112 (Lua mcp:tagComplete should use V records) — deferred until Lua-side tag completion is implemented
- [ ] O47: R1115: WithAppendChunkCallback not yet wired in append paths — tags still extracted from tagWindowForAppend (R1127). Wire when microvec supports incremental chunk updates
- [ ] O48: Editor JS bundle not in release pipeline — Makefile must copy ark-markdown-editor.js to zip-graft for ark install
- [ ] O49: No unit tests for content fetching: handleContentFetch, handleContentView, handleContentRaw, contentPath
- [ ] A27: handleContentView reads file even for markdown (only needs path validation) — acceptable, keeps contentPath shared
- [ ] O50: No unit tests for content view/edit toggle: renderMarkdownForContent, contentLinkRewriter, ink-mde integration
- [ ] A28: R1200-R1215, R1222-R1224 (tag search panel UI) — TypeScript-only, no Go CRC card. Traced to specs/tag-search-panel.md
- [ ] A29: R1255-R1265 (spectral search UI — two-phase results, toggle, throttling) — TypeScript-only, no Go CRC card. Traced to specs/spectral-search.md
- [ ] O51: No unit tests for handleShowInFolder endpoint
- [ ] O52: No unit tests for Librarian: QueueExpand, DrainPending, WaitForRequest, WaitForResult, FuzzyMatchTags, fuzzyMatch, fuzzyMatchWords, trigrams
- [ ] O53: No unit tests for expansion CLI subcommands: --wait, --fuzzy, --search, --result, --error
- [x] D7: R1317-R1325 (use vs mention filtering for tag embeddings) — designed in spec and requirements but not yet implemented in ExtractTagValues or EV record writing
- [x] D8: R1317-R1325 (use vs mention filtering) — four heuristics: no preceding space, odd quote count (all strategies), fenced code blocks, indented code (markdown only). Skip mentioned tags during extraction — no V, T, F, or EV records.
- [ ] O54: Tag search panel UI not yet implemented (TagSearchWidget fires search but has no panel to display results)
- [ ] O55: innerHTML for non-markdown previews — sanitize if content is untrusted
- [ ] O56: No search deduplication/cancellation for in-flight requests
- [ ] A30: R1329 (packaging) is a build concern — documented in design.md cross-cutting, no CRC card needed

- [ ] A31: R1368-R1371 (package structure) — build/config concern, no CRC card needed
- [ ] A32: R1374-R1376 (extraction scope — what stays in markdown-editor) — verified by absence of move, no design artifact needed
- [ ] O57: No unit tests for Store.MatchTagNames, Store.MatchTagValues
- [ ] O58: No unit tests for TagContainsChunkFilter or tag-contains mode in BuildChunkFilters
- [ ] O59: No integration test for resolveTagContainsQuery end-to-end (structured tag query → regex search)
- [ ] O60: No unit tests for wrapTagElements — tag pattern matching, idempotency, HTML attribute avoidance
- [ ] O61: ark-search-element.js symlink not in install/release pipeline — must be added to Makefile like ark-markdown-editor.js
- [ ] A33: R1500 (markdown path unchanged) — verified by absence, no design artifact needed
- [ ] A34: R1502 (/raw/ unchanged) — verified by absence, no design artifact needed
- [ ] A35: R1503 (/fetch unchanged) — verified by absence, no design artifact needed
- [ ] O62: No unit tests for DB.AllChunks
- [ ] O63: No unit tests for chunked content rendering in handleContentView
- [ ] O64: No unit tests for ChunkStats, NewTokenizer, printChunkStats
- [x] O65: Embedding model not tracked in DB — switching tag_model in ark.toml silently mixes vectors from different models. Store model filename in LMDB, detect mismatches on startup.
- [ ] O66: No unit tests for DiffConfig, ApplyConfigChanges, config recover, server startup gate
- [ ] O67: No unit tests for cmdFiles --status, --detail, matchBaseSet, percentileInts
- [ ] O68: No unit tests for Store EC/EF record methods: WriteChunkEmbedding, WriteChunkEmbeddingBatch, ReadChunkEmbedding, WriteFileCentroid, ReadFileCentroid, ScanFileCentroids, RemoveFileChunkEmbeddings, DropChunkEmbeddings
- [ ] O69: No unit tests for Librarian BatchEmbedChunks, embedWithTierCtx, flushBucket, multi-context ensureModel/unloadModel
- [x] O70: Crash-orphan centroid drift: if EC records are written but EF centroid write is interrupted, the centroid will be stale on next run. The seeded-from-EF path trusts efCount, missing any orphan EC records beyond that count
- [ ] O71: Per-chunk ReadChunkEmbedding for partially-embedded files is O(N) LMDB reads. Could use prefix scan with EC+fileID to batch-check existing chunk indices
- [ ] O72: AllChunks internally calls CheckFile — a variant accepting pre-resolved fileID would eliminate one redundant lookup per file in BatchEmbedChunks
- [x] O73: PDF chunker: paragraph gap threshold (1.5x) too strict for tightly-spaced documents like cover letters
- [x] O74: PDF chunker: CJK text extraction unverified with seehuhn library
- [ ] O75: PDF chunker: no test design (test-PDFChunker.md)
- [ ] O76: Overlay search broken for all tmp:// documents (pre-existing, not PDF-specific)
- [x] O77: PDF chunker: truncated PDFs with recoverable xref tables fail seehuhn's strict xref parser (error: 'xref: table at byte N: strconv.ParseInt: parsing "trailer <<"'). pdftotext and other tolerant parsers recover these. Currently surfaced as one log line per scan via FileChunks; the empty-file-set doesn't help because the files are non-empty. Needs a fallback parser strategy.
- [x] O78: PDF chunker: contiguous heading lines produce separate heading chunks instead of being merged into one. Resumes with multi-line section titles fragment.
- [x] O79: PDF chunker: heading + following paragraph should merge into one chunk (spec R1632/R1633 says 'a heading and the body text following it form a heading chunk' but the implementation splits on heading/body transition). Search UX suffers when many short heading-only chunks surface as long, thin result rows.
- [ ] A36: R1624, R1625, R1626, R1627, R1628, R1629, R1631, R1632, R1634, R1636 (PDF span-level extraction and structure detection) — superseded by pdftext. R1624 → R1729 (library swap); R1625–R1634, R1636 → R1730 (pdftext's Blocks replace our in-house structure detection)
- [ ] A37: R1652, R1653, R1654, R1655, R1656, R1657, R1658, R1660 (byte-stream salvage codepath) — superseded by R1734 (pdftext emits Salvage as a BlockKind inline with structured blocks; no separate in-ark salvage path)
- [ ] A38: R1661, R1662, R1663, R1664 (blank-line filtering for ONLYOFFICE-style PDFs) — superseded by R1729 (line-level layout handling is pdftext's responsibility)
- [ ] A39: R1669, R1674 (tag rect from line spans, first-line-only for wraps) — superseded by R1735, R1736 (tag scan uses Block.Chars; rect unions all covered glyph BBoxes including wrapped lines)
- [ ] A40: R1723, R1657, R1658 (salvage at page 0 with no rect) — superseded by R1737 (salvage keyed at actual page, carries Block.BBox)
- [ ] O80: No unit tests for parseFilterStack, formatFilterStack, or -parse output
- [ ] O81: No unit tests for files mode in BuildChunkFilters
- [x] A41: -about as chunk filter (AboutChunkFilter) not yet implemented — requires embedding model in filter path. -about works as primary search only.
- [x] O82: About chunk filter requires configured embed_cmd/query_cmd in ark.toml — currently tag_model is set (Librarian path) but query-time embedding path is not. vec.Search fails silently.
- [ ] O83: About filter in CLI local path (no server) silently skips — Librarian only available when server is running
- [ ] A42: R547-R562 (ark vec bench) superseded by R1790-R1801 (ark embed subcommands)
- [ ] A43: R1302-R1305, R1587 (ark embed flat flags) superseded by R1790-R1801 (ark embed subcommands)
- [ ] A44: R1834 (old EC key format superseded by R1833) — no design artifact needed
- [ ] A45: R1861 (R1598/R1607 superseded by R1833/R1849) — no design artifact needed
- [ ] A46: R1817-R1829, R1832 (embed dedup high-water tracking) superseded by R1847 (chunkID dedup)
- A47: R1868-R1872 are deferred or scope-negative requirements from the microfts2-abi-catchup migration: they declare what this catch-up does NOT do (use Locator field, implement AppendAwareChunker, consume FileIDCount.Count, etc.). No inline code coverage is expected; the deferrals are owned by the chunkid-tag-store migration. The migration spec body documents the intentional non-implementations.
- T1: R1598 retired by R1833 (EC key moved from (fileID, chunkIdx) to chunkID via ec-rekey migration)
- T2: R1600 retired by R1836 (WriteChunkEmbedding signature changed to single chunkID arg via ec-rekey)
- T3: R1601 retired by R1838 (ReadChunkEmbedding signature changed to single chunkID arg via ec-rekey)
- T4: R1607 retired by R1849 (EC deletion on re-index moved to microfts2 callback path (per-chunkID); see also R1850 R1851)
- T5: R1802 retired (Orphan EC check reworded post-rekey: chunkID with no C record in microfts2 (per R1855); old fileID/chunkIdx framing obsolete)
- T6: R1803 retired (EF/EC count mismatch reworded post-rekey: count must match unique chunkIDs in file's F-record list (per R1857); old per-file EC framing obsolete)
- T7: R1804 retired (Missing EC check reworded post-rekey: chunkIDs with C records but no EC record (per R1856); old per-file framing obsolete)
- T8: R1099 retired by R1281 (V record key gained trailing tvid varint via tag-embeddings work (pre-migration-workflow); landed in tag-embeddings.md)
- T9: R1110 retired by R1309 (Direct (tag,value) lookup is no longer possible without tvid; prefix scan now returns the one record with tvid in key suffix (pre-migration-workflow))
- T10: R1083 retired by R1876 (T count semantic shifted from files-with-tag to chunks-with-tag (chunkid migration))
- T11: R126 retired by R1899 (File removal no longer touches F directly; orphan-chunkid callback drives F/V/T cleanup)
- T12: R1100 retired by R1873 (V record value: packed chunkids instead of fileids)
- T13: R1281 retired by R1873 (V key kept its tvid suffix; value semantic shifted fileids→chunkids)
- T14: R1311 retired by R1875 (F record value layout preserved but key shifted from F[fileid][tag] to F[chunkid][tag])
- T15: R1312 retired by R1899 (Cleanup driven by orphan-chunkid callback, not file-removal F-scan)
- T16: R1313 retired by R1900 (Per-chunkid V cleanup, not per-fileid)
- T17: R1314 retired by R1900 (removeFileidByTvids removed; orphan-chunkid callback handles cleanup)

- T18: R2 retired (2026-04-28 microvec-to-ec-search)
- T19: R5 retired (2026-04-28 microvec-to-ec-search)
- T20: R32 retired (2026-04-28 microvec-to-ec-search)
- T21: R39 retired (2026-04-28 microvec-to-ec-search; covered by R1832-R1845 EC path)
- T22: R40 retired (2026-04-28 microvec-to-ec-search; chunkid is the embedding key now)
- T23: R42 retired (2026-04-28 microvec-to-ec-search; covered by R1899-R1901 callback cleanup)
- T24: R44 retired (2026-04-28 microvec-to-ec-search; covered by R1899-R1901 callback cleanup)
- T25: R1125 retired (2026-04-28 microvec-to-ec-search; no microvec append path)
- T26: R1897 retired (2026-04-28 microvec-to-ec-search; no vec.AddFile)
- T27: R7 retired by R1911 (2026-04-28 microvec-to-ec-search)
- T28: R30 retired by R1912 (2026-04-28 microvec-to-ec-search)
- T29: R48 retired by R1915 (2026-04-28 microvec-to-ec-search)
- T30: R52 retired by R1916 (2026-04-28 microvec-to-ec-search)
- T31: R1116 retired by R1913 (2026-04-28 microvec-to-ec-search)
- T32: R1906 retired by R1926 (2026-04-28 microvec-to-ec-search)
- [ ] O84: EmbedCmd/QueryCmd config fields are vestigial post-microvec removal — should be deleted from Config, Store I records, and CLI flags
- T33: R1927 retired (2026-04-28 about-multi-search amendment; only one request kind (top-K) needed)
- T34: R1933 retired (2026-04-28 about-multi-search amendment; centroid + chunk filter use different shapes (threshold vs top-K))
- A48: R523, R524 are negative requirements (no changes to microfts2 API / fsnotify coordination) — un-anchorable by design. Their fulfillment is the absence of changes, not the presence of an artifact.
- T35: R701 retired (bigram strategy removed)
- T36: R702 retired (bigram strategy removed)
- T37: R808 retired by R853 (schedule moved to ark.toml config + day-bucket indexing (R853-R855))
- T38: R826 retired by R868 (scheduler reads day buckets at startup, not subscription-triggered (R868-R870))
- T39: R827 retired by R853 (zero overhead unless tag is in ark.toml [schedule])
- T40: R828 retired by R868 (scheduler fires to all listening sessions, not per-subscription)
- T41: R1028 retired (day bucket LMDB indexing replaced by month buckets (obsolescence marker for R866))
- T42: R1029 retired (TF reverse index for deletion no longer needed (obsolescence marker for R871))
- T43: R1030 retired (TD JSON array no longer needed (obsolescence marker for R911))
- T44: R1031 retired (ack status embedded in day buckets no longer needed (obsolescence marker for R912))
- T45: R1032 retired (dayBucketsFromLogFile no longer needed (obsolescence marker for R1019))
- T46: R1042 retired (@ark-event-fired: log entries no longer needed for gap detection (obsolescence marker for R870))
- [x] I1: R1951 (shared tvid map) — to be resolved when crc-TvidMap.md lands. Subpoint 3 specifies the shared resolver (`specs/tvid-map-overlay.md`, R1953–R1969); TmpTagStore moves from `(tag, value)` strings to tvids in the same change.