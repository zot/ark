# `ark tag inspect`

Observability for the tag system: dump on-disk state, in-memory
state, and the decoded bridges that connect them. Read-only — never
mutates. Server-aware: proxies through the running server when one
is up (so the in-memory view matches what's actually serving), opens
LMDB read-only when the server is stopped.

Distinct from `ark tag verify`:

- `verify` validates and (with `--repair`) writes corrections;
  requires the server stopped because of the write transaction.
- `inspect` reveals state without judgment or repair; runs alongside
  the live server.

Both use the same `--scope` axis (today: `ext`; future: `tag-totals`,
`all`).

## Command

```
ark tag inspect [--scope SCOPE] [--target PATH] [--json]
```

- `--scope ext` — limit to @ext routing state. Default scope.
  Future: `tag-totals` for V/T cross-decoding, `all`.
- `--target PATH` — narrow output to one file. Filters X records
  whose target_chunk is in PATH's chunkid set, V[ext] entries whose
  source chunk is in PATH's chunkid set, and ExtMap entries that
  reference PATH's fileid. Without `--target`, dump everything.
- `--json` — machine-readable output. Default is plain text grouped
  by section, suitable for terminal reading.

## Output

For `--scope ext`, three sections, each labeled.

### On-disk

- **X records** — for each `X[tvid_ext, target_chunkid]`, print the
  tvid_ext, target_chunkid, decoded target file path (via
  `chunkFileID` + `resolveFilePath`), and routed_tvids each
  decoded back to `(tag, value)` via TvidMap.
- **V[ext] records** — for each `V[ext][value][tvid_ext]`, print the
  value text, tvid_ext, and the source-chunkid list.
- **F[chunkid][ext] records** — for each unique source_chunkid
  appearing in V[ext], print the F record's tvid list (every @ext
  declaration on that chunk) decoded via TvidMap.

### In-memory ExtMap

- `targetToChunk[tvid_ext] → []chunkid`
- `chunkToTargets[chunkid] → []tvid_ext`
- `extSource[tvid_ext] → source_chunkid`
- `fileidToTvids[fileid] → []tvid_ext` (with fileid → path resolved)
- `extByAnchor[anchor] → []tvid_ext`
- `unresolvedTargets` (set of tvid_ext)
- `virtualTagCount[tag] → count`
- `overlayRoutings[tvid_ext][target_chunkid] → []routed_tvid`
- `overlayValues[tag][value] → []chunkid`

### Bridges

For each tvid_ext present in either disk or in-memory state, one
consolidated entry:

- tvid_ext + (tag, value) recovered via TvidMap
- on-disk X targets vs in-memory targetToChunk[tvid_ext] —
  consistency obvious by inspection
- decoded routed (tag, value) pairs
- source chunkid + source file path
- target chunkids + target file paths

## Server vs. direct

The CLI tries the server first. When `ark serve` is up, it `POST
/tags/inspect` with `{scope, target}` and prints the response. The
in-memory ExtMap section is meaningful only via this path —
direct-LMDB inspection cannot read the running server's
reconstructed state.

When the server is down, the CLI opens LMDB read-only and dumps the
on-disk sections directly. The in-memory section is replaced with a
note: "ExtMap state unavailable — server not running. Disk view only."

## Cost

Linear in X records, V[ext] records, and F[chunkid][ext] records.
For typical corpora, milliseconds. Not on any hot path.

## Out of scope (deferred)

- `--scope tag-totals` — V multi-set sizes vs T records vs
  ExtMap.virtualTagCount. Adds when tag-totals drift becomes a
  recurring concern.
- `--scope all` — runs every scope.
- Diff mode — compare two inspect outputs over time. Useful for
  before/after a reindex, but not the v1 motivating use.
