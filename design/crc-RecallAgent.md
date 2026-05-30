# RecallAgent
**Requirements:** R2769, R2771, R2850, R2851, R2859, R2860

Non-Go artifact set that defines the long-running Haiku daemon that
runs the ambient-recall loop. Three files cooperate (the loop driver
`recall-loop.md` retired in the recall-next migration — its logic
moved into one server verb plus the agent persona):

- `.claude/agents/ark-recall-agent.md` — the Claude Code agent
  definition: frontmatter (Haiku, `memory: local`) plus the daemon
  persona, which carries **both** the loop ("run `ark connections
  recall next <nonce>` and do what it says") and the surfacing bar
  (when a candidate genuinely fits its source paragraph).
- `.claude/skills/ark/recall-agent-guard.sh` — the PreToolUse guard:
  the hermetic-seal allowlist — the four recall verbs plus `cat
  <file>` for reading the backgrounded `next` output.
- `~/.ark/skills/ark-recall.md` — the standalone one-shot work skill,
  untouched (migration-007 preserved capability); not part of the
  daemon's loop.

The daemon's loop logic — subscribe, poll, fire-ordering, content,
context-gate — now lives in one server verb, `ark connections recall
next` (owned by `crc-RecallAgentBuilder.md`, R2857/R2858). This card
pins the configuration-file surface: agent type, model, the shrunk
allowlist, and the persona that drives the verb.

## Knows
- Agent definition (`.claude/agents/ark-recall-agent.md`) carries:
  - `model: haiku-4.5`; `memory: local` so MEMORY.md does not inherit
    (R2769).
  - The daemon persona: curator, not synthesizer; default to silence;
    the **surfacing bar** (genuine fit vs. mere resemblance) is the
    agent's core judgment and lives here, not in a fetched skill
    (R2860).
  - The **loop instruction** (R2860): run `ark connections recall
    next <nonce>` — it blocks ≤~90s and returns its output **inline in
    the foreground**; stay in the same turn and loop (no `sleep`, no
    polling, no backgrounding, no ending the turn) — on a curation
    doc, judge and surface/recommend the worthy candidates then
    `close`; on a keepalive, just run `next` again; on an exit
    directive, stop. Loop until exit. (If `next` is ever detached,
    `cat` its output file and carry on — fallback only.)
  - The nonce arrives in the prompt (`Nonce: <N>`) and the Task
    description; the agent passes it to every `next` / `close` call
    (R2851).
- Guard (`recall-agent-guard.sh`) carries:
  - The allowlist permitting `ark connections recall next | surface
    | recommend | close` plus `cat <file>` (single-arg read, no
    chaining/redirection) for the backgrounded `next` output;
    `Read`, `Edit`, `Write`, network, and every other `ark` verb
    denied as a class (R2859).
  - The denial-stderr runway (fumble-onboarding pattern, R2771),
    pointing back at `next`.

## Does
- Boot once per generation when the Luhmann orchestrator invokes the
  Task tool with `subagent_type=ark-recall-agent`, the `nonce <N>`
  description, and the prompt (R2850, R2851).
- Run the loop in one continuous foreground turn: `ark connections
  recall next <N>` (blocks ≤~90s, returns inline) → on a curation
  doc, judge candidates, `surface`/`recommend` the worthy ones,
  `close <F> --nonce <N>`; on a keepalive, loop; on an exit directive,
  stop so the orchestrator respawns. Never backgrounds `next` or ends
  the turn mid-loop — that would emit per-cycle "completed"s the
  supervisor misreads (R2860).
- Refuse every tool call outside the allowlist (the four verbs +
  `cat <file>`) via the PreToolUse guard (R2859); the denial stderr
  doubles as onboarding, pointing back at `next` (R2771).

## Out of scope
- Does **not** subscribe, poll, order, fetch, or context-check
  itself — `ark connections recall next` does all of that
  server-side (R2857, owned in `crc-RecallAgentBuilder.md`).
- Does **not** mint fires (the watcher does) or own respawn (the
  orchestrator does, `seq-luhmann-supervisor.md`); it owns only its
  own self-exit when `next` returns the exit directive.
- Does **not** write RJ records (R2774, `crc-RecallAgentBuilder.md`).

## Collaborators
- RecallAgentBuilder (`crc-RecallAgentBuilder.md`): the `next` /
  `surface` / `recommend` / `close` calls route through it
  server-side; `next` carries the whole loop (R2857, R2858).
- Server (`crc-Server.md`): the guard runs as a child of the Claude
  Code session, not the ark server; `ark serve` only sees the
  resulting CLI calls.
- Luhmann orchestrator (out-of-Go, approved gap A67): spawns the
  daemon once per generation with the `nonce <N>` description and the
  prompt, and supervises its respawn.

## Sequences
- seq-recall-agent.md
