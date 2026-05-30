# Migration: Retire one-shot recall, agent becomes daemon

**Language/environment:** Claude Code agent definition (markdown +
YAML frontmatter), a bash PreToolUse guard script, and the `ark`
CLI (Go). The Go side is already shipped — `ark subscribe`,
`ark listen`, and `ark connections recall context` all exist and
need no changes. This migration touches only the agent-side
configuration files and the orchestrator/loop skills that drive
them.

## Problem (state A)

The recall agent was built and tested as a **one-shot curator**: an
assistant detects a `@ark-recall-curate` event, reserves a nonce,
and spawns `ark-recall-agent` via the Task tool once per fire,
passing the curation-doc path and the `(fire, nonce)` pair in the
agent's prompt. The agent reads one doc, writes surface/recommend
items, closes, and exits. This is what we used during testing.

We then added `recall-loop.md` — the lotto-tube *loop* skill for a
**long-running daemon**: one persistent subagent that subscribes to
`@ark-recall-curate`, blocks on `ark listen`, processes each fire
inline per `ark-recall.md`, and self-recycles when its context
fills. The Luhmann orchestrator spawns and supervises it.

But the loop skill shipped without updating the three artifacts it
depends on. They are all still one-shot:

1. **The agent persona** (`ark-recall-agent.md`) tells the agent it
   is one-shot — "you do not loop; the `(fire, nonce)` pair in your
   prompt is your only context." When the orchestrator spawns it as
   a daemon with `"Start the recall loop now. Context limit: …"`,
   the agent finds no `(fire, nonce)` and stalls, hunting for a pair
   that the daemon contract never provides.
2. **The guard** (`recall-agent-guard.sh`) permits only `ark fetch`
   of a curation doc plus `surface`/`recommend`/`close`. It **denies
   `ark subscribe` and `ark listen`** — the two commands the loop is
   built on. Even a correctly-instructed daemon would be sealed out
   of its own loop on the first `ark listen`.
3. **The onboarding runway** (the guard's denial stderr, and the
   agent's first-action skill fetch) points at `ark-recall.md` (the
   per-doc *work* skill), never at `recall-loop.md` (the *loop*
   skill). Nothing teaches a spawned agent how to loop.

There is also a latent seam the daemon contract never closed: the
spawn puts the nonce only in the Task **description**, which a
hermetically-sealed Haiku agent cannot read. The agent needs its
nonce `N` to address `subscribe --session recall-loop-<N>`,
`close --nonce N`, and `context --nonce N` — but the daemon prompt
carries no nonce.

## State B — the daemon contract

The recall agent becomes the long-running daemon. The one-shot
*wiring* is retired; the one-shot *capability* is preserved, because
the per-doc work skill `ark-recall.md` is untouched and stays a
separate, reusable skill. We are not merging it into the loop skill.

What changes:

- **Spawn.** The Luhmann orchestrator spawns the agent **once per
  generation** (not the assistant, not per-fire). A *generation* is
  one spawn-to-exit cycle of the daemon: it runs the loop until its
  context fills past the limit, exits on its own, and the
  orchestrator respawns the next generation with a fresh nonce.
  On-disk state (`recall.jsonl`, RC/RD records, tmp:// docs) survives
  the cut — only the agent's working context is recycled. The prompt
  carries the nonce so the sealed agent can address its own calls:
  `"Start the recall loop now. Nonce: <N>. Context limit: <L>."`
  The nonce stays in the description too, so the supervisor's
  `nonce → .meta.json` discovery (`spawn-record`, `inspect-exit`,
  `close` token sums) keeps working.

- **Loop.** The daemon subscribes once to `@ark-recall-curate`
  (bare, under subscription session `recall-loop-<N>`), then blocks
  on `ark listen`. On each popped event it derives the fire `F` from
  the curation-doc path (trailing integer) / `@ark-recall-fire:`
  header — the fire is **minted upstream by the watcher**, never by
  the consumer, which is why there is a `reserve-nonce` but no
  `reserve-fire`. It fetches, decides, and calls
  `surface`/`recommend`/`close <F> --nonce N` per `ark-recall.md`.
  After each close it self-checks context via `ark connections
  recall context --nonce N --limit L`; on exit 1 it exits so the
  orchestrator recycles it with a fresh nonce.

- **Guard.** The hermetic-seal allowlist gains `ark subscribe`
  (for the `ark-recall-curate` tag), `ark listen`, and
  `ark connections recall context`, alongside the existing
  `ark fetch` of a curation doc and the three `recall` verbs.
  `Read`/`Edit`/`Write`/network stay denied. The denial-stderr
  runway points at `recall-loop.md`.

- **Bootstrap skill.** The daemon's first action loads
  `recall-loop.md` (the loop skill), via a body instruction rather
  than a lifecycle hook — lifecycle hooks do not fire in subagents.
  `recall-loop.md` delegates each iteration's per-doc work to
  `ark-recall.md`, which remains the work skill.

- **Assistant role.** A user-facing assistant no longer subscribes
  to `@ark-recall-curate`, reserves nonces, or spawns the agent. It
  runs a single subscription — the value-scoped result subscription
  `@ark-recall-result=<session>` — and pops result events via
  `ark listen` to decide whether to surface recall to the user. The
  curate subscription moves to the daemon.

What does **not** change:

- `ark-recall.md` — the per-doc work skill (curation-doc shape,
  surface/recommend/close mechanics, the surfacing bar). Untouched,
  still a separate skill, still the contract for one-shot work if we
  ever revive it.
- The watcher pipeline (curation-doc production, fire minting,
  subscriber-presence gate).
- The builder CLI verbs and the `recall.jsonl` monitoring log.
- `ark-recall-agent.md`'s location, model (Haiku 4.5), and
  `memory: local`.
- The agent never writes RJ records; permanent rejection stays a
  user-relayed decision.
- The Read-denial-as-runway mechanism and the Fumble Log.

## Why preserve one-shot capability

The daemon is the go-forward path for autonomy — one orchestrator
hosts the loop so individual assistants need only subscribe to
results. But a single-fire curation is still a coherent operation,
and the work skill that defines it costs nothing to keep. Retiring
the *wiring* (assistant-spawns-per-fire) while keeping the *recipe
card* (`ark-recall.md`) lets us re-spawn a one-shot curator later
without rebuilding the contract.
