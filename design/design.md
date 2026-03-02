# Ark Design

Orchestration layer over microfts2 and microvec. Digital zettelkasten
with hybrid search.

## Intent

Ark coordinates two search engines (trigram + vector) through a single
interface. Files on disk are the source of truth; the database is just
an index. The CLI and HTTP API expose identical operations. A
long-running server keeps the embedding model warm; the CLI proxies to
it or falls back to cold-start.

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

### Test Designs
- [ ] test-Config.md → `config_test.go`
- [ ] test-Matcher.md → `match_test.go`
- [ ] test-Searcher.md → `search_test.go`
- [ ] test-Store.md → `store_test.go`

## Gaps

- [ ] O1: Test files not yet written: config_test.go, match_test.go, search_test.go, store_test.go
- [ ] O2: serverClient TOCTOU race — probe can succeed but actual request fails if server dies between. Acceptable for v1
- [ ] A1: IndexBuilt field removed from StatusInfo during simplification — spec still mentions it, update spec
- [ ] A2: MissingRecord.FileID always serializes as 0 in stored JSON (populated from LMDB key on read)