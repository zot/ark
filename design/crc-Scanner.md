# Scanner
**Requirements:** R8, R9, R14, R15, R35, R75, R106, R206, R207, R208, R1644, R1645, R1646, R1647, R1648, R1649, R1650, R1651, R3352, R3353, R3373

Walks configured source directories and classifies files using the
pattern matcher. Identifies new files to index and new unresolved
files.

## Knows
- config: *Config — source configuration
- matcher: *Matcher — pattern evaluation
- fts: *microfts2.DB — to check if files are already indexed
- emptyFiles: *EmptyFiles — in-memory map of size-zero paths → mtime. Access is confined to the DB actor goroutine (R1644, R1651)

## Does
- Scan(config): walk all source directories, classify each file
  - For each file: Matcher.Classify(includes, excludes, absPath, src.Dir, isDir) — passing `src.Dir` selects the **source-scoped** anchoring context, and passing both path forms lets the matcher pick filesystem-absolute or root-relative per pattern (R3196, R3198)
  - included + size == 0 + already in empty-set with current mtime → skip (R1645, R1646)
  - included + size == 0 + not in empty-set (or mtime changed) → record in set, add to EmptyFiles result (R1647)
  - included + size > 0 + not already indexed → add to NewFiles list (R1649)
  - unresolved + not already tracked → add to NewUnresolved list
  - excluded → skip (don't walk into excluded directories)
- **Directory symlink: record it as an entity and stop** (R3352, R3373).
  `lstat` says the entry is a symlink to a directory → hand the path to
  `LinkTable.Observe` and **do not descend**. The files beneath it are still
  indexed exactly once, by whichever source walk reaches them through real
  directories, so the alias lives on the *link* rather than on each file
  below it — one record per link against many thousands of reachable files
  - tracked → the link is an indexed entity and becomes a legal address
    prefix (R3364, R3380)
  - proposed → add to NewUnresolved with reason `symlink`, naming the link
    that proposed it, since the record is keyed on the target and cannot
    otherwise explain itself (R3355, R3357)
  - Recording rather than *following* is what keeps one file from becoming
    several index entries governed by different include/exclude rules.
    Recording rather than *skipping* is what makes the link something ark
    can talk about at all: today a directory symlink is classified as a
    non-directory, matched against file patterns, and silently dropped when
    its name fails them, so its contents are unreachable with no error and
    no log line
- **A scan meeting a cycle terminates** (R3353). The walk itself cannot
  enter one, because it never descends through a link; the exposure is in
  chain-following, which is `LinkTable.Observe`'s guard. A link may point at
  a directory containing links back to it and ark's own layout has one, so
  termination is a property of the walk rather than of the data
- **The refresh path must recognise a directory-symlink entry.** A change to
  a tracked link — repointed, replaced, removed — arrives through the
  ordinary per-path refresh, not through a full scan, and that path today
  classifies a directory symlink as a non-directory, tests it against file
  patterns, and drops it. The event already fires and nothing acts on it.
  Every association verified by the single-hop tier rests on this
  (crc-ExtMap.md, R3384), and the failure is invisible when present: the
  association keeps returning its old, once-correct answer
- ScanResults: struct with NewFiles []FileEntry, NewUnresolved []UnresolvedRecord, EmptyFiles []string
- FileEntry: struct with path, strategy (from Config.StrategyForFile with source's strategies merged over global — matched through Matcher in the same source-scoped context, R3202)

## Collaborators
- Config: provides directories and effective patterns
- Matcher: classifies each file path
- Store: persists new unresolved files, cleans stale unresolved
- EmptyFiles: map[path]mtime — consulted during walk; serialized through the DB actor
- LinkTable: classifies each directory symlink the walk meets — tracked or
  proposed — and follows its chain with the cycle guard (crc-LinkTable.md)

## Sequences
- seq-add.md
- seq-server-startup.md
- seq-empty-file-skip.md
