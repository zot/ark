# Bloodhound CLI — external-app access to the warm bloodhound

Language: Go (recall-service additions + the `ark bloodhound` CLI group +
`ark luhmann next` from [luhmann.md](luhmann.md)). Environment: built-in
subsystem of `ark serve`, on top of Bloodhound
([bloodhound.md](bloodhound.md)) and Simple Recall
([simple-recall.md](simple-recall.md)).

The in-session bloodhound ([bloodhound.md](bloodhound.md)) is **pull** for an
agent already driving a Claude Code session: it emits a `<BLOODHOUND>`
watermark and a finding comes back through its own `listen`. The warm directed
search is useful to **external apps** too (a job tracker, a shell script, an
editor plugin) that have no session of their own. This spec gives them a CLI:
`ark bloodhound search TERMS` runs a directed hunt and prints the curated
result as JSONL. Batteries Included: one command carries the whole protocol,
so the client needs no knowledge of tubes, tags, or agents.

The constraint that shapes everything: a hunt still needs a **listening
agent** to run it — a sealed Haiku secretary to find, a strong parent to
curate. The CLI has no session, so it **borrows Luhmann's**.

## Precondition: Luhmann is the server

To use the CLI bloodhound, the **Luhmann orchestrator must be running** — "it
is the server." There is no prefer-Luhmann-else-fallback and no multi-listener
election: it is Luhmann or an error. This kills the role-identity question
(there is exactly one orchestrator, guarded by the `next` ownership lease in
[luhmann.md](luhmann.md)) and makes the watcher's scheduling deterministic.
When no Luhmann owns the seat, `ark bloodhound search` reports that the
orchestrator is not running and exits non-zero.

## The two docs and the tag baton

The pipeline runs on the **Fixer** pattern
(`~/.claude/personal/patterns/fixer.md`): the recall **watcher is the deciding
go-between** that routes work between the ends, and a `tmp://` doc's **tag is
the routing baton**, rewritten at each hop to summon whoever acts next. Two
docs carry a hunt:

- **The request doc** `tmp://BLOODHOUND-CLI/<id>` — the internal working
  artifact. The CLI creates it; it accumulates the query, the watcher's search
  seed, and the secretary's raw results as the baton passes. Its tag mutates
  `@ark-bloodhound-cli` → `@ark-secretary-work=<pool-sec>` →
  `@ark-bloodhound-cli-return`, walking the doc from the watcher to a secretary
  and back to the watcher.
- **The result doc** — the clean external output. Luhmann creates it, writes
  the refined JSONL, and tags it `@ark-bloodhound-cli-result: <id>`, which the
  CLI has been waiting on since it submitted. The CLI never sees the request
  doc; two docs keep its view free of the internal pipeline.

**Every hop is atomic.** A stage accumulates its additions and, in one write
(the write actor's single-go flush), appends its content *and* rewrites the
tag. The next stage therefore never wakes on a half-built doc: the tag flips
*because* the content is ready. This atomicity is the load-bearing correctness
property of the whole baton.

The baton passes CLI → watcher → secretary → watcher → Luhmann → CLI. The one
hop that is **not** a doc-tag is watcher → Luhmann: both are server-side, so
the watcher hands the request-doc path to Luhmann as a crank-handle event on
the in-process `next` queue ([luhmann.md](luhmann.md)) rather than through
pubsub.

## `ark bloodhound search`

```
ark bloodhound search [CLUE...]  [--file PATH | --file -] [--wait] [--timeout S] [--raw] [--markdown]
```

The whole client protocol in one blocking command:

1. **Create + subscribe.** The CLI creates the request doc
   `tmp://BLOODHOUND-CLI/<id>` carrying the clue and the `scope`/`depth`/`want`
   metadata under the watcher tag `@ark-bloodhound-cli`, and subscribes to
   `@ark-bloodhound-cli-result: <id>` before the doc lands, so no result
   notification can be missed. The server accumulates the doc's fields and writes
   them in one atomic go, so the watcher sees a complete request.

### Clue input — one-liner or file/heredoc (markdown)

The **clue** is the searchable content; `scope`/`depth`/`want` are metadata that
shape the hunt, not search text. Two ways to supply the clue:

- **Positional `CLUE...`** — the one-liner case, joined into a single line.
- **`--file PATH`** — read the clue from a file. **`--file -`** reads stdin, which
  is the **heredoc** path an agent or script uses to pass a **multi-paragraph
  markdown** clue without escaping newlines (fidelity by construction, the same
  move as the messenger's `--content-file`). `CLUE...` and `--file` are mutually
  exclusive.

A multi-paragraph clue is the point: each blank-line-separated paragraph is a
distinct **search idea**, and the Recall seed splits the clue and searches each
idea separately, unioning the hits (see the Recall-seed "per-idea seeding" in
[bloodhound.md](bloodhound.md)). A one-liner clue is a single idea — unchanged.
`argv` can't carry paragraph breaks, so the file/heredoc path is what makes the
CLI a first-class multi-idea search client.
2. **Block for the result.** `ark bloodhound search` is synchronous: it waits
   on its result tag, then prints. `--wait` governs the one case that is not an
   ordinary wait — a **busy pool** (no free secretary, pool at `pool_max`):
   with `--wait` the CLI blocks stubbornly until a slot frees (Stubborn
   Plumbing — a server bounce is a wait condition, so it redials and keeps
   waiting); without `--wait` a busy pool fails fast. `--timeout S` bounds the
   total wait (default 300 s).
3. **Print.** When the result tag fires, the CLI reads the result doc and
   prints it in the mode set by its flags (all client-side; the pipeline is
   identical). Default: clean **JSONL** on stdout, one object per line — the
   script contract. `--markdown`: the same curated findings rendered as a
   markdown locator list (Baby Food — the chewed view an agent reads). `--raw`:
   the secretary's own findings, uncurated (see "Curation ownership" below);
   because those are already markdown, `--raw` output is markdown regardless of
   `--markdown`.

## The watcher as scheduler + hub + router

The recall watcher is the Fixer for CLI hunts: the deterministic Go go-between
that schedules the pool and routes the request doc between the ends. All
scheduling is deterministic Go; nothing is decided by a language model. The
watcher is touched on **every** secretary transition (dispatch and return), so
occupancy and pool bookkeeping stay entirely local to it. Two triggers:

**On the request tag `@ark-bloodhound-cli`** (a new hunt):

- **Enhance.** Run the corpus's combined search on the payload
  (`Librarian.Recall`, R3006) and add the `## Recall seed` block, then add the
  search crank handle (R2938). This makes the request doc a standard bloodhound
  task doc, so a pool secretary runs it identically to an in-session hunt.
- **Schedule + route.** If a pool secretary is free, re-tag the doc to that
  secretary's `@ark-secretary-work=<pool-sec>` tube. If none is free and the
  pool has room (`class.bloodhound.pool_max`), push a *stand up another
  secretary* directive onto Luhmann's `next` and route once it reports ready.
  If none is free and the pool is full, a busy error (which `--wait` rides
  out). If too many secretaries sit idle past cooldown, push a *stop one*
  directive.

**On the return tag `@ark-bloodhound-cli-return`** (a secretary finished):

- **Free the secretary** — it is occupancy-free the instant its return lands at
  the watcher, *before* curation, and enters a **cooldown**
  (`class.bloodhound.cooldown_seconds`) during which it stays warm and
  preferred for the next hunt; only past cooldown is it eligible for a *stop
  one*. There is no separate occupancy state machine: free means the return is
  back at the hub.
- **Route to curation** — push the request-doc path onto Luhmann's `next` queue
  as a curation crank-handle (in-process; no doc-tag hop).

## The discernment split

The CLI path's return differs from the in-session bloodhound's on purpose.
In-session, the secretary's finding goes straight to the session assistant. On
the CLI path the sealed Haiku is not the last word: its raw find is refined by
a **strong parent** (Luhmann) before the external client sees anything. Along
the baton:

1. The pool secretary hunts, appends its **raw results** to the request doc,
   and re-tags it `@ark-bloodhound-cli-return`, handing it back to the watcher
   (not to Luhmann, not to the CLI), which frees it.
2. The watcher pushes the request-doc path onto Luhmann's `next` queue as a
   curation task.
3. Luhmann drains it, **refines** the raw results (the discernment the Haiku
   lacks), and writes the **result doc** — emitting each kept item through `ark
   bloodhound add` and tagging `@ark-bloodhound-cli-result: <id>` to wake the
   CLI.

Curation is the **default** — every CLI finding gets Luhmann's judgment unless
the client opts out with `--raw` (see "Curation ownership" below) — and is
**decoupled from occupancy**: the secretary was already freed at step 1, so
curation costs only the CLI's own latency, never a held pool slot. Luhmann's
**opt-in to serve CLI curation is simply owning the `next` seat** (`--first`,
[luhmann.md](luhmann.md)); there is no separate subscription — the ownership
lease is the opt-in, and a session not draining `next` serves no CLI hunts.

## Curation ownership: curated (default) vs `--raw`

Directed search is not one tool but three points on a **curation-ownership
axis** — who does the discernment the sealed Haiku lacks, and in whose context:

| Option | Secretary (the hunt) | Curation | Overhead | When |
|---|---|---|---|---|
| **Local `/bloodhound`** | you spawn + supervise | **you**, in your own session context | high (spawn, listen loop, respawn) | search is central to your work; you want own-context, iterative curation |
| **Remote `--raw`** | **Luhmann's** pool secretary | **you**, in your own context | ~zero (one CLI call) | you want the finds and will judge them yourself; no agent management |
| **Remote curated** (default) | Luhmann's pool secretary | **Luhmann**, separate context | ~zero | scripts; or an agent that wants a finished answer |

`--raw` is the missing **middle**: it borrows Luhmann's warm secretary (the
expensive part — a Haiku working the trail) but keeps the *other* half of
`/bloodhound`, the curation, in the caller's own head, where folding finds into
live reasoning is powerful — and it needs **no agent management**, just one CLI
call. Curated stays the **default** because a *script* has no context to curate
*in*: it consumes the JSONL as-is and needs Luhmann's discernment already baked
in. `--raw` is the agent opt-in, never a replacement.

**How `--raw` routes.** The request doc carries the client's intent — `--raw`
writes `curate: false` in the `TERMS` payload. The watcher (the Fixer) records
that intent in memory keyed by `<id>` when the request first arrives (the doc
body is rewritten at later hops, so the flag can't live there). At the **return
hop** (`@ark-bloodhound-cli-return`) the watcher branches:

- **raw** — write the result doc `tmp://BLOODHOUND-CLI-RESULT/<id>` directly
  from the request doc's `## Raw findings`, flip its tag to
  `@ark-bloodhound-cli-result: <id>`, drop the request doc, and **skip the
  Luhmann `next` push** entirely. The secretary's findings are already the
  markdown locator lines it wrote (`- path:range (size) — note`), relayed
  verbatim — the Baby Food an agent reads.
- **curated** — push the request-doc path onto Luhmann's `next` queue exactly as
  the discernment split above describes.

The CLI stays dumb (Batteries Included): the same result-tag subscription wakes
it either way, and it prints per the flag it sent (`--raw` → the relayed
markdown verbatim; else the curated JSONL, `--markdown`-rendered on request).
The routing intelligence lives in the go-between, where all the other routing
already is. A raw hunt paired with a warm pool touches Luhmann **not at all** —
no stand-up, no curation — so it takes the slow, serial curation step off the
critical path (the latency the interactive path is most sensitive to).

## `ark bloodhound add` — the result stencil

Luhmann never hand-writes the result JSON. It calls a stencil, one item per
call — the Stencil pattern, the same discipline as `surface` / `recommend` /
`finding`:

```
ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --loc PATH:RANGE --note NOTE [--chunk TEXT]
ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --done
```

- Each item call appends one JSON line — `{path, range, note, chunk?}` — to the
  result doc. The `--loc PATH:RANGE` locator matches the `surface` / `finding`
  siblings; `--note` is the curated one-liner; `--chunk` optionally carries the
  excerpt so an external app need not re-fetch.
- The terminal `--done` writes the result doc `tmp://BLOODHOUND-CLI-RESULT/<id>`
  and flips its tag to `@ark-bloodhound-cli-result: <id>`, the notification the
  waiting CLI is subscribed to; it also drops the internal request doc. A
  `--done` with no prior `add` writes an empty-body result doc — an empty hunt
  prints no lines and exits 0.

The pool secretary appends its **raw** results to the **request** doc through
the existing bloodhound builder path (its `finding` / `close`,
[bloodhound.md](bloodhound.md)), re-tagging to `@ark-bloodhound-cli-return`
instead of writing a session-result doc; `ark bloodhound add` is Luhmann's
write into the **result** doc (`tmp://BLOODHOUND-CLI-RESULT/<id>`, a namespace
separate from the request doc). Two write targets, two docs. Luhmann curates
from the request doc's content, which `ark luhmann next` **inlines** into its
crank-handle (a `tmp://` path is not a Read-able file).

## Tags and namespaces

Three new tags carry the CLI path, plus one reused tube — kept in the existing
`@ark-*` service family (matching `@ark-bloodhound-result` /
`@ark-recall-result`). Every one is written **with a colon** (`@word: value`);
a colon-less head tag is never extracted or published, so the next stage never
wakes:

- **`@ark-bloodhound-cli`** (request) — the CLI writes the request doc under
  it; the watcher subscribes and wakes on a new hunt.
- **`@ark-secretary-work=<pool-sec>`** (*reused*, R2948) — the existing
  secretary work tube; the watcher re-tags the request doc to the chosen pool
  secretary's value so its `next` picks up the hunt. `<pool-sec>` is the
  **composite `<luhmann-session>-<nonce>`** (the Luhmann seat owner plus the
  secretary's reserved nonce), so each pool secretary owns a *unique* tube — no
  shared queue, no claim needed for one-grabs-each. Because the value is still a
  real session id with the nonce appended after a dash, it fits the existing
  `<session>-<segment>` shape, so the pool secretary runs the ordinary
  `recall next --session <luhmann-session>-<nonce>` and every subscribe / route
  path works unchanged.
- **`@ark-bloodhound-cli-return`** — the secretary re-tags the request doc to
  it when its raw results are in; the watcher subscribes and wakes to free the
  secretary and route to curation.
- **`@ark-bloodhound-cli-result: <id>`** — value-scoped to the request id;
  Luhmann tags the result doc with it, and `ark bloodhound search` (subscribed
  since step 1) wakes only on *its* result.

The watcher → Luhmann hand-off uses no tag: it is an in-process `next`-queue
push. Request docs live in the `tmp://BLOODHOUND-CLI/` namespace, separate from
recall's `ARK-RECALL/` and the in-session bloodhound's `ARK-BLOODHOUND/`.

## Pool identity and the nonce

Luhmann is the hands — it holds the Task tool, so it launches each pool
secretary — and the watcher is the go-between that decides *when* one is needed.
So the secretary's identity (its nonce) is born where the launch happens, and
the watcher learns it from the reservation rather than minting it:

- **`ark connections recall reserve-nonce --luhmann`** — Luhmann reserves a
  nonce (the ordinary monotonic counter) for a pool spawn; the `--luhmann` flag
  makes the server also **register that nonce in the watcher's pool roster**
  (`cliPool`) as a pending pool secretary. The reservation doubles as pool
  registration, so the go-between learns the nonce with no report-back call.
- Luhmann then spawns the secretary via the Task tool with the composite
  `--session <luhmann-session>-<nonce>` in its prompt and records it with
  `spawn-record`. The watcher already holds both halves — the Luhmann seat owner
  (the S1 `luhmannOwner` lease) and the reserved nonce — so it composes the same
  `<luhmann-session>-<nonce>` and routes a hunt by tagging
  `@ark-secretary-work=<that composite>`. Both sides compute the identical key
  with nothing to report back.
- **`ark luhmann exit-record --class bloodhound`** — when a pool secretary
  finishes (a clean context-limit fill, a crash, or a quit-early), Luhmann's
  exit record **deregisters** that nonce from the watcher's roster — the
  symmetric bookend to `reserve-nonce --luhmann`. Without it, a secretary that
  exits on its own (not via a watcher *stop*) would linger in the roster,
  miscounting `pool_max` and drawing hunts to a dead tube until the cooldown
  prune eventually swept it. Deregistration is idempotent with `prune`'s own
  removal, so a self-exit and a *stop* directive reconcile safely.

## Output contract

Three client-side modes over the one pipeline:

- **JSONL (default)** — one JSON object per line, the machine contract external
  apps consume. Each line is one curated finding carrying at least its `path`,
  `range`, and a curated `note`; the exact field set is fixed in Design. An
  empty hunt prints no lines and exits zero, so a client can treat "no output"
  as "no findings."
- **`--markdown`** — the same curated findings rendered as a markdown locator
  list (Baby Food): `- \`path:range\` — note`, with the chunk excerpt as a
  blockquote when present, and a "no findings" line for the empty case. A pure
  client-side render of the JSONL the CLI already holds; no server or protocol
  change. Opt-in; JSONL stays the default (matching the `--json`-bool
  convention elsewhere).
- **`--raw`** — the secretary's *uncurated* findings, relayed verbatim from the
  request doc's `## Raw findings` (already markdown locator lines). The empty
  case relays the `(no findings)` body the secretary wrote. Since raw output is
  inherently markdown, `--markdown` is redundant with `--raw`.

## Reap and re-issue (robustness backstop)

The tag baton is liveness-fragile at two points, both time-based, so nothing
event-driven wakes the watcher to recover them. A **periodic sweep** on the
watcher's worker closes both:

- **Reap an abandoned request.** `ark bloodhound search` is a blocking command
  with no clean "I gave up" signal — on `--timeout` it simply exits, leaving its
  request stranded in the watcher (pending, or in-flight at a secretary whose
  return the client no longer awaits). A later stand-up would then waste a hunt
  on a query nobody is listening for. The sweep reaps any request older than a
  **reap TTL** (`class.bloodhound.request_ttl_seconds`, generously longer than a
  typical `--timeout` so a live client is never reaped): drop it from the
  roster's pending/in-flight sets and remove its request doc.
- **Re-issue a dropped stand-up.** When the watcher pushes a *stand up another*
  directive, it expects a new secretary to subscribe so `pump()` routes the
  waiting request. If Luhmann drops that directive (a drift or a garbled tool
  call mid-turn — the same failure class the re-launch-first crank handle in
  [luhmann.md](luhmann.md) hardens the loop against), no secretary arrives and
  the request sits unrouted until it is reaped. The sweep re-pushes a *stand up*
  for any request that has sat pending, unrouted, past a **re-issue threshold**
  while the pool has room — bounded by `pool_max` and the count of already
  in-flight stand-ups so it never over-spawns. This recovers the item that
  re-launch-first alone cannot: re-launch-first keeps Luhmann's loop alive, but
  the *dropped work item* still needs re-issuing.

Both are best-effort recovery of a fragile hop, not a guarantee: a client that
has already given up gets nothing either way. They keep a dropped hunt from
silently wedging the pool or stranding a request forever.

## Configuration

The pool is configured on the Luhmann side ([luhmann.md](luhmann.md)):
`class.bloodhound.pool_max` (max concurrent secretaries),
`class.bloodhound.cooldown_seconds` (warm-idle window before pruning — default
`600`, warm enough that follow-up hunts in an interactive burst reuse the same
secretary rather than paying a cold stand-up), and
`class.bloodhound.request_ttl_seconds` (the reap TTL above). The existing
`[luhmann]` crash / quit-early policy governs pool secretaries as it would any
managed class.

## What this slice does not do

- **No fallback when Luhmann is absent.** Luhmann-or-error; no multi-listener
  election, no ephemeral spawn. An external client with no orchestrator has no
  warm path — that is the precondition, not a bug.
- **No new Luhmann-side curation skill yet.** Teaching the Luhmann orchestrator
  session to handle the curation-task kind on `next` (read the request doc,
  refine, run `ark bloodhound add`) is a later, prose-only slice on the Luhmann
  skill; this spec builds the Go surface it drives. Because opt-in is owning the
  `next` seat, no separate serve-subscription skill is needed.
- **No change to the in-session bloodhound.** The watermark path
  ([bloodhound.md](bloodhound.md)) is untouched; the CLI path reuses its
  secretary, seal, crank handle, and Recall seed but adds its own request-doc
  submit, scheduler, tag baton, curation split, and JSONL output.
- **No new-tag invention, no RJ writes.** Inherited non-goals: the hunt only
  searches and reports.

## Test strategy

- **Precondition** — with no Luhmann owning the `next` seat, `ark bloodhound
  search` exits non-zero with an orchestrator-not-running message and submits
  nothing.
- **End-to-end** — a submitted request doc (tag `@ark-bloodhound-cli`) is
  enhanced by the watcher (Recall seed + crank handle), re-tagged to a free
  pool secretary's `@ark-secretary-work`, hunted, re-tagged
  `@ark-bloodhound-cli-return` with raw results, pushed to Luhmann's `next`,
  refined into a result doc tagged `@ark-bloodhound-cli-result: <id>`, and read
  by the CLI as JSONL.
- **Atomic hop** — a consumer subscribed to a hop's tag never observes the doc
  before that hop's content is written: content and re-tag land in one write.
- **Occupancy freed before curation** — a second hunt submitted while Luhmann
  is still refining the first dispatches immediately, proving the first
  secretary was freed on its return-to-watcher, not on the result tag.
- **Autoscale up** — with no free secretary and room in the pool, the watcher
  pushes *stand up another* and routes once ready; recorded via `spawn-record`.
- **Autoscale down** — a secretary idle past `cooldown_seconds` draws a *stop
  one* directive; within cooldown it does not.
- **Pool full** — no free secretary and the pool at `pool_max` yields a busy
  error; `--wait` rides it out until a slot frees, `--timeout` bounds the wait;
  without `--wait` it surfaces.
- **Stubborn wait across a bounce** — with `--wait`, an `ark stop` / restart
  during the block does not fail the CLI; it redials and still prints the
  result.
- **Empty hunt** — a hunt with no findings prints no JSONL lines and exits
  zero.
- **Raw relay** — a `--raw` hunt (`curate: false`) returns the secretary's
  `## Raw findings` verbatim and pushes **no** curation task to Luhmann; a
  curated hunt still routes through Luhmann.
- **Markdown render** — `--markdown` renders the curated JSONL as the locator
  list (chunk excerpt as a blockquote when present); an empty result prints the
  "no findings" line.
- **Reap** — a request older than `request_ttl_seconds` is dropped from the
  roster (pending / in-flight) and its request doc removed; a fresh request is
  left alone.
- **Re-issue** — a request pending unrouted past the re-issue threshold with
  pool room re-pushes a *stand up* directive; at `pool_max` (or with a stand-up
  already in flight) it does not.

## Sequencing

Depends on (landed): the in-session bloodhound
([bloodhound.md](bloodhound.md)) — secretary seal, search crank handle, Recall
seed, finding/close verbs — and Simple Recall
([simple-recall.md](simple-recall.md)). Depends on (this feature set): `ark
luhmann next` and the CLI-bloodhound managed class
([luhmann.md](luhmann.md)).

Front-load the Go: the `ark bloodhound` CLI group, the watcher scheduler / hub
/ router, the `ark luhmann next` tube, the `ark bloodhound add` stencil, and
the three tags. The Luhmann-side curation handling is a later prose-only slice.
