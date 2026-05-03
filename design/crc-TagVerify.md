# TagVerify
**Requirements:** R2092, R2093, R2094, R2095, R2096, R2097, R2098, R2099, R2100, R2101, R2102

Diagnostic / repair logic for `ark tag verify`. Cross-checks F, V,
T, X records and the in-memory ExtMap to detect drift; with
`--repair`, writes corrections inside a single LMDB write
transaction.

Lives behind the `ark tag verify` subcommand. Not on any hot path —
designed for diagnostics and post-import hygiene. Linear in the
number of F-with-ext records, X records, and T records.

## Knows
- db: *DB — facade for LMDB env access, ResolveExtTarget, ExtMap accessor
- store: *Store — F/V/T record access, X record CRUD primitives
- extmap: *ExtMap — in-memory anchor / target maps cross-checked against X
- scope: string — `ext`, `tag-totals`, or `all`
- repair: bool — write corrections vs report-only
- issues: []verifyIssue — accumulated drift findings

## Does
- Run(): orchestrate the chosen scope, emit issue lines, return summary
- verifyExt(): walk F[*][ext] tag entries; for each, ParseExtTarget +
  ResolveExtTarget; compare against X records by `tvid_ext`;
  surface missing / stale / routed-tvid-drift / orphan issues
- verifyExtMapConsistency(): compare extByAnchor / targetToChunk /
  chunkToTargets / fileidToTvids / unresolvedTargets / extSource
  against the on-disk X record set
- verifyTagTotals(): for each T record, recompute from V multi-set
  sizes plus ExtMap.virtualTagCount; report drift
- repairExt(): apply corrections via WriteExtRecord +
  addChunkIDToVRecord, DeleteExtRecord +
  removeOneChunkIDFromVRecord; trigger ExtMap.Rebuild after
- repairTagTotals(): rewrite T values from computed totals
- emitIssue(severity, message): one-line plain-text output
- summary(): emit `verify: N issues found, M repaired` and return
  exit code

## Collaborators
- Store — owns F/V/T/X record I/O and the V multi-set primitives
  (`addChunkIDToVRecord`, `removeOneChunkIDFromVRecord`)
- DB — exposes `ResolveExtTarget`, the LMDB environment, and the
  ExtMap; coordinates the write txn for repairs
- ExtMap — supplies the in-memory state to cross-check; exposes
  `Rebuild` for post-repair consistency
- CLI — owns flag parsing (`--repair`, `--scope`), invokes
  TagVerify.Run(), formats summary

## Sequences
- seq-tag-verify.md
