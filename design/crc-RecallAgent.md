# RecallAgent
**Requirements:** R2769, R2771, R2859, R2860, R2873, R2890, R2895, R2897, R2898, R2941, R2942, R3108

Non-Go artifact set that defines the **per-session Haiku secretary**
that runs the ambient-recall loop — one per active session, spawned by
that session's own assistant via `/recall` (seam 3a; superseding the
shared Luhmann-spawned daemon). Three files cooperate (the loop driver
`recall-loop.md` retired in the recall-next migration — its logic
moved into one server verb plus the agent persona):

- `.claude/agents/ark-recall-agent.md` — the Claude Code agent
  definition: frontmatter (Haiku, `memory: local`) plus the secretary
  persona, which carries **both** the loop ("run `ark connections
  recall next --session <S> <nonce>` and do what it says") and the
  judgment bar (does a candidate fit the **live conversation** injected
  into the doc, not merely resemble the source paragraph — and filter
  *and* sharpen tags, the bar being discrimination not mere accuracy).
- `.claude/skills/ark/recall-agent-guard.sh` — the PreToolUse guard:
  the hermetic-seal allowlist — the four recall verbs, `cat <file>`,
  and the Read tool **only** on the curation-doc path
  (`.../recall-curation/curation-*.md`, R2897).
- `~/.ark/skills/ark-recall.md` — the legacy one-shot work skill, now
  marked **OBSOLETE** (migration-007 unhooked it from the daemon; the
  persona + `next` crank-handle carry the live instructions). Retained
  for reference a cycle or two before removal; not the live instruction
  surface (R2873).

The daemon's loop logic — subscribe, poll, fire-ordering, content,
context-gate — now lives in one server verb, `ark connections recall
next` (owned by `crc-RecallAgentBuilder.md`, R2857/R2858). This card
pins the configuration-file surface: agent type, model, the shrunk
allowlist, and the persona that drives the verb.

## Knows
- Agent definition (`.claude/agents/ark-recall-agent.md`) carries:
  - `model: haiku-4.5`; `memory: local` so MEMORY.md does not inherit
    (R2769).
  - The secretary persona: a **zettelkasten-keeper on an eternal
    hunt to flesh out a living collection** — curating and searching
    are one hunt, not two jobs (R2942). The **judgment bar** (does the
    candidate fit the live conversation injected into the doc, vs.
    mere resemblance; and filter *and* sharpen tags on discrimination)
    is the ambient half and lives here, not in a fetched skill (R2860,
    R2895). The tell for which half it is: *did the assistant ask?* —
    unasked → discriminate (default sparing); asked → deliver
    thoroughly (even "nothing in scope" is an answer).
  - The **directed-hunt instruction** (R2942): when `next` returns a
    `## Search task` (inline, not a curation-doc pointer), follow the
    search crank handle in its body — run the searches, curate to what
    answers the clue, emit via `finding <cookie> …`, then `close
    <cookie> --nonce <N>`. The craft lives in the crank handle, not
    the persona.
  - The **loop instruction** (R2860, R2896): run `ark connections
    recall next --session <S> <nonce>` — it blocks ≤~90s and returns a
    **short pointer** inline; stay in the same turn and loop (no
    `sleep`, no polling, no backgrounding, no ending the turn) — on a
    curation-doc pointer, **Read the file it names** (`Read` is permitted
    for that one path, R2897), judge, surface/recommend the worthy
    candidates, then `close`; on a keepalive, just run `next` again; on
    an exit directive, stop. Loop until exit.
    Surface/recommend the `## Candidate:` locator (`<path>:<range>`,
    matching the call) — **never** the `# Source:` locator, which is
    the reader's own conversation paragraph (R2873, R2898).
  - The session UUID and nonce arrive in the prompt (`Session: <S>`,
    `Nonce: <N>`) and the nonce also in the Task description; the agent
    passes them to every `next --session <S> <N>` / `close` call (R2890).
- Guard (`recall-agent-guard.sh`) carries:
  - The allowlist permitting `ark connections recall next | surface
    | recommend | close | finding`, `cat <file>` (single-arg, no
    chaining), and the **Read tool only when `file_path` matches
    `.../recall-curation/curation-*.md`** (R2897); Read of any other
    file, plus `Edit`, `Write`, network, and every other `ark` verb,
    denied as a class (R2859).
  - **Search verbs for directed hunts** (R2941): the read-only
    `ark search …` and `ark chunks …` the search crank handle uses
    are permitted (least-privilege — the crank handle's actual
    verbs); mutating verbs and `Read`/`Write`/`Edit`/network stay
    denied. A bloodhound task returns inline, so no new Read keyhole
    is needed.
  - The denial-stderr runway (fumble-onboarding pattern, R2771),
    pointing back at `next`.

## Does
- Boot once per generation when the **session's own assistant** (via
  `/recall`) invokes the Task tool with `subagent_type=ark-recall-agent`,
  the `nonce <N>` description, and the prompt carrying its `Session: <S>`
  and `Nonce: <N>` (R2890).
- Run the loop in one continuous foreground turn: `ark connections
  recall next --session <S> <N>` (blocks ≤~90s, returns inline) → on a
  curation doc, judge candidates against the injected conversation,
  `surface`/`recommend` the worthy ones, `close <F> --nonce <N>`; on a
  keepalive, loop; on an exit directive, stop so the spawning assistant
  respawns. Never backgrounds `next` or ends the turn mid-loop — that
  would emit per-cycle "completed"s the assistant misreads as an exit
  (R2860).
- Refuse every tool call outside the allowlist (the four verbs +
  `cat <file>`) via the PreToolUse guard (R2859); the denial stderr
  doubles as onboarding, pointing back at `next` (R2771).

## Out of scope
- Does **not** subscribe, poll, order, fetch, or context-check
  itself — `ark connections recall next` does all of that
  server-side (R2857, owned in `crc-RecallAgentBuilder.md`).
- Does **not** mint fires (the watcher does) or own respawn (the
  session's own assistant does, via `/recall`); it owns only its
  own self-exit when `next` returns the exit directive.
- Does **not** write RJ records (R2774, `crc-RecallAgentBuilder.md`).

## Collaborators
- RecallAgentBuilder (`crc-RecallAgentBuilder.md`): the `next` /
  `surface` / `recommend` / `close` calls route through it
  server-side; `next` carries the whole loop (R2857, R2858).
- Server (`crc-Server.md`): the guard runs as a child of the Claude
  Code session, not the ark server; `ark serve` only sees the
  resulting CLI calls.
- The session's own assistant (out-of-Go, via the `/recall` skill):
  reserves the nonce, spawns the secretary once per generation with the
  `nonce <N>` description and the `Session: <S>` / `Nonce: <N>` prompt,
  and respawns it on its clean context-limit exit (seam 3a — replaces
  the Luhmann orchestrator's recall-class supervision).

## Sequences
- seq-recall-agent.md
