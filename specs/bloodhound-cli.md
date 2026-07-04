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
ark bloodhound search TERMS...  [--wait] [--timeout S]
```

The whole client protocol in one blocking command:

1. **Create + subscribe.** The CLI creates the request doc
   `tmp://BLOODHOUND-CLI/<id>` carrying the `TERMS` payload (the same clue ·
   scope · depth · want a watermark carries) under the watcher tag
   `@ark-bloodhound-cli`, and subscribes to `@ark-bloodhound-cli-result: <id>`
   before the doc lands, so no result notification can be missed. The server
   accumulates the doc's fields and writes them in one atomic go, so the
   watcher sees a complete request.
2. **Block for the result.** `ark bloodhound search` is synchronous: it waits
   on its result tag, then prints. `--wait` governs the one case that is not an
   ordinary wait — a **busy pool** (no free secretary, pool at `pool_max`):
   with `--wait` the CLI blocks stubbornly until a slot frees (Stubborn
   Plumbing — a server bounce is a wait condition, so it redials and keeps
   waiting); without `--wait` a busy pool fails fast. `--timeout S` bounds the
   total wait (default 300 s).
3. **Print.** When the result tag fires, the CLI reads the result doc and
   prints the findings as clean **JSONL** on stdout, one object per line.

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

Curation is **mandatory** (every CLI finding gets Luhmann's judgment) but
**decoupled from occupancy**: the secretary was already freed at step 1, so
curation costs only the CLI's own latency, never a held pool slot. Luhmann's
**opt-in to serve CLI curation is simply owning the `next` seat** (`--first`,
[luhmann.md](luhmann.md)); there is no separate subscription — the ownership
lease is the opt-in, and a session not draining `next` serves no CLI hunts.

## `ark bloodhound add` — the result stencil

Luhmann never hand-writes the result JSON. It calls a stencil, one item per
call — the Stencil pattern, the same discipline as `surface` / `recommend` /
`finding`:

```
ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --id CHUNK-ID --path PATH --chunk CHUNK-TEXT
```

- Each call appends one JSON line to the result doc.
- A terminal call flips the result doc's tag to `@ark-bloodhound-cli-result:
  <id>`, the notification the waiting CLI is subscribed to. Whether the
  terminal is a `--done` flag on `add` or a sibling close verb is a
  Design-phase choice.

The pool secretary appends its **raw** results to the **request** doc through
the existing bloodhound builder path (its `finding` / `close`,
[bloodhound.md](bloodhound.md)), re-tagging to `@ark-bloodhound-cli-return`
instead of writing a session-result doc; `ark bloodhound add` is Luhmann's
write into the **result** doc. Two write targets, two docs.

## Tags and namespaces

Three new tags carry the CLI path, plus one reused tube. Names are
**provisional**, kept in the existing `@ark-*` service family (matching
`@ark-bloodhound-result` / `@ark-recall-result`) and pinned in Design:

- **`@ark-bloodhound-cli`** (request) — the CLI writes the request doc under
  it; the watcher subscribes and wakes on a new hunt.
- **`@ark-secretary-work=<pool-sec>`** (*reused*, R2948) — the existing
  secretary work tube; the watcher re-tags the request doc to the chosen pool
  secretary's value so its `next` picks up the hunt.
- **`@ark-bloodhound-cli-return`** — the secretary re-tags the request doc to
  it when its raw results are in; the watcher subscribes and wakes to free the
  secretary and route to curation.
- **`@ark-bloodhound-cli-result: <id>`** — value-scoped to the request id;
  Luhmann tags the result doc with it, and `ark bloodhound search` (subscribed
  since step 1) wakes only on *its* result.

The watcher → Luhmann hand-off uses no tag: it is an in-process `next`-queue
push. Request docs live in the `tmp://BLOODHOUND-CLI/` namespace, separate from
recall's `ARK-RECALL/` and the in-session bloodhound's `ARK-BLOODHOUND/`.

## JSONL output contract

`ark bloodhound search` prints one JSON object per line — the machine contract
external apps consume. Each line is one curated finding carrying at least its
`path`, `range`, and a curated `note`; the exact field set is fixed in Design.
An empty hunt prints no lines and exits zero, so a client can treat "no
output" as "no findings."

## Configuration

The pool is configured on the Luhmann side ([luhmann.md](luhmann.md)):
`class.bloodhound.pool_max` (max concurrent secretaries) and
`class.bloodhound.cooldown_seconds` (warm-idle window before pruning). The
existing `[luhmann]` crash / quit-early policy governs pool secretaries as it
would any managed class.

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
