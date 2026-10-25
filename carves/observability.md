# Observability

When `ark serve` starts it may spend a long time embedding, reconciling or
indexing, and today the only way to see that is to tail the server log. The CLI
and the UI both go blind exactly when the server is busiest, and much of ark's
state (hunt queues, seat holders, how a PDF was chunked) has no view at all.
Observability is how ark gets debugged, so this carve is load-bearing for
every other one.

The move: ark's state becomes readable at `tmp://` paths, and every surface
that wants to show it reads or subscribes to those paths rather than asking
the server ad hoc. Some of the paths are **generated on read**, the way Plan 9's
`/proc` or a SQL virtual table peers into a running program.

A skeleton (Bill, 2026-10-07), restructured around generated paths (Bill,
2026-10-21). None of the parts is designed yet.

## Status

- [ ] **Item 1 — generated tmp:// paths.** **OPEN (not queued.)** The mechanism
      the rest is built on. See Item 1.
- [ ] **Item 2 — the state document.** **OPEN (not queued.)** A generator that
      people subscribe to, plus the identity header and its publishers.
- [ ] **Item 3 — the CLI view.** **OPEN (not queued.)**
- [ ] **Item 4 — the UI view.** **OPEN (not queued.)**
- [ ] **Item 5 — the hunt path, as the testbed.** **OPEN (not queued.)** Filed
      by Bill, 2026-10-21. The first inspector: make bloodhound delegation
      visible (seats, hunts, queues, listeners) and fix what that shows. See
      Item 5.
- [ ] **Item 6 — the PDF inspector.** **OPEN (not queued.)** Filed by Bill,
      2026-10-21. The second inspector: one path per indexed PDF, showing its
      anatomy. See Item 6.
- [ ] **Item 7 — embedding and routing counts per file.** **OPEN (not queued.)**
      Filed by Bill, 2026-10-25. `ark files --status --detail` also shows each
      file's `EC` and `X` record counts. `/mini-spec`. See Item 7.

<a id="problem"></a>
## The problem

Found starting ark on 2026-10-07: the server was embedding, and nothing outside
the log file said so. The same holds for every long-running activity the server
owns (reconciliation after a restart, a re-index, the embedding queue, watcher
backlogs). `ark status` reports counts as of its last pass, not what is
happening now.

A second blind spot, found 2026-10-12: nothing says *which build* is serving.
That morning's server ran a binary built an hour before ui-engine's per-app
static serving was committed, so `/mcp/css/` returned 404 after `html/mcp` was
removed. Only `go tool nm` on the binary showed why. Every dev build since the
release reports `0.5.0`, so the version string alone would not have told them
apart; a build stamp would have.

A third and a fourth, both reported by a HollowRpg session on 2026-10-20: the
**hunt path** (watermarks queued for a secretary that never came, with nothing
showing they were waiting; see Item 5), and **how a file was indexed** (PDF
chunks with stray bytes, split boundaries and duplicates that took a
hand-run investigation to find; see Item 6).

<a id="model"></a>
## The model

- **Two kinds of path.** A **stored** tmp:// doc is written by its owner,
  indexed like any content, and pushed to subscribers through the existing
  tmp:// subscription path ([tmp-documents.md](../specs/tmp-documents.md),
  [tmp-subscription.md](../specs/tmp-subscription.md)). A **generated** path is
  computed when it is read and never stored, so it costs nothing until someone
  looks. Most of what we want to see is inspector-shaped (a PDF's anatomy, a
  hunt's history, the seat holders) and only needs reading. Only "what is
  happening right now" needs a push.
- **The two may meet.** If a generator can mark itself dirty when its state
  changes, the server can regenerate and push it only while it has
  subscribers. The state document would then be just a generator people happen
  to listen to, and nobody would pay its update cost when nobody is watching.
  Whether that works is Item 1's to settle.
- **Readers do not poll the server for state.** The CLI and the UI read or
  subscribe to these paths ("Ark monitor" in tmp-subscription.md is this use
  case).
- **Tagged text.** Generated output is markdown with tagged lines, so it reads
  well at the CLI, renders in the UI, and stays subscribable by tag. It is not
  indexed by default: you search for a file, then inspect it.
- **Precedents in ark.** The BIBLE chunker's addresses
  ([bible-chunker.md](../specs/bible-chunker.md)) already resolve virtual paths
  with no file behind them. `ark tag inspect` is already a read-only inspector,
  over `@ext` state, and is a candidate to become a generated path.

## Parts

**Item 1.** Generated tmp:// paths, the mechanism. Little of it is designed;
what is decided comes first, then the questions the design has to answer.

Decided so far:

- **Read side and write side, never both on one path.** Generated paths are
  markdown with links, so together they work like a read-only SNMP tree you can
  walk. The write side is `tmp://ctl/…`: ark already supports it (Go code
  subscribes on a ctl path and fires when something writes it), and a write
  carries ark tags as its key-value pairs. **A ctl path is write-only**: reading
  one is an access-permission error, not "not found", so a mistaken read says
  what happened. Since a ctl path cannot be read back, the effect of a write
  shows in the generated path it changes.
- **A failed ctl write answers in baby food**, the pattern
  [tags-baby-food.md](../specs/tags-baby-food.md) applies to tag output:
  agent-readable markdown that says what failed (unknown key, bad value, no
  such target) and what would have been accepted, so the writer can correct
  itself without reading source.
- **Actions stay CLI verbs first.** Verbs are easier to document and to confirm
  before acting; a ctl path is there when a verb is not enough. A verb should
  carry its whole job (list, confirm, act) so the caller needs no protocol.
- **Generators follow the operation-object discipline**
  ([architecture.md](../specs/architecture.md)). That is a standing rule, not a
  choice made here: index state comes through a snapshot, and no generator
  reaches into another actor's state.

Questions for the design:

- **Namespace.** How generated paths are reserved so they cannot collide with a
  user's own tmp:// docs (a prefix, a registry keyed by prefix, something
  else), and what a write to one says.
- **Actor-owned state.** How a generator gets in-memory state another actor
  owns (hunt queues, seats): a copy the owning actor hands over is the obvious
  shape, unproven.
- **Change and subscription.** Whether a generator can declare a change source
  so the server regenerates it only while it has subscribers, and how that
  coalesces under load. If it can, the state document needs no separate
  mechanism.
- **Path syntax.** A path that embeds a file path (`…/pdfs/PATH`) has to carry
  its slashes, and on Windows a drive letter's colon. That is the same problem
  chunk addresses have ([windows-support.md](windows-support.md) 7.4).

**Item 2.** The state document, built as a generator: its name, its shape
(which activities, which tags, how progress is expressed), and its coalescing
under load.

It opens with an **identity header**: ark's version plus a build stamp (commit
and build time), fixed at startup. The stamp is injected by the Makefile
through `-ldflags`, as `Version` is today; Go's own VCS stamping is off
(`-buildvcs=false`, since the repo is both git and fossil), so it has to come
from the build. The commit comes from git, and a dirty tree is marked as such.

Its publishers, the server activities that report into it: embedding first
(the case that prompted this), then reconciliation, indexing and the queues.

A second case for reconciliation, met 2026-10-25: every `ark.toml` edit reloads
the config and starts a full reconcile, and clients wait behind it with nothing
saying why. Measured that day on the live index, two reloads minutes apart: one
reconcile took 2 min 56 s (12:43:30 to 12:46:26), during which `ark status` timed
out at 15 s and a search at 30 s; the next took 24 s. Not known why they
differ: the first followed a `search_exclude` change and dropped 8 scheduler
queue entries, but neither is shown to be the cause. A client that saw
"reconciling, N of M files" would have known to wait. Whether a config reload
needs a full reconcile at all is a separate question, worth asking when this
item is designed.

The identity also goes to Lua directly, as a `sys.version` string (and the
build stamp beside it, name to settle), set once in `registerLuaFunctions`.
A constant needs no subscription, so the UI can read it without the document
existing yet. This adds to `specs/lua-api.md`.

**Item 3.** A CLI view of the state document and the inspectors, live while the
server is busy (form to settle: a `status` flag, its own verb, or reading
generated paths through `ark fetch`).

**Item 4.** A Frictionless view of the same paths (ui-\* work, separate pass).

Its first piece is the identity in the mcp status bar, at the far right after
the status text: `ark v<version>` with the build stamp on hover. It lives in
ark's copy of the mcp app only, since Frictionless's copy has no `sys` global.
It reads `sys.version` until the document view arrives, then can switch to the
document's header.

**Item 5.** The hunt path: a watermark from being recognised through to
being served. Bill chose it (2026-10-21) as the testbed for the rest of the
carve. It is the most tangled state ark owns, and the one where not being able
to see it has already cost a real session.

What happened, as a HollowRpg session reported it on 2026-10-20. **Not yet
reproduced or checked against the server's own records**; this is the
sender's reading of the evening, written as it unfolded, and the views below
are how to check it. `<BLOODHOUND>`
watermarks were sent while a Luhmann seat was held and the session had no
secretary of its own. None were served or reported. They turned out to be
**queued, not lost**: a per-session secretary spawned later for other work
served three of them, with an empty seed set each time. So the pool did not
serve this session's hunts. They waited for a secretary of the session's own,
and nothing showed that they were waiting. Two more never got a hunt id at all,
so they were apparently never recognised. One was a single scoped paragraph,
the other three paragraphs with no `filter-files`. Separately, stopping a
Luhmann session left its `ark luhmann next --first` running; three seat
processes were alive at once, one of them reparented to init.

What to show (the report's list, kept):

- **Seat holders:** each `luhmann next` waiter, with pid, client session, start
  time, whether that session is alive, and which one holds the seat.
- **Hunts:** each recognised watermark with id, source session, clue head,
  scope, status (recognised, routed, queued, served, unservable) and
  destination. Plus recent recognitions, so a watermark that was *never*
  recognised shows up as missing.
- **Queued hunts:** age, and what each is waiting for (a pool secretary, a
  per-session secretary, the seat).
- **Listeners:** each `bloodhound listen` and each secretary blocked on
  `recall next`, with nonce, class, parent session, last delivery.
- **A verb to drop queued hunts** by session, id, or age, listing what it would
  drop before dropping it.

What to fix, once it can be seen: why the pool did not take another session's
hunts (the routing of pool-served hunts back to the emitting session is a
suspect), why two watermarks went unrecognised, and the orphaned seat
processes. Measure each against the views above rather than reasoning from the
code.

**Item 6.** The PDF inspector: one generated path per indexed PDF, showing how
ark indexed it. Pages, and per page the chunks by kind (`para`, `table`, …) with
their boundaries; pages with no extractable text (image-only, so unsearchable
without OCR); anomalous bytes in chunk content (NUL and other control
characters); chunks whose text duplicates another chunk's; every indexed path
that resolves to the same real file; and which sources and scopes include it. A
corpus-level view beside it: how many PDFs, how many with image-only pages,
how many with anomalies.

The reason is the one Visible Substrate gives: render the structure in a medium
the reader already uses, and a bug that was invisible in the numbers becomes
obvious. Here the structure is the chunking and the medium is markdown.

What it should have shown in seconds (reported by the same HollowRpg session,
2026-10-20, and not yet re-checked by ark; one of the fragment-chunk claims
came secondhand from a review agent): a chunk carrying a NUL byte with the heading split across its edges
("DECLARING" came out as `…De` | `LARING…`); fragment chunks holding text from elsewhere in the same PDF;
a symlinked PDF indexed twice under both names, so every hit came back twice
([symlinks.md](symlinks.md)); and a `-files` scope returning hits from the
symlink's target and from a build output. The fixes belong to the text
extraction, the chunker, symlink handling and search scoping. The inspector is
how they get found and checked.

**Item 7.** `ark files --status --detail` shows, per file, its state, bytes,
chunk count and chunk-size spread, but nothing about what the index holds for
those chunks. Add each file's counts of `EC` records (how many of its chunks
have an embedding) and `X` records (`@ext` routings onto its chunks), as
specified in [record-formats.md](../specs/record-formats.md). Needs none of
Item 1; it is an addition to an existing verb. To settle in design: whether `X`
counts routings *onto* the file, routings a mirror file *authors*, or both as
separate columns.

Two questions on 2026-10-25 needed exactly this and had only `ark status -db`'s
whole-index totals. How many chat chunks gained vectors in the 26 seconds a
commented-out `search_exclude` let the embedding pass reach the chat logs (the
gate is still that setting until chat-embeddings Item 1 lands). And whether any
`@ext` routings existed while bloodhound-fix Item 14 was being designed (none
did). A per-file count answers both directly, and an `-files` scope narrows it
to the chat logs or a mirror tree.

### Identity decisions

- DECIDED (Bill, 2026-10-12): the version string and the status-bar display
  join this carve, the Lua support with Item 2 and the status bar with Item 4.
  The identity slice (header, `sys.version`, status bar) can land ahead of the
  rest of the carve, since it depends on none of the open questions below.

### Structure decisions

- DECIDED (Bill, 2026-10-21): the carve is built on generated tmp:// paths
  (Item 1). The state document is one generator (Item 2), the hunt path is the
  first inspector and the testbed (Item 5), and PDFs are the second (Item 6).
- DECIDED (Bill, 2026-10-21): `ark tag inspect` moves onto a generated path,
  after Item 5 proves the mechanism.
- DECIDED (Bill, 2026-10-21): `tmp://ctl/…` paths are write-only, with tagged
  key-value lines as their payload; a read is an access-permission error.
- DECIDED (Bill, 2026-10-21): a failed ctl write returns a baby-food markdown
  error saying what failed and what would have been accepted. So do the other
  errors on these paths: reading a ctl path, and reading a generated path that
  does not exist.
- DECIDED (Bill, 2026-10-21), wider than this carve: **every ark response is
  baby food by default**; JSON or terse output only behind a flag (`--json`,
  `--quiet`, `--terse`, `--compact`). Generated paths are written to that rule
  from the start.

## Open questions

@undecided: the reserved namespace's prefix and the registry's shape; settled when Item 1 is designed.
@undecided: the state document's name and whether it is one path or one per activity; settled with Item 2.
@undecided: coalescing rate for a subscribed generator under load; settled by measuring a regeneration's cost during a busy embed.
@undecided: whether the existing `ark status` reads the state document when a server is up; settled with Item 3.
