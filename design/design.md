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

### Tag Source Parity (R2344)
Tags reach the index from three sources: **inline** (T/F/V records in
LMDB, extracted from chunk text), **ext-routed virtual** (ExtMap entries
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
- [x] crc-DB.md → `db.go`, `locator.go`
- [ ] crc-Config.md → `config.go`
- [x] crc-Matcher.md → `match.go`
- [ ] crc-TagMatcher.md → `tagmatch.go`
- [x] crc-Store.md → `store.go`
- [x] crc-Scanner.md → `scanner.go`
- [x] crc-Indexer.md → `indexer.go`, `ext.go`
- [x] crc-ExtMap.md → `extmap.go`
- [x] crc-Searcher.md → `search.go`
- [x] crc-Server.md → `server.go`, `watcher.go`, `recall.go`
- [x] crc-CLI.md → `cmd/ark/main.go`, `dm.go`
- [ ] crc-CLITree.md → `cmd/ark/main.go`, `cmd/ark/connections_cli.go`, `cmd/ark/monitoring_cli.go`, `cmd/ark/embed_cli.go`, `cmd/ark/discussed_cli.go`, `cmd/ark/tag_cli.go`, `cmd/ark/config_cli.go`, `cmd/ark/schedule_cli.go`, `cmd/ark/message_cli.go`, `cmd/ark/ui_cli.go`
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
- [ ] crc-Librarian.md → `librarian.go`, `connections.go`, `recall.go`
- [x] crc-RecallWatcher.md → `recall_watcher.go`
- [x] crc-RecallAgentBuilder.md → `recall_agent_builder.go`, `recall_agent_handlers.go`, `recall_next.go`, `server.go`, `cmd/ark/main.go`, `.claude/skills/recall/SKILL.md`
- [x] crc-RecallAgent.md → `.claude/agents/ark-recall-agent.md`, `.claude/skills/ark/recall-agent-guard.sh`, `.claude/skills/ark/ark-recall.md`
- [ ] crc-Monitor.md → `monitoring.go`, `cmd/ark/main.go`, `server.go`
- [ ] crc-LuhmannCLI.md → `monitoring.go`, `cmd/ark/main.go`, `server.go`

### Sequences
- [x] seq-add.md → `scanner.go`, `indexer.go`, `store.go`
- [x] seq-search.md → `search.go`
- [x] seq-server-startup.md → `server.go`, `scanner.go`, `indexer.go`
- [x] seq-cli-dispatch.md → `cmd/ark/main.go`, `server.go`
- [ ] seq-cli-urfave.md → `cmd/ark/main.go`, `cmd/ark/connections_cli.go`, `cmd/ark/monitoring_cli.go`, `cmd/ark/embed_cli.go`, `cmd/ark/discussed_cli.go`, `cmd/ark/tag_cli.go`, `cmd/ark/config_cli.go`, `cmd/ark/schedule_cli.go`, `cmd/ark/message_cli.go`, `cmd/ark/ui_cli.go`
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
- [ ] seq-recall-agent.md → `recall_watcher.go`, `recall_agent_builder.go`, `recall_agent_handlers.go`, `recall_next.go`, `server.go`, `cmd/ark/main.go`, `.claude/agents/ark-recall-agent.md`
- [ ] seq-ext-author.md → `db.go`, `server.go`, `extmap.go`, `indexer.go`
- [ ] seq-suggest-locator.md → `db.go`, `server.go`
- [ ] seq-luhmann-supervisor.md → `cmd/ark/main.go`, `monitoring.go`, `server.go`, `recall_agent_builder.go`
- [ ] seq-subscriber-presence.md → `pubsub.go`, `recall_watcher.go`, `recall_agent_builder.go`, `server.go`
- [ ] seq-chimes.md → `scheduler.go`, `server.go`, `config.go`, `pubsub.go`
- [ ] seq-spec-change.md → `scheduler.go`, `config.go`, `indexer.go`, `server.go`
- [ ] seq-tmp-audit-trim.md → `scheduler.go`, `db.go`

### CRC Cards (TypeScript — Ark Search Component)
- [x] crc-SearchAPI.md → `ark-search/src/search-api.ts`
- [x] crc-ArkSearchElement.md → `ark-search/src/ark-search-element.ts`
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
- [x] test-Searcher.md → `search_test.go`
- [x] test-Store.md → `store_test.go`
- [x] test-Tags.md → `indexer_test.go`, `store_test.go`
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
- [x] test-DerivedTags.md → `store_test.go`, `recall_test.go`, `cmd/ark/main_test.go`
- [x] test-SurfaceCooldown.md → `store_test.go`
- [x] test-Secretary.md → `recall_secretary_test.go`
- [x] test-ConnectionsCLI.md → `cmd/ark/main_test.go`
- [x] test-TagSourceParity.md → `tag_source_parity_test.go`
- [x] test-Curation.md → `curation_test.go`
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
- A2: MissingRecord.FileID always serializes as 0 in stored JSON (populated from LMDB key on read)
- [ ] O3: Integration tests need live microfts2+microvec: merge/intersect (test-Searcher), FillChunks/FillFiles (test-ChunkRetrieval)
- [ ] O4: Fetch uses O(n) StaleFiles scan — add direct path lookup to microfts2 when performance matters
- [ ] O5: ark stop does not verify process is ark (PID rollover could kill wrong process) — check /proc/PID/cmdline on Linux
- A3: R187 (vector search) deferred to V4 — no design artifact needed until then
- A4: R214 (negative requirement — no separate lock file) — verified by absence, no design artifact needed
- [ ] O6: JSONL chunks flood search results — single conversation file produces hundreds of score-1.0 chunks, burying small .md files. Mitigated by --filter-files/--exclude-files filtering.
- A5: microfts2 WithOnly/WithExcept — implemented in microfts2 dependency, used by Searcher.ResolveFilters
- A6: R231 (no backward compatibility for --source/--not-source) — verified by removal, no design artifact needed
- A7: R235 (test for per-source add-include round-trip) — covered in test-Config.md
- [ ] O7: Shutdown race: signal handler calls db.Close() while reconcileLoop and watchLoop goroutines may still be running. watcher.Close() stops watchLoop, but reconcileLoop can still be mid-Scan/Refresh against the LMDB env. Need to close reconcileCh and wait for goroutine to drain before db.Close().
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
- [ ] O14: gollama v0.1.8 SIGILL on Zen 2 (Steam Deck) — llama.cpp compute graph uses unsupported instructions. vec bench loads model but crashes on GetEmbeddings. Needs gollama rebuild with -march=znver2 or compatible flags.
- [ ] O15: No unit tests for SearchMulti — needs test with mock microfts2 DB or integration test
- [ ] O16: QueryTrigramCounts + BM25Func open two separate LMDB Views for overlapping data — could be one transaction with a BM25FuncFromQuery helper in microfts2
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
- [ ] O33: No unit tests for scheduling: ParseDateValue, day-bucket Store ops, log file read/write, EnsureUpcoming, ScanScheduleLogs, crankForward
- [ ] O34: Store.WriteDayBuckets/QueryDayBuckets not yet called from indexer — wiring deferred until calendar UI (Lua API mcp:scheduled)
- [ ] O35: Lua APIs not yet registered: mcp:scheduled, mcp:reschedule, mcp:tagComplete, mcp:fileStatus, mcp:subscribe (R893-R898)
- [ ] O36: Event payload does not yet carry IsScheduledFire flag (R878) — publisher delivers same Event type for both scheduled fires and tag changes
- [ ] O37: Gap detection (R890-R892) not implemented — comparing @ark-event-fired: in log vs @ack: in source file
- [ ] O38: No unit tests for: ParseAcks, AckCoversDate, WriteDayBucketsForFile, handleScheduleSearch, handleScheduleChange, CheckScheduleConfig, cmdSchedule
- [ ] O39: handleScheduleChange uses strings.Replace matching trimmed value in untrimmed line — fragile for lines with unusual leading content
- A23: R980 (calendar virtual items from recurrence specs) — deferred to Lua/UI work
- [ ] O40: No unit tests for write actor: enqueueWrite, startNextWrite, ScanAsync, RefreshAsync
- [ ] O41: R1066: deferred-schedule pattern (pendingSchedule/DrainSchedule) not yet removed — schedule I/O still deferred rather than running in write goroutine
- [ ] O42: No unit tests for editor endpoints: handleSearchGrouped, handleTagComplete, handleTagValues, handleSave, handleSetTags
- [x] O43: handleTagValues reads files to extract values — O(files) I/O on each completion request. Store tag values in LMDB during indexing for O(1) lookup when this becomes slow.
- [ ] O44: handleSave allows writing to any indexed file — no authorization check. Acceptable for local use; revisit if ark ever serves untrusted clients.
- A24: R1098 (CORS) — same-origin, no explicit headers needed for localhost. Revisit if editor loaded from file:// origin.
- [ ] O45: No unit tests for V record Store methods: UpdateTagValues, AppendTagValues, RemoveTagValues, QueryTagValues, TagValueChunks
- [ ] O46: RemoveTagValues scans all V keys to find one fileid — O(total V records). Add reverse index (VF prefix) if profiling shows this is slow.
- A25: R1107 (V records rebuilt by ark rebuild) — rebuild already regenerates T/F/D; V follows same pattern, no separate design artifact needed
- A26: R1112 (Lua mcp:tagComplete should use V records) — deferred until Lua-side tag completion is implemented
- [ ] O47: R1115: WithAppendChunkCallback not yet wired in append paths — tags still extracted from tagWindowForAppend (R1127). Wire when microvec supports incremental chunk updates
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
- A36: R1624, R1625, R1626, R1627, R1628, R1629, R1631, R1632, R1634, R1636 (PDF span-level extraction and structure detection) — superseded by pdftext. R1624 → R1729 (library swap); R1625–R1634, R1636 → R1730 (pdftext's Blocks replace our in-house structure detection)
- A37: R1652, R1653, R1654, R1655, R1656, R1657, R1658, R1660 (byte-stream salvage codepath) — superseded by R1734 (pdftext emits Salvage as a BlockKind inline with structured blocks; no separate in-ark salvage path)
- A38: R1661, R1662, R1663, R1664 (blank-line filtering for ONLYOFFICE-style PDFs) — superseded by R1729 (line-level layout handling is pdftext's responsibility)
- A39: R1669, R1674 (tag rect from line spans, first-line-only for wraps) — superseded by R1735, R1736 (tag scan uses Block.Chars; rect unions all covered glyph BBoxes including wrapped lines)
- A40: R1723, R1657, R1658 (salvage at page 0 with no rect) — superseded by R1737 (salvage keyed at actual page, carries Block.BBox)
- [ ] O80: No unit tests for parseFilterStack, formatFilterStack, or -parse output
- [ ] O81: No unit tests for files mode in BuildChunkFilters
- A41: -about as chunk filter (AboutChunkFilter) not yet implemented — requires embedding model in filter path. -about works as primary search only.
- [x] O82: About chunk filter requires configured embed_cmd/query_cmd in ark.toml — currently tag_model is set (Librarian path) but query-time embedding path is not. vec.Search fails silently.
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
- [ ] O85: ExtMap state mutation isn't transactional with the env.Update — if microfts2's commit aborts, the in-memory maps may be briefly inconsistent. Convergence is restored by ExtMap.Rebuild on next DB.Open. Acceptable per design (.scratch/EXT-V-F.md, multi-file batch convergence).
- [ ] O86: No tests for @ext storage layer — test-ExtMap.md and integration coverage of multi-set V, X record CRUD, and the canonical re-resolution flow not yet written
- [x] O87: @ext targets that resolve to overlay (tmp://) chunkids are logged-and-skipped in applyIndexExt. Full support needs (a) in-memory overlay E-records parallel to TmpTagStore — diagnostics that live and die with the session, since persisting them would outlive the context that produced them — (b) overlay-aware chunkFileID via Store.filesForChunk, (c) ExtMap state for overlay chunkids in chunkToTargets/fileidToTvids/targetToChunk, rebuildable from session state on startup. Tracked in PLAN.md.
- [ ] O88: No tests yet for @ext overlay (tmp://) target routing — covers four routing scopes, overlayRoutings/overlayValues lifecycle, lock-flap-free per-target dispatch, TmpTagStore→ExtMap cleanup hook, and the ark errors API. Belongs alongside O86.
- [ ] O89: ark errors CLI command not yet implemented. ExtMap exposes RecordOverlayError/AddOverlayError/OverlayErrors/ClearOverlayErrors per R2030; CLI surface (PLAN.md V2.5) needs to consume them via dump/clear/add subcommands.
- [ ] O90: Multi-source tvid_ext edge case predates overlay routing: two source chunks with identical @ext value text share a tvid_ext, so cleanup of one source incorrectly clears the other's contributions from targetToChunk and the overlay maps. Pre-existing — not regressed by overlay work — but visible in overlay flows when persistent and overlay sources happen to write the same @ext line.
- T47: R2077 retired (pdftext.Block.BBox already provides heading rects; PDFChunker already emits rect in chunk Attrs. No upstream pdftext change needed.)
- [ ] O91: Tag-overview routing emission regression: fresh @ext routings stopped registering after several restart + cleanup cycles in dev DB. Pristine pre-implementation binary has same behavior — accumulated LMDB state, not code. Workaround: ark rebuild. Investigate if reproducible from clean state.
- [ ] I2: R2063 R2064 width persistence uses localStorage as Stage B substrate — HostAPI/I-record swap deferred (Go endpoint work needs its own mini-spec pass). Functional persistence works; per-machine instead of per-DB.
- [ ] I3: R2032-R2064 R2065-R2072 R2081-R2084 tag-overview frontend Rs are referenced in tag-overview/src/tag-overview.ts via range comments rather than per-Rn inline refs. Coverage exists; granularity coarser than minispec validate expects.
- [ ] O92: Touch peek (R2042, R2044) deferred — desktop hover peek lands; touch tap conflicts with row-click scroll. Needs separate tap target (long-press, peek-toggle icon) or mode-specific hit area.
- [ ] O93: R2082 PDF body indicator positioning is a stub — server emits <ark-ext-tags rect=...> but the bundle does not yet position the indicator over the <pdf-chunk> canvas at the rect coordinates. Heading-overlay positioning works; ext-tags overlay does not. Needs <pdf-chunk> coordination.
- [ ] O94: ExtRoutingsForTargetChunk per-chunk LMDB View txn — handleContentView and renderPdfChunksByPage call it inside a per-chunk loop, opening N read txns. Cheap individually; for 100+ chunk files worth batching to one txn per request via an ExtRoutingsForTargetChunks([]uint64, *DB) variant.
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
- [ ] O96: Librarian-level ED tests (rebuild regenerates ED, BatchEmbed writes ED for missing pairs) require a real GGUF model and are not run by 'go test'. Store-level ED tests in store_test.go cover R2151-R2162 at the LMDB layer; the model-side path is exercised manually after each model swap.
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
- [ ] O98: R2402-R2403 ChunkTextByID does two LMDB+file-read passes — ChunkInfo and ChunkText each call db.fts.NewChunkCache() which re-reads the underlying file. Acceptable at workshop cadence (per-card, low frequency); revisit if profiling shows this dominates.
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
- A65: RP/RPE/RR record prefixes reserved for LLM-driven definition proposals (ARK-STATE.md item 1, agent layer). This slice (item 2) covers statistical attach-proposals only — chunkID+tagname keyed via RC/RJ/RF. Definition proposals (tagname+value+definition keyed, with embedding + reasoning) require an LLM and land with the recall agent. Letters chosen now to avoid collision; record-formats.md notes the reservation.
- [ ] O115: Recall --propose substrate scan: derivation iterates every recalled chunk × every ED record to compute per-tag max cosine. For an N-chunk corpus with M tag values this is O(N·M) cosine compares per --propose invocation. Acceptable at current scale (Bill: "simple first") and amortized by RF freshness skip; revisit if interactive latency degrades when N·M grows. Vector index (HNSW or similar) is the obvious lever.
- [ ] O116: Lua bridge for AcceptDerived / RejectDerived not yet wired — Store API exists (per Bill's front-load-Go-for-UI preference, [[feedback_frontload-go-for-ui]]) and sys.recall surfaces proposed tags via the Lua bridge, but the write-side bridge (sys.acceptProposal / sys.rejectProposal or similar) lands in the next /ui-thorough pass alongside the curation-view accept/reject UI. No mini-spec/frictionless thrash needed when that pass starts.
- A66: R2699 — watcher relies on substrate self-exclusion (R2645); no code anchor by design (delegated non-feature)
- [ ] O117: RecallWatcher integration tests deferred — test-RecallWatcher.md lists ~15 pipeline scenarios (cooldown gate, similarity gate, mark-on-send, propose passthrough, source-dir whitelist, live config reload). Unit tests in recall_watcher_test.go cover pure helpers + SourceQualifies; pipeline scenarios need DB + librarian + chat-jsonl chunk scaffolding.
- T94: R2691 retired (turn-boundary firing makes a separate per-session cooldown redundant (2026-05-25 simple-recall revision))
- T95: R2697 retired by R2734 (per-chunk trigger replaced by turn_duration-armed debounce (2026-05-25 simple-recall revision))
- T96: R2702 retired by R2748 (2026-05-26 simple-recall v2 — curation-doc header @ark-recall-curate replaces DM @ark-recall-fire line)
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
- [ ] O131: Ext-routing threads a raw nilable *lmdb.Txn through runExtRouting/runOverlayExtRouting/applyIndexExt/chunkFileID; a wrong nil reaches fts.ReadCRecord and panics (the R2915 crash). Enhancement: model the transaction as a scope object (Monadic Wrapper) constructed only inside an actor closure, so 'inside a txn' is non-nilable by construction and this whole bug class disappears. Also covers librarian.go flushNow, which today relies on a documented 'call me from the write goroutine' contract rather than a local guard.
- T142: R1302 retired by R1791 (2026-06-07 cli-urfave: ark embed text subcommand)
- T143: R1303 retired by R1792 (2026-06-07 cli-urfave: ark embed bench tags subcommand)
- T144: R1304 retired by R1792 (2026-06-07 cli-urfave: ark embed bench chunks subcommand)
- T145: R1305 retired (2026-06-07 cli-urfave: embed uses local Librarian via withDB, no server (was false))
- T146: R1587 retired by R1793 (2026-06-07 cli-urfave: ark embed bench --ctx subcommand flag)
- T147: R1621 retired by R1792 (2026-06-07 cli-urfave: ark embed bench chunks --parallel subcommand flag)
- T148: R1622 retired by R1792 (2026-06-07 cli-urfave: ark embed bench output (subcommand))