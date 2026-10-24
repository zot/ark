# LuhmannCLI
**Requirements:** R2791-2796, R2861, R3010-3019, R3026, R3036, R3122-3125, R3130, R3147, R3148, R3321, R3578

The Go surface the orchestrator session calls into. Three verbs record
its own supervisor lifecycle into `~/.ark/monitoring/luhmann.jsonl` —
`spawn-record` and `exit-record` are server-required (they route through
the write actor); `inspect-exit` is cold-start (it only reads subagent
JSONLs). A fourth verb, **`next`**, is the orchestrator's blocking
**drain tube**: it hands back curation tasks, supervisor directives, and
keepalives from the recall service, guarded by an in-memory ownership
lease that keeps exactly one Luhmann draining (R3010–R3016).

The orchestrator session itself — the persona, the supervisor logic,
the Task-spawning of managed subagents — lives in a Claude Code
skill (`.claude/skills/luhmann.md`) and is **not** Go code. This
card covers only the Go surface the skill calls into.

## Knows
- arkDir: string — `~/.ark/`
- logPath: string — `~/.ark/monitoring/luhmann.jsonl`
- luhmannOwner: string — the session that currently holds the
  Luhmann role (R3012). In-memory server state, empty when
  unowned; cleared on a server restart (no persistence). The
  ownership lease `next` enforces.
- luhmannActive: int — how many `next` calls the seat owner has in flight
  (R3321). Normally 0 or 1, the drain being a single loop. Non-zero is
  proof of life: the server is the party blocking on that call, and sees a
  dead client as a cancelled request.
- luhmannLiveUntil: time — the instant the lease expires once nothing is in
  flight (R3321). Set to `now + luhmannLeaseGrace` when a call returns
  normally, and to the **zero time** when one is cancelled, since then no
  caller is coming back. A lease neither active nor within this instant is
  stale, and a stale lease is claimable by another session's `--first`
  (R3013).
- luhmannDrain: chan — the supersede signal of the seat's current drain, the
  newest claimed `next` call (R3578). A claim closes the previous one's and
  installs its own in the same locked step; the call clears it on return if it
  is still the current one. Nil when nothing is draining.
- nextQueue: chan work — in-memory queue of work items `next` drains
  (R3011): curation tasks (a request-doc path to refine) and supervisor
  directives (stand up / stop a pool secretary), both pushed by the recall
  watcher; plus **command** items (R3130) — a `send` instruction with an
  inert correlation nonce, pushed by the synchronous producer
  (crc-LuhmannSend.md); plus **frictionless-event** items (R3147) — a UI
  event, pushed by the event-routing pump (crc-LuhmannEvents.md) while the
  seat owner has opted in. Server state; a bounce drops it.

## Does

### `luhmann spawn-record --class C --nonce N --task-id T`
- Server-required. All three flags are required; missing-flag exit
  is non-zero before any state change.
- Read the existing log tail to find the most recent record for
  the named class. `crashes` and `quit_early` both carry forward
  from that record's fields (or `0` if no prior record exists). (R2861)
- Append one record:
  ```
  {ts: now, kind: "spawn", class: C, nonce: N, task_id: T,
   reason: "", crashes: <carried>, quit_early: <carried>, backoff: 0}
  ```
  via the write actor. (R2794)

### `luhmann exit-record --class C --nonce N --reason R [--crashes K] [--quit-early K]`
- Server-required. `--class`, `--nonce`, `--reason` required.
- Look at the tail to find the previous record for this class and
  read both `crashes` and `quit_early`. Reason → kind, then
  recompute both counters (R2795, R2861):
  - `context-limit` → kind `exit`; `crashes := 0`, `quit_early := 0`
    (a healthy recycle is a success — resets both).
  - `quit-early` → kind `quit-early`; `quit_early := prev + 1`,
    `crashes := prev` (held).
  - any other reason → kind `crash`; `crashes := prev + 1`,
    `quit_early := prev` (held).
  - `--crashes K` / `--quit-early K` override the respective
    computed value when the caller has already classified.
- Append one record:
  ```
  {ts: now, kind: <"exit"|"quit-early"|"crash">, class: C, nonce: N,
   task_id: "", reason: R, crashes: <computed>,
   quit_early: <computed>, backoff: <see below>}
  ```
  For `crash`, `backoff` is the seconds the supervisor will wait
  before respawn (orchestrator chooses, passes as `--backoff`).
  For `exit` and `quit-early`, `backoff` is zero. (R2795)

### `luhmann inspect-exit --nonce N [--json]`
- Cold-start.
- Reuse the nonce → `.meta.json` scan (R2759). **This path keeps
  reading `$CLAUDE_CODE_SESSION_ID` on purpose** — unlike the
  server, `inspect-exit` runs in an agent shell by construction and
  must work with no server at all, so the environment is the right
  source here and the asymmetry with R3306 is deliberate. On lookup
  failure, label is `unknown`.
- Read the subagent's JSONL backwards. Classify (R2796):
  - **healthy** requires `tokens_at_close` (the R2777 value) at or
    over `[luhmann].context_limit` — the generation filled and
    recycled as designed. With `ark connections recall next` the
    only clean exit is its context-gate directive, so a real
    recycle always reaches the limit.
  - **quit-early** when the most recent record is a clean turn
    boundary (a `tool_result` for `ark connections recall next` /
    `close`, or a `recall.jsonl` outcome ∈ {`result-emitted`,
    `silent-close`, `no-subscriber`}) but `tokens_at_close` is
    *below* the limit — the agent stopped before filling. The
    orchestrator respawns it (fresh nonce) like a healthy exit but
    does not count it as a crash; the distinct label keeps the early
    stop visible instead of masquerading as healthy.
  - **crash** when the most recent record is an error tail
    (`isError: true`) or the JSONL ends mid-turn (last record is
    not a complete tool_result / assistant turn).
  - **unknown** when the JSONL is empty or discovery failed.
- Default output: the label string on stdout. `--json`: object
  with `label`, `last_record_kind` (e.g. `tool_result`, `assistant`,
  `user`), `last_error` (string or null), and `tokens_at_close`
  (the R2777 value, or `0` when unmeasured).

### `luhmann next [--first|--force] [--keepalive N]`
- Server-required. The orchestrator's **drain tube**: a blocking
  long-poll that returns one work item and is re-invoked in a loop
  (R3010). The Luhmann skill backgrounds it so the session stays
  conversational; on completion the harness re-injects the result,
  Luhmann acts, and re-invokes. Same background-lotto-tube family as
  `RecallNext` and `{cmd} event`.
- **Ownership lease** (R3012): the calling session is an *identity*, not a
  tube scope — the queue is one global feed of all CLI hunts and
  directives, and a single `luhmannOwner` holds the role. The identity is
  resolved from the live environment rather than typed (R3314), so a
  re-hosted session cannot claim under the id its replayed history carries.
  - **`--first`** claims: sets `luhmannOwner := S` when unowned, when `S`
    already owns it, or when the incumbent's lease is **stale** (R3321);
    when a *live* different session owns it, errors `you don't have
    ownership` (R3013).
  - **plain** (no flag) validates and **never claims**: proceeds only
    if `S == luhmannOwner`; errors `there are no sessions` when
    unowned, `you don't have ownership` when a different session owns
    it — stale or not, since validating is not claiming (R3013).
  - **`--force`** sets `luhmannOwner := S` unconditionally — the
    deliberate takeover that needs no staleness judgment (R3013).
  - **Liveness** (R3321): the lease is held by the drain, not by a timer.
    `luhmannActive` rises for the duration of an owner's `next` call and
    falls on every exit path including cancellation, stamping
    `luhmannLiveUntil`; the lease is live while active, and for the grace
    window after a **normal** return. A **cancelled** return opens no window —
    the grace is for a caller that is coming back, and a cancelled request
    means the client is gone — so a killed drain frees the seat at once. The window is sized by a work turn rather than by the
    keepalive cadence, because the owner spends its keepalive *inside* a
    call where it is visibly in flight — so no permitted `--keepalive`
    makes a live orchestrator look stale (R3016). This is what makes a
    forked-away orchestrator displaceable at all: the re-host kills its
    backgrounded drain, and ark cannot learn of the departure any other
    way, the pty host having lost custody of the very child whose exit it
    would otherwise have seen (R3125).
  - **One drain** (R3578): every successful claim, in any mode, supersedes
    the call already draining the seat, which returns the stand-down
    disposition at once instead of waiting on `nextQueue`. A live
    orchestrator never has two calls in flight, so the older one is an orphan
    (a stopped session's backgrounded drain, or a displaced owner's) and any
    item it took would be lost. A superseded call from the owner decrements
    `luhmannActive` like any return, and the successor's call keeps the lease
    live. An in-flight orphan under a different session id still holds the
    lease and is not superseded, because it is not displaced: `--first` is
    refused against it, and `--force` is the remedy.
  - Whenever the seat changes to a **different** session, the claim also
    clears Frictionless event routing in the same locked step
    (`clearEventRouting`, crc-LuhmannEvents.md, R3148): routing does not
    inherit, so the incoming orchestrator must ask for itself. A re-claim by
    the *same* session leaves its routing intact.
  - The lease is **in-memory**, so a bounce clears it. The two error
    strings drive the skill's reflexes and make the protocol
    self-converging with no persistence (R3014): `there are no
    sessions` → re-invoke `--first`; `you don't have ownership` →
    **exit** (a losing second Luhmann steps down after a post-bounce
    race, so exactly one survives).
- **Blocks** up to the keepalive window (`--keepalive N`, default
  ~45 min; R3016) in a select over `nextQueue`, the keepalive timer,
  and ctx. On an item it returns crank-handle prose; on the deadline a
  keepalive (run next again). The kinds (R3011): a **curation task**
  (the request doc's content is **inlined** into the crank-handle — a
  `tmp://` path is not a Read-able file — and the skill refines it, then
  runs `ark bloodhound add … --loc … --note …` per kept item and a
  terminal `add … --done`, naming the request-doc path only as the
  `--result` arg, R3025/R3027); a **supervisor directive** (stand up /
  stop a pool secretary — the skill spawns/stops via Task and records
  with `spawn-record` / `exit-record`, R3019); the **keepalive**. A
  fifth kind, the **frictionless-event** (R3147), is produced by the event
  pump (crc-LuhmannEvents.md) once the seat owner opts in with `ark luhmann
  events`: a UI event routed onto this tube instead of `ark ui event`,
  fire-and-forget like a directive. A
  fourth kind, the **command** (R3130), is produced by `ark luhmann send`
  (crc-LuhmannSend.md): a synchronous CLI instruction rendered as a
  markdown request carrying an inert correlation nonce; `next` hands it
  back like any work item (leading with re-launch-first, R3036), and the
  sender brackets the reply off the JSONL tap. Only this kind needs
  correlation, and it rides a nonce in its own content rather than a tube
  protocol change.
- **Re-launch-first** (R3036): every *work* crank handle (curation,
  directive) LEADS with the instruction to fire the successor `next`
  (backgrounded) **before** processing the item (`relaunchFirst`),
  replacing the old trailing "run next again". Loop continuity thus
  moves from tail to front, so a mid-work drift or a garbled tool call
  can't kill the loop — the successor is already blocking. The keepalive
  already re-launches and is unchanged; the `/luhmann` skill teaches the
  same order.
- **Stubborn plumbing** (R3015): the `next` CLI treats an `ark serve`
  bounce as a wait condition — redials with backoff, and on reconnect
  a `there are no sessions` routes into the re-`--first` path rather
  than failing the loop (mirrors `RecallNext`'s R2903 redial).
- The keepalive is a **backgrounded-loop** clock (< 1 hr main-agent
  cache), NOT the secretary's 90 s foreground number (R3016); the tube
  **subsumes** the standalone `ark heartbeat` design.

### Pool lifecycle + curation opt-in (R3017–R3019, R3026)
- The **CLI-bloodhound secretary pool** is a Luhmann-managed class
  (R3019): on stand-up / stop directives the skill spawns / stops pool
  secretaries and records them on the same crash / quit-early streak
  machine and `[luhmann]` policy the recall class once used. Config:
  `class.bloodhound.pool_max` (default 3, R3017) and
  `class.bloodhound.cooldown_seconds` (default 600, R3018) — read from
  `[luhmann]` on the same reload path (R2801).
- Luhmann's **opt-in to serve CLI curation is simply owning the `next`
  seat** (R3026): no separate parent-signal subscription; a session not
  draining `next` serves no CLI hunts. Curation is the default (opt-out
  per-hunt via the CLI's `--raw`, R3038) but decoupled from occupancy —
  the watcher already freed the secretary on its return (R3024), so a slow
  refine costs only the CLI's latency.

## Collaborators
- Monitor (crc-Monitor.md): reads back the log this card writes.
- RecallAgentBuilder (crc-RecallAgentBuilder.md): provides the
  the same `.meta.json` scanning behavior (R2759); LuhmannCLI has
  its own cold-path parent lookup (above) because the server-side
  helper resolves the parent from a registry this CLI cannot reach
  without a server (R3306).
- Server (crc-Server.md): hosts the HTTP handlers for the two
  write-actor verbs, and the `next` long-poll endpoint + the
  `luhmannOwner` / `nextQueue` state.
- CLI (crc-CLI.md): dispatches the four subcommands.
- RecallWatcher (crc-RecallWatcher.md): the Fixer that **pushes**
  curation tasks (on a hunt's return) and supervisor directives
  (pool scaling) onto `nextQueue`; `next` drains what the watcher
  enqueues (R3011, R3024).
- LuhmannSend (crc-LuhmannSend.md): the synchronous producer — pushes
  **command** items onto `nextQueue` via `EnqueueLuhmann` and reads
  `LuhmannOwner()` to gate on a live orchestrator (R3130).
- LuhmannEvents (crc-LuhmannEvents.md): the event-routing producer — pushes
  **frictionless-event** items onto `nextQueue`, hangs its opt-in off this
  card's seat, and shares `luhmannMu` so a seat change clears routing
  atomically (R3147, R3148).

## Sequences
- seq-luhmann-supervisor.md
- seq-bloodhound-cli.md
- seq-luhmann-events.md
