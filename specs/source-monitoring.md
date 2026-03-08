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

## Phase A: Config-Triggered Reconcile

When any config mutation command runs (add-source, remove-source,
add-include, add-exclude, remove-pattern), the server triggers a
reconciliation cycle afterward: sources-check, scan, refresh. The
same cycle that runs at startup.

This is extracted into a `Reconcile` method that startup also calls.
Same logic, idempotent, safe to call repeatedly. Runs in a background
goroutine so the HTTP handler returns immediately.

If a reconcile is already running when another is requested, the new
request waits for the current one to finish and then runs. Not
dropped — config may have changed again during the previous run.

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

This is universal — not strategy-specific. Every file gets it. Small
files: hash is trivial, full reindex is cheap anyway. Large files:
skip re-chunking everything before the watermark.

Strategies can report whether they produce clean chunk boundaries.
Line-based and JSONL strategies always end on boundaries. Markdown
heading-based strategies might not.

### chat-jsonl (done)

The `chat-jsonl` strategy extracts chat-specific data (speaker,
tool use). Renamed from `jsonl`. A generic JSONL strategy should
also exist for non-chat JSONL files.

## Search Consistency

Searches may return results from stale files. The search path handles
this:

1. Search and check results for staleness.
2. If stale hits exist, re-index those files and re-search.
3. If still stale after 2 retries, prune stale results and return
   what's valid. Don't loop forever chasing a moving target.
4. Never block search on achieving a perfectly consistent index.
   Background reconciliation catches up eventually.
