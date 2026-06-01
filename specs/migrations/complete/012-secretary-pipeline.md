# Migration: shared recall daemon → per-session Secretary (seam 3a)

Language: Go (`ark serve` subsystem) + Claude Code agent definition +
the `/recall` skill. Configured via `[recall]` in `ark.toml`.

This is **seam 3a of the Recall Secretary** (ARK-STATE item 1) — the
topology change. It builds on seam 1 (signed Judgment record, migration
011) and seam 2 (RM surface-cooldown). Settled design:
[`.scratch/SECRETARY-NEW.md`](../../.scratch/SECRETARY-NEW.md), sections
"Topology — settled" and "Lifecycle & resilience — settled". Seam **3b**
(reinforcement of attached-tag edges via `AdjustJudgment` on keep/drop,
plus the fade / suggest-removal pruning thresholds) is the next carve and
is **not** in this seam.

## Problem (state A): one shared daemon

Today recall curation is done by a **single shared daemon** (the
`ark-recall-agent` Haiku), spawned and supervised by the **Luhmann
orchestrator** (the `[luhmann.class.recall]` class; `seq-luhmann-supervisor.md`):

- The daemon's loop verb `recall next <nonce>` subscribes to **bare**
  `@ark-recall-curate` (subscription session `recall-<nonce>`), so it
  matches *every* session's curation docs, and `lowestPendingCuration`
  scans all sessions, gating each doc on a result subscriber (R2857).
- A single shared process **cannot hold any one session's conversation**,
  so it judges from thin context — the curation-doc excerpts only, never
  the live conversation. It grabs the obvious, boilerplate tag (R2873
  documents the thin-context problem from the other direction).
- The watcher's activation gate `pipeSubscribed` already queries
  `SubscriberCount("ark-recall-curate", sessionID)` **per session**, but
  the daemon's bare subscription matches every value — so today the gate
  means "is the one shared daemon up?", not "is *this* session covered?".
- Surfacing has no per-(session, chunk) cooldown — a chunk can be
  re-offered every fire.

The settled design replaces this with a **per-session, context-isolated
secretary** that judges with the live conversation injected — making the
assistant's expensive taste affordable by cutting volume *with* context,
not blind to it.

## Solution (state B, 3a): the per-session Secretary

**One secretary per session, owned by that session's assistant.** The
secretary is the same Haiku-subagent shape as the daemon and reuses the
entire builder/verb machinery (`surface` / `recommend` / `close`,
monitoring log, nonce → JSONL discovery, the result-doc flow,
`RecallListen`) — only the loop's *scope*, *ownership*, *input context*,
and *persona* change.

### 1. Value-scoped curate subscription (`recall next --session`)

`recall next` gains a `--session <S>` flag. With it, the loop verb:

- subscribes **value-scoped** `@ark-recall-curate=<S>` (subscription
  session `recall-<nonce>`), instead of bare `@ark-recall-curate`; and
- `lowestPendingCuration` dispatches the lowest-fire pending
  `curation-<S>-<fire>` doc **for session S only** — no cross-session
  scan. The per-doc result-subscriber gate (R2857) is unchanged; for a
  per-session secretary it simplifies to "does S have an assistant
  listening", which it does by construction (the assistant that spawned
  the secretary).

The context-gate, keepalive window, foreground discipline, and dual
output (R2857/R2858) are unchanged. Without `--session`, `next` retains
the legacy bare-curate behavior (so the verb stays usable for a
one-shot/diagnostic curator), but the secretary always passes it.

### 2. Spawn + supervise ownership moves to the assistant

The per-session assistant — via the `/recall` skill — **spawns and
supervises** its session's secretary, replacing Luhmann's recall-class
supervision:

- On `/recall`, the assistant reserves a nonce (`recall reserve-nonce`),
  launches the secretary Task in the background with `(session, nonce)`
  in the prompt and `nonce <N>` in the Task description (for JSONL
  discovery, R2759 — unchanged), then runs its own consumer loop
  (`recall listen --session <S>`, R2865 — unchanged).
- When the secretary exits at its context limit (the `next` exit
  directive, R2857), the spawning assistant **respawns** it with a fresh
  nonce. With no shared daemon there is nothing central to supervise:
  the secretary self-gates on context, and the assistant that owns it
  respawns it. The migration-009 quit-early/crash/backoff streak machine
  was built for one always-on *unattended* daemon; at per-session scale
  with an interactive assistant present, the assistant simply respawns
  (no streak machine ported in 3a).
- **No respawn gaps.** Subscriptions are in-memory server state that
  outlives the agent (R803). While the secretary is down, curation docs
  queue against the live subscription and the respawned secretary
  resumes. A session that never resumes stops triggering curations
  (nothing lost); its orphaned subscription clears on the next `ark serve`
  bounce.

### 3. Conversation context injected into the curation doc

`recall next --session <S>` prepends the **last N conversation turns** of
S to the curation doc body it returns to the secretary, so the secretary
judges relevance *with the live conversation*, not just the source-
paragraph excerpts. N is `[recall].context_turns` (default 3). The turns
are read server-side from S's JSONL (the same genuine-user / assistant
records the watcher already parses); injection happens in `recall next`
at hand-off time, so the stored curation doc is unchanged and the watcher
is untouched. When the JSONL can't be read, the doc is handed over
without the context block (best-effort; never blocks a fire).

### 4. Surface-cooldown floor (seam 2 consumed)

The deterministic floor runs server-side in the watcher's `fire()`,
before a candidate ever reaches the curation doc: a candidate chunk whose
`(session, chunk)` was surfaced within `[recall].surface_cooldown`
(seam 2 `Store.LastSurfaced`) is dropped. The secretary then spends
judgment only on novel candidates. On the surface side, `SurfaceItem`
(the `surface` verb) calls `Store.MarkSurfaced(session, chunkID)` so the
cooldown starts ticking the moment a chunk is surfaced. (Dedup of
already-*discussed* tags via RD mark-on-send is unchanged.)

### 5. Secretary persona: filter **and enhance**

The agent definition is reframed from "shared recall daemon" to
"per-session secretary". The discriminate-and-surface judgment (the
existing persona's core) is unchanged. Added:

- **Conversational grounding** — the persona instructs the secretary to
  read the injected conversation turns and judge each candidate against
  *what is actually being discussed*, not just the source paragraph.
- **Filter and enhance tags, not just accept/reject** — the secretary
  may `recommend` a *sharper* tag than the thin-context `proposed-tags`
  suggest, or a tag the proposal pass couldn't see, because it has the
  conversation. The bar is **discrimination, not mere accuracy**: a tag
  that fits everything sharpens nothing. (The `recommend` verb already
  accepts an arbitrary `@t[:v]`, so this is a persona capability, not a
  new verb.)

The secretary still does **not** write rejection records — permanent
rejection stays user-relayed (R2774, unchanged).

### 6. The two presence gates (no code change)

Both gates already exist and already key per-session
(`SubscriberCount(tag, session)`); the value-scoped curate subscription
(item 1) is what makes the *curate* gate mean "this session's secretary
is up" instead of "the shared daemon is up". The watcher's
`pipeSubscribed` and `fire()` backstop, and `CloseResult`'s
result-subscriber gate, are unchanged.

### Config

- `[recall].context_turns` (int, default `3`) — how many trailing
  conversation turns `recall next --session` injects into the curation
  doc.
- `[recall].surface_cooldown` — already added in seam 2.

## What this migration reuses unchanged

The `RecallAgentBuilder` (nonce counter, curation builder,
`surface`/`recommend`/`close` verbs, monitoring log + Fumble Log, the
nonce → `.meta.json` JSONL discovery and token sums), the result-doc
shape, `RecallListen` (the consumer loop), the watcher's trigger
semantics and activation/backstop gates, the curation-doc shape, and the
fire counter — all unchanged.

## What this migration retires

- **The Luhmann recall-supervision role.** `[luhmann.class.recall]` and
  the `/luhmann` skill's recall-class spawn/inspect-exit/respawn steps
  (`seq-luhmann-supervisor.md` driven for recall) are removed; the
  assistant owns the secretary's lifecycle. The supervisor *mechanism*
  (`ark luhmann` verbs, the streak machine) stays for any future managed
  class — only the recall class leaves it. Luhmann's trajectory is
  promotion to user-side majordomo, not retirement (future work).
- **The bare-curate shared-daemon model** — superseded by the
  value-scoped per-session subscription. The legacy bare-`next` path
  remains for one-shot/diagnostic use but is no longer the production
  topology.

## What this migration does NOT do (deferred)

- **Seam 3b — reinforcement + pruning.** No `AdjustJudgment` on
  attached-tag keep/drop, no fade-from-results / suggest-removal
  thresholds or their `[recall]` knobs. The seam-1 signed-Judgment
  substrate and seam-2 RM record are in place for 3b; the maintenance
  half is its own carve.
- **Match-frequency** (the RM `varint(match_count)` trailer) — deferred
  with seam 2.
- **Relevance/tag bar floors as config** — the secretary's bars are
  persona-driven in 3a; learning floors from drop-logs is future work.
- **Decay-on-read** of the Judgment score — a knob, not 3a.
- **The strikeout `/ui` pass** — downstream UI work.
- **Porting the quit-early/crash streak machine** to the assistant's
  respawn — the interactive assistant respawns directly; revisit if
  per-session secretaries prove crash-prone.

## Test strategy

- `recall next --session S` subscribes value-scoped
  `@ark-recall-curate=S` (not bare) and dispatches only S's curation
  docs; a doc for session T is left pending.
- Per-session dispatch: with two sessions' docs pending, S's secretary
  returns S's lowest-fire doc and never T's.
- Context injection: `recall next --session S` prepends up to
  `context_turns` of S's conversation to the doc body; absent/unreadable
  JSONL hands the doc over with no context block and no error.
- `context_turns = 0` injects nothing.
- Cooldown floor: a candidate whose `(S, chunk)` was `MarkSurfaced`
  within `surface_cooldown` is dropped from the curation doc; outside the
  window it appears.
- `SurfaceItem` writes an RM record (`LastSurfaced` present after a
  `surface` call).
- Watcher gate parity: a session with a value-scoped curate subscriber +
  a result subscriber is tracked; dropping either drops the session
  (unchanged `pipeSubscribed` behavior, now driven by a value-scoped
  sub).
- Legacy bare `recall next <nonce>` (no `--session`) still subscribes
  bare and scans all sessions (back-compat for the diagnostic path).

## On completion (fold plan)

1. **`specs/simple-recall.md`** — rewrite the "Recall agent",
   "Assistant subscriptions — `recall listen` and `/recall`", and
   architecture sections to describe the per-session secretary: the
   value-scoped curate subscription, assistant-owned spawn/respawn,
   context injection, the cooldown floor, and the filter-and-enhance
   persona. Fold the "shared daemon / Luhmann-spawned" framing into
   "secretary / assistant-spawned".
2. **`specs/luhmann.md`** — remove `[luhmann.class.recall]`; note the
   recall class moved to the assistant.
3. **`specs/config.md`** — add `[recall].context_turns`.
4. **`specs/cli-commands.md`** — add `--session` to `recall next`.
5. **Retire** the requirements that pinned the shared-daemon topology
   and the Luhmann recall-supervision (R2850/R2851/R2857's bare-curate
   clause, the recall-class supervisor requirements) → their seam-3a
   replacements. Reword R2857 for the value-scoped `--session` path.
6. `minispec update migration-complete secretary-pipeline`.
