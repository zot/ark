# Spec Index

The root of ark's specs. **Start here** before working in an area — and
when you add or change a spec, add or fix its entry here.

Per-feature specs are the leaves: each owns the behavior of one
capability. This index is the *root* that says where to look, and — in
the Themes section — states the cross-cutting invariants the leaves
can't see on their own (and surfaces the contradictions *between* leaves
that nothing else catches).

**Maintenance.** Entries are *pointers, not copies* — the named spec is
canonical, this index mirrors it. A theme may carry a one-line
invariant, but the owning feature spec wins on any disagreement; fix the
mirror. Rationale, drift strategy, and open questions:
[.scratch/SPEC-INDEX.md](../.scratch/SPEC-INDEX.md).

**Systems are complete; Themes are earned.** The Systems list maps every
per-feature spec — a spec missing from it is drift, surfaced by the
`minispec` spec-index check. Themes are added only when a real navigation
failure names one — not enumerated up front.

## Summary specs (cross-cutting inventories)

Each indexes existing behavior along one axis. Per-feature specs are
canonical; these mirror. Keep in sync when you add along one of these
axes (this is the pinned list CLAUDE.md refers to).

- [cli-commands.md](cli-commands.md) — every CLI subcommand + flag
- [record-formats.md](record-formats.md) — every index record prefix / key / value
- [lua-api.md](lua-api.md) — every Lua binding (`mcp` / `MCP` / `sys` / `session` / `ui`)
- [features.md](features.md) — every capability: motivation + objective
- [config.md](config.md) — every `ark.toml` key
- [chunkers.md](chunkers.md) — every chunker + the interface matrix
- [future.md](future.md) — deferred ideas not yet built (`@future:` holding pen, until mini-spec tracks them natively)

## Systems

Every per-feature spec has a home here, placed by primary concern (a few
could sit in two; cross-listing is kept minimal).

- **Indexing & chunking** — chunkers.md, chunker-strategies.md, pdf-chunker.md, bible-chunker.md, chunk-callback.md, chunk-context.md, parallel-indexing.md, source-monitoring.md, indexing.md, v25-enhancements.md
- **Tags** — tags.md, ark-tag-element.md, tag-extraction-fixes.md, tag-value-index.md, derived-tags.md, discussed-tags.md, at-ext-parsing.md, at-ext-storage.md, internal-disposition.md, at-id.md, at-link.md, tag-block-commands.md, tags-baby-food.md, tag-value-filtering.md, suggest-tag-names.md, chunks-for-tag.md, tag-name-contains-tokens.md
- **Embeddings** — chunk-embeddings.md, tag-embeddings.md, tag-def-embeddings.md, embed-dedup.md, vector-freshness.md, vec-bench.md, embed-subcommands.md, llama-libs.md
- **Search** — search.md, fuzzy-search.md, bigram-search.md, multisearch.md, search-filtering.md, search-cli-filters.md, spectral-search.md, search-profiling.md, chunk-filters.md, file-tag-filter.md
- **Recall & substrate** — recall.md, simple-recall.md, bloodhound.md, bloodhound-cli.md, find-connections.md, find-connections-substrate.md, curation.md, curation-workshop-primitives.md, luhmann.md, hot-correlations.md
- **Scheduling & monitoring** — scheduling.md, schedule-lifecycle.md, chimes.md, monitor.md
- **PDF** — pdf-chunker.md, pdf-chunk-element.md *(see Theme: Dealing with PDFs)*
- **Messaging** — messaging.md, messaging-support.md, inbox-enhancements.md, inbox-v-records.md, inbox-bookmark-fields.md, inbox-entry-enrichment.md
- **tmp:// documents** — tmp-documents.md, tmp-subscription.md, tmp-tag-overlay.md
- **Storage & concurrency** — architecture.md *(overview — see Theme: Actors & DB-access discipline)*, record-formats.md, db-write-actor.md, db-concurrency.md, http-operations.md, rebuild-read-serve.md, cli-dispatch.md, chunk-cache-threading.md, pubsub.md, tvid-map-overlay.md, serve-compact.md
- **UI / Frictionless** — embedded-ui.md, viewer.md, app-search.md, app-source-tree.md, content-view-edit.md, ark-search.md, chunked-content-view.md, content-fetching.md, content-iframe.md, editor-endpoints.md, tag-overview.md, tag-search-panel.md, table-sort.md, chat-transcript.md
- **Nano (local LLM agent)** — nano-overview.md, nano-cli.md, nano-library.md, nano-sessions.md, nano-tool-loop.md
- **Status & diagnostics** — status-db.md, status-enhanced.md, files-status.md, chunk-stats.md, tag-inspect.md, tag-verify.md, verbose-flags.md
- **Server, config & infrastructure** — main.md, sessions.md, subscriber-presence.md, config-flag-bug.md, config-tracking.md, bundle-assets.md, infrastructure.md, managed-pty.md, luhmann-terminal-element.md

## Cross-cutting themes

A theme names a concern that crosses feature specs, states its invariant
once, and lists every spec the concern touches — so the invariant has a
home and contradictions between specs become visible.

### Text vs meaning

The two indexes answer different questions and must not be conflated:

- **Trigram = literal text.** Chunk content is indexed verbatim — ark
  tags kept — so a full-text search for a literal `@note: bubba` finds
  the chunk carrying it. Stripping tags here would make FTS lie about
  its own corpus.
- **Embedding (EC) = meaning.** ark strips ark-tag spans before
  embedding, so meta-tags don't pull a chunk toward a "taggedness"
  direction unrelated to its prose. Stripping is **ark-side, embed-only**;
  microfts2 indexes verbatim.

Touches: chunkers.md, chunk-embeddings.md, recall.md,
[migrations/recall-substrate-v3.md](migrations/recall-substrate-v3.md) (R2913), search.md.

### Dealing with PDFs

- **Chunking:** pdftext yields per-page structure `Block`s; the chunker
  emits one chunk per block, content = the block's newline-delimited
  extracted text (NFKC-normalized). (pdf-chunker.md)
- **Tags:** a PDF chunk's text gets the **same per-chunk tag extraction
  as any text chunk** — `@name: value` → T/F/V/D records, normally. PDF
  is *not* tag-excluded at the chunk level. (Only *file-level* extraction
  skips pdf, because that path sees raw PDF bytes, not extracted text.)
- **Presentation:** the chunker also emits `tag_rects` / `tag_segments`
  so `<pdf-chunk>` can overlay clickable `<ark-tag>` widgets on the
  rendered page — users see and expand tags *inside* the PDF.
  (pdf-chunk-element.md)

Invariant: **extracted PDF text is ordinary tag-bearing text.** Any spec
that says "pdf carries no ark tags" is talking about *raw bytes /
file-level*, not chunk text — keep that distinction explicit.

Touches: pdf-chunker.md (canonical — §Tag Extraction), pdf-chunk-element.md,
chunkers.md, migrations/recall-substrate-v3.md (R2913).

### Actors & DB-access discipline

Off-actor DB access is governed by one rule no single feature spec can
see: **the actor-protected Go caches (pathCache / pathToID / frecordCache)
are never read off the shared original.** An off-actor read *operation*
reads through a private `fts.Copy()` (`l.db.withFTS`); a write goes through
the write actor (which writes on its own copy, then `InvalidateCaches` back
on the actor). bbolt is MVCC-safe, so bbolt `View` reads are exempt — only
the Go caches above them are protected.

Invariant: **no off-actor goroutine touches the shared original's Go
caches.** A bare `db.fts.FileIDPaths` / `FileInfoByID` / `SearchFuzzy` off
the actor is a latent data race (this is what O154 was).

Touches: architecture.md (canonical overview), db-concurrency.md
(§Protected Resources), db-write-actor.md, find-connections-substrate.md
(substrateOp — the pattern's first instance), http-operations.md (the
HTTP front door for operations), chunk-cache-threading.md.

### Glob anchoring

Every path glob in ark runs through **one matcher** (`ark.Matcher`,
doublestar). Surfaces differ only in what an *unanchored* pattern is
measured against, and there are exactly three contexts:

- **CLI flags** — root is the current directory, so bare `*.md` means
  what it means in a shell (top level only).
- **Source-scoped config** — root is the source's directory, so bare
  `X` means `SOURCE/**/X`.
- **Rootless** (`search_exclude`, `[schedule]` filters, and the
  Lua/MCP `filter_files` opts) — no root exists, so bare `X` means
  `**/X`. The server has no cwd to anchor against.

Invariant: **a bare glob is relative to where you are standing; `/X` and
`./X` mean the same thing on every surface.** Only the bare form varies,
and only ever by narrowing. A spec that describes a *fourth* mechanism, a
second matcher, or a per-command anchoring rule is drift — this is what
O158 was.

`[[source]].dir` is outside the invariant on purpose: it is filesystem
*expansion*, not filtering, and rejects `**` because a recursive source
glob multiplies watcher events per ancestor level (latently, only once a
nested directory appears).

Touches: main.md (canonical — §Glob Patterns), cli-commands.md,
config.md, source-monitoring.md, chunker-strategies.md, scheduling.md,
search-cli-filters.md, search-filtering.md, pubsub.md, bloodhound.md.
