# DB
**Requirements:** R1-3, R5-7, R28-34, R40, R127-130, R135-138, R161-163, R166-168, R196-200, R236-255, R257, R258, R382, R383, R392, R506, R510, R563-568, R605, R606, R617-619, R621, R622, R624-630, R636-638, R663-668, R682, R692, R714, R716, R719-721, R723, R765, R766, R909, R986-990, R993, R995, R1020-1022, R1051-1068, R1130, R1145-1150, R1507, R1508, R1517-1522, R1539-1542, R1550-1555, R1832, R1871, R1879-1882, R1903, R1909-1912, R1923-1925, R1948, R1952, R1976-1978, R1985-1987, R2028, R2086-2088, R2090, R2138-2142, R2147-2150, R2162, R2271-2275, R2281, R2285-2287, R2366-2378, R2386, R2387, R2389-2401, R2403, R2407, R2473, R2478-2482, R2913, R2914, R2952, R2954, R2955, R2974, R2976-2978, R2981, R2982, R2986, R2989, R2994, R2998, R2999, R3005, R3047, R3051-3055, R3069, R3071, R3073, R3075, R3077, R3078, R3086, R3087, R3089, R3090, R3092, R3100-3103, R3106, R3107, R3163, R3165, R3171, R3179, R3180, R3186, R3214, R3216, R3217, R3220, R3221, R3223, R3244, R3245, R3248, R3255, R3263, R3264, R3266, R3344, R3346, R3351, R3358-3361, R3367, R3370, R3381, R3382, R3390, R3395, R3433-3436, R3567-3569, R3571, R3574

Main ark facade. Owns the bbolt database lifecycle — the file is microfts2's
(`fts.DB() *bbolt.DB`); ark opens its own `ark` bucket inside it, and a
`bbolt.Tx` spans the `fts` and `ark` buckets so cross-repo reads/writes stay
atomic (R2976, R2977). Coordinates microfts2, the Librarian/EC embedding
pipeline, and the ark bucket. Entry point for all operations.

All operations are serialized through a closure actor (**crc-ChanSvc.md**,
which owns the actor's own contract — ordered delivery, work off the loop
goroutine, the panic policy). It is a shared component rather than a DB
detail: the recall watcher and the pty host hold their own. What stays a
detail here is that the public API is unchanged (db.Search, db.AddFile,
etc.) — each method wraps itself in Svc (fire-and-forget for watcher
mutations) or SvcSync (synchronous for operations that return results),
and callers never see the channel. Callers may rely on posts from one
goroutine running in the order posted (R3344).
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
- svc: ChanSvc — the closure actor serializing all DB access (crc-ChanSvc.md). Not a bare channel: it keeps a queue and an in-flight flag so caller work runs off its loop goroutine, which is what makes delivery ordered (R3344, R3346).
- writeQueue: []func() — queued write closures, drained one at a time (R1053)
- writing: bool — true while a write goroutine is in flight (R1067)
- writeIdleWaiters: []chan struct{} — one-shot channels signaled when the
  write queue drains; actor-only, drives WaitWritesIdle (R2989)
- pendingRefresh: map[string]struct{} — paths with a refresh queued or in
  flight; coalesces duplicate watcher events. Guarded by refreshMu (R3005)
- reconcilePending: bool — a reconcile scan is queued but has not begun, so
  further requests merge into it rather than queueing scans of their own
  (R346, R3351). Actor-owned, hence a plain bool with no guard. Cleared as the
  scan *starts*: a request arriving mid-scan must queue a fresh one, since a
  running scan cannot be relied on to have seen a change made after it walked
  past that part of the tree. Same rule and reason as pendingRefresh's
  per-path clearing.
- reconcileWalking / reconcileWalkPending: bool — the reconcile scan enumeration
  now runs off the actor in a goroutine; reconcileWalking is set while it is in
  flight and reconcileWalkPending records a reconcile that arrived during it.
  Actor-owned, plain bools with no guard. reconcileWalking also holds writeIdle
  open so a rebuild's WaitWritesIdle cannot return mid-walk. (R3433, R3434)
- reconcileStats: reconcileCounts — the stale/missing/unresolved counts (plus
  the unresolved-by-reason map) the last reconcile computed, with the wall-clock
  time it computed them. Written on the actor by captureReconcileStats, read via
  the bindStatusRead snapshot; actor-owned, a plain struct with no guard. The
  zero value (at.IsZero()) means no reconcile has completed in this process,
  which `ark status` renders as `-`. This is what lets status report staleness
  without a fresh corpus walk. (R3435)

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
  reads each line through TranscriptReader (R3553) and emits one chunk per
  user or assistant turn with text: `chatChunkText` (text, thinking and
  narration blocks, one per line; R238, R239) and `chatChunkRole`. Tool calls,
  tool results, turns without text and every record type the reader drops
  yield nothing (R240-R241, R3567). The only raw-bytes chunk is a partial
  trailing line not yet a complete record (R3567).
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
- resolvePath(path) (R3359, R3360, R3361): the one translation every **door**
  calls — `Fetch`, `IsIndexed`, `ReadSourceFile` (which the `/content` URL
  handler reaches through, so resolving once covers both). One helper rather
  than five open-coded calls, because the failure R3359 names is precisely a
  surface that resolves disagreeing with one that does not, silently, as a
  plausible "not indexed".
  - **Absolute is not resolved.** `filepath.Abs` on tilde-expanded input yields
    a *lexically* absolute path whether or not a component is a link, which is
    how an `@ext` target came to be stored under a spelling its own file would
    never match.
  - A path traversing no tracked link comes back unchanged; a dangling link
    resolves to its (non-existent) target, so the door answers ark's ordinary
    not-indexed rather than a resolution error; only a cycle falls back to the
    literal path (R3361).
  - Read off the actor with no synchronization, which the table's published
    snapshots make safe.
- resolvePathVia(path) (R3382, R3384, R3385): resolvePath plus the link chain
  the walk crossed. Exists because a **tier is a property of the resolution
  chain, not of a LinkEntry** — one address can cross two separately-tracked
  links (ark's own layout does: a link living inside another link's target
  tree takes two rewrites), and the premise recorded for that address has to
  be verified on a schedule covering both. Handing the concatenated hops to
  `ClassifyChain` gets that for free, since a two-link address is simply
  multi-hop and needs no special case. Nil hops means no tracked link was
  crossed, which is the same thing as needing no premise at all (R3372).
- expandAliases(canonical) (R3367): the inverse of `resolvePath` — every
  address naming the file at a canonical path. The half that runs when a
  *file* changes, and the one with no natural forcing function. A routing is
  keyed under the TARGET **as its author wrote it** (the key stays unresolved;
  only lookups resolve — O194), so asking `extByAnchor` for the canonical name
  alone finds nothing when that author spelled the path through a link. The
  forward half lives in `resolveExtFilePath`, which resolves before `CheckFile`
  so a target written through a link finds its file at all; a canonically
  written `@ext` is unaffected, since a canonical path crosses no tracked
  prefix. Both halves are fire-alarmed, because each failure is invisible when
  absent: a routing that never fires looks exactly like one nobody wrote.
- Fetch(path): verify file is indexed in microfts2, read and return full content
- Scan(): walk, index, flag. A file the patterns admitted, handed to a
  chunker, that yields `ErrNoChunks` becomes an **`unindexable`** candidate
  rather than being silently skipped (R3358). That is a statement about ark's
  current chunkers and not about the file, so it is recorded and persists, and
  adding a chunker revives every such candidate with no user action. Empty
  files are a different thing, already handled by `EmptyFiles` (R1648), so
  this reaches only files with content nothing could chunk. On the success
  side the file's candidate is removed, because nothing else drops one:
  `CleanUnresolved` tests whether the *path* still exists, and an indexable
  file's path exists.
- ReloadConfig(): rebind the LinkTable's config and re-derive the
  track/propose predicate (R3381), feeding `Rebuild` the tracked links **and**
  the links named by symlink-reason U records. That second list is what makes
  **promotion** work: a proposed link is in no table at all (`Observe` records
  only the tracked branch), so its identity lives solely in the record it
  wrote. Links that come back proposed are re-recorded; candidates whose links
  are no longer proposing are withdrawn. The withdrawal test is on the *link*,
  not the target — a promoted link still resolves to exactly the same
  directory, so a liveness question asked of the target would keep the record
  forever. "No longer proposing" covers both ways a proposal ends: the target
  became a source, or the link went away.
- Status(rc, tmpCount): return StatusInfo with file counts, total size, chunk
  count, strategy breakdown, source count, database file size. **Runs off the
  DB actor** on a bound fts.Copy() view (R3436) and **never walks the corpus**
  (R3435): it does not call StaleFiles and stats/hashes no indexed file. The
  stale/missing/unresolved counts and their as-of time come from `rc` — the
  last reconcile's captured counts, passed in by the caller; the tmp-file count
  comes from `tmpCount`, snapshotted on the actor at bind time (the view carries
  no tmpPaths). Computes the database file size from os.Stat on the database
  file (one stat, not the corpus). Enumerates the file records via FileIDPaths;
  computes chunk count by summing ChunkRanges from FileInfoByID per file, total
  size by summing FileLength, and the per-strategy tally from each FRecord's
  Strategy — all off the bound view. Fills `StatusInfo.ReconcileAt` from `rc.at`
  (zero → `ark status` prints `-` for the three counts).
  Also fills `StatusInfo.UnresolvedByReason` from `rc` (R3370): the total alone is
  unreadable once one reason dominates, and `unindexable` normally does, so a
  handful of actionable entries would hide behind thousands no user action can
  clear. A record written before reasons existed counts as `uncovered`, which
  is what it was.
  Also fills `StatusInfo.Warnings` from the E records (R3395), so the
  warnings block travels with the status rather than depending on the
  caller making its own index open — which is why a proxied `ark status`
  used to print none.
- StatusDB(): return DBRecordCounts with per-prefix counts for both
  buckets. Delegates to microfts2.RecordCounts() and
  Store.RecordCounts(). Both are index-wide record scans, so — like Status —
  StatusDB runs off the DB actor on the bound fts.Copy() view (R3436). The
  package-level `arkLabels` map is the
  status-db allowlist — one label per shown ark record class in
  record-formats.md (minus no-status-display classes like S); an
  unlabeled class is silently omitted by buildRecordCounts. (R2473,
  R2478, R2479, R2480, R2481, R2482, R3078)
- captureReconcileStats(staleCount): on the DB actor, record what the reconcile
  just computed for `ark status` to report off-actor — `staleCount` (the
  stale-file partition), plus the M-record and U-record counts and the
  unresolved-by-reason breakdown, stamped with the wall-clock time — into the
  actor-owned `reconcileStats` field. Called at the end of RefreshAsync, which
  runs on the actor in the reconcile's apply step. (R3435)
- bindStatusRead(): on the DB actor, capture everything a status read needs to
  run off the actor — a read-only view over fts.Copy(), the actor-owned
  reconcileStats snapshot, and the tmp-file count (read under tmpMu) — into a
  statusBinding. A trivial in-memory hop; holds the actor only for the capture,
  not the enumeration. (R3436)
- readStatusOffActor(read): bind the status view on the actor (bindStatusRead
  via one Sync), then run read(binding) OFF the actor on the caller's goroutine.
  The single seam the off-actor fire alarm injects against — moving read inside
  the bind Sync would hold the actor for the whole enumeration. (R3436)
- ChunkStats(filterFiles, excludeFiles []string, tokenize func(string) int):
  iterate all indexed files (filtered by path globs via Matcher rather than
  an inline glob test — R3195; the globs arrive already cwd-anchored from
  the `-files` stack, R3204), call AllChunks(path)
  for each, measure chunk sizes via len(Content) or tokenize callback.
  Collect strategy from StaleFiles. Return ChunkStatsResult with overall
  + per-strategy stats (count, min, max, mean, median, p90, p95, p99).
  Skip files that fail to read. (R1517-R1522) A missing chunk (stale, see
  design.md "Stale Chunks") is skipped, not measured as size 0. (R3571)
- ChunkMissing(path, rangeLabel) bool: GetChunks(path, range, 0, 0) and
  report its `Missing` flag, so a caller holding only a failed ChunkText can
  tell a stale chunk from a range the file never had. (R3574)
- Missing chunks read as absent (R3571): `chunkContent` answers not-found;
  the ext string/regex anchor walk and `chunkForVerse` skip them;
  `ChunkAttrs` returns nil; `SuggestExtLocator` skips them as comparison
  chunks and, for a missing target, errors naming the stale file.
  `FetchChunkContent` clears a missing entry's attrs, so `ark chunks` and
  `POST /chunks` never emit them (R3572).
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
- postCoalescedReconcile(scan): post scan so at most one reconcile is active and
  one pending (R346, R3351). The gate is a **separate, earlier closure** than the
  scan, and that is the mechanism rather than a style choice: the pending mark
  must clear when the scan *begins*, and deciding in one closure while scanning
  in the next is what makes that expressible. It is the
  post-the-follow-up-behind-the-message idiom, sound only because the actor
  delivers in order (R3344). A merged request's scan closure is **discarded**, so
  callers must pass work equivalent on every call — a reconcile is, by
  construction. Server.reconcile is the sole caller.
- launchReconcileWalk(walk, apply, relaunch): run the reconcile scan enumeration
  off the actor and coalesce it. On the actor: if a walk is in flight, mark
  pending and return without launching a second; else set reconcileWalking, run
  walk() in a goroutine (off the actor, which stays free for interleaving writes),
  then post apply() back on the actor; when apply returns the flag clears,
  write-idle is re-checked, and a pending reconcile is re-triggered once. The
  off-actor property and the coalescing are both invisible when violated, so this
  primitive is what makes them testable with a blocking fake walk. (R3433, R3434)
- applyReconcileScan(results): apply an off-actor walk's results on the actor —
  the deferred directory-symlink Observe (observeScannedLink over PendingLinks),
  the batched metadata writes (recordScannedLinks / AddUnresolvedRecord /
  CleanUnresolved), the synchronous config-file adds, the empty-file evictions,
  and the per-file scan dispatches. Mirrors ScanAsync's post-walk body. (R3383, R3433)
- observeScannedLink(results, path): run one deferred directory-symlink's Observe
  on the shared, actor-owned LinkTable, appending to TrackedLinks or NewUnresolved
  exactly as the inline walk did. (R3352, R3355, R3357, R3433)
- actor lifecycle: the ChanSvc is built with the DB (`newChanSvc`) and its loop
  started in `OpenWithTimeout`, once the rest of the DB's state exists; `Close`
  stops it. There is no `StartActor`/`StopActor` pair — this card named one until
  2026-07-31 and no such methods have ever existed. Stopping is a **sentinel
  post, not a channel close**: every closure posts back on completion (R3346), so
  closing would let a work goroutine finishing after the close panic on a send.
  Everything posted before the stop still drains, since the sentinel queues
  behind it. (R986, R3348)
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
  Calls SyncLinkPath for each path **first, on the actor** (R3390): the
  link table is actor-owned and the write actor runs off the actor, so a
  path that has stopped being a link must be forgotten before its
  replacement is queued as a file
- SyncLinkPath(path): bring the link table in line with one path and, when
  the link's target lies outside every source, record that target as an
  unresolved candidate naming the proposing link — the same disposal the
  scan makes, so the two surfaces agree (R3390, R3355, R3357). **Actor-only**;
  the table is DB-actor state (R3349). Reached two ways, and they are not
  redundant: from IndexPathsAsync for a path that *is* indexable (a link
  replaced by an indexable file), and directly from the watch loop for one
  that is not (every ordinary link event, since a link matches no file
  pattern)
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
- resolveExtPathBase(parts) / resolveExtFilePath(parts): the PATH-base entry
  point and the generic real-file resolution behind it. The entry point does
  one thing — dispatch on `targetStrategy` (R3220) — so the bible resolver is
  reached *before* the `CheckFile` that a virtual path cannot satisfy;
  everything else goes to resolveExtFilePath, whose behavior is unchanged.
- targetStrategy(path): the strategy governing a TARGET's path. An indexed file
  answers from the index (`FileStrategy`); anything else falls back to matching
  the global map against the **absolute** path (`Config.StrategyForPath`),
  where the hook-derived source-prefixed entry classifies a virtual
  `BIBLE/<Book>` address. Returns "" for a path no rule claims. (R3218, R3220)
- resolveBibleTarget(parts): the **strategy-dispatched** bible address
  decoder (R3220) — the bible resolver owns the whole bible path, replacing
  #41's inline `if FileStrategy == bibleStrategy` branch in
  resolveExtPathBase, which assumed a real indexed file (`CheckFile` first)
  and so could not handle the virtual form. Reached when the target's strategy
  is `bible` (which the source-prefixed in-memory entry, R3218, gives even to a
  virtual `BIBLE/<Book>` path). Two steps:
  - **Virtual-path rewrite (R3216):** if the path carries a `BIBLE/<Book>`
    segment, parse the anchor's chapter and look up the book index
    (lookupBookFile) → the real file, rewriting BASE to it. `BIBLE/` is the
    reserved marker, not a real path.
  - **Chapter.verse decode (R3179, R3180):** parseChapterVerse splits the
    anchor; return the chunk whose `chapter` matches and whose `verses` span
    contains the verse. A book, chapter, or verse that does not exist resolves
    to **nothing** — no fall-through to a location match or the first chunk.
- lookupBookFile(source, book, chapter) → path: read the book-index record
  keyed `B <source> \0 <book> \0 <chapter>` (R3214). Exact key — chapters do
  not span files. The BibleChunker writes these at index time through the write
  actor; this is the read side.
- activateSourceChunkers(config): at **config-resolve** (every startup **and**
  every `ReloadConfig`), for each source's *per-source* strategy entries, look
  up the chunker and, if it implements the optional `SourceChunker` interface,
  call `ActivateForSource(source, register)` (R3217). A chunker mapped only by
  the global strategy map is **not** called, and a chunker mapped by several of
  one source's patterns is called once. The `register` handle is
  `Config.AddDerivedStrategy`, which adds source-prefixed entries to the
  in-memory `Config.derivedStrategies` map rather than `Config.Strategies` —
  the latter is TOML-encoded by `SaveConfig` and compared by `DiffConfig`, so a
  derived entry there would be written into the user's ark.toml and would
  report a phantom strategies change (and an `index_stale` error) on every
  boot, both contradicting R3218's "re-established every startup rather than
  persisted." The map is cleared at the head of the pass, so re-running rebuilds
  rather than accumulates. A returned error fails that source's load — the
  vehicle for the BibleChunker's `<source>/BIBLE` collision guard (R3219) —
  and is logged rather than failing `Open`, so one misconfigured source cannot
  make the whole DB unopenable.
  - **Tail — reconcile (R3221):** the pass accumulates which sources *declare*
    each strategy, then hands the bible chunker its list so it can drop
    book-index records left by a source that no longer declares the strategy
    (`ReconcileBookIndex`, crc-BibleChunker.md). It runs even when the list is
    empty, which is the source-removed case a per-source hook could never
    reach. The criterion is *declares*, not *activated successfully*: a source
    that tripped the R3219 guard is still a bible source, and a
    misconfiguration must not delete its persisted data.
  - **Tail — announce (R3219):** `recordActivationFailures` keeps the
    `source_activation` E record in step with the config just resolved —
    written (payload: source dir → message) when any hook failed, deleted when
    none did. A hook failure is otherwise silent: the source keeps indexing and
    only its registered addressing stops resolving, which reads as a reference
    that returns nothing. Re-deriving the record per config load is what keeps
    it honest — fixing the source and reloading clears it, and a stale
    condition cannot outlive the problem it described. The source is **not**
    dropped from `cfg.Sources`: that would make `DiffConfig` report a sources
    change on every boot and could trip the catastrophe check, so a
    misconfigured source would corrupt unrelated config state.
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
- AcceptExtTag(targetSpec, tag, value string) (applied string, err error):
  commit every matching
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
  file/line is a silent no-op. `applied` reports the disposition actually
  landed — `"internal"` when any internal write succeeded, `"external"` when
  only external writes landed, `""` when nothing matched — so callers (the
  `mcp.extAccept` binding) can be honest about the capability degrade.
  (R3054, R3071, R3100, R3101, R3102, R3103, R3106, R3107, R3248)
- RemoveInternalTag(targetSpec, tag string) error: the internal-remove cell —
  delete the inline `@tag:` line from the target file's own body via the
  chunker's `RemoveTag` stencil (scope resolution mirrors `writeInternalTag`:
  bare path → file-level `@`-run, single-chunk address → chunk scope), with
  the same temp+rename on the DB actor. **Never degrades**: unresolvable or
  multi-chunk target, incapable chunker type, read-only zone, unwritable
  file, or no matching inline line in scope is an error, not a fallback — a
  fallback would delete a different fact. Writes no judgment.
  (R3244, R3245)
- RejectExtTag(targetSpec, tag, value string) error: rewrite matching
  `@ext-candidate` line(s) to a single tag-name-only
  `@ext-judgment: <date> TARGET @tag: @count: -N`, a durable judgment
  stamped with a first-seen date (frozen on later decrements). It creates
  the judgment (`@count: -1`) or decrements the existing signed `@count`;
  `@count` reaching 0 removes the line (absent ≡ neutral). The signed count
  carries both directions — negative rejection magnitude here, positive
  reinforcement for a future producer. One closure-actor read-modify-write.
  Missing file → no-op. (R3055, R3069, R3075, R3090)
- AmendExtCandidate(sourceTvid uint64, tag, value string, opts ExtAmendOpts) error:
  revise one `@ext-candidate` line **in place**, as a single closure-actor
  operation. `TvidMap.Resolve(sourceTvid)` recovers the old line's value
  string; the peels (`peelDate`, `peelExtCandidateDisposition`,
  `peelExtCandidateReplace`, the insight peel) plus `ParseExtTarget`
  recover its identity; `dropCountLine` drops exactly that line — matching
  the **whole** recovered identity rather than `applyExtMirrorEdit`'s
  `(target, tag, value)` triple, which several distinct candidates can
  share, and carrying the disposition **as written** so a line authored
  without a disposition token still matches; then the revision is authored
  through the same `upsertCountLine(candidateLine(...))` path
  `CandidateExtTag` uses. A revision that also retargets can land in a
  different mirror file than the one it was dropped from, in which case the
  drop and the author are two writes rather than one. Reusing
  the candidate author is what makes the two settled tally rules fall out
  rather than needing their own code: the revision is a **new identity at
  `@count: 1`** (the tally measured agreement on a proposal the human just
  changed), and a revision colliding with an existing line **merges into
  that line's tally** instead of duplicating it. `tag`/`value` are always
  the revised proposal; `opts`' nil fields carry the old line's values
  forward. Unlike accept/reject, a tvid that resolves to nothing — or to a
  tag that is not `@ext-candidate` — is an **error**, not a silent no-op:
  the caller named one specific line, so silence would report a revision
  that never happened. (R3255)
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

## Types

### ExtAmendOpts (R3255)
The carry-forward half of `AmendExtCandidate`'s signature. Each field is a
pointer so **nil means "keep the old line's value"**, which a plain string
or bool could not express — an empty `Insight` and an unset `Replace` are
both meaningful revisions. The `tag`/`value` positionals stay non-pointer
because the subject is always stated: were they inherited too,
`amend <tvid> <tag>` could not distinguish "make this name-only" from
"leave the value alone."
- Target: *string — retarget the proposal
- Insight: *string — edit the rationale (editable by the reviewer, but
  silence preserves the author's, rather than discarding it)
- Disposition: *string — `internal` / `external`
- Replace: *bool — accept collapses existing values vs. adds

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
