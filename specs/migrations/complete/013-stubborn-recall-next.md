# Migration: stubborn `recall next` — restart/rebuild-stable identity

Language: Go. Environment: built-in subsystem of `ark serve` —
`recall_watcher.go`, `recall_agent_builder.go`, `recall_next.go`, and the
`ark connections recall` CLI in `cmd/ark/main.go`. Configured via `[recall]`
in `ark.toml` (no new keys).

The settled design lives in [`.scratch/STUBBORN.md`](../../.scratch/STUBBORN.md);
this spec carves the identity changes that let `recall next` ride out an
`ark serve` restart — and an `ark rebuild` — without the secretary ever seeing
an error or acting on stale state. It is the **Stubborn Plumbing** pattern
applied to the recall loop (see `~/.claude/personal/patterns/stubborn-plumbing.md`).

## Problem (state A)

The recall pipeline keys its in-flight identity on **per-server volatile
counters** and on **chunkids**, both of which change underneath a restart or
rebuild while durable artifacts (the materialized curation files, the
still-running secretary subagent) survive. The result is four failure modes a
bounce can trigger, none of which the secretary can see:

- **A — connection death.** `next` blocks on the unix socket; when the server
  dies the read errors and the CLI returns a failed Bash call. The secretary's
  loop reads "connection refused" as "the operation failed."
- **B — nonce recycling → subscription-key collision.** The fire and nonce
  counters are in-memory and reset to 0 on restart (R2752, R2755). `next`
  subscribes under subscription session `recall-<NONCE>` (R2857/R2888); a
  surviving secretary holds nonce 3, the new server re-mints 1, 2, 3, and the
  `SubCount == 0` re-subscribe guard makes the colliding second subscriber a
  silent no-op — it starves.
- **C — counter reset → materialized-file clobber.** The fire counter resets,
  so a new `writeCurationFile(S, 1)` overwrites a surviving
  `~/.ark/recall-curation/curation-S-1.md`. These materialized files (the R2896
  keyhole) survive both a restart and a rebuild — they live outside the process
  and outside the index, so neither reconciles them.
- **D — stale chunkid.** Curation and result docs reference candidates by
  **chunkid** (R2749, R2751, R2756). Re-indexing even one file renumbers every
  chunkid after it, so a doc that straddles a rebuild points the secretary at
  the wrong content. `PATH:RANGE` is anchored to the source file and survives a
  rebuild; it drifts only when the file's *content* changes — universal
  deferred-reference staleness, not a rebuild artifact.

State A's design comment ("tmp:// is per-process, so a restart wipes everything
cleanly — no persistence required", R2752/R2747) is *true* for tmp:// docs, but
the materialized files and the surviving secretary break the clean-slate
assumption it rests on.

## Solution (state B)

The fix is **not** to persist state — it is to make identity durable enough that
the amnesiac server comes back to a namespace that cannot collide with the
survivors, and to make references survive a reindex. Four changes.

### 1. path:range is the sole chunk reference

Drop chunkid from the entire recall doc surface. References lead with
`<path>:<range>`; chunkid is resolved at **build time** (the watcher already
holds path+range for every candidate) and **never carried** across a deferral
hop (curation file → result doc → the assistant's later fetch).

- Curation doc: `## Candidate: <path>:<range> (<size>)` (was `## Candidate:
  <chunkid> (<size>) <path>:<range>`). The source heading becomes `# Source:
  <path>:<range>` (was `# Source Chunk: <jsonl-chunkid>`) — its path makes the
  reader's-own-session origin self-evident and removes the source-id footgun
  R2872 guards against.
- Result doc: `## Surface: <path>:<range> (<size>)` and `## Recommend:
  @<tag>[:<value>] on <path>:<range>` (chunkid dropped from both).
- CLI: `surface <F> -loc <path>:<range> -reason …` and `recommend <F> -loc
  <path>:<range> -tag … -reason …` (was `-chunk N`). The server fetches content
  via the existing `ChunkText(path, range)` primitive.
- Consumer fetch is unaffected — `ark chunks` already accepts either a chunkid
  or a path:range, so the assistant fetches surfaced chunks by path:range with
  no new mode.
- `SurfaceItem`'s source-session rejection (R2872) now reads the path directly
  from the loc; `MarkSurfaced` (R2894) still keys on chunkid, resolved
  just-in-time from the loc inside the same generation (see *NOT doing*).

Fixes D's corruption: a survived or deferred doc is at worst subject to
universal source-drift staleness, never chunkid-poison.

### 2. FIRE → per-session in-memory counter, dir-seeded at max + 2

Replace the global `fireCounter` (R2752) with a **per-session counter held in
memory** (a `map[session]uint64` under the watcher lock — the same race-freedom
the global counter has today, keyed by session).

- **Seed once, lazily, on the first fire for a session after cold start:** scan
  `~/.ark/recall-curation/` for that session's `curation-<session>-<fire>.md`
  files, take the max fire suffix, seed the counter at **max + 2** (or start at
  1 if the session has no surviving files). Then increment in memory.
- The surviving files **are** the high-water record (Alibi Stamp / Heirloom) —
  no separate persistence. The dir holds exactly the survivors (orphans +
  `-preserve-curation` docs); a cleanly-closed fire deletes its file and leaves
  no live reference, so reusing its number is harmless. The counter can only
  collide with a file it can see, and seeds above those.
- **Per-session, not global:** the in-flight count is bounded per session (one
  secretary ⇒ at most one dispatched-but-unclosed doc) but unbounded globally (N
  sessions ⇒ up to N in-flight), so no constant margin over a *global* max is
  safe; over a *session* max it is.
- **+2, not +1:** skips the one doc that may be allocated-but-not-yet-
  materialized — the file is written at dispatch (`next`), not at allocation
  (watcher), so the true high-water can sit one above what is on disk. One
  secretary ⇒ lag ≤ 1 ⇒ +2 clears it.
- **In-memory, not recompute-from-dir-each-time:** a constant offset cannot
  close the live allocation→materialization race (two watcher fires in that gap
  read the same dir max and pick the same number regardless of offset). Only an
  in-memory counter closes it; the dir scan is the *seed*, not a per-allocation
  recompute.

Because per-session fire numbers collide across sessions, the in-flight
`curations` / `results` maps and the CLI cookie key on the composite
`<session>-<fire>` token (the curation-doc basename) rather than a bare fire
integer. The crank-handle generates the token; the secretary pastes it opaque,
exactly as it does the bare fire today.

Fixes C.

### 3. Curate subscription keyed on session, not nonce

`next`'s subscription session becomes the durable `<session>` instead of
`recall-<NONCE>` (R2857/R2888). Two secretary generations across a restart then
share one stable, unique key — no recycled-nonce collision, no silent
no-op. This mirrors the **consumer side**, which already keys its result
subscription on session (R2865, `@ark-recall-result=<SID>`).

The nonce keeps its other jobs unchanged — JSONL/token lookup (R2760), close /
monitor correlation (R2758/R2763), the context-gate self-recycle check (R2777).
Those already tolerate a recycled nonce via mtime-descending discovery (freshest
wins, R2760). `reserve-nonce` (R2755) is unchanged.

Fixes B.

### 4. `recall next` connection redial

Identity (§1–§3) is the *reconcile* half of Stubborn Plumbing — making sure the
re-subscribe after a bounce lands in a non-colliding namespace. §4 is the
*reclassify* half, client-side in `cmdConnectionsRecallNext` and its HTTP proxy:

- Reclassify dial-refused / mid-block EOF from **error** to **not-yet**: redial
  with bounded backoff, re-issue the request (which re-subscribes idempotently
  via §3), resume.
- Cover **both** phases — cold dial (the secretary's next invocation lands on a
  server still restarting) and mid-block (the server dies while `next` blocks).
- On a cold dial, redial with bounded geometric backoff up to a budget (~20s).
  On a mid-block drop, or once the budget is exhausted, return a **keepalive**
  (exit 0) — a contract-honoring "keep waiting," never a fatal error and never
  the context-limit exit. The lotto-tube loop then re-invokes `next` and rides
  out the bounce across iterations. The failure path never reissues a fresh
  blocking request, so each call stays under the harness foreground threshold
  and the loop never hangs (no infinite-hang floor needed). A surviving
  secretary's abandoned in-flight fire is accepted as lost across a bounce — the
  loop re-syncs to a fresh doc.

Together: the secretary gets a result or keeps waiting, never a mid-flight
"server gone."

## Migration (no data migration)

Recall docs are ephemeral (tmp://, regenerated each fire) and the materialized
files are working artifacts, so there is **no record migration and no clean
reset to run**. State B takes effect on the next `ark serve` start:

- The per-session counter seeds from whatever materialized files happen to be on
  disk; pre-existing orphans are skipped over (the +2 seed), never clobbered.
- The first fires after the upgrade write path:range docs; any pre-upgrade
  materialized file lingering on disk is inert (never re-dispatched) and is swept
  by the same-session orphan sweep (R2758) or left as harmless litter.
- The surface-cooldown `RM` records (chunkid-keyed) keep working within a
  generation; see *NOT doing* for why they are not re-keyed here.

## What this migration does NOT do

- **No `RM` cooldown re-key.** `Store.MarkSurfaced` / `LastSurfaced` stay
  chunkid-keyed (R2894); `SurfaceItem` resolves the loc → chunkid just-in-time
  before calling them, which is correct within a generation. Re-keying `RM` on
  path:range (so the cooldown survives a rebuild) is a *separate* record-format
  migration with its own `record-formats.md` fold — a follow-on bonus, not
  required for stubbornness.
- **No startup file-sweep as a correctness mechanism.** With §1 and §2, survived
  files are inert litter, not a hazard. A sweep is optional housekeeping; if
  added later it is hygiene, not load-bearing, and must not be relied on (the
  rebuild case proves a reset-event sweep can be missed).
- **No same-session double-restart hardening.** A user restarting twice with the
  same session id could in principle leave two unmaterialized in-flight docs
  (needing +3). Claude Code is expected to prevent same-session double-spawn;
  designing for it is out of scope.
- **No nonce change.** The nonce counter stays in-memory and resets on restart
  (R2755); only its use as the subscription key moves to session.
- **No topology change.** The per-session secretary, the two presence gates, the
  context-injection, and the builder verb set are unchanged in shape.

## Test strategy

- **Per-session counter isolation** — two sessions each allocate fires 1, 2, …
  independently; the `(session, fire)` map keys never collide.
- **Dir-seed skips survivors** — with `curation-S-7.md` on disk, the first fire
  for S after a cold start is **9** (max 7 + 2), and writing it does not touch
  `curation-S-7.md`.
- **Dir-seed empty** — a session with no surviving files starts at fire 1.
- **path:range candidate format** — a curation doc carries `## Candidate:
  <path>:<range> (<size>)` with no chunkid; `# Source: <path>:<range>` heading.
- **surface by loc** — `surface <S>-<F> -loc P:R -reason …` writes `## Surface:
  P:R (<size>)`; the result doc carries no chunkid; `ark chunks P:R` resolves the
  content.
- **source-session rejection by loc** — `surface` with a loc whose path resolves
  to the fire's own session is rejected (R2872 parity, loc-based).
- **session-keyed subscribe** — `next --session S <nonce>` subscribes under key
  `S`; a second generation with a different nonce but the same S re-subscribes as
  a no-op (no silent starvation); two different sessions get independent
  subscriptions.
- **redial absorbs a bounce** — with the server stopped mid-block, `next`
  redials and returns a keepalive (not an error) within the window; a cold `next`
  against a down server waits and then keepalives rather than failing; an
  extended outage returns the catastrophic exit.

## On completion (folding plan)

When implementation lands, fold state B into `specs/` as the present and retire
the obsoleted requirements:

1. **`specs/simple-recall.md`** — rewrite "Fire counter" (per-session, in-memory,
   dir-seeded max+2), "Curation doc shape" and "Result doc shape" (path:range
   references, `# Source:` heading, composite token), the "Builder CLI" block
   (`-loc` flags, composite `<F>`), the subscription description (session-keyed),
   and add a "Connection resilience" subsection for §4.
2. **`specs/cli-commands.md`** — update `recall surface` / `recall recommend`
   (`-loc` replaces `-chunk`; composite `<F>`) and note `recall next`'s redial.
3. **`specs/config.md`** — no change (no new `[recall]` keys).
4. **Retire / reword requirements:** retire **R2752** (global fire counter) →
   new per-session Rn; retire **R2749** and **R2751** (chunkid in curation/result
   docs) → new path:range Rn; retire **R2756** (`-chunk` surface) → new `-loc`
   Rn. Reword **R2747** (session segment now load-bearing, not redundant),
   **R2758** (composite `<F>` cookie), **R2857** and **R2888** (subscription
   session = `<S>`, not `recall-<NONCE>`), **R2872** (loc-based source
   rejection), **R2894** (chunkid resolved from loc). Add new Rn for the redial
   behavior (§4).
5. **`ark status -db`** — unaffected (no record-class change).
6. `minispec update migration-complete stubborn-recall-next`.
