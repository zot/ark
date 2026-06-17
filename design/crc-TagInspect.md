# TagInspect
**Requirements:** R2113, R2114, R2115, R2116, R2117, R2118, R2119

Observability for `@ext` (and future scopes) state. Read-only.
Sibling of TagVerify: verify validates and repairs, inspect reveals
without judgment. Server-aware so the in-memory ExtMap dump matches
what the live server is using.

## Knows
- db: *DB — facade for index access, ExtMap accessor, fileid/chunkid resolution
- store: *Store — F/V/X record access, TvidMap for tvid → (tag, value) decoding
- extmap: *ExtMap — in-memory state to dump
- scope: string — `ext` (v1)
- target: string — optional path filter (empty = all)
- json: bool — output format

## Does
- Run(w io.Writer): orchestrate the chosen scope, emit grouped output
  (or JSON), no state mutation
- inspectExt(): collect on-disk sections (`Store.DumpExtRecords`),
  in-memory sections (`ExtMap.DumpState`), build the per-tvid_ext
  bridge view by joining the two; emit
- bridgeView(): for each tvid_ext seen on disk or in memory, decode
  via TvidMap to (tag, value), resolve target chunks to file paths,
  resolve source chunkid to file path, emit one consolidated entry
- emitText(sections): group by section (On-disk, In-memory, Bridges),
  one entry per line in stable sort order; default formatter
- emitJSON(sections): single object with `disk`, `inmemory`, `bridges`
  keys; structured for tooling

## Collaborators
- Store — exposes `DumpExtRecords(scope, filter)` returning the X /
  V[ext] / F[ext] disk view; also TvidMap accessor for decoding
- ExtMap — exposes `DumpState(filter)` returning a snapshot of every
  in-memory map under one RLock so consumers see a consistent view
- DB — exposes `chunkFileID`, `resolveFilePath`, `ChunkIDsForPath`
  for the bridge / target-filter logic
- Server — `POST /tags/inspect` handler invokes `DB.InspectExt(...)`
  and writes the response; CLI proxies via this endpoint when the
  server is up
- CLI — owns flag parsing (`--scope`, `--target`, `--json`); chooses
  server-proxy or direct read-only index open based on server liveness

## Sequences
- (none required for v1; flow is straightforward proxy or direct)
