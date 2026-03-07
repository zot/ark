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
The four-form pattern language (name, name/, name/*, name/**) with
dotfiles support, anchoring, and glob wildcards is used throughout:
source config, CLI remedy commands, scan classification.

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
The ui-engine (`github.com/zot/ui-engine/cli`) runs in-process
alongside the ark API server. `cfg.Server.Dir` points to `~/.ark/`,
where UI assets (html/, lua/, viewdefs/, apps/) coexist with ark
data (data.mdb, ark.toml, ark.sock). The ui-engine manages its own
`ui-port` and `mcp-port` files. If the ui-engine fails to start,
the ark API server continues — UI is optional. On shutdown, the
ui-engine shuts down before the LMDB env closes.

## Artifacts

### CRC Cards
- [x] crc-DB.md → `db.go`
- [x] crc-Config.md → `config.go`
- [x] crc-Matcher.md → `match.go`
- [x] crc-Store.md → `store.go`
- [x] crc-Scanner.md → `scanner.go`
- [x] crc-Indexer.md → `indexer.go`
- [x] crc-Searcher.md → `search.go`
- [x] crc-Server.md → `server.go`
- [x] crc-CLI.md → `cmd/ark/main.go`

### Sequences
- [x] seq-add.md → `scanner.go`, `indexer.go`, `store.go`
- [x] seq-search.md → `search.go`
- [x] seq-server-startup.md → `server.go`, `scanner.go`, `indexer.go`
- [x] seq-cli-dispatch.md → `cmd/ark/main.go`, `server.go`
- [x] seq-config-mutate.md → `config.go`, `cmd/ark/main.go`, `server.go`
- [x] seq-sources-check.md → `config.go`, `db.go`, `cmd/ark/main.go`, `server.go`

### Test Designs
- [x] test-Config.md → `config_test.go`
- [x] test-Matcher.md → `match_test.go`
- [x] test-Searcher.md → `search_test.go`
- [x] test-Store.md → `store_test.go`
- [x] test-Tags.md → `indexer_test.go`, `store_test.go`
- [x] test-ChunkRetrieval.md → `search_test.go`

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
- [ ] O7: Shutdown SIGSEGV: signal handler goroutine (server.go:113-119) calls db.Close() while background reconciliation goroutine (server.go:122-139) may still be running Scan/Refresh against the LMDB env. Need to cancel/wait for reconciliation before closing.
- [ ] O8: ark install UI asset extraction not yet implemented — R276-R281 designed but install command needs the go:embed directives and extraction logic
- [x] O9: ~/.ark/mcp shell script (R283) not yet created — needs adaptation of .ui/mcp pattern for ~/.ark/ paths