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
  subagent's. If it holds, resolution means "the session that owns my
  transcript" rather than strictly "me" — indistinguishable for a main session,
  different for a subagent, and worth confirming before anything relies on the
  narrower reading.
- **`${CLAUDE_SESSION_ID}`** is a **skill-template substitution**, resolved by
  the harness when a skill is *loaded* and written into the conversation as
  literal text. It is not an environment variable at all — `printenv
  CLAUDE_SESSION_ID` exits non-zero.

ark skills historically carried a `sessionid=${CLAUDE_SESSION_ID}` header, and
the agent then typed that value into `--session` flags. In an ordinary session
the two mechanisms agree, which is why the difference went unnoticed.

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
