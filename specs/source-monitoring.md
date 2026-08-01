# Source Monitoring

The server should keep its index current without manual intervention.
When config changes or files change on disk, the server detects it
and updates the index. This happens in three phases, each independent
and useful on its own.

## ~/.ark Is Always a Source

The database directory (~/.ark) is a hardcoded source. It contains
tags.md, notes, config, and other ark-managed content. It cannot be
removed via `ark config remove-source` — that's an error. The server
ensures ~/.ark is a source on every startup, before reading ark.toml
sources. It does not appear in ark.toml.

### What inside ~/.ark is indexed — the allowlist (R961, R962)

The source carries an explicit include **allowlist**, not a bare
recursion. Two rules build it: ark-managed standard files at the top
level are named individually, and under each content directory only the
**file extensions ark ships a chunker for** are listed. This is the
canonical inventory; `arkSourceIncludePatterns` in `config.go` and the
commented `[[source]]` block in `install/ark.toml` both express it, and
the three must stay in sync.

| Entry | Covers | See |
|---|---|---|
| `ark.toml` | user-edited config | [config.md](config.md) |
| `chimes.md` | auto-created chime declarations | [chimes.md](chimes.md) |
| `tags.md` | starter tag bible, from the install seed | [tags.md](tags.md) |
| `schedule/**/*.md` | schedule log files | [schedule-lifecycle.md](schedule-lifecycle.md) |
| `apps/**/*.{lua,js,html,css,md}` | Frictionless app sources | [embedded-ui.md](embedded-ui.md) |
| `storage/**/*.{md,pdf}` | app-managed user data ark can chunk | [embedded-ui.md](embedded-ui.md) |
| `external/**/*.md` | `@ext` mirror chunks | [at-ext-storage.md](at-ext-storage.md) |
| `skills/**/*.md` | ark-managed agent skill files | [infrastructure.md](infrastructure.md) |
| `personal-patterns/**/*.md` | default personal pattern collection | [personal-patterns.md](personal-patterns.md) |

**Why per-extension and not `dir/**`.** A bare recursion sweeps in
Fossil checkout files, `*.docx`, undo-tree dumps, lock files, build
outputs, and index pages — each of which used to surface as an `fts add
…: chunk "1-1" contains invalid UTF-8` line on every startup.
Extension-scoped includes drop them before they reach the chunker. So
adding a new chunkable extension means appending the matching
`dir/**/*.ext` pattern, **not** flipping to `**` and excluding binaries
afterward.

**Two entries carry consequences worth knowing.** `skills/**/*.md`
indexes symlinks into the repo's skill sources, so the content is
indexed twice — accepted, because `ark fetch` serves only indexed
content and does not resolve symlinks, so a sealed subagent bootstrapping
via `ark fetch ~/.ark/skills/<skill>.md` needs the symlink path itself
indexed. `personal-patterns/**/*.md` is what makes that feature's
retrieval half work at all: `ark patterns list` reads the directory
rather than the index, so without this entry priming would keep working
perfectly while search and recall found nothing, and nothing would
report the discrepancy (R3278).

**The allowlist is the whole gate.** Anything unmatched — `index.db`,
logs, `schedule-archive/` — is simply not indexed, and forgetting to add
an entry is silent, since nothing reports a file that was never a
candidate. That silence is why this inventory is kept here, beside the
source it scopes, rather than in whichever feature spec happened to add
an entry first.

### Override path — commented in `install/ark.toml`

The synthetic source is the default, but a user may need to override it
(a new extension under `apps/`, a new top-level ark-managed file).
`install/ark.toml` carries the same include list as a commented
`[[source]] dir = "~/.ark"` block, UNIX-config style — uncomment and
edit to replace the synthetic defaults. `EnsureArkSource` skips its
synthetic addition when any `[[source]]` already targets the database
directory, so a user-defined block wins.

## Source Directory Globs

A `[[source]].dir` may be a glob (`~/work/*`), expanded against the
filesystem to yield one source per matching directory. Expanded sources
carry `from_glob` so `ark sources check` can reconcile additions,
disappearances, and orphans.

**`**` is rejected in a source dir**, with an error naming `*` as the
single-level alternative. A recursive source glob makes every
subdirectory its own source root, so one file change fires a watcher
event per ancestor level. The failure is latent — `~/work/**` behaves
correctly until someone creates a nested directory under `~/work` — so it
cannot be caught by validating the expansion at load time, and the
pattern itself is refused instead. Multiple *non-nested* directories stay
fine.

This is filesystem expansion, not path filtering, so the anchoring forms
in [main.md](main.md#glob-patterns) do not apply here; `~` expansion is
the only rewriting done.

## Phase A: Config-Triggered Reconcile

When any config mutation command runs (add-source, remove-source,
add-include, add-exclude, remove-pattern), the server triggers a
reconciliation cycle afterward: sources-check, sweep, scan, refresh.
The same cycle that runs at startup.

This is extracted into a `Reconcile` method that startup also calls.
Same logic, idempotent, safe to call repeatedly. Runs in a background
goroutine so the HTTP handler returns immediately.

If a reconcile is already running when another is requested, the new
request waits for the current one to finish and then runs. Not
dropped — config may have changed again during the previous run.

**Beyond that, requests merge: at most one reconcile is active and one
pending.** A third, fourth and fiftieth request arriving while one is
already waiting all fold into that single pending scan rather than
queueing scans of their own. Nothing is lost by merging, because the
pending scan has not started yet and will therefore observe everything
every merged request was asking about. A reconcile is a full pass over
every source, so N requests during one run would otherwise cost N
further full passes to reach a state one pass already reaches.

The correctness of this rests on **when** the pending mark clears, and
it is easy to get backwards. It clears as the pending scan *begins*,
not when it ends. A request arriving during a run therefore queues a
fresh scan rather than merging into the one already underway — which
it must, because a scan cannot be relied on to see a change made after
it passed that part of the tree. Merging only ever happens into a scan
that has not started. This is the same rule, for the same reason, that
the per-path refresh coalescing follows (db-concurrency.md §Refresh
Coalescing).

### Sweep step: drop newly-excluded files

Scan adds new files and Refresh re-indexes stale ones, but neither
revisits the *classification* of files already in the index. When the
user adds an exclude (or removes an include) that newly excludes
already-indexed files, those files persist in the index until manual
removal or rebuild.

The sweep step closes that gap. Before scan, it walks the indexed
file set and re-classifies each path against the current effective
include/exclude patterns of its source. Any file that no longer
classifies as `Included` is removed. Removal goes through the same
path as `ark remove`, so all derived state — chunks, tag values,
ext routings — is dropped consistently.

A file whose source has been removed entirely is also swept (no
source claims it, so it cannot be classified Included).

Sweep is part of every Reconcile cycle, not only the post-mutation
ones. A hand-edit to ark.toml, a startup with an updated config, or
any other entry into Reconcile produces the same result.

## Phase B: Filesystem Watching

The server watches source directories and ark.toml for changes using
fsnotify.

### ark.toml watching

Any write to ark.toml triggers config reload and Reconcile(). This
catches hand-edits and edits by external processes.

### Source directory watching

Each resolved source directory gets an fsnotify watch. Watches are
recursive — subdirectories are watched too. When Reconcile() adds
new sources, new watches start. When sources are removed, watches
stop.

**Watch coverage equals scan coverage.** The recursive watch descends
into exactly the directory set the Scanner walks. A subdirectory is
watched iff the directory-classification rule does not mark it
Excluded — the same `Classify` call (with `isDir=true`, respecting
`dotfiles` and the source's include/exclude) the Scanner uses to
decide descent during Scan(). Both go through one rule
(`DB.IsWatchableDir`), so neither descends a directory the other
skips. Concretely, with `dotfiles=true` a dot-directory such as
`.scratch/` is watched (it is not excluded, so the Scanner indexes
files inside it), while a directory excluded *as a directory* such as
`.git/` is skipped by watcher and Scanner alike. Every directory that
can contain an indexable file is watched; an edit to a file in such a
directory triggers live re-indexing without a manual scan/refresh.

File events use throttled on-notify: the first notification triggers
an immediate index update, then imposes a throttle window. Events
during the window are ignored — the filesystem is the source of
truth, only the final state matters. When the window expires, one
re-index of current state runs. If more events arrived during that
re-index, another window starts. When a window expires with no
events, the next notification is immediate again.

A maximum wait ceiling prevents event storms from starving the
index indefinitely. After the ceiling, force a re-index regardless
of incoming events.

### Watcher pattern filtering

Not every file in a watched directory is indexable. Index database
files, socket files, log files, PID files — all live in watched
directories (especially ~/.ark) but should never trigger reconcile.
Without filtering, a reconcile writes to the index, the index modifies
index.db, fsnotify fires, and the watcher triggers another reconcile.
The database changes itself in a loop.

Before triggering reconcile on a file event, the watcher checks
whether the changed file would actually be indexed. It finds which
source directory the file belongs to, gets the effective
include/exclude patterns, and runs the same Classify check that the
Scanner uses during Scan(). If the file wouldn't be included, the
event is ignored.

Directory creation events skip the *file*-indexability filter, but a
newly created directory is still watched only when it is watchable as
a directory (the `Classify` isDir=true rule above is not Excluded). A
new ordinary directory is Unresolved — no include pattern matches it
yet — and is watched so later indexable files are caught; a new
directory that is excluded as a directory (e.g. `node_modules/`) is
not watched, matching the Scanner.

ark.toml changes have their own code path and also bypass this filter.

#### Ignored-file cache

Files that fail the indexability check are added to a set of known
non-indexable paths. Subsequent events for the same path skip the
Classify call entirely — a set lookup instead of glob matching.

Yes, this is negative caching — normally an antipattern. It works
here because the invalidation is clean and complete: the cache is
cleared whenever ark.toml is reloaded, which is the only event that
can change pattern rules. Between reloads, a file's indexability
cannot change.

The cache only holds paths that were checked and rejected. It does
not cache positive results — indexable files go through normal
throttle/reconcile and don't benefit from caching.

### Startup + fsnotify (not either/or)

fsnotify only sees changes while watching. Anything that changed
between shutdown and startup is invisible. The startup reconciliation
scan catches the gap. Watch first, then reconcile — so nothing falls
between the cracks.

## Phase C: Append Detection

When a file's modtime changes, check whether the change was
append-only before doing a full reindex.

1. Read stored file length and content hash from the index.
2. Hash the file's content up to the stored length.
3. If hash matches — content before the watermark is unchanged.
   This is an append-only change.
4. If hash differs — content was modified. Full reindex.

For append-only changes, ark stores the start offset of the last
chunk. Seek to that offset, compare bytes against stored chunk
content. If the last chunk matches, it ended on a clean boundary —
append new chunks from the end. If it doesn't match (boundary
wasn't clean), re-chunk from that offset only. Either way, much
cheaper than full reindex.

Append-only chunks lighten tag indexing too — only extract tags
from new chunks, add to existing counts.

### Saying which branch ran

Every refresh names its outcome on one line, at the coarsest
verbosity, in a shared vocabulary:

```
index: <path> APPENDED 179 bytes → 1 new chunk(s)
index: <path> REINDEXED in full (fileID=9089)
index: <path> UNCHANGED (skipped — same length and hash)
index: <path> APPEND FAILED (<err>) — falling back to REINDEX
```

Which branch ran decides whether a change was re-derived at all, so
it is the first thing anyone debugging the index needs and the last
thing they should have to infer. Before this it could only be pieced
together from `isAppend=` and `unchanged` buried mid-line across four
separate `-vv` lines, and the two optimizations above are quiet enough
to defeat a hand probe: touching a file takes the append path and
re-derives nothing, while restoring its original bytes is skipped
outright as unchanged.

The append line carries the chunk count deliberately. An append that
produces **zero** chunks means the file grew and nothing downstream
re-ran, which looks identical to success in any log that reports only
the branch.

The new watermark ark stores after an append — file length plus
content hash, the inputs the next detection compares against — comes
from a single in-memory snapshot of the file. The stored length is the
byte count actually hashed and chunked, never a size from a separate,
later stat. A write landing between the read and such a stat would
record a length past the hashed content: the next detection then
hashes a different span, wrongly reports a modification, and forces a
full reindex, while the bytes between the two reads go unchunked.
Content appended after the snapshot is caught by the next change event.

This is universal — not strategy-specific. Every file gets it. Small
files: hash is trivial, full reindex is cheap anyway. Large files:
skip re-chunking everything before the watermark.

Clean-boundary handling is delegated to the chunker, not reported
per-strategy to ark: a strategy that implements microfts2's
`AppendAwareChunker` (`chat-jsonl`, `markdown`) merges boundary-spanning
appends itself. Line-based and JSONL strategies always end on clean
boundaries; markdown heading-based strategies might not, which is why the
unclean-boundary back-seek above remains deferred for any chunker that is
not append-aware.

### chat-jsonl (done)

The `chat-jsonl` strategy extracts chat-specific data (speaker,
tool use). Renamed from `jsonl`. A generic JSONL strategy should
also exist for non-chat JSONL files.

## Empty Files

A file with size zero yields no chunks from any chunker. Attempting to
index it wastes time, and — because microfts2 returns `ErrNoChunks`
without recording the file — every subsequent scan re-attempts the
same empty file. For a zero-byte PDF in particular, the chunker prints
a parse-error line each time, flooding the log.

The scanner maintains an in-memory **empty-file set** keyed by path
with the file's mtime as the value. During Scan():

1. If the file's size is zero and it is already in the set with the
   current mtime, skip — do not flag as new, do not call the
   indexer.
2. If the file's size is zero and it is not in the set (or its mtime
   has changed), record it in the set and report it separately from
   new files. The caller removes the path from the index via
   `fts.RemoveFile(path)` so microfts2 can update its own
   refcounting — chunks may be shared with other paths, so ark never
   deletes chunks directly.
3. Any non-zero-size file goes through the normal CheckFile flow
   unchanged.

The set lives only for the process lifetime. On restart, each empty
file gets re-checked once — then the set absorbs it again. This is
acceptable: a single size-zero `os.Stat` per broken file per restart
is cheap, and we avoid persisting state that can drift from disk.

Access to the set is serialized through the DB actor: Scanner.Scan()
runs on the actor goroutine, so writes to the set are single-threaded.
Evictions that touch the index are routed through the write queue
(`enqueueWrite`) in async scan paths, so they serialize behind any
in-flight write transaction instead of contending with it on the
actor. Synchronous scans (e.g. `ark add` of a directory) run the
eviction in the actor since the rest of their indexing also runs
there. Either way, no mutex is needed — the actor model does the
serialization.

## Search Consistency

Searches may return results from stale files. The search path handles
this:

1. Search and check results for staleness.
2. If stale hits exist, re-index those files and re-search.
3. If still stale after 2 retries, prune stale results and return
   what's valid. Don't loop forever chasing a moving target.
4. Never block search on achieving a perfectly consistent index.
   Background reconciliation catches up eventually.
