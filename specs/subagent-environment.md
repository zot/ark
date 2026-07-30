# Subagent environment — observed harness facts ark depends on

**This spec owns no requirements.** It is a dated record of behavior belonging to
Claude Code, not to ark, written so nobody has to re-derive it by digging through
transcripts. ark's *design consequences* live in
[session-identity.md](session-identity.md); the raw observations live here.

Every line below names how it was measured and on which harness version, because
this is a dependency we do not control and an undated claim about it rots
silently. Where a fact was checked on two versions, both are given — agreement
across versions is evidence of stability, which a single reading cannot be.

## Identity: what a subagent can and cannot know about itself

**A subagent has no self-knowledge of identity.** This is the fact the rest
follows from.

| fact | how it was measured |
|---|---|
| A subagent's `$CLAUDE_CODE_SESSION_ID` is its **parent's** session id, not its own | 2.1.150 (2026-05-26): a subagent that dumped its whole environment reported `c342d265-…`, and its transcript sat under `c342d265-…/subagents/`. 2.1.220 (2026-07-30): a spawned probe reported the spawning session's id. In both the parent was known by a route independent of the value tested. |
| A subagent has **no session UUID of its own** | Neither probe's environment contained any UUID other than the parent's. |
| The harness's per-subagent identifier is an **`agentId`** — 17 hex characters, *not* a UUID (`a634bacba8dca4272`) | Returned by the Task tool to the parent; also the transcript filename. |
| The `agentId` is **not visible to the subagent itself** | 2.1.220: a probe ran `env > /tmp/subagent-env` (61 variables); its own `agentId` appeared **0 times**. The only agent-shaped variable is `AI_AGENT`, a version string. |
| A subagent therefore cannot be individuated by anything it can read | Follows from the three above. This is why ark mints its own id — see session-identity.md §Why ark mints its own subagent id. |

### Where a subagent's transcript lives

```
~/.claude/projects/<cwd-with-slashes-as-dashes>/<parent-session-uuid>/subagents/
    agent-<agentId>.jsonl        the transcript
    agent-<agentId>.meta.json    {"agentType": "...", "description": "..."}
```

The `.meta.json` carries **no session id** — only the agent type and the
description string the *parent* supplied at spawn. Verified 2026-07-30 by reading
one directly.

Records inside the transcript carry both identifiers: `sessionId` is the
**parent's** session, `agentId` is the subagent's own. So the transcript knows
what the subagent cannot.

## `${CLAUDE_SESSION_ID}` is not an environment variable

Two mechanisms are easy to confuse, and confusing them is what produced a whole
class of silent bug (see session-identity.md).

| fact | how it was measured |
|---|---|
| `CLAUDE_SESSION_ID` is not set in any shell | `printenv CLAUDE_SESSION_ID` exits **1**, in both a main session and a subagent (2.1.220). |
| `${CLAUDE_SESSION_ID}` is substituted **only when a skill file is loaded** | A one-line probe skill (`Your id is ${CLAUDE_SESSION_ID}`) rendered a real UUID when loaded. |
| The harness does **not** substitute it in tool input | `echo '${CLAUDE_SESSION_ID}'` (single-quoted, so bash cannot expand it) returned the token **verbatim**. |
| Reaching for it at runtime fails **silently** | `echo ${CLAUDE_SESSION_ID}` prints an **empty line** — no error. The empty string then flows into whatever was being built. Prefer `printenv CLAUDE_CODE_SESSION_ID`, which exits non-zero when it cannot answer. |
| Inside a subagent the skill substitution gives the **parent's** id too, agreeing with the environment | 2.1.220: a subagent loaded the probe skill and ran `printenv`; both returned the parent's id, and its own `agentId` appeared in neither. |
| So the two mechanisms diverge **temporally, not positionally** | Being a subagent does not separate them. Only **re-hosting** does (`--session-id <new> --fork-session --resume <old>.jsonl`), because the skill value is a snapshot taken at load while the variable is read live. |

## Other environment facts (2.1.220, 2026-07-30)

From the 61-variable dump. Useful mainly for knowing what is *not* there.

- `AI_AGENT=claude-code_2-1-220_agent`, `CLAUDECODE=1`,
  `CLAUDE_CODE_ENTRYPOINT=cli`
- `CLAUDE_CODE_EXECPATH=~/.local/share/claude/versions/<version>` — the cheapest
  way to read the running harness version
- `CLAUDE_PID`, `CLAUDE_CODE_SSE_PORT` — per-process
- `CLAUDE_CODE_BRIDGE_SESSION_ID` — present when the session runs under a bridge
- `CLAUDE_CODE_CHILD_SESSION=1` — **not a usable subagent discriminator.** It was
  set in the subagent *and* in the bridged main session that spawned it, and it
  does not appear at all in the 2.1.150 dump, so it is newer and its meaning is
  unconfirmed. Recorded because it looks like a discriminator and is not.

## How to re-derive any of this

Spawn a cheap subagent and have it run the command, then compare against the
parent's own id (known independently, since the spawner *is* the parent):

```
printenv CLAUDE_CODE_SESSION_ID     # its parent's session id
printenv CLAUDE_SESSION_ID          # exits 1
env > /tmp/subagent-env             # the full picture
```

For the skill mechanism, have it load a one-line skill whose body is
`Your id is ${CLAUDE_SESSION_ID}` and report what the text says.

Existing transcripts can also answer without spending anything: grep
`~/.claude/projects/*/*/subagents/agent-*.jsonl` for `CLAUDE_CODE_SESSION_ID`.
Any subagent that ever ran `env` recorded the answer, and the containing
directory names the parent independently. That is how the 2.1.150 data point was
recovered rather than re-run.

## What ark depends on, and what would notice a change

R3305 and R3306 rest on the parent-id semantics: the server resolves a subagent's
transcript from the `nonce → parent session` registry and scans
`<parent>/subagents/`. No ark test can assert what the harness exports, so the
guard is indirect. **R3307 is the canary** — an unresolvable transcript is marked
`unmeasured` rather than written as zeros, so a harness that began reporting the
child's own id here would show up as a rising unmeasured count in
`ark monitor status` instead of as plausible-looking numbers.
