# Scanner
**Requirements:** R8, R9, R14, R15, R35, R75, R106, R206, R207, R208

Walks configured source directories and classifies files using the
pattern matcher. Identifies new files to index and new unresolved
files.

## Knows
- config: *Config — source configuration
- matcher: *Matcher — pattern evaluation
- fts: *microfts2.DB — to check if files are already indexed

## Does
- Scan(config): walk all source directories, classify each file
  - For each file: Matcher.Classify with effective patterns
  - included + not already indexed → add to "new files" list
  - unresolved + not already tracked → add to "new unresolved" list
  - excluded → skip (don't walk into excluded directories)
- ScanResults: struct with newFiles []FileEntry, newUnresolved []string
- FileEntry: struct with path, strategy (from Config.StrategyForFile with source's strategies merged over global)

## Collaborators
- Config: provides directories and effective patterns
- Matcher: classifies each file path
- Store: persists new unresolved files, cleans stale unresolved

## Sequences
- seq-add.md
- seq-server-startup.md
