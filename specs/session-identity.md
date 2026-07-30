# Session identity — answering "which session am I?"

Several ark verbs need the Claude Code session UUID of the process calling them:
a consumer loop subscribes for its own session, an orchestrator claims the seat
as itself. Getting that answer wrong is unusually costly, because the wrong
answer does not look like an error. It looks like silence.

## The failure this exists to prevent

Two different mechanisms report a session id, and only one of them is live:

- **`$CLAUDE_CODE_SESSION_ID`** is a real environment variable, exported into an
  agent's shell by Claude Code. It is read per process, so it tracks a session
  even when the harness re-hosts it: in the observed fork, the variable reported
  the new id while the replayed skill text still carried the old one.

  **What it names, precisely.** For a main session it is that session. For a
  **subagent** the retired R2760 text claims it is the *parent's* id — which is
  what makes `<parent>/subagents/` resolvable and what the context-discovery
  registry (R3305) depends on. That claim is **unverified as of 2026-07-29**;
  the fork evidence above came from an orchestrator's own shell, not a
  subagent's. If it holds, the resolver's meaning is "the session that owns my
  transcript" rather than strictly "me" — indistinguishable for a main session,
  different for a subagent, and worth confirming before anything relies on the
  narrower reading.
- **`${CLAUDE_SESSION_ID}`** is a **skill-template substitution**, resolved by
  the harness when a skill is *loaded* and written into the conversation as
  literal text. It is not an environment variable at all — `printenv
  CLAUDE_SESSION_ID` exits non-zero.

ark skills have historically carried a `sessionid=${CLAUDE_SESSION_ID}` header,
and the agent then types that value into `--session` flags. In an ordinary
session the two mechanisms agree, which is why the difference went unnoticed.

**A template substitution is a snapshot, and a snapshot can go stale inside one
process.**
Claude Code may re-host a running session in its background daemon with
`--session-id <new> --fork-session --resume <old>.jsonl`. The fork replays the
earlier conversation — including the already-rendered `sessionid=<old>` — into a
session whose real id is `<new>`. The process environment correctly reports
`<new>`; the replayed text says `<old>`; nothing announces the change. The
snapshot was taken when the skill loaded, before the fork existed.

What that costs, concretely: the recall watcher derives a session from its JSONL
**filename**, so after a fork it fires for `<new>`. A consumer that subscribed as
`<old>` is watching a channel nobody publishes to. Both recall levels go dead —
ambient never arms because `<new>` has no result subscriber, and watermarks are
never recognised for the same reason. The consumer loop does not fail; it blocks
forever, which is indistinguishable from a quiet corpus. A stale seat claim is
worse still: the ownership check passes when a claim names the current owner, so
a re-hosted orchestrator can claim the seat under its pre-fork identity while the
seat points at a transcript that is no longer the working one.

## The rule

**Where a `--session` flag denotes the caller's own session, the live environment
is authoritative.**

- Flag omitted: resolve from `$CLAUDE_CODE_SESSION_ID`.
- Flag given and agreeing: use it; nothing to say.
- **Flag given and disagreeing: the environment wins, and the disagreement is
  reported** on stderr naming both values. The flag's value can only have come
  from text a model typed, and that text may have been replayed from before a
  fork. The environment comes from the live process, which is the only thing that
  knows its own identity. A process is a better witness to who it is than a
  document about it.
- Environment empty (a human or script outside an agent shell): use the flag.
  With neither, the verb fails as it always has.

Reporting the disagreement rather than silently correcting it matters as much as
the correction. Silent self-healing would fix each call and hide the condition,
leaving the operator with no way to learn that sessions are being re-hosted.

## Which flags this covers, and which it must not

**Own-session flags** — the caller is naming itself:

| verb | what `--session` means |
|---|---|
| `ark bloodhound listen` | the session subscribing for its own results |
| `ark connections recall listen` | same (older spelling) |
| `ark luhmann next` | the session claiming the orchestrator seat |

**Excluded: `ark connections recall next --session`.** That flag is a *routing
identity*, not a claim of selfhood. For a session-local secretary it happens to
equal the parent session, but for a pool secretary it is the composite
`<luhmann-session>-<nonce>` — deliberately not any process's own id. Resolving it
from the environment would break every pool secretary by pointing it at its
parent's tube instead of its own. The distinction is the point: a flag that
answers "who am I?" resolves from the environment, a flag that answers "which
tube?" does not.

## Skills stop capturing the id

The complement to the rule: skills should not carry a `sessionid=${CLAUDE_SESSION_ID}`
header at all.
Omitting `--session` and letting the CLI resolve it is both shorter to write and
impossible to get stale. A skill that captures the value is asking a model to
carry across a fork something the harness changed without telling it — and the
value is always available where the command actually runs.
