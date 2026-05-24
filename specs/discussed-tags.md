# Discussed Tags

A per-session record of tags the conversation has already covered,
used by the recall substrate to suppress tags the user doesn't need
to be reminded of. The agent-mediated recall pipeline writes to this
store after surfacing a batch of suggestions; the substrate reads it
when called with `--session SID` and filters its output accordingly.

Owns the persistence shape and the access surfaces. The substrate
itself ([recall.md](recall.md)) consumes the filter as one of its
result-time set operations; the agent layer
([.scratch/CONTEXTUAL-RECALL.md](../.scratch/CONTEXTUAL-RECALL.md))
calls the writer. This spec sits between them.

Language: Go (Store + CLI subcommand family). Environment: ark
server with the LMDB index open. The CLI works in-process via
`withDB` when the server is not running.

## Why this exists

The recall pipeline is a chain of three different responsibilities:
the substrate finds chunks, the LLM judges relevance, the messaging
substrate delivers. Conversation memory — *"we already covered this"*
— could live in any of those layers but fits worst in the LLM step,
which would burn tokens re-deriving information that is already a
fact in the store. Pushing dedup into the substrate keeps the
relevance judgment pure: the LLM only sees tag candidates that are
still in play.

This is the third instance of **set-based result filtering at the
substrate boundary** we've seen — alongside search's file/chunk-ID
sets and the recall substrate's `requireTags` check (empty-tag-set
drop). Same shape, different operators (subtraction with
empty-after check). Not yet enough instances to factor into a
shared abstraction; the pattern is worth naming for future work.

## Storage: the `RD` record class

Per-session dedup state lives in the `RD` record class (Recall
Discussed-tag). `R` is reserved as the **recall feature
namespace** for future records (emission log, per-session config,
trigger state, etc.); `RD` collides with nothing under that prefix.

```
Key:   "RD" + session-bytes + \x00 + tagname + \x00 + value
Value: 8 bytes — unix nanoseconds (creation timestamp)
```

- **session-bytes** is variable-length, expected to be a Claude
  Code session UUID (hex, no `\x00`).
- **tagname** matches ark's tag-name grammar (`[\w][\w\-.]*`,
  no `\x00`).
- **value** is the literal tag value bytes. A bare `@name` tag
  with no value writes an entry whose value-segment is empty
  (key ends `... + \x00 + tagname + \x00`).
- **Timestamp value** is big-endian `uint64`, written at insert.
  TTL is computed lazily at read time (see TTL semantics below).

Canonical row in [record-formats.md](record-formats.md); this
spec is the per-feature owner.

## `ark discussed` command family

```
ark discussed add    --session SID @tag[:value] [@tag[:value] ...]
ark discussed list   --session SID [--since DUR] [--json]
ark discussed clear  --session SID
ark discussed prune  [--ttl DUR]
```

- **`add`** writes one `RD` record per tag argument, stamped with
  `NOW`. Re-adding an existing (session, tag, value) overwrites
  the timestamp — i.e. "discussed *now*" wins over an earlier
  mention. Exit zero on success; non-zero if `--session` is
  missing or no tags were given.
- **`list`** range-scans `RD + session-bytes + \x00`, drops
  expired entries (lazy expiry — see TTL), and prints one tag per
  line as `@name` or `@name: value`. `--since DUR` keeps only
  entries newer than `NOW - DUR`. `--json` emits
  `[{"tag":"...","value":"...","timestamp":"RFC3339"}, ...]`.
- **`clear`** deletes every `RD` record under one session. Useful
  for "start fresh," e.g. when a Claude Code session compacts and
  its dedup horizon should reset.
- **`prune`** is an explicit sweep across all sessions, dropping
  expired entries. The default TTL (or `--ttl DUR` override)
  determines the cutoff. Lazy expiry handles most cases; `prune`
  exists for the user who wants to reclaim space now.

Tag input grammar matches ark's tag syntax: `@name` is bare,
`@name:value` is exact pair. Names cannot contain `\x00`; values
cannot contain `\x00`. Quoting follows shell conventions —
quote values with spaces.

### Examples

```
$ ark discussed add --session abc123 @topic:messaging @ext:tagdefs
$ ark discussed list --session abc123
@topic: messaging
@ext: tagdefs
$ ark discussed list --session abc123 --json
[{"tag":"topic","value":"messaging","timestamp":"2026-05-23T08:42:11Z"},
 {"tag":"ext","value":"tagdefs","timestamp":"2026-05-23T08:42:11Z"}]
$ ark discussed clear --session abc123
$ ark discussed prune --ttl 168h
```

## Substrate integration

`ark connections recall` gains two new flags:

| Flag                     | Default | Meaning                                                                 |
|--------------------------|---------|-------------------------------------------------------------------------|
| `--session SID`          | (none)  | Read the session's `RD` records and add them to the exclusion set       |
| `--discussed @t1,@t2:v`  | (none)  | Explicit exclusion list, comma-separated tag expressions                |

Both populate the **same exclusion set**; when both flags are
present, the substrate takes the union.

**Filter semantics — permissive.** For each candidate chunk, strip
any tag whose `(name, value)` matches the exclusion set from the
chunk's tag list. Drop the chunk only if its tag list becomes empty
after stripping. A chunk that loses one discussed tag but keeps
another non-discussed tag still surfaces — we lose the redundant
signal, not the chunk itself.

**Matching granularity:**

- A bare `@name` entry in the exclusion set matches *any* value
  under that name. Discussing `@topic` once covers every
  `@topic:*` pair.
- An `@name:value` entry matches the exact pair. Discussing
  `@topic:messaging` does not suppress `@topic:auth`.

The filter applies *before* the substrate's `requireTags` /
`-all` step. A chunk emptied by `--discussed` is dropped for the
same reason a chunk with no tags is dropped by default: nothing
left for downstream tag-shaped consumers to use. `-all` does
**not** override the discussed filter — `-all` means "keep
chunks even if they had no tags to begin with," not "ignore the
caller's dedup request."

## TTL semantics

Discussed entries are not permanent. Conversations move on,
topics return, and a tag the recall agent suppressed three days
ago is fair game again today.

- **Default TTL:** 24 hours. Long enough that a continuous
  Claude Code session doesn't see the same tag re-surfaced on
  every turn; short enough that yesterday's exploration doesn't
  shadow today's.
- **Configurable via** `[recall].discussed_ttl` in `ark.toml`,
  any Go duration string (`"24h"`, `"7d"`, `"30m"`, `"0"`).
  `"0"` means *never expire* — useful for "mark and forget"
  workflows where the user manages dedup state by hand.
- **Lazy expiry on read.** `list` and the substrate's exclusion-
  set load both skip entries whose timestamp + TTL is in the
  past. The records are not deleted; `prune` is the explicit
  cleanup verb.

### `ark.toml` shape

```toml
[recall]
discussed_ttl = "24h"     # 0 = never expire; omitted = 24h default
```

`[recall]` is a new top-level table; the agent-layer design adds
more fields under it later (`agent_cmd`, `poll_session`, etc. —
see [.scratch/CONTEXTUAL-RECALL.md](../.scratch/CONTEXTUAL-RECALL.md)).

## Lifecycle: recall agent writes; substrate reads

```
                     ┌─────────────────────┐
                     │ Recall agent        │
                     │ (LLM relevance)     │
                     └─────────┬───────────┘
                               │ emits N tag suggestions
                               │ for target session SID
                               ▼
                ┌──────────────────────────────┐
                │ ark discussed add            │
                │   --session SID @t1 @t2 ...  │
                └──────────────┬───────────────┘
                               │ writes RD records
                               ▼
                     ┌─────────────────────┐
                     │ LMDB store          │
                     └─────────┬───────────┘
                               │ range-scanned by
                               ▼
                ┌──────────────────────────────┐
                │ ark connections recall       │
                │   --session SID <inputs>     │
                └──────────────────────────────┘
```

**Mark-all-N writer.** The recall agent marks every tag it
*emitted*, not every tag the user ultimately *engaged with*. This
over-suppresses (a tag emitted but ignored still gets marked), but
the alternative — waiting for an ack channel back from the
target session — adds round-trip cost to a closed loop that is
already designed for forward-only DM flow. We revisit if
over-suppression bites in practice.

The substrate has no opinion about *who* wrote the RD records;
any caller can use `ark discussed add` directly. The agent layer
is the expected writer because it's where the surfacing happens.

## Public Lua/Go API

```go
// Discussed represents one entry in the exclusion set.
type Discussed struct {
    Tag   string // name without leading @
    Value string // empty = match any value
}

// RecallOpts gains a Discussed field; substrate behavior unchanged
// when the slice is empty.
type RecallOpts struct {
    // ... existing fields ...
    Discussed []Discussed // exclusion set; empty disables the filter
}
```

The Lua bridge exposes the same shape via `sys.recall`:

```lua
local result = sys.recall(inputs, {
    k = 20,
    discussed = {
        {tag = "topic", value = "messaging"},
        {tag = "ext"},                         -- bare, matches any value
    },
})
```

`sys.discussed` exposes the four `ark discussed` verbs as Lua
methods on the existing `sys` global (R-numbered when the design
lands).

## Empty / Error Cases

- `--session SID` with no `RD` records for that session → exclusion
  set is whatever `--discussed` contributes, possibly empty.
- `--discussed` with no tags after parse (`--discussed ""`) →
  treated as empty; no filter applied from the flag.
- Both flags absent → substrate behavior unchanged (this spec is
  purely additive).
- `ark discussed add` with no tag args after `--session` → exit
  non-zero with `error: no tags specified`.
- `ark discussed add --session ""` → exit non-zero with
  `error: session ID required`.
- TTL parse failure in `ark.toml` → fall back to 24h default and
  log a warning at server start; an invalid `--ttl` on
  `prune` exits non-zero.
- `RD` record with malformed value (not 8 bytes) → treated as
  expired; lazy-skipped on read. Should not happen — the writer
  never produces malformed values.

## Performance

- `add` is one LMDB put per tag, batched in a single write txn.
- `list` is a range scan over `RD + session-bytes + \x00`,
  bounded by the session's discussed-tag count. The 24h TTL keeps
  this small in practice (a long session emits dozens, not
  thousands).
- The substrate's exclusion-set load on `--session` runs once per
  recall call, before the chunk-tag filter. The set is small
  enough that per-chunk membership tests stay constant-time
  (Go map lookup on `(name, value)`).
- `prune` is a full-table scan over `RD` — O(N) in total
  discussed-tags across all sessions; rare operation.

## Test Strategy

- `add` writes the expected `RD` keys; re-adding bumps the
  timestamp.
- `list --since DUR` filters by timestamp.
- `list --json` is parseable as `[]{tag, value, timestamp}`.
- `clear` removes every entry under one session, leaves others
  intact.
- `prune --ttl 0` (or `prune` with default TTL) removes expired
  entries across sessions.
- `recall --session SID` excludes chunks whose tags are all in
  the session's discussed set; chunks with one surviving tag pass.
- `recall --discussed @topic:messaging` excludes the exact pair;
  `@topic:other` still surfaces.
- `recall --discussed @topic` excludes every `@topic:*` pair.
- Union behavior: `--session SID` + `--discussed @t` produces
  the union exclusion set.
- TTL: entries older than the configured TTL are skipped on read
  even though `prune` hasn't run.
- `RecallOpts.Discussed` non-nil with empty slice → substrate
  treats as "no filter."

## What This Spec Does Not Cover

- **The recall agent itself.** Process lifecycle, target-session
  discovery, compaction triggers, failure modes, multi-tenancy
  are still in design; see
  [.scratch/CONTEXTUAL-RECALL.md](../.scratch/CONTEXTUAL-RECALL.md)
  "Still blurry."
- **Accept/reject of substrate-derived tags.** Substrate-derived
  tags (D-record similarity to chunk embeddings, surfacing tags a
  chunk could carry but doesn't) have their own record classes
  (RC, RJ, RF) and lifecycle, owned by
  [derived-tags.md](derived-tags.md). "Discussed" stays clean for
  the conversation-covered semantic; derivation has its own
  surface.
- **Cross-session dedup.** Each session has its own `RD` range.
  A topic discussed in session A doesn't suppress recall in
  session B. The recall agent could synthesize a cross-session
  view by passing multiple session IDs in a future
  `--session SID,SID,...` form; not in scope here.
