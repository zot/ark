# `ark tag verify`

A diagnostic / repair subcommand that thoroughly cross-checks the
in-memory and on-disk state of the tag system: F records, V records,
T totals, X (ext-routing) records, and the in-memory ExtMap. Detects
drift between what should be true and what actually is.

The motivating case: on 2026-05-02 a heisenbug surfaced where a real
production rebuild produced 0 X records for `@ext:` declarations whose
targets were correctly indexed. Diagnostic instrumentation made the
bug stop reproducing. A tool that reports drift between F/V/X without
needing to instrument the binary would have made the diagnosis a
single command.

## Command

```
ark tag verify [--repair] [--scope SCOPE]
```

- `--repair` — write corrections. Default is read-only.
- `--scope ext` — limit to @ext routing checks. Other supported
  scopes: `tag-totals`, `all` (default).

## Checks

### 1. Ext routing integrity (`--scope ext`)

For each F record carrying the `ext` tag:

- Parse the value via `ParseExtTarget`.
- Re-resolve the target via `ResolveExtTarget`.
- Compare against X records in the store keyed by this `tvid_ext`:
  - Missing X records — target resolves to chunks but no X record
    exists for the (tvid_ext, chunk) pair.
  - Stale X records — X record points to a target chunk that no
    longer matches the current resolution.
  - Routed-tvid drift — the X record's stored routed_tvids don't
    match the routed tags in the current `@ext:` value (e.g. the
    source was edited and routed-tags changed, but the V multi-set
    still carries the old tvids).

For each X record without a corresponding F record, report it as
orphaned (source chunk no longer has the @ext value).

Cross-check the in-memory ExtMap against the X record set:
`extByAnchor`, `targetToChunk`, `chunkToTargets`, `fileidToTvids`,
`unresolvedTargets`, `extSource`. Any anchor key, target list, or
chunk list that doesn't match the on-disk X records is reported.

### 2. Tag totals (`--scope tag-totals`)

For each T record (tag totals):

- Recompute the total from the V multi-set sizes for that tag.
- Add the ExtMap's `virtualTagCount` contribution.
- Compare against the stored T value.

Drift here usually means a removal path failed to decrement T
properly, or a routed contribution wasn't accounted for.

### 3. Combined (`--scope all`)

Run both check sets. Default scope.

## Output

Plain-text summary, one issue per line:

```
ext: missing X record for tvid_ext=1092 target=42 (source chunk 85894)
ext: stale X record tvid_ext=1110 → chunk 9999 (no longer resolves)
ext: extByAnchor[/path/to/x.md] has 3 entries, X records have 2
tag-total: drift on @food: stored=42 computed=39 (diff=3)
ext: orphan X record tvid_ext=200 chunk=300 (no matching F record)

verify: 5 issues found, 0 repaired
```

Exit codes:

- 0 — no issues found.
- 1 — issues found (read-only mode) or partial repair (some
  issues couldn't be auto-fixed).
- 2 — verification itself failed (DB error, etc.).

## Repair

With `--repair`:

- Missing X records → write via `WriteExtRecord`, plus the
  matching V multi-set additions (`addChunkIDToVRecord` for each
  routed_tvid).
- Stale / orphan X records → delete via `DeleteExtRecord`, plus
  matching V multi-set removals (`removeOneChunkIDFromVRecord`).
- Routed-tvid drift → delete + rewrite the X record with the
  correct tvids; reconcile V multi-sets.
- Tag total drift → rewrite the T value to the computed total.
- ExtMap drift → rebuild the in-memory state via `ExtMap.Rebuild`
  (it already exists; this just invokes it after the X records
  are made consistent).

Repairs run inside a single LMDB write transaction. Partial repair
is reported per-issue.

## Cost

Linear in the number of F records carrying `ext`, plus linear in
the number of X records, plus linear in the number of T records.
For typical corpora this is fast — a few seconds at most. Not
suitable for the hot path; designed for diagnostics and post-import
hygiene.

## Out of scope

- Repairing microfts2 records (chunks, files, trigrams).
  `microfts2` is a separate process boundary; if its state is
  inconsistent that's a separate bug class.
- Verification of @id values themselves (uniqueness, format).
- Performance benchmarks during verification.
