# DB
**Requirements:** R1, R2, R3, R5, R6, R7, R28, R29, R30, R33, R40, R31, R32, R34, R127, R128, R129, R136, R138, R130, R135, R137, R161, R162, R163, R166, R167, R168, R196, R197, R198, R199, R200, R236, R246, R248, R237, R238, R239, R240, R241, R242, R243, R244, R245, R247, R249, R250, R251, R252, R253, R254, R255, R257, R258, R382, R383, R392, R506, R510, R563, R564, R565, R566, R567, R568, R605, R606, R617, R618, R619, R621, R622, R624, R625, R626, R627, R628, R629, R630, R636, R637, R638, R663, R666, R667, R682, R664, R665, R668, R692, R714, R716, R719, R720, R721, R723, R765, R766, R909, R2473, R2478, R2479, R2480, R2481, R2482, R986, R987, R988, R989, R990, R993, R995, R1020, R1021, R1022, R1051, R1052, R1053, R1054, R1055, R1056, R1057, R1058, R1059, R1060, R1061, R1062, R1063, R1064, R1065, R1066, R1067, R1068, R1130, R1145, R1146, R1147, R1148, R1149, R1150, R1507, R1508, R1517, R1518, R1519, R1520, R1521, R1522, R1539, R1540, R1541, R1542, R1550, R1551, R1552, R1553, R1554, R1555, R1832, R1871, R1879, R1880, R1881, R1882, R1903, R1909, R1910, R1911, R1912, R1923, R1924, R1925, R1948, R1952, R1976, R1977, R1985, R1986, R1987, R2028, R2086, R2087, R2088, R2090, R2138, R2139, R2140, R2141, R2142, R2147, R2148, R2149, R2150, R2162, R2271, R2272, R2273, R2274, R2275, R2281, R2285, R2286, R2287, R2366, R2367, R2368, R2369, R2370, R2371, R2372, R2373, R2374, R2375, R2376, R2377, R2378, R2386, R2387, R2389, R2390, R2391, R2392, R2393, R2394, R2395, R2396, R2397, R2398, R2399, R2400, R2401, R2403, R2407, R1978, R2913, R2914, R2952, R2954, R2955, R2974, R2976, R2977, R2978, R2981, R2982, R2986, R2989, R2994, R2998, R2999, R3005, R3047, R3051, R3052, R3053, R3054, R3055, R3069, R3071, R3073, R3075, R3077, R3078, R3086, R3087, R3089, R3090, R3092, R3100, R3101, R3102, R3103, R3106, R3107, R3163, R3165, R3171, R3179, R3180, R3186

Main ark facade. Owns the bbolt database lifecycle — the file is microfts2's
(`fts.DB() *bbolt.DB`); ark opens its own `ark` bucket inside it, and a
`bbolt.Tx` spans the `fts` and `ark` buckets so cross-repo reads/writes stay
atomic (R2976, R2977). Coordinates microfts2, the Librarian/EC embedding
pipeline, and the ark bucket. Entry point for all operations.

All operations are serialized through a closure actor (ChanSvc).
The actor is an implementation detail — the public API stays unchanged
(db.Search, db.AddFile, etc.). Each method wraps itself in Svc
(fire-and-forget for watcher mutations) or SvcSync (synchronous for
operations that return results). Callers never see the channel.
Go-side caches (pathCache/pathToID/frecordCache) are protected by the
actor: an off-actor read operation reads through a private fts.Copy()
(withFTS returns a read view bound to the copy), never the shared
original whose caches the reconcile step nils — bringing the remaining
bare readers onto this rule is ongoing tech debt. Methods with
synchronization delay (blocking until queued operations complete)
document this on the API. (R986, R993, R995, R3163)

## Knows
- fts: *microfts2.DB — trigram search engine
- store: *Store — ark's own bucket (R1909, R1910)
- config: *Config — parsed source configuration
- dbPath: string — database directory path
- svc: ChanSvc — closure actor channel, serializes all DB access
- writeQueue: []func() — queued write closures, drained one at a time (R1053)
- writing: bool — true while a write goroutine is in flight (R1067)
- writeIdleWaiters: []chan struct{} — one-shot channels signaled when the
  write queue drains; actor-only, drives WaitWritesIdle (R2989)
- pendingRefresh: map[string]struct{} — paths with a refresh queued or in
  flight; coalesces duplicate watcher events. Guarded by refreshMu (R3005)

## Does
- Init(path, opts): create new database — open microfts2, create ark
  bucket, write default config, register func strategies (lines,
  chat-jsonl, markdown), register chunker strategies from ark.toml
  [[chunker]] entries, create starter tags.md. Seed ark.toml from
  install/ark.toml via BundleReadFile if not present. Write full
  config to I records via Store.WriteConfig. (R1539, R1911, R1912)
- Open(path): open existing database — same sequence, read config.
  Registers func strategies (lines, chat-jsonl, markdown). Registers
  chunker strategies from ark.toml [[chunker]] entries. Passes store
  to Indexer for tag tracking. Diffs loaded config against stored I
  records via DiffConfig. (R1540)
- DiffConfig(loaded, stored *Config) []ConfigChange: compare each field,
  return list of changes with field name, classification (defer,
  fix-minimal, benign), and old/new values. (R1540, R1550-R1555)
- ApplyConfigChanges(changes []ConfigChange): process classified changes.
  Benign: update I records. Fix-minimal: apply fix (e.g. drop embeddings
  for the [embedding] model), update I record. Deferred: write E record, do not
  update I record. (R1553, R1554, R1555)
- registerChunkers(cfg): iterate cfg.Chunkers, construct BracketLang
  from TOML fields, call AddChunker with BracketChunker or IndentChunker
  based on type field. Warn and skip on invalid configs.
- buildBracketLang(cc): translate ChunkerConfig into a microfts2.BracketLang.
  Strings (easy `strings`, full `string_defs`) become BracketGroups with
  non-nil AllowedInner (scan-restricted) and the configured Escape (default
  `\` for easy form). Code brackets (easy `brackets`, full `bracket_defs`)
  become BracketGroups in code mode unless the full-form entry sets
  `allowed_inner` explicitly. `allowed_parent` and per-bracket `escape`
  pass through from full form. Empty `allowed_inner = []` means
  scan-restricted with no inner openers (raw mode), distinct from omitted.
  (R2147, R2148, R2149, R2150)
- JSONLChunker: content-aware JSONL chunker — empty struct implementing
  `microfts2.Chunker` and `microfts2.AppendAwareChunker` (R2273). `Chunks`
  parses JSON, extracts text and thinking blocks (R238, R239); skips
  `tool_use`, `tool_result`, `planContent`, and operational record types
  (R240-R243) because their content is already represented elsewhere in
  the index or is operational metadata. Every other non-empty line emits
  a chunk (R2271); when text extraction yields no content (parseable
  JSON without recognized text shape, malformed JSON, partial JSON at
  the tail), the chunk's content is the raw line bytes (R2272).
  `AppendChunks` re-chunks from the byte offset in `lastLocator` through
  end-of-file; first emitted chunk decides clean vs replace boundary
  via byte-range comparison with the previous last chunk (R2274). Each
  chunk's `Locator` is a byte range encoded by
  `microfts2.EncodeByteRangeLocator` (R2275); `Range` continues to
  carry the 1-based line number for display (R244). Extracts role attr
  from `type`+`isMeta` fields: human, assistant, or skill. For skill
  chunks, parses `Base directory for this skill: PATH` to extract skill
  name attr. (R1507, R1508)
- Close(): close in reverse order (store, fts) (R1923)
- TagList(): delegate to Store.ListTags
- TagCounts(tags): delegate to Store.TagCounts
- TagFiles(tags): delegate to Store.TagFiles, resolve fileids to paths/sizes
- TagContext(tags): delegate to Store.TagContext
- TagDefs(tags): delegate to Store.ListTagDefs, resolve fileids to paths
- Inbox(showAll, includeArchived): query TagFiles("status") for candidate
  fileids, filter to /requests/ paths. When !showAll, build exclusion set
  from TagValueChunks("status","completed") and TagValueChunks("status","denied").
  For each remaining candidate, call Store.FileTagValues to get tag values
  from V records (no file reads). Build []InboxEntry from indexed values.
  RequestID from ark-request or ark-response tag. Kind is "request",
  "response", or "self". Comma-separated to-project normalized to first entry.
  ResponseHandled from response-handled tag (empty if absent).
  RequestHandled from request-handled tag (empty if absent).
  StatusDate from status-date tag (empty if absent).
- Fetch(path): verify file is indexed in microfts2, read and return full content
- Status(): return StatusInfo with file counts, total size, chunk count,
  strategy breakdown, source count, database file size.
  Computes the file size from os.Stat on the database file. Computes chunk
  count by summing ChunkRanges from FileInfoByID per file. Computes
  total size by summing FileLength from FileInfoByID per file. Counts
  files per strategy from StaleFiles.
- StatusDB(): return DBRecordCounts with per-prefix counts for both
  buckets. Delegates to microfts2.RecordCounts() and
  Store.RecordCounts(). The package-level `arkLabels` map is the
  status-db allowlist — one label per shown ark record class in
  record-formats.md (minus no-status-display classes like S); an
  unlabeled class is silently omitted by buildRecordCounts. (R2473,
  R2478, R2479, R2480, R2481, R2482, R3078)
- ChunkStats(filterFiles, excludeFiles []string, tokenize func(string) int):
  iterate all indexed files (filtered by path globs via Matcher rather than
  an inline glob test — R3195; the globs arrive already cwd-anchored from
  the `-files` stack, R3204), call AllChunks(path)
  for each, measure chunk sizes via len(Content) or tokenize callback.
  Collect strategy from StaleFiles. Return ChunkStatsResult with overall
  + per-strategy stats (count, min, max, mean, median, p90, p95, p99).
  Skip files that fail to read. (R1517-R1522)
- LastChunkID(fileID uint64) (uint64, error): return the ChunkID of
  the final chunk in the FTS F-record for a file. Used by
  BatchEmbedChunks for high-water tracking. (R1832)
- QueryTrigramCounts(query): delegate to microfts2, returns trigram counts for CLI grams command
- Add(paths, strategy): index each path. Directories route through
  addDirectory (scanner resolves per-file strategy). For a single
  file with an empty strategy, resolve it the way the watcher does —
  findSourceForPath + Config.StrategyForFile (per-source over global,
  default `lines`) — instead of passing the empty value to microfts2.
  A file outside every configured source with no strategy has nothing
  to resolve against and returns ErrFileOutsideSource (a client-error
  sentinel handleAdd maps to HTTP 400). An explicit strategy is always
  used as-is. (R2954, R2955)
- AddTmpFile(path, strategy, content): instantiate a chunkAccumulator,
  call microfts2.AddTmpFile with WithIndexedChunkCallback(acc.callback).
  After return, write per-chunk tag entries via Store.UpdateTagValues —
  Store dispatches to TmpTagStore by fileid high bit. Track tmpPaths
  for path → fileid lookup. After the actor write commits, call
  `pubsub.PublishTmpDiff(writerID, path, content, strategy)` so
  subscribers see the new tags — prior tag-set is empty so every
  present tag fires (R2281, R2285). (R1948)
- UpdateTmpFile(path, strategy, content): same callback wiring as
  AddTmpFile; the overlay drops the file's prior chunks before re-
  indexing so Store.UpdateTagValues replaces the overlay's entries.
  After the actor write commits, call `pubsub.PublishTmpDiff` which
  diffs against the cached prior tag-set and publishes only changed
  (tag, value) pairs (R2281). (R1948)
- AppendTmpFile(path, strategy, content): same callback wiring;
  Store.AppendTagValues writes only newly-emitted chunks. The overlay's
  auto-create path (file did not exist) routes through AddTmpFile and
  propagates the callback unchanged. After the actor write commits,
  call `pubsub.PublishTmpDiff` with the **whole resulting content**
  (existing + appended) so prior tags don't re-fire on each append
  (R2281, R2286). (R1948)
- RemoveTmpFile(path): call Store.RemoveTagValues(fileid) before
  microfts2.RemoveTmpFile so the tag overlay drops first; then drop
  the trigram overlay; then delete tmpPaths entry. After the actor
  write commits, call `pubsub.ClearTagSetCache(path)` so the next
  AddTmpFile on the same path treats it as new (R2287). (R1944)
- HasTmp(): delegate to microfts2 overlay — true if any tmp:// docs exist
- TmpFiles(): list all tmp:// paths from the overlay
- Init seeding: if ark.toml exists, read case_insensitive/aliases from it
- SourcesCheck(): delegate to Config.ResolveGlobs, add new sources, flag MIA, report orphans
- IsIndexable(path): find which source the path belongs to, get effective
  patterns, call Matcher.Classify with both abs path and source dir.
  Returns true if any source would index it.
- IsWatchableDir(path): the directory analog of IsIndexable for the live
  watcher (R2952). Finds the claiming source, gets effective patterns, and
  returns true iff Matcher.Classify(isDir=true) is not Excluded — the same
  descent rule the Scanner uses. Differs from IsIndexable in two ways: isDir=true
  (directory patterns), and it accepts Unresolved as well as Included (the
  Scanner descends into Unresolved dirs). The watcher's recursive walk skips a
  subtree iff IsWatchableDir is false, so watch coverage equals scan coverage —
  dot-dirs like .scratch/ are watched (dotfiles=true), .git/ is skipped.
- Sweep(): walk every file currently indexed (microfts2.StaleFiles iterator).
  For each path: locate the claiming source (path under src.Dir);
  if no source claims it, remove via Indexer.RemoveFile (R2140).
  Otherwise run Matcher.Classify against that source's effective
  patterns. If the result is not Included, remove via Indexer.RemoveFile —
  the same canonical removal path as `ark remove`, so chunks/tag
  values/ext routings drop consistently (R2141). Sweep is part of every
  Reconcile cycle (R2138, R2142).
- StartActor(): create ChanSvc channel, start RunSvc goroutine. Called
  by Server on startup, or by CLI for cold-start operations. (R986)
- StopActor(): close the ChanSvc channel. Actor goroutine exits on
  channel drain. Called before Close(). (R986)
- enqueueWrite(fn): append write closure to writeQueue. If queue was
  empty and not currently writing, call startWrite(). Called from
  inside the actor only. (R1053)
- startWrite(): dequeue head of writeQueue, set writing=true, spawn
  goroutine. Goroutine calls fts.Copy() to get a cache-less DB copy,
  executes the write closure (file I/O off the actor), then sends a
  reconcile closure back to the actor channel. (R1054, R1055, R1056)
- reconcileWrite(err): called inside the actor from the reconcile
  closure. On success: fts.InvalidateCaches(), commit transaction,
  set writing=false. If writeQueue not empty, call startWrite()
  (continuation); else signal write-idle waiters. On error: log, skip
  batch, set writing=false, continue with next. (R1057, R1058, R1060, R1061)
- withFTS(fts): return a shallow read-only DB view bound to an alternate
  fts (typically fts.Copy()), for an off-actor read operation that must
  not touch the shared original's Go-side caches (pathCache/pathToID/
  frecordCache), which reconcileWrite nils via InvalidateCaches. The copy
  carries its own caches; the overlay pointer is shared (its own mutex,
  untouched by InvalidateCaches) so tmp:// still resolves. Rebinds the
  Searcher to the same fts (Searcher.withFTS); carries no svc / write
  queue. Backs the substrateOp read pass. See specs/db-concurrency.md
  "Protected Resources". (R995, R3163)
- WaitWritesIdle(): block until the write queue has drained — every
  enqueued write committed (`!writing && len(writeQueue)==0`). On the
  actor: if already idle, return at once; else register a one-shot
  waiter channel (writeIdleWaiters), signaled by the write-completion
  path when the queue empties. The completion signal for a rebuild's
  exit-on-idle. Accessed only inside the actor — no extra locking.
  (R2986, R2989; seq-rebuild-read-serve.md#2)
- classifyForWrite(paths): partition file list into config files
  (ark.toml) vs content files. Config files processed synchronously
  in the actor; content files queued via enqueueWrite. (R1052, R1062)
- IndexPathsAsync(paths): schedule per-path index updates through the
  write actor. Coalesces via claimRefreshPaths — only paths without a
  refresh already queued/in-flight are enqueued; each is released as its
  refresh begins so a change during the refresh re-queues. (R991, R3005)
- claimRefreshPaths(paths) []string: under refreshMu, return the subset of
  paths not already in pendingRefresh, marking them queued. (R3005)
- releaseRefreshPath(path): under refreshMu, clear a path's pendingRefresh
  mark so later events re-queue it. (R3005)
- ResolveLink(value) (path, location string, ok bool): resolve an
  `@link:` value to a /content/ URL target. UUID branch first
  (TvidMap.Lookup("id", value) → tvid → V record → chunkid → fileid →
  path + chunk Location); path branch second (microfts2.CheckFile).
  Returns ok=false when neither resolves. Used by wrapTagElements in
  the rendering hot path. (R1976, R1977, R1978)
- ResolveExtTarget(target, sourceDir string) []uint64: return
  chunkids identified by an `@ext:` target spec. Two-phase:
  **decompose** the target into `(BASE, modifier, anchor)` per the
  grammar in `specs/at-ext-parsing.md`, then **resolve**. BASE is
  a `%UUID_VALUE` (UUID branch via `TvidMap.Lookup("id", value)`
  → V record's full chunkid blob) or a PATH (absolute, `~/`-relative,
  or source-relative). The `%` sigil makes BASE disambiguation
  structural — no UUID-vs-path guessing. Relative PATH bases are
  absolutized via `filepath.Join(sourceDir, base)` with minimal
  normalization (no Clean, no EvalSymlinks). `\%` escape on
  TARGETs starting with literal `%` is stripped at lookup. With
  no narrower: path → first chunk (preamble); UUID → every chunk
  carrying the id. With `:"string"` → literal substring match
  scoped to the base; with `:/regex/` → regex match scoped to the
  base; with `:RANGE_STRING` (PATH-only) → chunker dispatch
  against the file. MODIFIER (`[N]` or `^`) post-filters the
  anchor result set; no modifier = all matches. UUIDs reject
  RANGE_STRING anchors. Empty result means broken or unknown.
  (R2366, R2367, R2368, R2369, R2370, R2371, R2372, R2373, R2374,
  R2375, R2376, R2377, R2378, R1985, R1986)
- ChunkInfo(chunkID) (ChunkInfo, error): assemble the metadata
  bundle the workshop UI needs. Resolves chunkID → fileID →
  canonical path; retrieves the chunk's Range, byteStart, byteEnd
  from microfts2; looks up the file's chunker; queries
  ChunkerMetadata (`IsWritable()`, `CommentSyntax()`) if
  implemented or defaults to `(true, "")`; folds in the hardcoded
  read-only zone check (paths under `~/.claude/projects/**` force
  writable=false). Returns `{ChunkID, FileID, Path, Range,
  ByteStart, ByteEnd, Writable, CommentSyntax}`. (R2386, R2387,
  R2389)
- ChunkTextByID(chunkID uint64) ([]byte, error): resolve a
  chunkID to its text bytes. Calls `ChunkInfo` to get `(path,
  range)`, reads through the existing `ChunkText(path, range)`
  primitive. Treats a `nil` text return from `ChunkText` (range
  unresolvable) as the error `"chunk text unavailable"`. Backs
  the `mcp.chunkText` Lua bridge. Sync read; no DB mutation.
  (R2403)
- TmpContent(path string) ([]byte, error): read the stored
  content of an existing `tmp://` document. Validates the
  `tmp://` prefix (non-tmp paths return `"not a tmp:// path"`),
  reads through `db.fts.TmpContent(path)`, returns the bytes.
  Backs the `mcp.tmp_get` Lua bridge. Sync read; no overlay
  mutation. (R2407)
- ReplaceRegion(path, byteStart, byteEnd uint64, newText []byte)
  error: atomically replace the byte range `[byteStart, byteEnd)`
  in `path` with `newText`. Direct file I/O (matching `mcp.setTags`'s
  precedent for Lua-driven file mutation): validates path is indexed
  (rejects tmp:// — those have their own path via `UpdateTmpFile`);
  bounds-checks the range; uses write-to-temp + rename atomicity;
  the watcher picks up the change and triggers reindex. The
  fundamental file-region write primitive — `mcp.replaceRegion` is
  a thin Lua bridge. (R2390, R2391)
- SetExtTag(targetSpec, tag, value string) error: author an
  `@ext` routing into the mirror tree under `~/.ark/external/`.
  Resolves `targetSpec` to a target file; computes the source-slug
  (path-as-slug, `/` → `-`) of the source root containing the
  target; the mirror path is
  `~/.ark/external/<slug>/<target-path-within-source>.md` — unless the
  target's source sets `ext_mirror`, in which case the base moves in-tree
  to `<source-root>/<ext_mirror>/<target-path>.md` (R3171); a target
  already inside its source's `ext_mirror` dir has no mirror (self-mirror
  rejected). Reads
  the mirror file (empty if absent), then **collapses** every
  `(TARGET, tag)` span (byte-for-byte TARGET, same tag name) to
  the one new value — first match rewritten in place, later
  matches dropped (a line left with no tags is dropped whole) —
  otherwise appends `@ext: TARGET @tag: value`. Single-value case
  == plain in-place replace. Shares `upsertExtTag` with AddExtTag.
  Direct file I/O with temp+rename atomicity (matching
  `mcp.setTags`); the watcher/indexer reindex the mirror file so
  the in-memory ext map updates. (R2392, R2393, R2394, R2395)
- AddExtTag(targetSpec, tag, value string) error: append a
  `(TARGET, tag, value)` routing to the mirror file, leaving
  existing values in place so a `(TARGET, tag)` can carry several
  values; an exact `(TARGET, tag, value)` already present is a
  silent no-op. Same resolution / mirror-path / temp+rename write
  as SetExtTag (shared `upsertExtTag`, distinguished by op).
  (R2392, R2393, R2394, R3047)
- RemoveExtTag(targetSpec, tag, value string) error: remove an
  `@ext` routing from the mirror tree. Locates the mirror file
  (missing → silent no-op), finds **every** matching
  `@ext: TARGET @tag:` line (missing → silent no-op), removes the
  matching routing(s): empty `value` removes all `(TARGET, tag)`
  spans, non-empty `value` removes only spans whose value matches.
  Single-tag lines: delete the whole line including trailing
  newline. Multi-tag lines (rare in v1): remove only the matching
  `@tag: value` span, preserve the rest. Direct file I/O with
  temp+rename atomicity. (R2396)
- The mirror-authoring machinery (`mutateExtLine` / `applyExtMirrorEdit`
  / `upsertExtTag`) is **class-aware**: it operates on any `@ext-*`
  class selected by a named `extClass` marker (`@ext`, `@ext-candidate`,
  `@ext-judgment`) rather than a hardcoded `@ext:` literal. Set/Add/Remove
  supply the committed class; the staging methods below supply the
  candidate and judgment classes. All three classes index as ordinary
  tags (F/V for the outer name); the RC/RJ derivation for candidate and
  judgment is a later pass. (R3051, R3052)
- CandidateExtTag(targetSpec, tag, value, insight, disposition string, replace bool) error:
  author an `@ext-candidate` (proposed routing) into the mirror tree,
  carrying an optional quoted `insight` placed **first, before the
  TARGET** (no `@` sigil), a `disposition` (internal|external, default
  external) right after the marker, and an optional bare `replace` token after
  the disposition (accept collapses the target's values instead of the default
  add). The line is stamped with a **first-seen date** (today) that is frozen
  on later bumps. Append semantics: a differing insight, disposition, *or*
  add-vs-`replace` is a new proposal line with its own tally — all three are
  part of the line identity — so distinct proposals on one `(TARGET, tag)` are
  preserved. An exact-identity duplicate (TARGET, tag, value, insight,
  disposition, replace) **increments the line's `@count`** rather than
  no-opping — the repetition tally lives in the file (R3075, superseding
  R3053's no-op) and the frozen date is preserved. Same resolution /
  mirror-path / temp+rename write as SetExtTag, on the candidate class.
  Runs as one closure-actor op, so the read-modify-write of `@count` cannot
  lose a concurrent bump. (R3053, R3075, R3090, R3092, R3104, R3105)
- AcceptExtTag(targetSpec, tag, value string) error: commit every matching
  `@ext-candidate` line (byte-for-byte TARGET, same tag, value filter when
  non-empty), each per its `(disposition, replace)` → **four cells**:
  external-add appends an `@ext` mirror edge (`extOpAdd`), external-replace
  collapses the mirror's values of the tag to this one (`extOpSet`),
  internal-add writes the tag into the target file's body via the chunker's
  `InsertTag` stencil (`writeInternalTag`; falls back to external when the type
  can't host it or the file isn't writable), and internal-replace locates the
  existing inline `@tag:` line and rewrites it in place (degrading to
  internal-add when absent). The candidate's leading date, disposition,
  `replace`, `insight`, and `@count` are dropped, and a positive
  `@ext-judgment @count:+1` lands, in the same file edit. On reindex the RC
  derivation drops and the edge/inline tag lands, so accept closes the
  derived-proposal loop by construction (Store.AcceptDerived retires). Missing
  file/line is a silent no-op. (R3054, R3071, R3100, R3101, R3102, R3103, R3106, R3107)
- RejectExtTag(targetSpec, tag, value string) error: rewrite matching
  `@ext-candidate` line(s) to a single tag-name-only
  `@ext-judgment: <date> TARGET @tag: @count: -N`, a durable judgment
  stamped with a first-seen date (frozen on later decrements). It creates
  the judgment (`@count: -1`) or decrements the existing signed `@count`;
  `@count` reaching 0 removes the line (absent ≡ neutral). The signed count
  carries both directions — negative rejection magnitude here, positive
  reinforcement for a future producer. One closure-actor read-modify-write.
  Missing file → no-op. (R3055, R3069, R3075, R3090)
- SuggestExtLocator(chunkID) (LocatorSuggestion, error): run the
  three-layer locator algorithm for the workshop's `@ext`
  authoring widget. Layer 1 — line-prefix token-minimum unique
  among other chunks in the same file (smallest token count;
  earliest line wins ties; case-insensitive uniqueness compare;
  emit regex if prefix contains a literal `"`). Layer 2 —
  rare-trigram-anchored substring (mid-line trigram unique to
  this chunk, expanded to word boundaries, clamped 12–60 chars).
  Layer 3 — `absolute` (the chunk's range string), unavailable
  when the range starts with `"` or `/` (non-conforming per the
  soft chunker contract). Returns `{Base, BaseValue, Locator,
  LocatorKind, LocatorText, WithinFileDupCount, CrossFileScope}`.
  Base = `"uuid"` when the chunk has `@id`, else `"path"`.
  CrossFileScope is computed by running the same resolution path
  the resolver would, scoped to the file for path bases or all
  files for UUID bases. (R2397, R2398, R2399, R2400, R2401)
- LocatorSuggestion.Target() string: assemble the suggestion into one
  `@ext` TARGET string (inverse of ParseExtTargetParts) — bare →
  `%uuid`/path, absolute → `:range`, string → `:"…"`, regex → `:/…/` —
  escaping the narrower delimiter (and backslash) so the closing delimiter
  stays findable. (R3077)
- SuggestAnchor(path, range) (string, error): resolve a chunk location (a
  bare chunkID as `range` with empty `path`, or a path+range looked up
  among the file's chunk entries via `chunkIDForLocation`) to a chunkID,
  then return `SuggestExtLocator(chunkID).Target()`. Backs `ark chunks
  -anchor` and `POST /chunks/anchor`; generic chunk addressing, not
  `@ext`-only. (R3077)
- AllTagsForFilePath(path) ([]TagValue, error): resolve path→fileID via
  PathFileID, then Store.AllTagsForFile. Backs `ark tag {chunk,get} FILE
  -all` and `POST /tags/chunk` with an empty range. (R3086, R3089)
- AllTagsAtLocation(path, range) ([]TagValue, error): resolve (path,range)
  →chunkID via chunkIDForLocation, then Store.AllTagsForChunk. Backs `ark
  tag chunk FILE:TARGET` and `POST /tags/chunk` with a non-empty range.
  (R3087, R3089)
- chunkFileID(txn, chunkID) (uint64, bool): branch on
  IsOverlayID(chunkID). Overlay chunkids resolve via
  Store.filesForChunk(chunkID), which routes through the
  SetChunkResolver wiring to TmpTagStore.FilesForChunk. Persistent
  chunkids continue reading fts.ReadCRecord(txn, chunkID). Used
  by ExtMap.Rebuild and the indexing self-reference check on
  overlay sources. (R2028)
- ChunkFileID(chunkID) (uint64, bool) (R3186): the txn-free wrapper —
  opens its own read view and delegates to `chunkFileID`, the same shape
  `ChunkInfo` uses. Exists for callers that need only the owning fileID
  and would otherwise pay for `ChunkInfo`'s full bundle (its
  `FileInfoByID` plus a linear scan of the file's chunk list); the recall
  substrate's per-candidate path-scope gate is the motivating caller.

## Collaborators
- Config: loads and validates ark.toml
- Store: ark's own bucket (missing, unresolved, tags, EC, EF)
- Scanner: walks directories (uses Config + Matcher)
- Indexer: adds/removes files in microfts2 and ark store, extracts tags
- Searcher: queries microfts2 + Librarian and merges results
- Librarian: embeds queries and ranks chunks via EC records (R1915, R1916)
- Matcher: pattern matching for IsIndexable

## Sequences
- seq-add.md
- seq-search.md
- seq-write-actor.md
- seq-tmp-tag-overlay.md
