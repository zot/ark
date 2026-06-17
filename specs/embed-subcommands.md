# Embed Subcommands

Restructure `ark embed` from a flat flag-driven command into proper
subcommands: `text`, `bench`, and `validate`. Also remove the obsolete
`ark vec` command tree.

Language: Go. Environment: CLI (`cmd/ark/main.go`, `cmd/ark/vecbench.go`).

## `ark embed text TEXT...`

Embed text using the configured tag model. Prints the embedding vector
as JSON to stdout. This is the current bare `ark embed <text>` behavior,
moved under a subcommand for consistency.

- Requires `[embedding] model` configured in ark.toml
- Joins all positional args with spaces
- Output: JSON array of float32

## `ark embed bench MODE`

Benchmark embedding performance. MODE is `tags` or `chunks`.

This was the `ark embed --bench tags|chunks` flag behavior, now promoted
from a flag to a subcommand.

- `--ctx N` — context window size in tokens (default 2048)
- `--parallel N` — parallel sequences per batch (default 8)

### `ark embed bench tags`

Collect all tag values from the index, embed via batch and single paths,
report timing comparison and speedup ratio.

### `ark embed bench chunks`

Sample 200 chunks from indexed files (file-first random sampling to
avoid JSONL domination), embed via batch and single paths, report
timing comparison and speedup ratio. Respects `--ctx` and `--parallel`
for tier sizing.

## `ark embed validate`

Cross-reference embedding records (EC/EF) against actual chunks in the
FTS index to find orphans, mismatches, and gaps. Reports problems and
optionally fixes the safe subset.

The concern: embedding records can drift from the actual chunk state
due to crashes, partial writes, or bugs in the batch embed loop.
Observed symptoms: continual high memory usage from an embedding loop
that only stops on restart, creeping database size, and more E records
in the ark bucket than C records in microfts2 — a clear sign of
orphaned embeddings.

### Checks

1. **Orphan EC records**: EC records whose chunkID has no
   corresponding C record in microfts2 (the chunk no longer exists
   in any file).

2. **EF/EC count mismatch**: EF centroid's stored count doesn't match
   the number of EC records resolvable from the file's F-record
   chunk list. Indicates a crash-interrupted centroid update.

3. **Missing EC records**: chunkIDs with C records in microfts2 but
   no EC record. These are gaps where embedding hasn't completed.
   Only counts chunks from files not matching `search_exclude` — the
   embedding pipeline skips excluded files, so their chunks are
   expected to lack EC records. The count of excluded chunks (those
   belonging exclusively to `search_exclude` files) is reported
   separately so the numbers are transparent.

4. **Orphan EF records**: EF records whose fileID has no
   corresponding EC records reachable via the file's F-record chunk
   list, or no FTS entry.

5. **Dimension consistency**: All EC vectors should have the same
   dimension. Report the distribution of dimensions found (e.g.,
   "742 records at dim=768, 3 records at dim=384"). Flag any that
   differ from the majority.

EC/EF record key/value layouts: see
[record-formats.md](record-formats.md).

### Options

- `--fix` — delete orphan EC and EF records, and delete EC records
  with wrong dimensions (the safe subset of problems that can be
  auto-repaired without re-embedding). Missing embeddings are not
  fixed here — re-embedding requires a running server with the
  model warm.
- `--verbose` / `-v` — show per-file detail instead of just summary
  counts

### Output

Summary line per check category: count of problems found. Exit 0
if clean, exit 1 if any problems detected. With `--verbose`, list
each problem file/record. With `--fix`, report what was deleted.

## Remove `ark vec`

The `ark vec bench` and `ark vec bench-search` subcommands are
obsolete — superseded by `ark embed bench`. Remove the `vec` case from
the command dispatcher and delete `cmd/ark/vecbench.go`.
