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
  **subagent it is the parent's id** — verified, not assumed. That is what makes
  `<parent>/subagents/` resolvable and what the context-discovery registry
  (R3305) is built on.

  Two observations, deliberately on **different harness versions**, because the
  behavior is undocumented and a single reading only tells you about one build:

  | when | harness | subagent reported | parent (known independently) |
  |---|---|---|---|
  | 2026-05-26 | 2.1.150 | `c342d265-…b2d62a2d` | its transcript sat under `c342d265-…b2d62a2d/subagents/` |
  | 2026-07-30 | 2.1.220 | `39768aaf-…413f4c3ef8a5` | the spawning session's own id |

  The first came from a subagent that happened to dump its whole environment; the
  second from a subagent spawned to answer this question. In both the parent was
  known by a route that did not go through the value being tested. Seventy point
  releases apart and unchanged, which is evidence of *stability* rather than just
  of the value.

  So resolution means **"the session that owns my transcript"** rather than
  strictly "me" — indistinguishable for a main session, and for a subagent it is
  the only useful reading anyway, because **a subagent has no session UUID.** Its
  own identity is an `agentId` (`a5d1fa0de1d60e6a9`, `a11676fc0bfbca033`), a
  different namespace that no session-scoped ark verb accepts; the transcript
  record's `sessionId` field likewise carries the parent's. A subagent therefore
  cannot "act as itself" in ark's terms, which is why nothing needs to.

  The 2026-07-30 probe also confirmed the distinction above from the other side:
  `printenv CLAUDE_SESSION_ID` in a live agent shell **exits non-zero**. The
  template substitution really is not an environment variable.

  **And an agent cannot reach it at runtime, which fails quietly.** Measured the
  same day: `echo ${CLAUDE_SESSION_ID}` from a Bash tool call prints an **empty
  line**. Single-quoting it returns the token verbatim, which proves the harness
  does not substitute it in tool input at all — the substitution happens only
  when a *skill file* is loaded, never in text an agent writes while running. So
  an agent reasonably reaching for its own id this way gets `""` and no
  complaint, and that empty string then flows into whatever it was building. This
  is the reason to prefer `printenv CLAUDE_CODE_SESSION_ID`, beyond it being the
  correct variable: it exits non-zero when it cannot answer, where `${...}`
  answers wrongly and silently. Same failure shape as the stale id this spec
  exists to prevent, one layer further out.

  **This is a dependency we do not control, and R3307 is what watches it.** If a
  future harness ever reported the child's own id here, `findSubagentJSONL` would
  resolve nothing — and since R3307 marks an unresolvable transcript
  `unmeasured` rather than writing zeros, the change surfaces as a rising
  unmeasured count in `ark monitor status` instead of as silently plausible
  numbers. Worth knowing where the canary is, because no ark test can assert what
  the harness exports.
- **`${CLAUDE_SESSION_ID}`** is a **skill-template substitution**, resolved by
  the harness when a skill is *loaded* and written into the conversation as
  literal text. It is not an environment variable at all — `printenv
  CLAUDE_SESSION_ID` exits non-zero.

ark skills historically carried a `sessionid=${CLAUDE_SESSION_ID}` header, and
the agent then typed that value into `--session` flags. In an ordinary session
the two mechanisms agree, which is why the difference went unnoticed.

**They diverge temporally, not positionally** — worth stating because the other
possibility is the one a reader would guess at. Measured 2026-07-30 with a
one-line probe skill loaded inside a subagent: the substitution and `printenv`
both returned the **parent's** id, in agreement, with the subagent's own
`agentId` appearing in neither. So the substitution is not child-specific and
being a subagent does not separate the two mechanisms. Only **re-hosting** does,
because one value is a snapshot taken at load and the other is read live.

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

## Why ark mints its own subagent id: the nonce

A subagent has **no self-knowledge of identity**, which is what forces ark to
supply one. The raw observations and how to re-derive them are in
[subagent-environment.md](subagent-environment.md); in brief, measured 2026-07-30:

- Its `$CLAUDE_CODE_SESSION_ID` is its **parent's** session, so it cannot
  distinguish itself from any sibling under the same parent.
- The harness *does* mint a per-subagent identifier — the `agentId` in
  `<parent>/subagents/agent-<agentId>.jsonl` — but it is **not exposed to the
  subagent itself**. A probe dumping its whole environment (61 variables) did not
  contain its own `agentId` anywhere; the only agent-shaped variable is
  `AI_AGENT`, a harness version string.

So the reserved nonce (R2755) is ark's **hand-rolled subagent id**, and it must
be *handed in* at spawn precisely because it cannot be *discovered*. That is why
the spawner passes it in the prompt rather than the secretary resolving it, why
`reserve-nonce` is called by the parent rather than the child (R3305 — the
reserving shell is the only one that knows the parent), and why a pool
secretary's tube is the composite `<luhmann-session>-<nonce>` (R3032): the
session half alone would collide across every secretary of one Luhmann, and the
nonce is what individuates them.

It is also a **join key between two namespaces neither side can traverse alone.**
ark knows nonces; the harness knows `agentId`s; and `.meta.json` is the single
place they are co-located, because the parent wrote the `description` carrying
ark's nonce while the harness wrote the filename carrying its own id. That is why
R2759's `nonce <N>` substring scan over `<parent>/subagents/*.meta.json` is the
mechanism for finding a subagent's transcript — not a workaround, but the only
seam where the two identifier spaces touch. And it is why the `nonce <N>`
substring in the Task description is load-bearing rather than cosmetic: remove it
and the two namespaces have no common key at all.

## The rule

**Identity is not a flag. The client sends who it is, and the command tree knows
which subcommands care.**

- **A command node declares whether it acts as the calling session.** The need is
  a property of the node, so the tree carries the knowledge and there is no
  per-verb decision for a later change to get wrong.
- **A declaring node resolves its identity from `--force-session` when given,
  otherwise from `$CLAUDE_CODE_SESSION_ID`.** No node takes a `--session` flag
  for identity. The environment beats anything typed because a typed value can
  only have arrived as text, and that text may have been replayed from before a
  fork; the variable is read where the command actually runs. A process is a
  better witness to who it is than a document about it.
- **When neither resolves, the command errors and names `--force-session`** as
  the remedy, rather than passing an empty value into the body.
- **`--force-session` is global**, like `--dir`, and is consulted only by nodes
  that declare a session need.
- **The resolved value travels to the server as its own wire field,
  `client_session`**, distinct from any verb's own session-shaped parameter.

### Which verbs declare the need

| verb | why it is the caller |
|---|---|
| `ark bloodhound listen` | the session subscribing for its own results |
| `ark connections recall listen` | same (older spelling) |
| `ark luhmann next` | the session claiming the orchestrator seat |
| `ark luhmann events` | routing is a privilege of the seat the caller owns |
| `ark connections recall next` | so the server can re-register `nonce → parent` |

### The one flag that is not an identity

**`ark connections recall next --tube`.** Its value is a *routing address*, not a
claim of selfhood: for a session-local secretary it happens to equal the parent
session, but for a pool secretary it is the composite
`<luhmann-session>-<nonce>` — deliberately not any process's own id. Resolving it
from the environment would break every pool secretary by pointing it at its
parent's tube instead of its own.

It used to be spelled `--session`, which is what made that hazard reachable: one
word meant both "who am I" and "which queue" depending on the verb being read,
so a rule applied uniformly would silently redirect every pool secretary. The
flag now says what it holds, and its wire parameter is `tube=` for the same
reason — a parameter named `session` carrying a tube is the same overload one
layer down.

## Why `--force-session` rather than an authoritative environment

An earlier design kept the `--session` flag and simply preferred the environment
whenever the two disagreed. It fixed the fork bug and introduced three faults:

1. **The flag became decorative.** With the variable set, no value of the flag
   could change the outcome — it only decided whether a warning printed. The CLI
   advertised an input it did not accept.
2. **Deliberate cross-session use became impossible, and silently.** An explicit
   `--session <other>` was overridden to the caller's own. That capability
   existed before and was removed without being named.
3. **The rule had to be applied verb by verb**, in each command body, so
   coverage depended on remembering every call site. `ark luhmann events` was
   missed exactly that way.

`--force-session` is the honest form of the same intent: an override that
actually overrides, which also restores the cross-session use. A disagreement
between it and the environment is not reported, because the flag's whole purpose
is to differ — warning on it would put noise in the one case it exists for.

**Who actually needs it.** A human or script driving a session-scoped verb from
outside an agent shell, where the variable is unset; and anyone deliberately
naming another session. Not a subagent acting as itself: that shape was left open
as a possibility while the subagent semantics were unverified, and the answer
above closes it — a subagent has no session id to force. No skill needs the flag,
because in an agent shell the variable is always set and every skill verb wants
its own session.

## Skills do not capture the id

The complement to the rule: skills should not carry a
`sessionid=${CLAUDE_SESSION_ID}` header at all. Omitting identity and letting the
CLI resolve it is both shorter to write and impossible to get stale. A skill that
captures the value is asking a model to carry across a fork something the harness
changed without telling it — and the value is always available where the command
actually runs.

Where a spawner genuinely needs a value the CLI will not resolve for it — a
secretary's `--tube`, which is a routing address rather than an identity — the
skill directs it to read the id live with `printenv CLAUDE_CODE_SESSION_ID` at
spawn time rather than from earlier text.

`/ark` and `/diag` are deliberately out of scope: their captured id only suffixes
a message filename or prints a diagnostic line, neither of which subscribes to
anything or claims a seat, so a stale value there misnames a file rather than
silently disabling recall. They keep the header pending a decision (gap O181).
