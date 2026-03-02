# Sequence: Add Files

Covers `ark add <file-or-dir>` and `ark scan`. When given a directory,
walks per config and classifies files before indexing.

## Participants
- CLI
- DB
- Config
- Scanner
- Matcher
- Store
- Indexer
- microfts2
- microvec

## Flow: ark add <directory>

```
CLI ──> DB.Add(path)
         │
         ├──> Config.EffectivePatterns(source)
         │     └── returns includes, excludes for this source dir
         │
         ├──> Scanner.Scan(config)
         │     │
         │     ├── walk directory tree
         │     │    for each file:
         │     │    ├──> Matcher.Classify(includes, excludes, path)
         │     │    │     ├── included? ──> check if already indexed
         │     │    │     │                  microfts2.CheckFile(path)
         │     │    │     │                  if fresh → skip
         │     │    │     │                  if not indexed → newFiles list
         │     │    │     ├── excluded? ──> skip (if dir, don't descend)
         │     │    │     └── unresolved? ──> newUnresolved list
         │     │    └── (repeat for all files)
         │     │
         │     └── return ScanResults{newFiles, newUnresolved}
         │
         ├──> Store.AddUnresolved(path, dir)  [for each new unresolved]
         ├──> Store.CleanUnresolved()         [remove gone-from-disk entries]
         │
         └──> for each newFile:
               Indexer.AddFile(path, strategy)
               │
               ├──> microfts2.AddFile(path, strategy)
               │     └── returns fileid, (chunk offsets in FileInfo)
               │
               ├──> read chunk text from file using offsets
               │
               └──> microvec.AddFile(fileid, chunks)
```

## Flow: ark add <file>

```
CLI ──> DB.Add(path)
         │
         └──> Indexer.AddFile(path, strategy)
               │
               ├──> microfts2.AddFile(path, strategy)
               │     └── returns fileid
               │
               ├──> read chunk text from file using offsets
               │
               └──> microvec.AddFile(fileid, chunks)
```
