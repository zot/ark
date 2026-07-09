# Ark Design

Orchestration layer over microfts2 (trigram) and the Librarian/EC
embedding pipeline (vector). Digital zettelkasten with hybrid search.

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
instead of calling DB methods directly. bbolt handles its own
concurrency via MVCC; the actor protects the Go-side caches above
it (pathCache, pathToID, frecordCache). Watcher mutations use
fire-and-forget (Svc). HTTP/CLI operations use synchronous calls
(SvcSync). The former reconcileLoop merges into the actor.
Call direction is always session → DB, never the reverse.

**Read/Write Separation:** Reads execute directly in the actor and
return immediately (bbolt MVCC provides consistent snapshots). Writes
are queued and processed one at a time in a goroutine: Copy() creates
a cache-less DB copy, the goroutine indexes off the actor, then sends
a reconcile closure back. The actor invalidates caches, commits, and
dequeues the next write. Config files (ark.toml) bypass the queue and
index synchronously in the actor. See seq-write-actor.md.

### Index Lifecycle
microfts2 owns the `*bbolt.DB`. Ark opens microfts2 first
(which opens the database), then opens its own `ark` bucket; the Store
and the Librarian (EC chunk embeddings) share that `*bbolt.DB` (R1910).
The `ark` bucket lives alongside microfts2's `fts` bucket in the same
file — no separate vector bucket (R2975). Closing follows reverse order.

### File Identity
microfts2 allocates fileids and is the single source of truth for
path→fileid mapping. The ark bucket references files by fileid;
chunk embeddings (EC records) are keyed by chunkid (R1914).

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
add and refresh. Stored in the ark bucket with T (global count)
and F (per-file count) prefix keys. Tag names are the portion between
@ and :, stored lowercase. The tag vocabulary file (`~/.ark/tags.md`)
documents definitions using `@tag: name -- description` format.

### Tag Source Parity (R2344)
Tags reach the index from three sources: **inline** (T/F/V records in
the index, extracted from chunk text), **ext-routed virtual** (ExtMap entries
written by `@ext:` directives, originating in inline files via X records
or in tmp:// documents via overlay routings — both merge into the same
in-memory maps), and **tmp:// overlay** (TmpTagStore mirror of T/F/V
semantics for tmp:// content). A read API that enumerates tag names, tag
values, tag counts, or per-target tag sets unions all three. The only
exception is tag definitions (D records), which are structurally inline-
only — virtual and overlay tags have no defining text. Explicitly inline-
only read APIs (e.g. `Store.TagsForChunk`) name themselves as such and a
parallel all-sources variant (e.g. `Store.AllTagsForChunk`) exists for
the canonical union. Referenced from `crc-Store.md`, `crc-ExtMap.md`,
and `crc-TmpTagStore.md`.

### Chunk/File Retrieval
Search results can include content via `--chunks` (JSONL per chunk)
or `--files` (JSONL per file, deduplicated). This enables the
permission end-run: indexed content emitted without per-file prompts.
Content is read from disk at query time using offsets from microfts2.

### Embedded UI Engine
Frictionless runs in-process via the `flib` facade package
(`github.com/zot/frictionless/flib`), which wraps the ui-engine.
`cfg.Server.Dir` points to `~/.ark/`, where UI assets (html/, lua/,
viewdefs/, apps/) coexist with ark data (index.db, ark.toml,
ark.sock). Two listeners: the unix socket serves both ark API and
Frictionless `/api/*` routes (via `flib.RegisterAPI`); the HTTP port
(written to `ui-port`) serves the browser UI. If the ui-engine fails
to start, the ark API server continues — UI is optional. On shutdown,
`flib.Shutdown()` runs before the `*bbolt.DB` closes.

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
- [x] crc-DB.md → `db.go`, `locator.go`, `compact.go`, `util.go`
- [x] crc-Config.md → `config.go`
- [x] crc-Matcher.md → `match.go`
- [ ] crc-TagMatcher.md → `tagmatch.go`
- [x] crc-Store.md → `store.go`
- [x] crc-Scanner.md → `scanner.go`, `emptyfiles_test.go`
- [x] crc-Indexer.md → `indexer.go`, `ext.go`
- [x] crc-ExtMap.md → `extmap.go`
- [x] crc-Searcher.md → `search.go`
- [x] crc-Server.md → `server.go`, `watcher.go`, `recall.go`
- [x] crc-CLI.md → `cmd/ark/main.go`, `dm.go`, `verbose.go`, `cmd/ark/chats.go`, `connections_doc.go`
- [x] crc-CLITree.md → `cmd/ark/main.go`, `cmd/ark/connections_cli.go`, `cmd/ark/monitoring_cli.go`, `cmd/ark/embed_cli.go`, `cmd/ark/discussed_cli.go`, `cmd/ark/tag_cli.go`, `cmd/ark/config_cli.go`, `cmd/ark/schedule_cli.go`, `cmd/ark/message_cli.go`, `cmd/ark/ui_cli.go`, `cmd/ark/pubsub_cli.go`, `cmd/ark/flat_cli.go`, `cmd/ark/search_cli.go`, `cmd/ark/bloodhound_cli.go`, `cmd/ark/ext_cli.go`
- [x] crc-TagBlock.md → `tagblock.go`
- [x] crc-Session.md → `session.go`
- [x] crc-SearchCmd.md → `server.go`, `session.go`
- [x] crc-PubSub.md → `pubsub.go`
- [x] crc-EventScheduler.md → `scheduler.go`
- [x] crc-TmpTagStore.md → `tmp_tag_store.go`
- [x] crc-TvidMap.md → `tvid_map.go`
- [x] crc-TagVerify.md → `cmd/ark/main.go`, `verify.go`
- [x] crc-TagInspect.md → `cmd/ark/main.go`, `inspect.go`, `server.go`, `store.go`, `extmap.go`
- [x] crc-Curation.md → `curation.go`, `server.go`
- [x] crc-Librarian.md → `librarian.go`, `embed.go`, `connections.go`, `recall.go`
- [x] crc-LlamaLibs.md → `llamalibs.go`
- [x] crc-RecallWatcher.md → `recall_watcher.go`, `recall_watcher_cli.go`
- [x] crc-RecallAgentBuilder.md → `recall_agent_builder.go`, `recall_agent_handlers.go`, `recall_next.go`, `server.go`, `cmd/ark/main.go`, `.claude/skills/recall/SKILL.md`
- [x] crc-RecallAgent.md → `.claude/agents/ark-recall-agent.md`, `.claude/skills/ark/recall-agent-guard.sh`, `.claude/skills/ark/ark-recall.md`
- [ ] crc-Monitor.md → `monitoring.go`, `cmd/ark/main.go`, `server.go`
- [x] crc-LuhmannCLI.md → `monitoring.go`, `cmd/ark/main.go`, `server.go`, `recall_next.go`

### Sequences
- [x] seq-add.md → `scanner.go`, `indexer.go`, `store.go`
- [x] seq-search.md → `search.go`
- [x] seq-server-startup.md → `server.go`, `scanner.go`, `indexer.go`
- [x] seq-cli-dispatch.md → `cmd/ark/main.go`, `server.go`
- [x] seq-cli-urfave.md → `cmd/ark/main.go`, `cmd/ark/connections_cli.go`, `cmd/ark/monitoring_cli.go`, `cmd/ark/embed_cli.go`, `cmd/ark/discussed_cli.go`, `cmd/ark/tag_cli.go`, `cmd/ark/config_cli.go`, `cmd/ark/schedule_cli.go`, `cmd/ark/message_cli.go`, `cmd/ark/ui_cli.go`, `cmd/ark/pubsub_cli.go`, `cmd/ark/flat_cli.go`, `cmd/ark/search_cli.go`
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
- [x] seq-rebuild-read-serve.md → `cmd/ark/main.go`, `server.go`, `db.go`
- [x] seq-editor-endpoints.md → `server.go`, `search.go`
- [x] seq-tag-value-index.md → `store.go`, `indexer.go`, `server.go`
- [x] seq-content-fetching.md → `server.go`
- [x] seq-filter-stack.md → `cmd/ark/main.go`, `server.go`, `search.go`
- [x] crc-PDFChunker.md → `pdfchunker.go`
- [x] seq-spectral-expand.md → `librarian.go`, `server.go`
- [x] seq-tag-embed.md → `librarian.go`, `store.go`, `server.go`
- [x] seq-suggest-tags.md → `librarian.go`
- [x] seq-chunks-for-tag.md → `librarian.go`
- [x] seq-hot-correlations.md → `librarian.go`, `store.go`
- [x] seq-chunk-embed.md → `librarian.go`, `store.go`, `server.go`, `config.go`
- [x] seq-pdf-chunk.md → `pdfchunker.go`
- [x] seq-empty-file-skip.md → `scanner.go`, `db.go`, `emptyfiles.go`
- [x] seq-pdf-chunk-retrieval.md → `pdfchunker.go`, `store.go`
- [x] seq-embed-validate.md → `cmd/ark/main.go`, `store.go`
- [x] seq-tmp-tag-overlay.md → `db.go`, `store.go`, `tmp_tag_store.go`, `indexer.go`
- [x] seq-tvid-overlay.md → `tvid_map.go`, `store.go`, `tmp_tag_store.go`, `db.go`
- [x] seq-ext-routing.md → `extmap.go`, `indexer.go`, `store.go`
- [ ] seq-tag-verify.md → `cmd/ark/main.go`, `verify.go`, `extmap.go`, `store.go`, `db.go`
- [ ] seq-tmp-subscription.md → `pubsub.go`, `db.go`, `server.go`
- [x] seq-find-connections.md → `connections.go`, `server.go`, `cmd/ark/main.go`
- [x] seq-find-connections-substrate.md → `connections.go`, `connections_substrate.go`, `server.go`, `cmd/ark/main.go`
- [ ] seq-recall.md → `cmd/ark/main.go`, `server.go`, `recall.go`
- [x] seq-discussed.md → `cmd/ark/main.go`, `server.go`, `recall.go`, `store.go`
- [ ] seq-derived-tags.md → `recall.go`, `store.go`, `cmd/ark/main.go`, `server.go`
- [x] seq-recall-watcher.md → `recall_watcher.go`, `indexer.go`, `server.go`, `cmd/ark/main.go`
- [x] seq-recall-agent.md → `recall_watcher.go`, `recall_agent_builder.go`, `recall_agent_handlers.go`, `recall_next.go`, `server.go`, `cmd/ark/main.go`, `.claude/agents/ark-recall-agent.md`
- [ ] seq-ext-author.md → `db.go`, `ext.go`, `server.go`, `extmap.go`, `indexer.go`, `cmd/ark/ext_cli.go`
- [ ] seq-suggest-locator.md → `db.go`, `server.go`
- [ ] seq-luhmann-supervisor.md → `cmd/ark/main.go`, `monitoring.go`, `server.go`, `recall_agent_builder.go`
- [x] seq-bloodhound-cli.md → `cmd/ark/bloodhound_cli.go`, `recall_watcher.go`, `recall_agent_builder.go`, `recall_next.go`, `server.go`, `cmd/ark/monitoring_cli.go`
- [ ] seq-subscriber-presence.md → `pubsub.go`, `recall_watcher.go`, `recall_agent_builder.go`, `server.go`
- [ ] seq-chimes.md → `scheduler.go`, `server.go`, `config.go`, `pubsub.go`
- [ ] seq-spec-change.md → `scheduler.go`, `config.go`, `indexer.go`, `server.go`
- [ ] seq-tmp-audit-trim.md → `scheduler.go`, `db.go`

### CRC Cards (TypeScript — Ark Search Component)
- [x] crc-SearchAPI.md → `ark-search/src/search-api.ts`
- [x] crc-ArkSearchElement.md → `ark-search/src/ark-search-element.ts`, `pdf-chunk/src/pdf-host.ts`
- [x] crc-ArkTagElement.md → `install/html/content-markdown.html`, `install/html/content-plain.html`
- [ ] crc-CuratePinButton.md → `install/html/content-markdown.html`, `install/html/content-plain.html`
- [x] crc-PdfChunkElement.md → `pdf-chunk/src/pdf-chunk-element.ts`

### CRC Cards (TypeScript — Tag Overview)
- [ ] crc-TagOverviewSidebar.md → `tag-overview/src/tag-overview.ts`
- [ ] crc-ArkExtTagsElement.md → `tag-overview/src/tag-overview.ts`

### Sequences (Tag Overview)
- [ ] seq-tag-overview-load.md → `server.go`, `tag-overview/src/tag-overview.ts`
- [ ] seq-tag-overview-click.md → `tag-overview/src/tag-overview.ts`

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
- [x] test-DateParseGuards.md → `scheduler_test.go`
- [x] test-Config.md → `config_test.go`
- [x] test-Matcher.md → `match_test.go`
- [x] test-Searcher.md → `search_test.go`, `search_tag_funnel_test.go`, `search_nildb_test.go`
- [x] test-Store.md → `store_test.go`
- [x] test-Tags.md → `indexer_test.go`, `store_test.go`, `ext_test.go`
- [x] test-ChunkRetrieval.md → `search_test.go`
- [x] test-TagBlock.md → `tagblock_test.go`
- [x] test-Sweep.md → `db_test.go`
- [x] test-TagDefEmbed.md → `store_test.go`
- [x] test-SuggestTagNames.md → `librarian_test.go`
- [x] test-ChunksForTag.md → `librarian_test.go`
- [x] test-HotCorrelations.md → `librarian_test.go`, `store_test.go`
- [x] test-VectorFreshness.md → `store_test.go`
- [x] test-TmpSubscription.md → `pubsub_test.go`, `tmp_subscription_test.go`
- [x] test-FindConnections.md → `connections_test.go`
- [x] test-FindConnectionsSubstrate.md → `connections_substrate_test.go`
- [x] test-Recall.md → `recall_test.go`
- [x] test-Discussed.md → `store_test.go`, `recall_test.go`, `cmd/ark/main_test.go`
- [x] test-DerivedTags.md → `store_test.go`, `recall_test.go`, `derived_tags_test.go`, `derived_flip_test.go`, `cmd/ark/main_test.go`
- [x] test-SurfaceCooldown.md → `store_test.go`
- [x] test-Secretary.md → `recall_secretary_test.go`
- [x] test-LuhmannCLI.md → `luhmann_next_test.go`
- [x] test-BloodhoundCLIFixer.md → `bloodhound_cli_test.go`
- [x] test-BloodhoundCLI.md → `cmd/ark/bloodhound_cli_test.go`
- [x] test-ChatTranscript.md → `cmd/ark/chats_test.go`
- [x] test-ConnectionsCLI.md → `cmd/ark/main_test.go`
- [x] test-TagSourceParity.md → `tag_source_parity_test.go`
- [x] test-Curation.md → `curation_test.go`
- [x] test-WatchCoverage.md → `watch_coverage_test.go`
- [ ] test-RecallWatcher.md → `recall_watcher_test.go`

### CRC Cards (Nano)
- [x] crc-Nano.md → `nano.go`
- [x] crc-NanoOllamaClient.md → `nano.go`
- [x] crc-NanoShellTool.md → `nano.go`
- [x] crc-NanoApprover.md → `nano.go`
- [x] crc-NanoSystemPromptBuilder.md → `nano.go`
- [x] crc-NanoSessionStore.md → `nano.go`
- [x] crc-NanoCLI.md → `cmd/ark/nano.go`
- [x] crc-NanoPicker.md → `cmd/ark/nano.go`

### Sequences (Nano)
- [x] seq-nano-run-loop.md → `nano.go`
- [x] seq-nano-repl-turn.md → `nano.go`, `cmd/ark/nano.go`
- [x] seq-nano-session-resume.md → `cmd/ark/nano.go`
- [x] seq-nano-shell-exec.md → `nano.go`

### Test Designs (Nano)
- [ ] test-Nano.md → `nano_test.go`
- [ ] test-NanoShellTool.md → `nano_test.go`
- [ ] test-NanoApprover.md → `nano_test.go`
- [ ] test-NanoSessionStore.md → `nano_test.go`
- [ ] test-NanoCLI.md → `cmd/ark/nano_cli_test.go`

## Gaps

- [x] O1: Test files not yet written: config_test.go, match_test.go, search_test.go, store_test.go, tags_test.go
- [ ] O2: serverClient TOCTOU race — probe can succeed but actual request fails if server dies between. Acceptable for v1
- A1: IndexBuilt field removed from StatusInfo during simplification — spec still mentions it, update spec
- A2: MissingRecord.FileID always serializes as 0 in stored JSON (populated from the index key on read)
- [ ] O3: Integration tests need live microfts2 + the Librarian/EC pipeline: merge/intersect (test-Searcher), FillChunks/FillFiles (test-ChunkRetrieval)
- [ ] O4: Fetch uses O(n) StaleFiles scan — add direct path lookup to microfts2 when performance matters
- [ ] O5: ark stop does not verify process is ark (PID rollover could kill wrong process) — check /proc/PID/cmdline on Linux
- A3: R187 (vector search) deferred to V4 — no design artifact needed until then
- A4: R214 (negative requirement — no separate lock file) — verified by absence, no design artifact needed
- [ ] O6: JSONL chunks flood search results — single conversation file produces hundreds of score-1.0 chunks, burying small .md files. Mitigated by --filter-files/--exclude-files filtering.
- A5: microfts2 WithOnly/WithExcept — implemented in microfts2 dependency. Searcher.resolveFilters uses WithOnly (negatives subtracted into the set); the about/centroid pre-filter (computeCentroidFilters) uses both WithOnly (Early) and WithExcept (Late, "without" rows)
- A6: R231 (no backward compatibility for --source/--not-source) — verified by removal, no design artifact needed
- A7: R235 (test for per-source add-include round-trip) — covered in test-Config.md
- [ ] O7: Shutdown race: signal handler calls db.Close() while reconcileLoop and watchLoop goroutines may still be running. watcher.Close() stops watchLoop, but reconcileLoop can still be mid-Scan/Refresh against the index. Need to close reconcileCh and wait for goroutine to drain before db.Close().
- [x] D1: R338-R339 (EnsureArkSource) — designed in crc-Server.md but not implemented. Server should ensure ~/.ark is a source on every startup, before reconciliation.
- [x] D2: R340 (RemoveSource guard for ~/.ark) — designed in crc-Config.md line 25 but not implemented. Config.RemoveSource should reject the ark database directory.
- [x] O8: ark install UI asset extraction not yet implemented — R276-R281 designed, bundle commands (R297-R318) implemented, install needs to call ExtractBundle
- [x] O9: ~/.ark/mcp shell script (R283) not yet created — needs adaptation of .ui/mcp pattern for ~/.ark/ paths
- A8: R294 (zip-graft allows layering without recompilation) — build process property, no code artifact
- A9: R296 (re-export CreateBundle/ExtractBundle from ui-engine) — upstream change in ui-engine/cli/exports.go, done
- A10: R303 (bundle is build-time command) — inferred, covered by R297 implementation
- A11: R319-R322 (Makefile asset pipeline) — Makefile infrastructure, not Go code
- [x] O10: Self-triggered ark.toml events — configMutate saves ark.toml, watcher fires, watchLoop reloads config + triggers a second reconcile. Harmless (idempotent) but wasteful. Could suppress with a short debounce or write-flag.
- [ ] O11: No tests for watcher/throttle — the throttle state machine is testable (inject clock, mock watcher channels) but untested
- [x] D3: Phase C (R360-R369, append detection) blocked on microfts2 — needs FileLength in N record, AppendChunks API, chunker offset support. Requests in ~/work/microfts2/UPDATES.md items 2-4
- [ ] O12: Append detection assumes clean chunk boundaries — all current strategies (lines, chat-jsonl) produce clean boundaries. For markdown strategy, derive boundary cleanliness from last chunk end vs file length. When unclean, implement back-seek from last chunk to find match point and WithReplaceFrom in microfts2.AppendChunks
- [ ] O13: AppendFile reads full file twice (once in DetectAppend for prefix hash, once in AppendFile for new bytes + full hash). Acceptable because savings come from avoiding re-chunking/re-indexing old content. Could optimize with hash state passing if profiling shows it matters.
- A12: R376-R381, R384 (markdown chunker) — implemented in microfts2 as MarkdownChunkFunc
- [x] D4: R415-R416, R541-R546 RegisterLuaFunctions — mcp:indexing(), mcp:search_grouped(), mcp:open() all registered. HTTP endpoints removed.
- A13: R423-R428 (MCP event pulse indicator) — pure Lua/CSS in Frictionless status bar, no ark Go code needed
- A14: R418 (browser reconnect on reload) — handled by ui-engine WebSocket reconnect logic, no ark code
- A15: R421-R422 (second tab detection) — ui-engine/Frictionless concern, not ark Go code
- [x] D5: R420 (preferred port on restart) — needs flib.Config.Port field in Frictionless upstream
- [ ] D6: R438-R439 (browser count) — flib.Runtime doesn't expose WebSocket connection count. UIStatus reports running/port/indexing but not browser count. Needs flib API addition.
- [x] O14: (resolved by yzma migration, see T159) SIGILL on Zen 2 (Steam Deck) was a gollama static-build artifact — its bundled llama.cpp compute graph used unsupported instructions. yzma sidesteps this entirely: it dlopens prebuilt llama.cpp shared libs provisioned by `ark embed install`, so the backend is selected at runtime rather than baked in at compile time.
- [ ] O15: No unit tests for SearchMulti — needs test with mock microfts2 DB or integration test
- [ ] O16: QueryTrigramCounts + BM25Func open two separate Views for overlapping data — could be one transaction with a BM25FuncFromQuery helper in microfts2
- [ ] O17: --proximity only works with --multi currently — spec says it composes with any search mode (R597)
- [ ] O18: No unit tests for Session actor — testable with injected clock/mock FTS
- A16: SearchCmd has no separate code file — command object is implicit in session closures (server.go, session.go). CRC card documents the concept.
- [ ] O19: No unit tests for tmp:// operations — AddTmpFile, RemoveTmpFile, onlyIfTmp probe, --no-tmp flag
- [x] O20: ark files does not yet list tmp:// documents (R671 — needs handleFiles update)
- [x] O21: ark status does not yet report tmp:// document count (R676)
- A17: R703 (QueryBigramCounts return type) — superseded, bigrams removed from microfts2
- A18: R704-R705 (bigrams on by default, rebuild needed) — superseded, bigrams removed
- A19: R706 (ark rebuild recreates v3 format) — superseded, DB reverted to v2
- A20: R707 (index size impact) — superseded, no bigram index
- A21: R717 (--unmatched implies request-only) — inferred, behavior falls out of the filter logic naturally
- [x] O22: R737: flib.Config needs Verbosity field to propagate ark verbosity to ui-engine cfg.Logging.Verbosity — requires cross-project change to frictionless/flib
- [ ] O23: No unit tests for SearchFuzzy — testable with mock microfts2 DB
- [x] C1: search_grouped mode dispatch — added "fuzzy" case (opts.Fuzzy=true, query stays). UI replaces "about" button with "fuzzy".

- A22: R753 (tagPattern/tagblock regexes unchanged) — verified by absence, no design artifact needed
- [ ] O24: Pubsub: SessionID on ScheduledEvent not used in fire() delivery — events fire to all sessions, not per-subscriber. Wire per-session filtering when needed.
- [ ] O25: Pubsub: writerID always empty string — self-notification exclusion (R798) cannot trigger. Wire actual session ID through indexer when session-aware indexing exists.
- [x] O26: Pubsub: ExtractTagValues only matches first tag per line on compound tags — subsequent tags in compound lines won't fire subscriptions. Needs RE2-compatible non-greedy or two-pass approach.
- [ ] O27: Pubsub: ErrorReporter uses nil — tmp:// append now available (R909), wire TmpErrorReporter to use it.
- [x] O28: Pubsub: Watchdog results not persisted — Watchdog() returns results but no caller writes them to tmp:// yet. Wire when tmp:// append lands.
- [ ] O29: Pubsub: No unit tests for PubSub, EventScheduler, Watchdog, or CLI commands
- [x] O30: Lua dead code: Source:makeNode() no longer called after mcp.listSource migration. Source._missingPaths also unused. Safe to remove.
- [ ] O31: Inherited new() on ui-engine prototypes captures root prototype in closure — creates Object type instead of target type. Workaround: use rawget to detect type-specific new(), fall back to session:create. See R846-R848.
- [ ] O32: No unit tests for mcp.listSource — testable with mock Config and DB.Missing()
- [ ] O33: No unit tests for scheduling: ParseDateValue, log file read/write, EnsureUpcoming, ScanScheduleLogs, crankForward
- [x] O34: (obsolete) day-bucket Store wiring abandoned — event management taken out of the DB; the indexer arms the in-memory priority queue via EnsureUpcoming. See schedule-record-only.md.
- [x] O35: Lua APIs now registered (2026-06-25): mcp.scheduled, mcp.reschedule, mcp.tagComplete, mcp.fileStatus (R893-R896, server.go:registerLuaFunctions); mcp.subscribe (R897-R898) was already live at server.go:5647
- [ ] O36: Event payload does not yet carry IsScheduledFire flag (R878) — publisher delivers same Event type for both scheduled fires and tag changes
- [ ] O37: R892 — no dedicated Lua/mcp API for gap-detection results. Gap detection itself IS implemented via the @check-gap: lifecycle: fire writes @check-gap:, ResolveCheckGapsFromAcks compares vs @ack: (AckCoversDate), ScanCheckGaps(7) surfaces unresolved within lookback → tmp://watchdog/missed-events at startup (server.go:399). R889/R890/R891 anchored there 2026-06-30 (rescued from this gap's stale "not implemented" wording). Open: is R892 satisfied by Franklin reading that tmp doc via the existing mcp:search/fetch Lua bindings (→ NATURAL), or does it want a dedicated gap binding? Deferred for Bill.
- [ ] O38: No unit tests for: ParseAcks, AckCoversDate, handleScheduleSearch, handleScheduleChange, CheckScheduleConfig, cmdSchedule
- [ ] O39: handleScheduleChange uses strings.Replace matching trimmed value in untrimmed line — fragile for lines with unusual leading content
- A23: R980 (calendar virtual items from recurrence specs) — deferred to Lua/UI work
- [ ] O40: No unit tests for write actor: enqueueWrite, startNextWrite, ScanAsync, RefreshAsync
- [ ] O41: R1066: deferred-schedule pattern (pendingSchedule/DrainSchedule) not yet removed — schedule I/O still deferred rather than running in write goroutine
- [ ] O42: No unit tests for editor endpoints: handleSearchGrouped, handleTagComplete, handleTagValues, handleSave, handleSetTags
- [x] O43: handleTagValues reads files to extract values — O(files) I/O on each completion request. Store tag values in the index during indexing for O(1) lookup when this becomes slow.
- [ ] O44: handleSave allows writing to any indexed file — no authorization check. Acceptable for local use; revisit if ark ever serves untrusted clients.
- A24: R1098 (CORS) — same-origin, no explicit headers needed for localhost. Revisit if editor loaded from file:// origin.
- [ ] O45: No unit tests for V record Store methods: UpdateTagValues, AppendTagValues, RemoveTagValues, QueryTagValues, TagValueChunks
- [ ] O46: RemoveTagValues scans all V keys to find one fileid — O(total V records). Add reverse index (VF prefix) if profiling shows this is slow.
- A25: R1107 (V records rebuilt by ark rebuild) — rebuild already regenerates T/F/D; V follows same pattern, no separate design artifact needed
- A26: R1112 (Lua mcp:tagComplete should use V records) — deferred until Lua-side tag completion is implemented
- [ ] O47: R1115: WithAppendChunkCallback not yet wired in append paths — tags still extracted from tagWindowForAppend (R1127). Wire when the append path gains incremental chunk re-embedding (EC records)
- [ ] O48: Editor JS bundle not in release pipeline — Makefile must copy ark-markdown-editor.js to zip-graft for ark install
- [ ] O49: No unit tests for content fetching: handleContentFetch, handleContentView, handleContentRaw, contentPath
- A27: handleContentView reads file even for markdown (only needs path validation) — acceptable, keeps contentPath shared
- [ ] O50: No unit tests for content view/edit toggle: renderMarkdownForContent, contentLinkRewriter, ink-mde integration
- A28: R1200-R1215, R1222-R1224 (tag search panel UI) — TypeScript-only, no Go CRC card. Traced to specs/tag-search-panel.md
- A29: R1255-R1265 (spectral search UI — two-phase results, toggle, throttling) — TypeScript-only, no Go CRC card. Traced to specs/spectral-search.md
- [ ] O51: No unit tests for handleShowInFolder endpoint
- [ ] O52: No unit tests for Librarian: QueueExpand, DrainPending, WaitForRequest, WaitForResult, FuzzyMatchTags, fuzzyMatch, fuzzyMatchWords, trigrams
- [ ] O53: No unit tests for expansion CLI subcommands: --wait, --fuzzy, --search, --result, --error
- [x] D7: R1317-R1325 (use vs mention filtering for tag embeddings) — designed in spec and requirements but not yet implemented in ExtractTagValues or EV record writing
- [x] D8: R1317-R1325 (use vs mention filtering) — four heuristics: no preceding space, odd quote count (all strategies), fenced code blocks, indented code (markdown only). Skip mentioned tags during extraction — no V, T, F, or EV records.
- [ ] O54: Tag search panel UI not yet implemented (TagSearchWidget fires search but has no panel to display results)
- [ ] O55: innerHTML for non-markdown previews — sanitize if content is untrusted
- [ ] O56: No search deduplication/cancellation for in-flight requests
- A30: R1329 (packaging) is a build concern — documented in design.md cross-cutting, no CRC card needed

- A31: R1368-R1371 (package structure) — build/config concern, no CRC card needed
- A32: R1374-R1376 (extraction scope — what stays in markdown-editor) — verified by absence of move, no design artifact needed
- [ ] O57: No unit tests for Store.MatchTagNames, Store.MatchTagValues
- [x] O58: No unit tests for TagContainsChunkFilter or tag-contains mode in BuildChunkFilters — both retired in the sigil-syntax migration (T65), no longer applicable
- [ ] O59: No integration test for resolveTagChunks + GroupTagChunks end-to-end (structured tag query → V-record chunkIDs → direct chunk lookup)
- [ ] O60: No unit tests for wrapTagElements — tag pattern matching, idempotency, HTML attribute avoidance
- [ ] O61: ark-search-element.js symlink not in install/release pipeline — must be added to Makefile like ark-markdown-editor.js
- A33: R1500 (markdown path unchanged) — verified by absence, no design artifact needed
- A34: R1502 (/raw/ unchanged) — verified by absence, no design artifact needed
- A35: R1503 (/fetch unchanged) — verified by absence, no design artifact needed
- [ ] O62: No unit tests for DB.AllChunks
- [ ] O63: No unit tests for chunked content rendering in handleContentView
- [ ] O64: No unit tests for ChunkStats, NewTokenizer, printChunkStats
- [x] O65: Embedding model not tracked in DB — switching the `[embedding] model` in ark.toml silently mixes vectors from different models. Store model filename in the index, detect mismatches on startup.
- [ ] O66: No unit tests for DiffConfig, ApplyConfigChanges, config recover, server startup gate
- [ ] O67: No unit tests for cmdFiles --status, --detail, matchBaseSet, percentileInts
- [ ] O68: No unit tests for Store EC/EF record methods: WriteChunkEmbedding, WriteChunkEmbeddingBatch, ReadChunkEmbedding, WriteFileCentroid, ReadFileCentroid, ScanFileCentroids, RemoveFileChunkEmbeddings, DropChunkEmbeddings
- [ ] O69: No unit tests for Librarian BatchEmbedChunks, embedWithTierCtx, flushBucket, multi-context ensureModel/unloadModel
- [x] O70: Crash-orphan centroid drift: if EC records are written but EF centroid write is interrupted, the centroid will be stale on next run. The seeded-from-EF path trusts efCount, missing any orphan EC records beyond that count
- [ ] O71: Per-chunk ReadChunkEmbedding for partially-embedded files is O(N) index reads. Could use prefix scan with EC+fileID to batch-check existing chunk indices
- [ ] O72: AllChunks internally calls CheckFile — a variant accepting pre-resolved fileID would eliminate one redundant lookup per file in BatchEmbedChunks
- [x] O73: PDF chunker: paragraph gap threshold (1.5x) too strict for tightly-spaced documents like cover letters
- [x] O74: PDF chunker: CJK text extraction unverified with seehuhn library
- [ ] O75: PDF chunker: no test design (test-PDFChunker.md)
- [ ] O76: Overlay search broken for all tmp:// documents (pre-existing, not PDF-specific)
- [x] O77: PDF chunker: truncated PDFs with recoverable xref tables fail seehuhn's strict xref parser (error: 'xref: table at byte N: strconv.ParseInt: parsing "trailer <<"'). pdftotext and other tolerant parsers recover these. Currently surfaced as one log line per scan via FileChunks; the empty-file-set doesn't help because the files are non-empty. Needs a fallback parser strategy.
- [x] O78: PDF chunker: contiguous heading lines produce separate heading chunks instead of being merged into one. Resumes with multi-line section titles fragment.
- [x] O79: PDF chunker: heading + following paragraph should merge into one chunk (spec R1632/R1633 says 'a heading and the body text following it form a heading chunk' but the implementation splits on heading/body transition). Search UX suffers when many short heading-only chunks surface as long, thin result rows.
- A36: R1624, R1625, R1626, R1627, R1628, R1629, R1631, R1632, R1634, R1636 (PDF span-level extraction and structure detection) — superseded by pdftext. R1624 → R1729 (library swap); R1625–R1634, R1636 → R1730 (pdftext's Blocks replace our in-house structure detection)
- A37: R1652, R1653, R1654, R1655, R1656, R1657, R1658, R1660 (byte-stream salvage codepath) — superseded by R1734 (pdftext emits Salvage as a BlockKind inline with structured blocks; no separate in-ark salvage path)
- A38: R1661, R1662, R1663, R1664 (blank-line filtering for ONLYOFFICE-style PDFs) — superseded by R1729 (line-level layout handling is pdftext's responsibility)
- A39: R1669, R1674 (tag rect from line spans, first-line-only for wraps) — superseded by R1735, R1736 (tag scan uses Block.Chars; rect unions all covered glyph BBoxes including wrapped lines)
- A40: R1723, R1657, R1658 (salvage at page 0 with no rect) — superseded by R1737 (salvage keyed at actual page, carries Block.BBox)
- [ ] O80: No unit tests for parseFilterStack, formatFilterStack, or -parse output
- [ ] O81: No unit tests for files mode in BuildChunkFilters
- A41: -about as chunk filter (AboutChunkFilter) not yet implemented — requires embedding model in filter path. -about works as primary search only.
- [x] O82: About chunk filter requires configured embed_cmd/query_cmd in ark.toml — currently `[embedding] model` is set (Librarian path) but query-time embedding path is not. vec.Search fails silently.
- [ ] O83: About filter in CLI local path (no server) silently skips — Librarian only available when server is running
- A42: R547-R562 (ark vec bench) superseded by R1790-R1801 (ark embed subcommands)
- A43: R1302-R1305, R1587 (ark embed flat flags) superseded by R1790-R1801 (ark embed subcommands)
- A44: R1834 (old EC key format superseded by R1833) — no design artifact needed
- A45: R1861 (R1598/R1607 superseded by R1833/R1849) — no design artifact needed
- A46: R1817-R1829, R1832 (embed dedup high-water tracking) superseded by R1847 (chunkID dedup)
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
- [ ] O85: ExtMap state mutation isn't transactional with the `db.Update` — if microfts2's commit aborts, the in-memory maps may be briefly inconsistent. Convergence is restored by ExtMap.Rebuild on next DB.Open. Acceptable per design (.scratch/EXT-V-F.md, multi-file batch convergence).
- [ ] O86: No tests for @ext storage layer — test-ExtMap.md and integration coverage of multi-set V, X record CRUD, and the canonical re-resolution flow not yet written
- [x] O87: @ext targets that resolve to overlay (tmp://) chunkids are logged-and-skipped in applyIndexExt. Full support needs (a) in-memory overlay E-records parallel to TmpTagStore — diagnostics that live and die with the session, since persisting them would outlive the context that produced them — (b) overlay-aware chunkFileID via Store.filesForChunk, (c) ExtMap state for overlay chunkids in chunkToTargets/fileidToTvids/targetToChunk, rebuildable from session state on startup. Tracked in PLAN.md.
- [ ] O88: No tests yet for @ext overlay (tmp://) target routing — covers four routing scopes, overlayRoutings/overlayValues lifecycle, lock-flap-free per-target dispatch, TmpTagStore→ExtMap cleanup hook, and the ark errors API. Belongs alongside O86.
- [ ] O89: ark errors CLI command not yet implemented. ExtMap exposes RecordOverlayError/AddOverlayError/OverlayErrors/ClearOverlayErrors per R2030; CLI surface (PLAN.md V2.5) needs to consume them via dump/clear/add subcommands.
- [ ] O90: Multi-source tvid_ext edge case predates overlay routing: two source chunks with identical @ext value text share a tvid_ext, so cleanup of one source incorrectly clears the other's contributions from targetToChunk and the overlay maps. Pre-existing — not regressed by overlay work — but visible in overlay flows when persistent and overlay sources happen to write the same @ext line.
- T47: R2077 retired (pdftext.Block.BBox already provides heading rects; PDFChunker already emits rect in chunk Attrs. No upstream pdftext change needed.)
- [ ] O91: Tag-overview routing emission regression: fresh @ext routings stopped registering after several restart + cleanup cycles in dev DB. Pristine pre-implementation binary has same behavior — accumulated index state, not code. Workaround: ark rebuild. Investigate if reproducible from clean state.
- [ ] I2: R2063 R2064 width persistence uses localStorage as Stage B substrate — HostAPI/I-record swap deferred (Go endpoint work needs its own mini-spec pass). Functional persistence works; per-machine instead of per-DB.
- [ ] I3: R2032-R2064 R2065-R2072 R2081-R2084 tag-overview frontend Rs are referenced in tag-overview/src/tag-overview.ts via range comments rather than per-Rn inline refs. Coverage exists; granularity coarser than minispec validate expects.
- [ ] O92: Touch peek (R2042, R2044) deferred — desktop hover peek lands; touch tap conflicts with row-click scroll. Needs separate tap target (long-press, peek-toggle icon) or mode-specific hit area.
- [ ] O93: R2082 PDF body indicator positioning is a stub — server emits <ark-ext-tags rect=...> but the bundle does not yet position the indicator over the <pdf-chunk> canvas at the rect coordinates. Heading-overlay positioning works; ext-tags overlay does not. Needs <pdf-chunk> coordination.
- [ ] O94: ExtRoutingsForTargetChunk per-chunk read txn — handleContentView and renderPdfChunksByPage call it inside a per-chunk loop, opening N read txns. Cheap individually; for 100+ chunk files worth batching to one txn per request via an ExtRoutingsForTargetChunks([]uint64, *DB) variant.
- [ ] O95: No automated tests for tag-overview frontend — verified manually via Playwright across modes, filter, category, resize, peek, autotrack. Vitest/Jest harness for tag-overview.ts plus a Go test for renderExtTagsBlock and assignSidebarIDs would lock in behavior.
- A49: R2103-R2107 are document-level requirements about specs/cli-commands.md itself (canonical inventory, exhaustive flag tables, verification target). They have no implementation in code — the spec is the artifact. Same pattern as record-formats.md, which is anchored in design but has no R-numbers.
- T48: R21 retired by R2133 (2026-05-04 pattern-anchoring (single/double slash semantics for filesystem-absolute vs source-anchored))
- T49: R342 retired by R2138 (2026-05-04 reconcile-sweep (cycle gains sweep step before scan))
- T50: R2134 retired (2026-05-04 shadow-rule-incoherent (include wins per R10, so blanket excludes are valid))
- T51: R2135 retired (2026-05-04 shadow-rule-incoherent)
- T52: R2136 retired (2026-05-04 shadow-rule-incoherent)
- T53: R2137 retired (2026-05-04 shadow-rule-incoherent)
- T54: R12 retired by R2143 (2026-05-04 default-replace-semantics (top-level patterns are defaults; per-source patterns replace them))
- T55: R13 retired by R2144 (2026-05-04 default-replace-semantics (per-source replaces, not adds))
- T56: R26 retired by R2145 (2026-05-04 default-replace-semantics (TOML keys renamed to default_include/default_exclude))
- [ ] O96: Librarian-level ED tests (rebuild regenerates ED, BatchEmbed writes ED for missing pairs) require a real GGUF model and are not run by 'go test'. Store-level ED tests in store_test.go cover R2151-R2162 at the store layer; the model-side path is exercised manually after each model swap.
- A50: R2246: Lua API mcp.sweepHotCorrelations() deferred — CLI subcommand 'ark sweep correlations' lands the invocation surface for 1E; the Lua wrapper will land alongside the curation view UI (Phase 1F) when the Lua binding is needed.
- A51: R2248: Cron-via-tag triggering deferred to a follow-up slice (the 'small extension' described in CURATION-VIEW.md). Sweep invocation in 1E is direct (CLI / Lua / Go); scheduler integration arrives separately.
- T57: R237 retired by R2271 (2026-05-11 chat-jsonl emit-all: lines without extractable text now emit raw-line chunks instead of being dropped)
- A52: R2306: (scope boundary) no event-history buffer; current state is in tmp:// doc tags at subscribe time, subscribers see live events only
- A53: R2307: (scope boundary) HTTP POST /subscribe and ark subscribe CLI surfaces are unchanged; they consume the centralized publish via R2281 without surface-contract changes
- A54: R2310: (scope boundary) compression is intra-batch only; events for the same (path, tag) in successive Listen batches are not coalesced
- A55: R2311: (scope boundary) subscribe-before-doc-exists is valid; events fire on first AddTmpFile after subscription registration
- A56: R2280: (scope/historical) internal callers that previously skipped publishing — bug surface closed by R2281 centralization, no separate code artifact needed
- A57: R2301: (lifecycle) server shutdown stops listening goroutines via existing flib shutdown path — no new explicit code, falls out of WithLua + listening-goroutine teardown
- A58: R2305: monitor 'since' semantics reuse existing Store.RecordSerial and Store.WalkRecordsSinceSerial (R2174-R2193); no new code in this slice
- A59: R2308: (scope boundary) no handle-based cancellation exposed from Lua — replace-by-(session,tag) via re-subscribe is the cancel; no code artifact
- A60: R2309: (scope boundary) Go PubSub.Subscribe/Cancel APIs unchanged — append semantics + value-pattern cancel preserved for HTTP and direct Go callers; no code change required
- A61: R2312: test-as-subscriber pattern — codified in test-TmpSubscription.md and exercised by the test files; no production-code artifact
- A62: R2314 ark-connections agent file lives at .claude/agents/ark-connections.md (external to Go code)
- A63: R2335-R2338 are scope-boundary requirements (deferred 1G work) — intentionally have no implementation
- A64: R2339-R2343 are test-only requirements covered by connections_test.go (validator scans non-test files for impl coverage)
- [x] O97: Tag source parity — unit tests deferred. Live verification via UI search 'shopping' in contains-name mode confirmed 2 expected hits. Methods touched: ListTags, TagCounts, QueryTagValues, FileTagValues, MatchTagNames, MatchTagValues, AllTagsForChunk plus ExtMap.VirtualTagNames/VirtualTagValues/RoutedTagsForChunk and TmpTagStore.TagNames/TagValuesForTag/TagCounts. Tests should cover each method picking up an ext-only tag, a tmp-only tag, and an inline tag with parity.
- T58: R1987 retired by R2366 (narrower forms formalized in R2365–R2379 (specs/at-ext-parsing.md grammar))
- T59: R1995 retired by R2380 (extByAnchor keys by BASE only — narrower stored alongside tvid_ext for resolve-time evaluation)
- [ ] I4: R2379 (workshop UI surfaces non-conforming range strings loudly) awaits the /ui-thorough pass — Go primitives already expose the needed state (locator falls back to bare or string-fallback when range is non-conforming, surfaced via mcp.suggestExtLocator).
- [ ] O98: R2402-R2403 ChunkTextByID does two index+file-read passes — ChunkInfo and ChunkText each call db.fts.NewChunkCache() which re-reads the underlying file. Acceptable at workshop cadence (per-card, low frequency); revisit if profiling shows this dominates.
- [ ] O99: R2408-R2409 SweepHotCorrelationsAsync frees the Lua VM but the inner closure still holds db.writing=true for the entire sweep (16s from-scratch). Pre-existing property of the sync sibling bridge — fix requires re-architecting SweepHotCorrelations to enqueue per-tag closures rather than running the whole sweep inside one enqueueWrite. Workshop UI mutations queue behind in-flight sweeps.
- [ ] O100: R2402-R2410 No unit tests yet for the four new bridges (mcp.chunkText / mcp.parseTagBlock / mcp.tmp_get / mcp.sweepHotCorrelationsAsync) and their backing Go methods (DB.ChunkTextByID, DB.TmpContent, Librarian.SweepHotCorrelationsAsync). Live verification deferred to the upcoming /ui-thorough pass when the workshop's slice B/C consumes them.
- [ ] O101: R2416-R2420 curate-pin inline JS and CSS duplicated between install/html/content-markdown.html and install/html/content-plain.html (~85 lines each). Extraction to install/html/curate-pin.js + curate-pin.css served from ~/.ark/html/ deferred; v1 keeps templates self-contained while the feature is exercised. Re-evaluate when a third content template arrives or when divergence appears.
- [ ] O102: R2415 handleContentView's new fileID lookup (db.fts.CheckFile(path)) plus the existing ChunkIDsForPath call each open a separate microfts2 read pair (CheckFile + FileInfoByID). A combined ChunkIDsAndFileIDForPath(path) helper would cut one read pair on the full-file and JSONL rendering paths. Workshop cadence makes the current cost acceptable; revisit if profiling shows content-view dominates.
- [x] O103: R2419 PDF chunks (rendered as <pdf-chunk> rather than <div class="ark-chunk">) currently get no curate-pin button. A parallel hook in pdf-chunk-element.ts would mirror the content-view selector. Out of scope for v1 of the curate buttons.
- [ ] O104: R2423 PDF chunk hover-outline shows transparent border by default and reveals dashed outline on :hover. The 1px outline may obscure tight ark-tag overlays at the page rect edges. If reports of cluttered overlap arrive, switch to a lighter affordance (focus ring on the pin button only, or rely on the existing ark-tag border on hover).
- [ ] O105: R2421-R2422 ark-curate-region positioning re-runs only on render() — no rescaleBand re-run hook yet. PdfChunkElement.handleResize switches scaleBand → re-renders; the regions get repositioned implicitly. If scaleBand-stable resizes start drifting visibly, expose positionRegions to the CSS-only resize path the same way positionHitRegions is.
- [ ] O106: R2428 PDF stale-index migration was manual (Bill removed all PDFs and let the watcher rescan after the fix landed). No automated detection or rebuild trigger for the missing-attrs state. A startup check that scans for PDFs with missing content_offset/content_len attrs and queues a refresh would prevent future installs from carrying the same staleness past an upgrade.
- [ ] O107: R2428 The microfts2 collectChunks dispatch policy 'prefer Chunker over FileChunker when both are implemented' is a footgun: any binary chunker that registers both interfaces (for tmp documents + indexed files) will silently drop FileChunks-specific persistence. PDFChunker's current fix is in-house — it makes Chunks persist too — but the underlying dispatch contract upstream remains a sharp edge for future binary chunkers. Worth filing a microfts2 request for either a marker interface (BinaryChunker / PreferFileChunker) or a dispatch policy that prefers FileChunker when both are implemented.
- [ ] O108: R2426-R2429 No unit tests yet. mcp.extractTagValues behavior (mid-chunk inline tags, @id lines, strategy plumbing) is exercised by the curation workshop UI but lacks a Go-side test. tagValueRegex post-colon gap regression test (an explicit assertion that '@e:\n@c: d' parses as two tags, not one) is missing. PDFChunker.Chunks persistence contract (sealPageBlob runs at index time regardless of dispatch interface) has no test either. Live-verified via 'ark search daneel' timing drop ~10 s -> ~0.4 s after fix + reindex.
- T60: R498 retired by R2431 (2026-05-18 inbox -project broadened)
- [ ] O109: R2432-R2440 No unit tests yet for the new value-aware ExtractResultTags or printTagsBabyFood printer. Suppression-flag combinatorics (hideName, hideValue, noValues/noChunks/noFiles) live-verified only. A small table-driven test (handful of SearchResultEntry fixtures, expected []TagResult; and printer cases for each cfg combination) would lock in the extraction shape and the bullet output.
- [ ] O110: R2433-R2434 ExtractResultTags uses 'markdown' strategy for ALL chunks regardless of source. Go/Lua/JSON chunks technically over-include @tag mentions inside comments (no fenced-code skip applies). Acceptable for an agent overview but a per-chunk strategy lookup would tighten correctness. SearchResultEntry would need either a Strategy field populated at FillChunks time or a fileID->strategy map passed through to the extractor.
- T61: R779 retired by R2458 (subscribe --value flag absorbed into sigil syntax (T:V / T=V / T~V))
- T62: R780 retired by R2458 (subscribe --value flag retired)
- T63: R788 retired by R2458 (subscribe --value cancel narrowing replaced by sigil cancel)
- T64: R1469 retired by R2442 (structured tag query (name_tokens/value_tokens/name_match) replaced by primary_tag_query sigil form)
- T65: R1470 retired by R2442 (tag-contains chunk-filter mode retired in favor of unified tag mode with sigil syntax)
- T66: R1472 retired by R2442 (client-side structured tag fields retired in favor of sigil-form primaryTagQuery)
- T67: R1473 retired by R2442 (tag-contains mode retired)
- T68: R1474 retired by R2442 (client name/value match modes encoded in sigil)
- T69: R2128 retired by R2445 (TokenizeTagValue removed; ValueContains uses strings.Fields on lowercased value)
- T70: R409 retired (orphaned numbering gap)
- T71: R413 retired (orphaned numbering gap)
- T72: R832 retired (orphaned numbering gap)
- T73: R833 retired (orphaned numbering gap)
- T74: R834 retired (orphaned numbering gap)
- T75: R1818 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T76: R1819 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T77: R1820 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T78: R1821 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T79: R1822 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T80: R1823 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T81: R1824 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T82: R1825 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T83: R1826 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T84: R1827 retired by R1848 (high-water tracking replaced by chunkID-based EC dedup)
- T85: R1828 retired by R1848 (incremental centroid seeding replaced by full recompute)
- T86: R1829 retired by R1848 (incremental centroid seeding replaced by full recompute)
- T87: R716 retired by R2484 (2026-05-19 --unmatched global pair lookup)
- [ ] D9: Nano test files not yet implemented: test-Nano.md, test-NanoShellTool.md, test-NanoApprover.md, test-NanoSessionStore.md, test-NanoCLI.md (carried over from nano-go's original D1)
- T88: R2515 retired (2026-05-20 nano-env-to-flags: OLLAMA_MODEL fallback dropped; model is set via -m only)
- T89: R2516 retired by R2561 (2026-05-20 nano-env-to-flags: OLLAMA_BASE_URL → --base-url)
- T90: R2517 retired by R2562 (2026-05-20 nano-env-to-flags: NANO_MAX_STEPS → --max-steps)
- T91: R2518 retired by R2563 (2026-05-20 nano-env-to-flags: NANO_APPROVE → --approve-all)
- [ ] O111: R2579 single-shared-View-txn not implemented: substrate helpers (EmbedQuery, ReadChunkEmbedding, ScanTagDefEmbeddings, ListTagDefs, SearchChunks, SearchFuzzy) each open their own txn. Refactor for a single shared txn once profiling justifies the perf cost.
- [ ] O112: 1G doc-body migration: apps/ark/curation.lua still parses legacy ## Themes / ## Shared Tag Candidates sections. The renderer emits both shapes in turbo mode (R2597) but the duplicate emission should be removed after a /ui-thorough pass switches the Lua workshop to the unified ## Proposals section.
- [ ] O113: ark-connections sidecar guard script (.claude/agents/ark-connections.md or equivalent) needs updating to allow sidecar-wait/fetch/result/error positional invocations; old --wait/--fetch/--result/--error flags now exit non-zero.
- T92: R2587 retired by R2643 (2026-05-22 trigram-normalize-jaccard)
- T93: R2621 retired by R2643 (2026-05-22 trigram-normalize-jaccard)
- [ ] O114: discussed-tags: deferred tests from test-Discussed.md — Lua sys.recall + sys.discussed bridge tests, ark.toml TTL config defaults/override/invalid warning, substrate-is-read-only-on-RD snapshot test. Behavior is exercised end-to-end via Go-level tests; add Lua tests when test-Recall.md's Lua harness pattern earns another instance.
- A65: RP/RPE/RR record prefixes reserved for LLM-driven definition proposals (ARK-STATE.md item 1, agent layer). This slice (item 2) covers statistical attach-proposals only (RC/RJ later re-keyed from chunkID+tagname to source_tvid+target_chunkid — R3058/R3059; RF stays chunkid-keyed). Definition proposals (tagname+value+definition keyed, with embedding + reasoning) require an LLM and land with the recall agent. Letters chosen now to avoid collision; record-formats.md notes the reservation.
- [ ] O115: Recall --propose substrate scan: derivation iterates every recalled chunk × every ED record to compute per-tag max cosine. For an N-chunk corpus with M tag values this is O(N·M) cosine compares per --propose invocation. Acceptable at current scale (Bill: "simple first") and amortized by RF freshness skip; revisit if interactive latency degrades when N·M grows. Vector index (HNSW or similar) is the obvious lever.
- [ ] O116: Lua bridge for AcceptDerived / RejectDerived not yet wired — Store API exists (per Bill's front-load-Go-for-UI preference, [[feedback_frontload-go-for-ui]]) and sys.recall surfaces proposed tags via the Lua bridge, but the write-side bridge (sys.acceptProposal / sys.rejectProposal or similar) lands in the next /ui-thorough pass alongside the curation-view accept/reject UI. No mini-spec/frictionless thrash needed when that pass starts.
- A66: R2699 — watcher relies on substrate self-exclusion (R2645); no code anchor by design (delegated non-feature)
- [ ] O117: RecallWatcher integration tests deferred — test-RecallWatcher.md lists ~15 pipeline scenarios (cooldown gate, similarity gate, mark-on-send, propose passthrough, source-dir whitelist, live config reload). Unit tests in recall_watcher_test.go cover pure helpers + SourceQualifies; pipeline scenarios need DB + librarian + chat-jsonl chunk scaffolding.
- T94: R2691 retired (turn-boundary firing makes a separate per-session cooldown redundant (2026-05-25 simple-recall revision))
- T95: R2697 retired by R2734 (per-chunk trigger replaced by turn_duration-armed debounce (2026-05-25 simple-recall revision))
- T96: R2702 retired by R2748 (2026-05-26 simple-recall v2 — curation-doc header @ark-secretary-work replaces DM @ark-recall-fire line)
- T97: R2703 retired (2026-05-26 simple-recall v2 — DM instruction block gone; bias-to-silence is now the recall agent's persona (R2769))
- T98: R2704 retired by R2749 (2026-05-26 simple-recall v2 — '## Recalled for chunk' DM section shape replaced by curation-doc '# Source Chunk:' + '## Candidate:')
- T99: R2707 retired (2026-05-26 simple-recall v2 — DM emission gone; Layer 1 @dm-subject pre-triage no longer applicable)
- T100: R2709 retired (2026-05-26 simple-recall v2 — DM instruction block gone; Layer 3 bias-to-silence is now the recall agent's persona briefing)
- T101: R2710 retired by R2763 (2026-05-26 simple-recall v2 — Layer 4 @ark-recall-acted instrumentation replaced by ~/.ark/monitoring/recall.jsonl)
- A67: The recall assistant/agent-persona control flow that is external to ark's Go codebase: R2890 (per-session secretary spawned + respawned by the session's own assistant via /recall, with session+nonce delivered in prompt + Task description), R2855 (the same assistant running the value-scoped ark-recall-result=<self> consumer subscription), R2866 (the /recall skill that starts both roles), and R2860/R2895 (the secretary persona that drives the loop and judges against the injected conversation). The contract is documented in specs/simple-recall.md; enforcement lives in the .claude/agents/* + skill files (ark-recall-agent.md, recall-agent-guard.sh, recall/SKILL.md), anchored via crc-RecallAgent.md. No Go CRC owns these. The loop machinery itself — subscribe / poll / fire-ordering / content / context-gate / conversation-inject — lives server-side in `ark connections recall next` (R2857/R2858/R2888–R2891, Go-owned in crc-RecallAgentBuilder), shrinking this external surface. (Originally scoped R2775/R2776 → retired T128/T129; daemon-loop reqs R2852/R2853/R2854 → retired T130/T131/T132; seam-3a retired the Luhmann-spawned-daemon reqs R2850/R2851 → R2890, T135/T136.)
- T102: R2700 retired by R2747 (2026-05-26 simple-recall v2 — composeDM call replaced by RecallCurationBuilder write)
- T103: R2701 retired by R2748 (2026-05-26 simple-recall v2 — DM recipient/subject/sender identity replaced by curation-doc head tags)
- T104: R2737 retired by R2749 (2026-05-26 simple-recall v2 — DM ## Recalled for paragraph grouping replaced by curation-doc # Source Chunk / ## Candidate)
- T105: R2738 retired by R2749 (2026-05-26 simple-recall v2 — DM section excerpt blockquote replaced by curation-doc Source Chunk excerpt)
- T106: R2694 retired (2026-05-26 simple-recall v2 — [recall].agent_cmd reservation retired; recall agent is invoked by the assistant via the Task tool, not by a configured command)
- T107: R810 retired by R2783 (2026-05-27 chime-convention (AddChime hardcoded 15m replaced by @chime-15m as one of six standard cadences routed through normal [schedule] path))
- T108: R853 retired by R2830 (2026-05-28 schedule-record-only: tag declaration via per-tag blocks)
- T109: R854 retired by R2831 (2026-05-28 schedule-record-only: default_duration moves to per-tag block)
- T110: R855 retired by R2833 (2026-05-28 schedule-record-only: IsScheduleTag now block-presence-based)
- T111: R856 retired by R2830 (2026-05-28 schedule-record-only: adding/removing tag means adding/removing block)
- T112: R858 retired by R2831 (2026-05-28 schedule-record-only: defaults table relocates)
- T113: R869 retired (2026-05-28 schedule-record-only: day buckets computed from specs, not from removed @ark-event-upcoming)
- T114: R874 retired by R2818 (2026-05-28 schedule-record-only: ScanScheduleLogs is audit-only)
- T115: R875 retired by R2819 (2026-05-28 schedule-record-only: queue populated by EnsureUpcoming pass exclusively)
- T116: R876 retired by R2818 (2026-05-28 schedule-record-only: no @ark-event-upcoming to convert)
- T117: R902 retired by R2819 (2026-05-28 schedule-record-only: EnsureUpcoming computes single next, not forward window)
- T118: R903 retired (2026-05-28 schedule-record-only: @ark-event-upcoming removed; use source @remove)
- T119: R904 retired (2026-05-28 schedule-record-only: @ark-event-upcoming removed; use ark schedule change + @add/@remove)
- T120: R905 retired (2026-05-28 schedule-record-only: @ark-event-upcoming removed; no duplicate check needed)
- T121: R924 retired (2026-05-28 schedule-record-only: no @ark-event-upcoming entry to update)
- T122: R957 retired by R2822 (2026-05-28 schedule-record-only: lifecycle is per-tag)
- T123: R958 retired by R2822 (2026-05-28 schedule-record-only: lifecycle is per-tag)
- T124: R959 retired by R2825 (2026-05-28 schedule-record-only: lifecycle=false fires through pubsub)
- T125: R960 retired by R2822 (2026-05-28 schedule-record-only: lifecycle is per-tag literal)
- [ ] O118: Malformed schedule values (ambiguous mm/dd, date+timezone-no-time) are silently skipped during the source-file scan (scheduler.go ParseDateValue callers return/continue on error). Previously they fired at midnight; now they don't fire at all. Could surface to tmp://watchdog/possible-typos so the user notices the typo. Not blocking — R2846 normalization rescues the common dash-form case; only genuinely malformed values are skipped.
- A68: @ark-event-start:/@ark-event-end: marker reads (scheduler.go:656/660) call dateparse.ParseLocal directly, bypassing parseDateTrimmingRaw's malformed-datetime guards (R2846-R2848). Intentional: these parse the scheduler's own machine-written canonical ISO markers, not user input, so the dash-typo and mm/dd-ambiguity guards don't apply. All user-facing parse paths (ParseDateValue, ExtractBounds, @ack:) do go through parseDateTrimmingRaw.
- T126: R2770 retired by R2853 (2026-05-29 retire-oneshot-recall — daemon guard allowlist adds subscribe/listen/context)
- T127: R2773 retired by R2854 (2026-05-29 retire-oneshot-recall — bootstrap skill is recall-loop.md, delegating to ark-recall.md)
- T128: R2775 retired by R2855 (2026-05-29 retire-oneshot-recall — assistant runs result subscription only; daemon owns curate)
- T129: R2776 retired by R2850 (2026-05-29 retire-oneshot-recall — daemon spawned once per generation by orchestrator, not per-fire by assistant)
- T130: R2852 retired by R2857 (2026-05-29 recall-next — agent subscribe+listen+fire-derivation absorbed into ark connections recall next)
- T131: R2853 retired by R2859 (2026-05-29 recall-next — guard allowlist shrinks to next+surface/recommend/close)
- T132: R2854 retired by R2860 (2026-05-29 recall-next — loop condenses to persona; recall-loop.md retired as loop driver)
- [ ] O119: R2872 SurfaceItem own-session gate has no dedicated unit test (needs DB-backed ChunkInfo); the pure predicate sessionFromJSONLPath is covered by TestSessionFromJSONLPath, and the gate is exercised by live recall runs
- T133: R2764 retired by R2874 (2026-06-01 recall-judgment: v2 reject-counter value superseded by v3 signed score)
- T134: R2683 retired by R2881 (2026-06-01 recall-judgment: monotonic-sticky RJ superseded by signed bidirectional axis)
- [ ] O120: ark connections clean has no CLI/handler-level test (RC/RD/RF/RJ/RM wiping in cmdConnectionsClean + handleConnectionsClean). Pre-existing; RM wiring mirrors RD exactly and the Store mechanism (Clear*SurfaceCooldown) is covered by test-SurfaceCooldown.md.
- [ ] O121: ark status -db omits the recall record family (RC/RD/RF/RJ via unrecognized two-byte prefix in recordPrefixOf -> lumped under unlabeled 'R'; HC under 'H'). Pre-existing; RM was added to recordPrefixOf + arkLabels in seam 2, but RC/RD/RF/RJ/HC remain invisible. Add their cases + labels in a status-db cleanup.
- T135: R2850 retired by R2890 (2026-06-01 secretary-pipeline: shared Luhmann-spawned daemon -> per-session assistant-spawned secretary)
- T136: R2851 retired by R2890 (2026-06-01 secretary-pipeline: Luhmann nonce delivery -> assistant delivers session+nonce)
- [ ] O122: Stubborn-recall-next deferred coverage: (1) redial (R2903) and session-keyed-subscription-collision (R2902) lack automated tests — redial is CLI-level and the collision is integration-level; the per-session fire dir-seed (R2901) is unit-tested (TestRecallWatcher_FireSeed). (2) RM surface-cooldown stays chunkid-keyed (resolved loc→chunkID JIT); re-keying it on path:range so the cooldown survives a rebuild is a separate record-format migration, not done here.
- T137: R2749 retired by R2898 (2026-06-02 stubborn-recall-next: curation doc references path:range, not chunkid)
- T138: R2751 retired by R2899 (2026-06-02 stubborn-recall-next: result doc references path:range, not chunkid)
- T139: R2752 retired by R2901 (2026-06-02 stubborn-recall-next: fire counter per-session, dir-seeded, not global)
- T140: R2756 retired by R2900 (2026-06-02 stubborn-recall-next: surface/recommend take -loc path:range, not -chunk N)
- T141: R2904 retired by R2913 (2026-06-03 content-transform rollback — trigram is full-text (tags kept), strip only at EC embed, dedup hash over original content)
- [ ] O123: R2913 test coverage: (1) full-text trigram keeps tags — an indexed chunk with a literal @tag: is found by FTS search; (2) BatchEmbedChunks skips an all-@tag (empty-after-strip) chunk. (1) is harness-only; (2) needs the embedding model. TestStripArkTags already covers the strip primitive.
- [x] O124: crc-Store.md UpdateTagValues/AppendTagValues/RemoveTagValues inline (Rxxx) refs still cite pre-chunkid-migration requirements (some retired: R1099/R1100/R1110/R1311-R1314). Signatures+prose corrected during the TagValueFiles->TagValueChunks reconciliation, but inline refs left as-is to avoid destabilizing minispec coverage. Reconcile to the live code refs (R1873-R1876/R1883 for Update, R1884/R1947 for Append, R1899/R1900/R1947 for Remove) in a focused pass after confirming how validate treats retired-requirement coverage.
- [x] O125: R1102 'Count of files with a given (tag,value) = number of varints' is semantically stale post-chunkid-migration: the V blob is a chunkid multi-set, so the varint count is chunk-contributions, not distinct files (and QueryTagValues' returned Count inherits this). Needs a deliberate semantic correction (files->chunk-contributions), distinct from the fileid->chunkid wording swaps already applied to R1101/R1103/R1105/R1143.
- [x] O126: Recall tag-axis part 2 interim ranking: tagAxisInternalK hardcoded at 50; trigramJaccardWithFloor's 0.1 query-coverage floor weakens the no-model tag-trigram leg on long inputs; normalizeCos's 0.5 floor lets tag-pulled chunks compete at >=0.5 in the single interim pool (Option A fold). All transient — part 3's 2x2 grid + R2912 [recall] config resolve them.
- [ ] O127: Recall 2×2 (part 3): specs/config.md mirror of the new [recall].per_cell_count key is deferred to the migration on-completion fold (recall-substrate-v3.md folding plan step 5). Design notes for tuning: opts.K caps the flat output at min(4×perCell, K); backfill is round-robin across cells (fairness) rather than strict global-score order.
- [ ] O128: Tag-axis tuning knobs (standing; informed by the now-landed R2909 per-cell logging): tagAxisInternalK hardcoded at 50 (top tag-values pulled per input); the shared trigramJaccardWithFloor 0.1 query-coverage floor can zero the tag-trigram leg for short values against long inputs (the vector leg compensates when a model is present). Revisit if the per-cell logs show distortion.
- [ ] O129: Recall chat sub-chunk locator (part 4): the snippet anchor (matched paragraph's first line, capped 60) resolves to the FIRST sub-chunk containing it; a rare collision (two paragraphs sharing that line) resolves to the first — the ext [N] modifier would disambiguate but is deferred with the offset locator (specs/future.md, R2914). Funnel embeds on-the-fly only with a model; no-model path is trigram-only. specs/recall.md algorithm/stencil fold deferred to migration on-completion (after part 5).
- [ ] O130: Part 5 EV leg: enrichProposedTags recomputes display scores for fresh-skip chunks via bestEDSim (ED-only), so an EV-leg-derived proposal shows its ED similarity (possibly 0) rather than its EV similarity. This-call derivations carry the correct EV-inclusive score; the propose decision itself is EV-correct. Display-only, fresh-skip path.
- [ ] O131: Ext-routing threads a raw nilable *bbolt.Tx through runExtRouting/runOverlayExtRouting/applyIndexExt/chunkFileID; a wrong nil reaches fts.ReadCRecord and panics (the R2915 crash). Enhancement: model the transaction as a scope object (Monadic Wrapper) constructed only inside an actor closure, so 'inside a txn' is non-nilable by construction and this whole bug class disappears. Also covers librarian.go flushNow, which today relies on a documented 'call me from the write goroutine' contract rather than a local guard.
- T142: R1302 retired by R1791 (2026-06-07 cli-urfave: ark embed text subcommand)
- T143: R1303 retired by R1792 (2026-06-07 cli-urfave: ark embed bench tags subcommand)
- T144: R1304 retired by R1792 (2026-06-07 cli-urfave: ark embed bench chunks subcommand)
- T145: R1305 retired (2026-06-07 cli-urfave: embed uses local Librarian via withDB, no server (was false))
- T146: R1587 retired by R1793 (2026-06-07 cli-urfave: ark embed bench --ctx subcommand flag)
- T147: R1621 retired by R1792 (2026-06-07 cli-urfave: ark embed bench chunks --parallel subcommand flag)
- T148: R1622 retired by R1792 (2026-06-07 cli-urfave: ark embed bench output (subcommand))
- T149: R2930 retired (2026-06-08 cli-urfave migration complete: legacyDispatch + migratedCommands name-routing deleted; root Action owns unknown-command handling)
- [ ] O132: Bloodhound integration tests deferred: the full OnAppend->jobs->dispatchBloodhound->task-doc path and next's bloodhound-before-curation dispatch priority (both kinds pending) are unit-covered piecewise (scanBloodhounds, RecallBloodhoundOpen/FindingItem/closeBloodhound round-trip) but not end-to-end. R2935/R2936/R2939.
- T150: R2933 retired by R2947 (2026-06-08 level-decoupling: bloodhound gated on its own @ark-bloodhound-result sub, not the shared activation gate)
- [ ] O133: Per-capability gate decoupling lacks an integration test: secretaryPresent/bloodhoundEnabled/ambientEnabled gate OnAppend recognition vs ambient-arming independently, but the helpers short-circuit to true when pubsub is nil (the test harness), so verifying 'bloodhound fires without ambient and vice versa' needs a pubsub+subscription harness. R2947/R2949.
- T151: R1274 retired by R2964 (2026-06-11 yzma-embedding: tag_model -> [embedding] model)
- T152: R1588 retired by R2965 (2026-06-11 yzma-embedding: embed_tiers -> [embedding] tiers)
- T153: R1595 retired by R2962 (2026-06-11 yzma-embedding: gollama context API -> yzma ContextParams)
- A69: R2971-R2972 (Makefile pure-Go CGO_ENABLED=0 build + cross-platform release target) — Makefile build/release infrastructure, not Go code (mirrors A11)
- [ ] D10: R2971/R2972 (CGO_ENABLED=0 build + frictionless-style release sweep) deferred — blocked on the LMDB->BBolt migration: lmdb-go links C in BOTH ark's store and microfts2. Makefile left untouched until that lands.
- [x] O134: yzma migration prose-supersede sweep done: residual tag_model/embed_tiers key names + gollama engine descriptions across the docs now reflect the yzma runtime-provisioning model (`[embedding] model`/`tiers`, `ark embed install` for prebuilt llama.cpp libs). Covers recall.md, derived-tags.md, config-tracking.md, vector-freshness.md, vec-bench.md, tag-embeddings.md (static-link note reworded), tag-def-embeddings.md, seq-tag-embed.md, main.md, and the CRC/seq/test design artifacts. Summary specs + ark.toml examples were reconciled earlier.
- [ ] O135: LlamaLibs model auto-fetch (R2969 permits but does not require fetching the GGUF when absent) not implemented — slim downloader fetches libs only; model is configured separately.
- [ ] O136: Rebuild read-only serve (R2984-R2990): no automated test yet — verified empirically (status/search return live + growing counts during rebuild ~30-66ms vs ~14s block; writes refused 503; exit-on-idle). Add a test for WaitWritesIdle and the read-only window.
- [ ] O137: No automated test for the NUL-byte embed guard (R2991/R2992): the path needs a loaded embedding model (GGUF + llama.cpp libs), unavailable in CI. Verified by construction (single tokenize chokepoint strips NUL before yzma) + live re-run of the rebuild that crashed.
- T154: R103 retired by R2975 (2026-06-17 lmdb-to-bbolt: LMDB subdatabase 'ark' -> bbolt 'ark' bucket)
- T155: R249 retired by R2982 (2026-06-17 lmdb-to-bbolt: status map-usage -> file size)
- T156: R251 retired by R2982 (2026-06-17 lmdb-to-bbolt: map-usage computation -> file size)
- T157: R2086 retired by R2981 (2026-06-17 lmdb-to-bbolt: CopyCompact -> bolt WriteTo)
- T158: R1911 retired by R2978 (2026-06-17 lmdb-to-bbolt: MaxDBs/subDB allocation removed (bbolt has no named-DB limit))
- T159: R1306 retired by R2969 (2026-06-17 yzma-embedding: gollama Vulkan/SIGILL-on-Zen2 moot — yzma provisions prebuilt libs)
- T160: R1307 retired by R2961 (2026-06-17 yzma-embedding: local gollama workspace dep retired — yzma binds llama.cpp at runtime)
- T161: R1308 retired by R2967 (2026-06-17 yzma-embedding: CPU gollama build moot — [embedding] backend selects cpu)
- A70: R3003 dispatch-via-proxyOrLocal exceptions (deliberate): cmdStatus + cmdFiles use a proxied-or-local if/else with shared output and fall-through state (convertible later, left for risk); messageInbox falls back to local when the proxy errors (proxyOrLocal would fatal); scheduleSearch is server-required with a non-fallback withDB side-effect; tagVerify refuses with os.Exit(2) when a server holds the index. All are guarded and hang-free.
- T162: R1243 retired by R1379 (2026-06-25 expand-to-curate rename — POST /search/expand renamed to /search/curate, mode field dropped)
- T163: R1244 retired by R1382 (2026-06-25 expand-to-curate rename — immediate curated-results return replaced by requestId + poll (GET /search/curate/result/{id}))
- T164: R1245 retired by R1378 (2026-06-25 expand-to-curate rename — all-in-one server-side pipeline replaced by separate curation step)
- A72: R1 (Ark is written in Go) — meta requirement, not code-anchorable
- [ ] I5: ~235 requirements have design coverage but no inline Rn code anchor (234 genuinely unanchored + R672 false positive) — the genuine anchoring/disposition bucket. Trajectory: 1015 raw → 700 (mini-spec v2.11.0 range + alternation harvest fix removed the tool artifact) → 658 (bucket-(a) anchored the 41 anchored-but-not-harvested refs, plus 2 bonus) → 652 (bucket-(d) implemented + anchored the mcp: D-gaps R893-R898) → 621 (bucket-(b) anchoring pass started: 31 pre-`Rn`-convention impls reclaimed — R3/R4 + 29 main.go command handlers) → 613 (bucket-(b) lane-2: anchored R1904/R933/R1596/R952, retired R1104/R1105/R1117/R1118 with confirmed-implemented overrides) → 608 (lane-2 C/D: A-gapped R1124/R1126 [A73, NEGATIVE], retired R1127→R1895, R1010→R2813, R1011→R2818) → 604 (lane-2 D: retired R979/R976/R977→R2818, anchored R1542 at db.go; R878 left as D-gap/O36) → 602 (E/F tail: anchored R1536 allocIDInTxn, R1762 parseSegmentsAttribute) → 596 (Parallel Indexing R517-R522 folded onto RefreshStale; Haiku-verified, Daneel-gated) → 573 (Search Filtering: all 23 dispositioned, all anchored — R215-R227 + R512-R516 cluster at resolveFilters, plus R228/R229/R943/R945/R946 at owning sites; R227's "WithExcept if only negatives" prose was an unrealized optimization, reconciled at source [search-filtering.md, R219/R227, seq-search, crc-Searcher] since resolveFilters always returns WithOnly(included); stage-1 verify now deterministic code [verify-feature.py], not a Haiku agent) → 551 (Tag Tracking: 20 anchored across indexer/store/db/server/tag_cli; R120 retired→R1874 [T218, chunkid-tag-store F re-key fileid→chunkid]; R130 A-gapped [A74, NEGATIVE — new-tags-emerge-by-use is non-enforcement, no positive site]; record-formats.md already chunkid-correct, but crc-Store.md:37-41 + seq-add.md:60 still name removed UpdateTags/RemoveTags, flagged as separate chunkid-migration residue) → 542 (Chunk Retrieval: all 9 anchored — R108/R115/R116 on FillChunks, R109/R111 on FillFiles, R112/R113 on ChunkResult/FileResult structs, R110 on the mutual-exclusion guard, R114 on the fill dispatch; flag rename --files→--file-content reconciled at source [requirements.md R109-R114, specs/search.md] — no released-doc drift, cli-commands.md was already correct; R112 schema startLine/endLine→range reconciled) → 530 (Config Mutation CLI: all 12 anchored, all clean — R148/R151 on Config.AddSource+AddInclude, R149 on AddInclude/AddExclude, R150 on RemovePattern, R157/R158 on ShowWhy [crc-Config]; R152-R156/R160 on the handleConfig* server handlers [crc-Server]; no retires/AGAPs/rewords) → 525 (Fetch: all 5 anchored, all clean — R161/R162/R163 on DB.Fetch [crc-DB: index-gate then ReadFile = read-approval], R164/R165 on handleFetch [crc-Server], R165 also on cmdFetch [crc-CLI: raw stdout half]) → 517 (Server Lifecycle: 7 anchored + 1 AGAP — R170/R171 on cmdServe, R172/R173/R174/R176 on cmdStop, R175 on the server.go SIGTERM handler; R174 [stop -f → SIGKILL] rescued from a false design-only flag; R177 [remove the defer os.Remove(pidPath)] banked A75 NEGATIVE — satisfied migration action, defer removed in V2.5, no positive site, invariant R176 anchored) → 510 (Fuzzy Search: all 7 anchored, all clean — R738 on DB.SearchFuzzy, R739/R741/R742/R743 on the runSearch primaryFuzzy dispatch [--fuzzy is a primary mode; positional query + filters/proximity/-k/--chunks/--file-content/--tags/--scores/--no-tmp compose via shared opts], R747 on Searcher.SearchGrouped opts.Fuzzy branch, R749 on handleSearch req.Fuzzy branch) → 502 (Enhanced Status: all 8 anchored, all clean — R252/R253/R254/R258 on DB.Status [chunk total, per-strategy tally, source count], R250/R255/R256 on printStatus [output order + MB/GB formatBytes], R257 on handleStatus) → 494 (Sessions: all 7 — R654/R655/R656 on the `ark search --session` flag [proxy-routed, server-required], and the SearchCmd cluster R649/R650/R651/R662 anchored as renamed→SearchOpts per Bill: SearchCmd never existed in code [pickaxe-confirmed; A16 already noted it conceptual], realized by SearchOpts [params+Session+Cache, built CLI/HTTP/Lua, →microfts2 searchConfig via With* in defaultSearchOpts], "submit to session"=Cache threading R1139/R1140; crc-SearchCmd.md + specs/sessions.md + seq-session-search.md all reconciled to realized state) → 456 (Source Monitoring: 36 anchored + 2 retired, 4 deferred per Bill — anchored Phase A ~/.ark source [R338/R339/R341 EnsureArkSource, R340 RemoveSource guard rescued from a false design-only flag], Reconcile lifecycle [R343 startup, R344 configMutate, R345/R346 reconcile() actor-queue dispatch, R347 doReconcile idempotent], Phase B watching [R348/R352-R357/R387/R389-R395 on watchLoop, R349 watchSourceDirs, R350 watchDirRecursive, R359 startup watch-before-reconcile, R388/R392 IsIndexable], Phase C append [R360-R362 DetectAppend, R364/R368 prepareRefresh, R365/R367 executeRefresh — AppendFile confirmed dead so live path anchored], R370 chat-jsonl rename; retired R369/R385→R2273 [T219/T220 — clean-boundary reporting realized by microfts2 AppendAwareChunker, not ark-side per-strategy; crc-Indexer refs removed + source-monitoring.md Phase C prose reconciled]; deferred R363/R366/R386 [append back-seek under open O12] + R371 [generic non-chat JSONL never built → D-gap] — disposition deferred per Bill 2026-06-27) → 427 (Multisearch: 29 anchored, 10 deferred per Bill — anchored multi-strategy search [R585/R591 *multi dispatch, R592 the --multi+about/regex/like-file guard at main.go:1186, R586/R604 buildStrategies, R587/R588/R589/R593/R594/R595/R596/R598/R601/R602 Searcher.SearchMulti, R603 SearchGrouped opts.Multi branch] + messaging-v2 inbox [R525 readInboxFields ark-request/ark-response identity, R530-R534/R539/R540 the inbox CLI post-filter flags --from/--all/--counts, R537/R538 --counts output, R501/R535/R536 the proxy-then-cold-start + --include-archived fetch]; deferred for Bill's call: messaging-v1 @msg cluster R495/R496/R497/R499/R500 [superseded by v2 @status/@ark-request, heir R525 now anchored — retire/reconcile candidate], passive reference tags R526/R527/R528/R529 [documented conventions, no code by design per R529 — A-gap/D-gap], R597 [--proximity "composes with any mode" overclaim: only --multi and --fuzzy honor opts.Proximity, not plain combined/split — reword or implement]) → 416 (Glob Sources: all 11 anchored, all clean — R194 IsGlob, R195/R201 AddSource [globs stored as-is, os.Stat skipped for glob patterns], R200 RemoveSource FromGlob guard, R196/R197/R198/R199/R203 ResolveGlobs [expand+diff, auto-add new dirs, MIA-flag-not-remove, orphan report, filepath.Glob post-tilde], R202 handleSourcesCheck, R204 doReconcile startup sources-check; R196/R201 rescued from false design-only flags) → 411 (Global Strategy Mapping: all 5 anchored, all clean — R205/R206/R207/R208 on StrategyForFile [merge global+per-source, longest-match tiebreaker, per-source precedence, "lines" default], R209 on AddFile [unregistered strategy rejected by microfts2 at scan time]) → 405 (Temporary Documents: 6 anchored, 5 deferred per Bill — anchored R665/R668 on AddTmpFile [in-memory overlay lifetime + tag counts, no LMDB], R683/R684 on defaultSearchOpts WithNoTmp [skips overlay, cheaper than WithExcept], R681 on the --session flag [always proxies], R693 on GetChunks [tmp:// handled inside microfts2]; deferred for Bill's call: onlyIfTmp protocol R677/R678/R679/R680 [superseded — nothing sets OnlyIfTmp; CLI always proxies when a server is up (main.go:1206), and server.go's OnlyIfTmp/204 path is now dead code — retire/reconcile + dead-code removal], R675 [--filter-files/--exclude-files matching tmp:// paths unverified: resolveFilters matches against persistent FileIDPaths, not the overlay — confirm or D-gap]) → 398 (Chat Transcript: all 7 anchored, all clean — R1044/R1048 cmdChats, R1050 findJSONLFiles, R1045/R1047/R1049 renderChat [❯/● markers, ⚙ tools, sidechain filter], R1046 printWrapped word-wrap; **harvest-trap found+fixed**: cmd/ark/chats.go was mapped to no Artifact, so minispec never scanned it for inline Rn refs — the anchors were present but didn't harvest. Added chats.go + connections_doc.go to crc-CLI.md's Artifacts mapping. Repo-wide audit (rederive scans all code, mapped or not): only R672 [known false positive — cites microfts2's R672] is hidden, so the remaining ~398 is real debt, not harvest artifact) → 310 (**ark core CLI feature — all 87 reqs dispositioned**: 86 anchored + R107 retired→R1571 [T221]. Anchored across server.go (HTTP routes R65/R89/R90-R102; serve lifecycle R61-R70 — socket-bind-is-the-lock Highlander, PID-outside-dir, exclusive bbolt lock, reconcile guard), cmd/ark/main.go (CLI command bodies R71-R88, search flags R53-R60, R51 filepath:range output, R29 defaultDB, R88 proxyOrLocal), match.go (glob semantics R9/R10/R15-R20 on Matcher.Classify/Match), config.go (R8/R11/R14/R22/R23/R25/R27 on Config/Source/validate/WriteDefaultConfig/LoadConfig), db.go (R28/R31/R33/R34 init+aliases, R35/R41/R43/R45 Add/Remove/Refresh, R24 ConfigPath, R63 exclusive open), store.go (R6 OpenStore ark bucket, R104-R106 missingKey/unresolvedKey/CleanUnresolved, R1571 IGet/IPut heir), librarian.go (R64 ensureModel warm), search.go (R46/R47 SearchCombined, R49/R50 merge, R53-R56 SearchSplit, R57 intersect), indexer.go (R36/R37/R38 AddFile); R107 [monolithic I-settings JSON blob, sourceConfig+dotfiles] retired→R1571 [per-field IGet/IPut; GetSettings/PutSettings gone] — heir anchored, test-Store.md round-trip + crc-Store.md reconciled. Clean-anchor batch, hybrid cadence — only R107 needed Bill's retire call; Bill stages) → 302 (**Chunk Embeddings — all 8 dispositioned**: R1602/R1603/R1606 on WriteFileCentroid/ReadFileCentroid/DropChunkEmbeddings, R1619 on ScanFileCentroids [centroid = sum/count], R1608/R1618 on the BatchEmbedChunks recompute loop [from-scratch re-sum; running-sum storage], R1604 on the inlined missing-detection [former MissingChunkEmbeddings() now a Pass-1 ReadChunkEmbedding check]. Two judgment calls (Bill): R1618 **reworded** [dropped the never-built O(1)-incremental/remove-subtract claim — git confirms no `sum -=` ever existed; kept running-sum storage + recompute, reconciled requirements.md + chunk-embeddings.md] then anchored; R1623 **retired→R3004** [T222 — file-level `efCount == len(chunkLens)` fast-skip never in any .go file, only req text; oversized-not-requeued goal met by R3004's per-chunk nil-sentinel EC]) → 295 (**Chunk Context Expansion — all 7 dispositioned**: R481/R482 on emitChunkResult [JSONL, one ChunkResult per chunk with path/range/content/index], R483/R484/R488 on db.go GetChunks [direct microfts2.GetChunks, opaque range label, positional order], R486 on FetchChunkContent [error if file not indexed]. R485 **reworded** [claimed "cold-start withDB, no server proxy needed" — code dispatches via proxyOrLocal: proxies to POST /chunks when a server holds the single-process index, else cold-starts; reconciled requirements.md + chunk-context.md + the crc-CLI.md `withDB` prose trap] then anchored at the cmdChunks dispatch) → 286 (**Tag Value Embeddings — all 9 anchored, all clean**: R1285/R1287/R1288 on the librarian.go tag-name embed block [hyphens→spaces, embedding inline in T via WriteTagNameEmbedding, no ET prefix], R1286/R1288 on the tag-value block ["tag: value", colon preserved], R1283 on store.go addChunkIDToVRecord [stable tvid — Lookup-reuse else alloc], R1282 on allocIDInTxn [next_tvid as I record], R1275/R1276 on the librarian.go model-path setup [dbPath-relative; empty/missing → modelPath "" → disabled, trigram fallback], R1279 on resetModelTimer [TTL→unloadModel, next query reloads]. No retires/rewords) → 276 (**Tag Extraction Fixes + Tag Pubsub — both fully dispositioned**, 12 reqs: 7 anchored, 3 retired, 2 D-gapped. Tag Extraction Fixes: R751 on tagRegex [matches anywhere, no `^`], R752 on tagDefRegex [keeps `(?:^|\n)` line-start], R757 on fileLevelTags [full refresh scans whole file]; R754/R755/R756 **retired→R1895** [T223/T224/T225 — the append `tagWindowForAppend` window-backup was removed, boundary handling moved to microfts2's chunker append protocol; tag-extraction-fixes.md "Append-detection tag boundary" section reconciled + crc-Indexer refs removed]. Tag Pubsub: R829/R830/R831 on the Publish @mute check [silences all events, before subscription matching, pubsub-only], R812 on ScanScheduleLogs [in-memory es.pushed cleared on restart, startup re-scan re-fires due]; R813 **D-gapped D11** [variable-date holiday Lua function — planned for ark's apps/ark/init.lua, not yet written; app-layer Lua, out of Go scope; pubsub.md section marked planned], R825 **D-gapped D12** [@ended: event-stop never in any .go file (git-confirmed), only req+spec; pubsub.md section marked planned/unimplemented]. The 2 D-gaps stay in the count as banked-not-pending) → 266 (**scheduling — 10 of 17 anchored, 7 deferred for Bill**: anchored R906 on the fireLogMutate log_cap trim [logs rotatable], R1009 on writeDateIndex [skips ~/.ark/schedule/* to prevent index cascade], R1012 on Config.MatchesScheduleFilterForTag [global excludes always apply, per-tag narrows], R1014 on the reconcile() onWriteComplete wiring [schedule processing deferred outside the DB actor, drained after scan, goroutine], R1015 on handleScheduleSearch [ParseDateValue = same date grammar as schedule tags], R1016 on scheduleParseAction, R1017 on ScheduleTagSummary [tags/defaults/lifecycle/filters], R1020 on util.go ParseDate [2006-01-02 15:04 form — **util.go newly mapped to crc-DB.md**, discharging the harvest-trap latent on it], R1021 on ReloadConfig [updates indexer.config], R1040 on crankForwardAndEnqueue [source spec authority, computed after @remove: exceptions]; all clean ANCHOR — the verifier's "design-only" flags on R1015/R1016/R1017 were false [it greps the literal help string, which lives only in .md]. **Deferred for Bill's call** (genuine judgment, the in-memory/query-time-vs-retired-design tension): R907/R908 [schedule logs are regular indexed ~/.ark files — property, anchor-at-writeDateIndex-skip vs AGAP-NATURAL], R912/R913 [index-time acked/ackText event array never built — no such struct fields exist; realized as query-time QueryRange @ack: merge — retire→R1041/R1043 or D-gap], R1006/R1008 [forward-window @ark-event-upcoming: materialization retired by T117/R2809/R2813 — R1006 retire→R2820; R1008 retire-or-reword: the bound-respect survives in crankForwardAndEnqueue but the upcoming-entry framing is dead], R1013 [tmp:// log path tmp://schedule/HASH.md superseded by R2824's tmp://schedule/TAG/SOURCE-ENCODED — retire→R2824 or reword]) → 261 (**Schedule Lifecycle — 5 of 12 anchored, 7 deferred for Bill**: anchored the @check-gap: gap-detection pipeline — R889/R891 on ScanCheckGaps [unresolved @check-gap: = staleness signal; lookbackDays default 7 at server.go:399], R890 on ResolveCheckGapsFromAcks [compare fired @check-gap: dates vs @ack: via AckCoversDate], R974 on ResolveCheckGap [ack resolution subscription-driven not polled; @check-gap: presence=unresolved, absence=handled] — these three **rescued from O37's stale "gap detection not implemented"** [O37 reworded to cover only R892's surfacing question]; R968 on fire()'s lifecycle=none branch [non-lifecycle tags publish but skip all log writing]. **Note:** the mid-line `CRC:` harvest trap bit again — `// Called at startup. CRC: …` did not harvest R891 [CRC: must lead the comment after `//`]; split onto its own line. **Deferred for Bill's call:** R878 [IsScheduledFire event-nature flag never built — confirmed D-gap O36; crc-EventScheduler `## Does` line 37 overclaims "Event carries IsScheduledFire=true", needs reconcile], R892 [gap-results surfacing — NATURAL via Franklin reading tmp://watchdog/missed-events through mcp:search/fetch vs a dedicated binding — O37], R963 [~/.ark/schedule-archive/ never built → D-gap], R964/R966/R967 [the @ark-event-upcoming: log-materialization lifecycle — convert-upcoming→fired, append-upcoming-NEXT, re-index-log→queue — all retired by R2809/R2813/R2818: the log is pure audit now, the in-memory queue is authoritative via armChunk/crankForwardAndEnqueue, and writeDateIndex skips the log (R1009) → retire→R2818/R2820/R2823], R978 [only-next-occurrence "materialized in the schedule log" — the single-next behavior is live but lives in the queue, not the log → reword→R2820 or retire]) → 258 (**Context Wrapping — 3 of 5 anchored, 2 deferred**: R178/R181 on printSearchResults [ark search --wrap <name> XML output; works with --chunks and --files] + R178 on the cmdFetch wrap block [fetch/files form], R180 on writeEscaped [</tag> → &lt;/tag> escaping keeps wrap XML valid]. Deferred for Bill: R179 [format spec says chunk form is `lines="start-end"` but code emits `range=%q` — same rename as R112's startLine/endLine→range; reword requirements.md + specs/search.md then anchor], R182 [memory/knowledge is an unenforced naming convention — --wrap takes any string → AGAP-NATURAL]) → 235 (**2026-07-01 clean-anchor search.md flag families + verbose/logging — 22 anchored + 1 retired, all Bill-approved where non-anchor**: Tag-only Search [R189 on the `if *tags` dispatch, R190/R193 on ExtractResultTags (tag vocab from results; ExtractTagValues = the indexing extractor), R192 on printTagsBabyFood (--scores → best-score in tag header); R191 **retired→R2433** [T226] — the flat "one tag per line with count" TSV output is superseded by tags-baby-food.md's markdown bullet tree (heir confirmed in code), search.md "Tag-only search" prose reconciled at source]; Scoring Strategy [R573/R579 on the `--score` mode switch (three modes + unknown→error+exit), R577 on the escalation `score=="" || "auto"` gate (auto-only), R578 on the --like-file always-density block]; File Similarity [R183/R184/R186/R188 on the like-file block in SearchSplit (query-time read, content-as-density-FTS, feeds split's FTS side intersected with --about vector), R185 on validateSearchFlags (mutual-exclusion with --contains/--regex)]; Verbose Flags [R730 on Logv (log.Printf → default-logger MultiWriter); R731-R734 **reworded to reality** — the spec's fixed per-level taxonomy (L1=lifecycle, L2=HTTP, L3=indexing, L4=full-values) was never honored (only Logv 0/1/2 ever called, no L3/L4 sites, server.go zero Logv); reframed as a graded dial (higher = strictly more verbose via the single `verbosity >= level` gate, no fixed category), reconciled verbose-flags.md §Behavior + requirements.md, anchored R731/R732 at representative Logv(1)/Logv(2) call sites (indexer.go executeFullRefresh / prepareRefresh) and R733/R734 at the verbose.go gate that realizes deeper tiers]; File Logging [R210-R213 on setupLogging: creates logs dir, truncates ark.log 10MB→last-1MB on startup, routes default logger through io.MultiWriter(stderr, file)]. Build + gofmt + validate clean) — see .scratch/impl-recs/lane2-table.md. NOT obsolete: the genuinely-superseded clusters from completed migrations are already retired (167 Tn entries). Residual is a mix of legitimately-AGAP top-level TypeScript web components (~214: <ark-search>, <pdf-chunk>, tag-overview, …), MORE pre-// CRC: Go ANCHOR impls still to reclaim (bucket (b), three lanes — see .scratch/IMPL-COVERAGE.md), and externals (microfts2/pdftext). Validate keeps surfacing these as the methodology tracking the debt; disposition proceeds by feature (PENDING #21 b). Analysis + regen recipe: .scratch/IMPL-COVERAGE.md; feature listing .scratch/impl-recs/UNREFERENCED-659.md
- T165: R489 retired by R525 (2026-06-25 messaging-v2 — @msg ack/close replaced by @status single-lifecycle (R525+))
- T166: R490 retired by R525 (2026-06-25 messaging-v2 — @msg ack/close replaced by @status single-lifecycle (R525+))
- T167: R491 retired by R525 (2026-06-25 messaging-v2 — @msg ack/close replaced by @status single-lifecycle (R525+))
- T168: R492 retired by R525 (2026-06-25 messaging-v2 — @msg ack/close replaced by @status single-lifecycle (R525+))
- T169: R493 retired by R525 (2026-06-25 messaging-v2 — @msg ack/close replaced by @status single-lifecycle (R525+))
- T170: R494 retired by R525 (2026-06-25 messaging-v2 — @msg ack/close replaced by @status single-lifecycle (R525+))
- T171: R1570 retired by R1571 (2026-06-25 config per-field I records — ArkSettings blob replaced by per-field iGet/iPut)
- T172: R1814 retired by R1791 (2026-06-25 embed-subcommands — ark vec bench removed, superseded by ark embed text)
- T173: R1815 retired by R1791 (2026-06-25 embed-subcommands — vecbench.go deleted, superseded by ark embed text)
- T174: R1816 retired by R1791 (2026-06-25 embed-subcommands — vec-bench supersession record, see R1791-R1801)
- T175: R1843 retired by R2115 (2026-06-25 ec-rekey — RemoveFileChunkEmbeddings removed, per-chunkid deletion)
- T176: R2867 retired by R2947 (2026-06-25 subscriber-presence superseded by per-capability gates)
- T177: R230 retired (2026-06-25 pre-T-gap removal record — --source/--not-source removed)
- T178: R543 retired (2026-06-25 pre-T-gap removal record — POST /search/grouped removed)
- T179: R544 retired (2026-06-25 pre-T-gap removal record — POST /open removed (converted from A71))
- T180: R545 retired (2026-06-25 pre-T-gap removal record — GET /indexing removed (converted from A71))
- T181: R879 retired (2026-06-25 event-out-of-DB — ark subscribe --scheduled/--recurring removed)
- T182: R880 retired (2026-06-25 event-out-of-DB — ScheduleMode type/constants removed)
- T183: R881 retired (2026-06-25 event-out-of-DB — ScanForSub removed)
- T184: R882 retired (2026-06-25 event-out-of-DB — RemoveForSession removed)
- T185: R1018 retired by R2819 (2026-06-25 event-out-of-DB — TD/TF day-bucket records removed; queue+log model)
- T186: R1019 retired by R2819 (2026-06-25 event-out-of-DB — WriteDayBucketsForFile removed; queue+log model)
- T187: R1022 retired by R2819 (2026-06-25 event-out-of-DB — day-bucket writes at DB open removed; queue+log model)
- T188: R1023 retired (2026-06-25 month buckets chucked — never landed; priority-queue + schedule-log model is current)
- T189: R1024 retired (2026-06-25 month buckets chucked)
- T190: R1025 retired (2026-06-25 month buckets chucked)
- T191: R1026 retired (2026-06-25 month buckets chucked)
- T192: R866 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T193: R867 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T194: R868 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T195: R870 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T196: R871 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T197: R872 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T198: R873 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T199: R911 retired by R2810 (2026-06-25 event-out-of-DB — TD/TF day-bucket record model removed; schedule-record-only log model (R2810+))
- T200: R914 retired by R1027 (2026-06-25 event-out-of-DB — schedule search no longer queries day buckets; QueryRange computes from specs+logs)
- T201: R920 retired by R1027 (2026-06-25 event-out-of-DB — ack now merged from source @ack: in QueryRange, not day-bucket record)
- T202: R929 retired by R2836 (2026-06-25 event-out-of-DB — config-change day-bucket re-materialization removed; priority queue re-arms (R2836))
- T203: R930 retired by R2836 (2026-06-25 event-out-of-DB — config-change day-bucket re-materialization removed; priority queue re-arms (R2836))
- T204: R931 retired by R2836 (2026-06-25 event-out-of-DB — config-change day-bucket re-materialization removed; priority queue re-arms (R2836))
- T205: R932 retired by R2836 (2026-06-25 event-out-of-DB — config-change day-bucket re-materialization removed; priority queue re-arms (R2836))
- T206: R934 retired by R1027 (2026-06-25 event-out-of-DB — @ack check during day-bucket write removed; ack merged at query time (QueryRange))
- T207: R935 retired by R1027 (2026-06-25 event-out-of-DB — DayBucketEvent acked field removed; ack merged at query time)
- T208: R1104 retired by R1884 (2026-06-26 chunkid migration (R1873-R1908): pre-chunkid append V-record req superseded by chunkid-keyed AppendTagValues)
- T209: R1105 retired by R1899 (2026-06-26 chunkid migration (R1873-R1908): pre-chunkid remove V-record req superseded by orphan-chunkid V cleanup)
- T210: R1117 retired by R1904 (2026-06-26 chunkid migration (R1873-R1908): per-chunk callback value extraction now indexedCallback)
- T211: R1118 retired by R2913 (2026-06-26 chunkid migration (R1873-R1908): per-chunk callback def extraction obsolete; defs now file-level via fileLevelTags)
- T212: R1127 retired by R1895 (2026-06-26 chunkid migration (R1873-R1908): R1127 claimed append tag extraction unchanged via tagWindowForAppend, but R1895 removed that helper — append tags now ride the callback)
- A73: R1124, R1126 — NEGATIVE/removal reqs (splitChunks removed from executeFullRefresh; prepareRefresh no longer extracts tags for full refresh — full-refresh tags ride the callback). Current truth verified by absence; no positive code site to anchor. Source: chunk-callback.md (carries superseded banner).
- T213: R1010 retired by R2813 (2026-06-26 schedule migration (006): @ark-event-upcoming log marker eliminated — in-memory priority queue is authoritative)
- T214: R1011 retired by R2818 (2026-06-26 schedule migration (006): crank-forward materialization is now ScanScheduleLogs queue-arming via crankForwardAndEnqueue; @ark-event-upcoming marker gone (R2813))
- T215: R979 retired by R2818 (2026-06-26 schedule migration (006): startup missed-events materialization subsumed by ScanScheduleLogs queue-arming (R2818); no missed-events surfacing in current model)
- T216: R976 retired by R2818 (2026-06-26 schedule migration (006): filter-change scope re-eval subsumed by ScanScheduleLogs drop-out-of-scope (R2818a/b) + EnsureUpcoming arm (R2819))
- T217: R977 retired by R2818 (2026-06-26 schedule migration (006): lifecycle check-gap re-eval subsumed by ScanScheduleLogs check-gap scan (R2818e) + out-of-scope drop)
- T218: R120 retired by R1874 (chunkid-tag-store: F record re-keyed fileid->chunkid)
- A74: R130 — NEGATIVE: new tags emerge by use; the vocabulary file documents meanings, not enforces them. Tag extraction is pure tagRegex pattern matching with no allowlist/validation (any @word: becomes a tag). No positive code site to anchor — the property is the absence of enforcement. Same shape as A73. Source: tags.md.
- [ ] O138: crc-Store.md:37-41 and seq-add.md:60 describe removed methods UpdateTags(fileid)/RemoveTags(fileid) — chunkid-tag-store (migration 003) residue. F-record writes now go through writeChunkTagValuesInTxn / RemoveTagValuesInTxn (chunkid-keyed). Reconcile the CRC ## Does bullets and the seq-add step to the chunkid methods. Surfaced during Tag Tracking R120 retire (#21).
- A75: R177 (NEGATIVE -- migration action): 'remove the defer os.Remove(pidPath)' is a satisfied negative -- the defer was removed in V2.5 (os.Remove(pidPath) last seen ff1edb9/4c3d5b4); server.go now only writes the PID (server.go:154) and never removes it, so there is no positive code site by nature. Steady-state invariant R176 is anchored on cmdStop (server never removes PID; stale PIDs safe because ark stop verifies the process).
- T219: R369 retired by R2273 (2026-06-27 source-monitoring: chunker clean-boundary reporting realized by AppendAwareChunker (R2273), not ark-side per-strategy reporting)
- T220: R385 retired by R2273 (2026-06-27 source-monitoring: 'no chunker reporting needed' superseded by AppendAwareChunker (R2273) chunker-level boundary handling)
- T221: R107 retired by R1571 (2026-06-28 per-field-I-records — monolithic JSON settings blob (sourceConfig, dotfiles) replaced by per-field IGet/IPut)
- T222: R1623 retired by R3004 (2026-06-28 chunk-embeddings — file-level efCount==len(chunkLens) fast-skip never built; oversized-not-requeued goal met by R3004 per-chunk nil-sentinel EC)
- T223: R754 retired by R1895 (2026-06-29 tag-extraction — append tag-scan-window backup superseded; tagWindowForAppend removed, boundary handling moved to microfts2 chunker append protocol)
- T224: R755 retired by R1895 (2026-06-29 tag-extraction — widened-window-both-extractors superseded by tagWindowForAppend removal (R1895))
- T225: R756 retired by R1895 (2026-06-29 tag-extraction — tag-scan-window-vs-AppendChunks-bytes distinction moot after tagWindowForAppend removal (R1895))
- [ ] D11: R813: variable-date holiday computation — planned Lua function for ark's apps/ark/init.lua (each Frictionless app may optionally include an init.lua; ark's is not yet written). Would write a tmp:// file with @ark-event: tags at startup. App-layer Lua, outside Go mini-spec scope.
- [ ] D12: R825: @ended: [REASON] event-stop — designed but never implemented in Go (git-confirmed: only requirements.md + pubsub.md ever carried it). The scheduler does not skip chunks containing both a scheduled tag and @ended:.
- T226: R191 retired by R2433 (2026-07-01 tags-baby-food: -tags output changed from flat TSV tag\tcount to markdown bullet tree)
- T227: R1235 retired by R1380 (2026-07-03 spectral co-process removal: Haiku expansion is driven via the Gen-2 sidecar queue, not a spawned claude --print co-process)
- T228: R1236 retired (2026-07-03 spectral co-process removal: --system-prompt-file/--tools were co-process-only flags; the sidecar agent carries its own persona + guard)
- T229: R1237 retired (2026-07-03 spectral co-process removal: no co-process session to --resume; the sidecar agent is a persistent subagent)
- T230: R1238 retired (2026-07-03 spectral co-process removal: no per-expansion spawn / prompt-cache; the sidecar agent is warm)
- T231: R1239 retired (2026-07-03 spectral co-process removal: no co-process session / TTL)
- T232: R1240 retired (2026-07-03 spectral co-process removal: no co-process session cache)
- T233: R1241 retired (2026-07-03 spectral co-process removal: no claude invocation to fail/clear)
- T234: R1242 retired (2026-07-03 spectral co-process removal: no closure-actor co-process; the sidecar queue serializes via a plain mutex)
- T235: R1247 retired (2026-07-03 spectral co-process removal: no co-process to 503 on; unavailability gates endpoint registration via the R1248 PATH check)
- T236: R1253 retired (2026-07-03 spectral co-process removal: no spawn reads ~/.ark/searching/CLAUDE.md (now vestigial))
- T237: R1254 retired (2026-07-03 spectral co-process removal: no spawn for CLAUDE.md changes to take effect on)
- T238: R1268 retired (2026-07-03 spectral co-process removal: --system-prompt-file was a co-process flag)
- T239: R1269 retired (2026-07-03 spectral co-process removal: --tools disabling was a co-process flag)
- [x] O139: specs/spectral-search.md still describes the dead Gen-1 claude --print co-process (§The Librarian, §Endpoints, §Searching Directory, §Availability); supersede-at-source spec-prose fix DEFERRED per Bill 2026-07-03 (leave the spec intact until the Gen-2 sidecar is removed). The retired reqs (T227-T239) already forward, and the code is the sidecar, so the trap is low-risk. Reconcile when the sidecar is torn out.
- [ ] O140: Full spectral->bloodhound: remove the live Gen-2 expansion sidecar (Librarian ExpandRequest queue, /search/curate + /search/expand endpoints, cmd/ark/main.go:1632-1759 CLI family, ark-expansion agent, search-expansion skill), retire R1246/R1270-R1273/R1378-R1383/R1248-R1252 to the bloodhound, and fold+retire spectral-search.md. DEFERRED per Bill 2026-07-03 as its own slice (a real feature removal; bloodhound does not yet replace the tag-search-panel expansion role in code).
- [ ] O141: CLI-bloodhound result docs (tmp://BLOODHOUND-CLI-RESULT/<id>) linger until server restart — no clean signal for when the waiting CLI has read the result, so BloodhoundCLIAddDone removes the request doc but not the result doc. tmp:// is per-process (wiped on restart) and each doc is small, so this is minor; add a TTL/reaper if it ever matters. (bloodhound-CLI S4, R3027)
- [ ] O142: sweepRequests reap is pending-only (R3041): a request whose secretary crashes mid-hunt leaves its cliPool.requests entry + request doc until a server bounce — DeregisterPoolSecretary drops the secretary+inflight but not the request record, and the reap never scans in-flight/orphaned requests. Bounded (crash-only, rare) and cleared on bounce; mid-flight-hunt recovery is out of scope per R3034. Deliberate, not an oversight.
- [ ] O143: Multi-idea seed (R3043): the clue-split, K-scaling, and payload helpers (clueOf/seedInputs/seedK/resolveClue/buildSearchPayload) are unit-tested, and Recall's per-input union is its own existing coverage — but the end-to-end composition (paragraph A → chunk X, paragraph B → chunk Y, both in the unioned seed) is not tested against a live corpus. Disproportionate: needs a tuned fixture where distinct paragraphs match distinct chunks. Covered by its parts.
- [ ] O144: ext authoring DB/CLI/server layer (DB.SetExtTag/AddExtTag/RemoveExtTag, ark ext {set,add,remove}, POST /ext/*) has no automated test — the pure line-mutation logic (mutateExtLine/applyExtMirrorEdit) is fully unit-tested (R2395/R2396/R3047), but the DB methods write to the real ~/.ark home via arkHomeDir (hardcoded os.UserHomeDir+/.ark), so a DB-level test would pollute the live home or need HOME injection. Verified live 2026-07-07: cold (exclusive DB) + warm (server proxy) paths, all three verbs, multi-value add, exact-dup no-op, collapse-all set, value-filtered + all-value remove, zero residue after cleanup. (R3048, R3049)
- [ ] O145: Pass A @ext-candidate authoring glue verified live, not unit-tested: DB CandidateExtTag/AcceptExtTag/RejectExtTag file-I/O + resolveExtMirror path resolution, POST /ext/{candidate,accept,reject} handlers, and ark ext {candidate,accept,reject} CLI dispatch. Live-driven end-to-end (candidate w/ insight-first, accept drops insight, reject tag-name-only; spacey path; zero residue). Pure logic (parsing, class-aware mutation, transitions, line builders) is unit-tested in ext_test.go; the wrappers need a config-source + ~/.ark mirror fixture disproportionate to unit-test (O144 precedent for Layer 1).
- T240: R2664 retired by R3058 (2026-07-09 tag-derived-subsystem)
- T241: R2665 retired by R3059 (2026-07-09 tag-derived-subsystem)
- T242: R2673 retired by R3070 (2026-07-09 tag-derived-subsystem)
- T243: R2674 retired by R3075 (2026-07-09 tag-derived-subsystem)
- T244: R2678 retired by R3067 (2026-07-09 tag-derived-subsystem)
- T245: R2679 retired by R3071 (2026-07-09 tag-derived-subsystem)
- T246: R2680 retired by R3069 (2026-07-09 tag-derived-subsystem)
- T247: R2877 retired by R3069 (2026-07-09 tag-derived-subsystem)
- T248: R3053 retired by R3075 (2026-07-09 tag-derived-subsystem)
- T249: R2875 retired by R3075 (2026-07-09 tag-derived-subsystem)
- T250: R2876 retired by R3059 (2026-07-09 tag-derived-subsystem)
- T251: R2878 retired by R3070 (2026-07-09 tag-derived-subsystem)