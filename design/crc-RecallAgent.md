# RecallAgent
**Requirements:** R2769, R2770, R2771, R2773

Non-Go artifact set that defines the one-shot Haiku subagent
spawned per ambient-recall fire. Three files cooperate:

- `.claude/agents/ark-recall-agent.md` — the Claude Code agent
  definition (frontmatter + persona briefing).
- A PreToolUse guard shell script — enforces the hermetic-seal
  tool allowlist and carries the fumble-onboarding stderr
  message when denying a forbidden tool call.
- `~/.ark/skills/ark-recall.md` — the skill reference the
  agent loads on first invocation (the runway target).

The runtime behavior (Bash invocations of the `ark connections
recall` verbs and `ark fetch`) is owned by
`crc-RecallAgentBuilder.md`; this card pins the
configuration-file surface that selects the agent type, sets
the model, scopes its allowlist, and seeds its persona.

## Knows
- Agent definition file at `.claude/agents/ark-recall-agent.md`
  carries:
  - `model: haiku-4.5` (R2769).
  - `memory: local` so MEMORY.md does not inherit (R2769).
  - The persona briefing: curator, not synthesizer; default to
    silence; one fire = one curation doc; rejection is
    user-decision relayed by the assistant.
- Guard script at `.claude/hooks/ark-recall-agent-guard.sh` (or
  equivalent location chosen at implementation time) carries:
  - The allowlist regexes for the four legal Bash commands
    (`ark fetch tmp://ARK-RECALL/curation-*`, `ark connections
    recall surface ...`, `... recommend ...`, `... close ...`)
    (R2770).
  - The Read-denial stderr template (R2771) — when the agent
    attempts the `Read` tool, the script exits non-zero with
    stderr that names the canonical `ark fetch tmp://ARK-
    RECALL/curation-<session>-<fire>` template the agent
    should use instead.
- Skill file at `~/.ark/skills/ark-recall.md` carries
  (R2773):
  - The curation-doc shape reference (how `# Source Chunk:` /
    `## Candidate:` blocks lay out).
  - The result-doc shape reference (how `## Surface:` /
    `## Recommend:` items look).
  - The four CLI verbs with their argument shapes.
  - The fumble-onboarding cue: "if you see a denial telling
    you to use `ark fetch`, use it."

## Does
- Boot a one-shot subagent per fire when the assistant invokes
  the Task tool with `subagent_type=ark-recall-agent` and the
  `(fire, nonce)` description (R2769).
- Refuse every tool call outside the allowlist via the
  PreToolUse guard (R2770).
- On the agent's first `Read tmp://ARK-RECALL/...` attempt,
  emit the canonical `ark fetch ...` template in stderr so the
  denial doubles as onboarding (R2771).
- Load the skill file on first reference (R2773).

## Out of scope
- Does **not** subscribe to pubsub. The agent is invoked
  one-shot by the assistant; it has no listener role.
- Does **not** write RJ records. (R2774, owned in
  `crc-RecallAgentBuilder.md`.)
- Does **not** persist any state between invocations. Memory
  scope is local; the fire/nonce pair plus the curation doc
  contents are the only context.

## Collaborators
- RecallAgentBuilder (crc-RecallAgentBuilder.md): the agent's
  `surface` / `recommend` / `close` calls route through this
  component server-side.
- Server (crc-Server.md): the guard script runs as a child of
  the Claude Code session, not the ark server; ark serve only
  sees the resulting CLI calls.
- Assistant (out-of-Go, see approved gap A): invokes the
  agent via the Task tool with the `(fire, nonce)`
  description and the curation doc path.

## Sequences
- seq-recall-agent.md
