# Migration: quit-early escalation — distinct error class, own ceiling, emergency flag

**Language/environment:** Go (`ark` CLI + `ark serve`), the
`[luhmann]` `ark.toml` schema, and the orchestrator's Claude Code
skill (`.claude/skills/luhmann/SKILL.md`). Builds on migration 008
(`recall-next-crank-handle`), which introduced the `quit-early`
classification.

## Problem (state A)

Migration 008's Bug A taught `inspect-exit` to distinguish a third
exit class: `quit-early` — a clean daemon exit *below*
`[luhmann].context_limit`, neither a healthy fill-and-recycle nor a
crash. With the true-lotto-tube `recall next`, the only thing that
produces a quit-early is a **loop-discipline lapse**: the agent
declines to call `next` again after a clean `close` (or after a guard
denial). `next` deleted the old timeout-as-stop *trigger*, but nothing
can force the model to emit one more tool call. So quit-early is a real
error class — an error in the recall agent itself or in its
environment.

But the supervisor can't act on it. The label exists; the response
doesn't:

- **`exit-record` only knows two kinds.** `--reason context-limit` →
  `kind: exit` (resets the crash counter); *any other reason* →
  `kind: crash` (increments). A `quit-early` reason therefore records
  as a crash, inflating the crash counter and escalating a benign
  loop-lapse toward the crash-pause — exactly what R2796 says must not
  happen ("does not count it as a crash").
- **There is no quit-early counter.** A recurring quit-early (agent
  reliably does one doc then stops) is the bug's symptom in a new hat —
  respawn forever, one doc per generation — and nothing tracks or caps
  it.
- **The decision tree has no quit-early branch.** `seq-luhmann-
  supervisor.md` and the orchestrator skill only handle healthy / crash;
  a `quit-early` label falls through.

(Adjacent staleness migration 008 left behind: `luhmann.md`'s
`inspect-exit` section still lists only `healthy / crash / unknown` and
the old close-based healthy rule. Folded here, since this migration owns
the quit-early supervisor response.)

## State B — quit-early is its own tracked, capped, escalating class

A `quit-early` exit is recorded distinctly, counted on its own streak,
respawned a limited number of times, and — when the streak trips a
ceiling — paused with a loud, machine-visible emergency the user can't
miss.

- **New record kind `quit-early`.** `luhmann.jsonl`'s `kind` enum gains
  `quit-early`. It is a *transient* kind like `exit`: a quit-early is
  immediately followed by a respawn, so `luhmannState` treats it as
  `running` (not `crashed`).

- **Independent streak counter.** Records gain a `quit_early` integer
  field alongside `crashes`. The two counters are symmetric, and a
  success resets both ("reset after any success at all"):
  - **healthy `exit`** (reason `context-limit`) → `crashes := 0`,
    `quit_early := 0`.
  - **`crash`** → `crashes := prev + 1`, `quit_early := prev` (held).
  - **`quit-early`** → `quit_early := prev + 1`, `crashes := prev`
    (held).
  A crash storm and a quit-early storm are tracked separately; neither
  masks the other, and any clean generation clears both. The server's
  record handler already had a counter `default` branch that holds the
  previous value — that hold is exactly the cross-kind semantics; this
  migration extends it to compute both counters. `spawn-record` carries
  both counters forward.

- **`exit-record` gains the kind.** Reason → kind: `context-limit` →
  `exit`; **`quit-early` → `quit-early`** (new); anything else →
  `crash`. An optional `--quit-early K` override mirrors the existing
  `--crashes K` for callers that pre-classified.

- **New ceiling `[luhmann].quit_early_pause_after`** (int, default
  `3`). After this many consecutive quit-earlies for a class, the
  supervisor stops respawning and pauses — the same shape as
  `crash_pause_after`, on its own counter.

- **Emergency flag in accessible Go state.** A storm pause (crash
  streak ≥ `crash_pause_after`, or quit-early streak ≥
  `quit_early_pause_after`) carries a **reason** on its `pause` record
  (`crash-storm` / `quit-early-storm`), distinguishing it from a plain
  user pause. `monitor status` derives an `emergency` field (active +
  class + reason) from the latest state-defining record, and a
  server-side accessor exposes the same state so Frictionless can
  reflect it instantly (front-loaded per the Go-before-UI rule — the
  red flashing light is a downstream Lua-only pass). The orchestrator
  session also escalates in chat/voice the moment the pause is written:
  "escalate in any way possible."

- **Decision-tree branch.** The orchestrator skill and
  `seq-luhmann-supervisor.md` gain the quit-early path: respawn fresh
  (no backoff, not counted as a crash) while `quit_early <
  quit_early_pause_after`; at the ceiling, `monitor pause` with reason
  `quit-early-storm` + chat escalation. The crash path is unified into
  the same emergency concept — a crash storm now also records reason
  `crash-storm` and lights the same flag.

- **Logged for analysis.** Every quit-early is a `kind: quit-early`
  record in `luhmann.jsonl` (with `reason`, `nonce`, counters,
  timestamp), so recurring patterns are visible to `monitor recent` and
  to later analysis.

## What changes

- `monitoring.go` — `ClassifyLuhmannReason` (add `quit-early`),
  `luhmannState` (quit-early → running), a `quit_early` counter read
  alongside `crashes` (a `PrevQuitEarly`/`PrevCounters` sibling),
  storm-pause reason handling, emergency derivation.
- `server.go` — `handleLuhmannRecord` computes both counters; the
  emergency accessor.
- `config.go` — `[luhmann].quit_early_pause_after` knob.
- `cmd/ark/main.go` — `exit-record --quit-early K`; `monitor pause
  CLASS [--reason R]`; `monitor status` emergency surfacing.
- `.claude/skills/luhmann/SKILL.md` — the quit-early decision branch and
  the escalation copy.

## What does not change

- The `recall next` verb and the daemon loop (migration 008).
- `inspect-exit`'s classification logic (R2796 already emits the label);
  only the *recording* and *response* are added here.
- The watcher pipeline, the result-doc shape, the builder verbs.
- The shape of a healthy recycle: a real fill still reaches the limit
  and records `kind: exit`, `crashes := 0`, `quit_early := 0`.

## Out of scope — downstream

- **The red flashing emergency light** in `apps/ark` (Frictionless). A
  separate `/ui-thorough` pass that watches the emergency flag this
  migration exposes. Lua + viewdefs only, because the Go side already
  carries the state.
