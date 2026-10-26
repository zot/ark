# FilenameCase
**Requirements:** R3626, R3627, R3628, R3629, R3630, R3631

One spelling per file on case-insensitive filesystems (specs/filename-case.md):
outside paths take their on-disk spelling at entry, and a path is live only
under that spelling.

## Knows
- nothing; stateless

## Does
- DiskSpelling(path): Windows: walk the cleaned path from its volume, and for
  each component ask the filesystem (`FindFirstFile` on the prefix so far) for
  its stored name; stop at the first component that does not exist or holds
  `*?[`, keeping the rest literal. Other platforms: identity. R3626
- respelled(path): `DiskSpelling(path) != path`; the liveness test's extra
  clause. R3629
- foldFilenames: const, true on Windows; the value of microfts2's
  `CaseInsensitiveFilenames` at create. R3630

## Collaborators
- LinkTable: ResolveVia applies DiskSpelling to the literal before rewriting,
  so every resolved outside path is disk-spelled; Observe treats a respelled
  link path as unresolvable. R3627, R3629
- Server: the watch loop rewrites each event path before classifying it. R3628
- Config: LoadConfig rewrites each `[[source]].dir` after tilde expansion. R3628
- Store: unresolvedIsLive treats a respelled path as vanished. R3629
- DB: Create passes foldFilenames as `CaseInsensitiveFilenames`. R3630
- Indexer: the scan store's unchanged-content skip also requires spelledAsStored (the
  committed `Names[0]` equals the path), so a case-only rename re-indexes and
  stores its new spelling; RefreshStale (before its pattern match) and
  RefreshAsync spell each stale path, which the index gives in its stored
  spelling, as the disk does. R3631
