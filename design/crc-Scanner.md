# Scanner
**Requirements:** R8, R9, R14, R15, R35, R75, R106, R206, R207, R208, R1644, R1645, R1646, R1647, R1648, R1649, R1650, R1651

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
  - For each file: Matcher.Classify(includes, excludes, absPath, src.Dir, isDir) — passes both forms so the matcher can pick filesystem-absolute or source-relative per pattern (R2133)
  - included + size == 0 + already in empty-set with current mtime → skip (R1645, R1646)
  - included + size == 0 + not in empty-set (or mtime changed) → record in set, add to EmptyFiles result (R1647)
  - included + size > 0 + not already indexed → add to NewFiles list (R1649)
  - unresolved + not already tracked → add to NewUnresolved list
  - excluded → skip (don't walk into excluded directories)
- ScanResults: struct with NewFiles []FileEntry, NewUnresolved []UnresolvedRecord, EmptyFiles []string
- FileEntry: struct with path, strategy (from Config.StrategyForFile with source's strategies merged over global)

## Collaborators
- Config: provides directories and effective patterns
- Matcher: classifies each file path
- Store: persists new unresolved files, cleans stale unresolved
- EmptyFiles: map[path]mtime — consulted during walk; serialized through the DB actor

## Sequences
- seq-add.md
- seq-server-startup.md
- seq-empty-file-skip.md
