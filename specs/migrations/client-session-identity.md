# Migration: the client always sends its session identity

**Supersedes the flag half of R3309–R3311** (landed 2026-07-29, same day). Those
requirements fixed the *right* problem with a mechanism that turned out to be
half-honest; this replaces the mechanism and keeps the reasoning.

## Problem

`resolveOwnSession` made `$CLAUDE_CODE_SESSION_ID` authoritative for a
`--session` flag that names the caller's own session, preferring the environment
whenever the two disagreed. That fixed the fork-staleness bug. It also produced
three faults, the first of which Bill spotted immediately:

1. **`--session` became decorative.** With the environment set, no value of the
   flag can change the outcome — it only decides whether a warning prints. The
   CLI advertises an input it does not accept.
2. **Deliberate cross-session use became impossible**, and *silently*: an
   explicit `--session <other>` is overridden to the caller's own. That
   capability existed before and was removed without being named.
3. **The rule had to be applied verb by verb**, so `recall next` — whose
   `--session` is a routing tube, not an identity — needed an exclusion list and
   a structural test to stop anyone applying it uniformly and redirecting every
   pool secretary to its parent's tube.

The state-A design also missed a robustness win. The parent registry (R3305) is
populated only at `reserve-nonce` and lost on a server bounce, so a nonce
reserved before a restart reports *unmeasured* forever.

## State B

**Identity is not a flag. The client sends who it is, and the CLI knows which
subcommands care.**

1. **A command node declares whether it needs a client session.** A property of
   the node, so the tree itself carries the knowledge and there is no per-verb
   decision to get wrong.
2. **For a node that needs one, the CLI resolves it: `--force-session` if given,
   otherwise `$CLAUDE_CODE_SESSION_ID`.** No `--session` flag for identity on
   any of them.
3. **If neither resolves, the command errors**, naming `--force-session` as the
   remedy. State A returned empty and let each caller's own required-flag
   message fire — which under B would name a flag that no longer exists.
4. **The resolved value travels as its own wire field**, distinct from any verb's
   own `--session`. This is what ends the overload: one word stops meaning both
   "who am I" and "which queue" depending on which verb you are reading.
5. **`--force-session` is global** (like `--dir`), consulted only by nodes that
   declare a session need.
6. **`recall next` carries client identity too**, which lets the server
   re-register `nonce → parent` on every loop iteration. The registry becomes
   self-healing across a bounce instead of depending on catching `reserve-nonce`
   beforehand.

### Why the environment, restated so it survives the rewrite

`${CLAUDE_SESSION_ID}` in skill text is a **template substitution the harness
resolves at skill-load time** — a snapshot, and not an environment variable at
all. `$CLAUDE_CODE_SESSION_ID` is a real per-process variable. When Claude Code
re-hosts a session with `--session-id <new> --fork-session --resume <old>.jsonl`,
the replay carries the pre-fork snapshot while the variable reports the live id.
Verified 2026-07-29: the forked orchestrator's shell reported `df13aec4` (which
matched its transcript directory) while its replayed skill text still said
`5ae14585`.

A stale identity fails **silently**, which is why this is worth the churn: the
watcher keys sessions off the JSONL filename, so it publishes for the live id
while a consumer subscribed under the stale one blocks forever on a channel
nobody writes to — indistinguishable from a corpus with nothing to say.

## Decisions

- **DECIDED (Bill, 2026-07-29):** the client always sends its session id for
  subcommands that care; `--force-session ID` overrides; otherwise the value of
  `$CLAUDE_CODE_SESSION_ID`.
- **DECIDED (Bill, 2026-07-29):** an empty environment with no `--force-session`
  is an **error** for a subcommand that requires a session, not a fallthrough.
- **DECIDED (Bill, 2026-07-29):** `--force-session` is **global**; only nodes
  that need a session consult it.
- **DECIDED (Bill, 2026-07-29):** no skill will need `--force-session` — in an
  agent shell the variable is always set, and every skill verb wants its own
  session. Neither of us could construct a case. The one hypothetical shape:
  a **subagent needing to act as itself**, since its environment reportedly
  holds its *parent's* id — contingent on semantics still unverified
  (`specs/session-identity.md`), so noted rather than designed for.

## Open question

**Rename `recall next --session`?** With identity travelling separately, that
flag still says "session" while meaning "which tube" (the pool composite
`<luhmann-session>-<nonce>`). Renaming it (`--tube`, `--for-session`) would end
the last of the overload; the cost is a breaking change to
`.claude/agents/ark-recall-agent.md` and `/bloodhound`'s spawn instructions,
both edited 2026-07-29. Not blocking B.

## Reconciliation inventory — everything state A wrote, mostly today

The prose grep at completion must clear all of these. Listed because they were
written hours before this migration and every one currently describes A:

| surface | what it says now |
|---|---|
| `specs/session-identity.md` | the whole env-preference rule; **rewrite to B**, do not delete |
| `specs/cli-commands.md` | two synopses as `[--session SID]`, plus an own-session rule paragraph |
| `cmd/ark/bloodhound_cli.go`, `connections_cli.go` | flag help: "optional — resolved from `$CLAUDE_CODE_SESSION_ID`" |
| `design/crc-CLITree.md` | the `resolveOwnSession` bullet + R3312's complement note |
| `design/test-SessionIdentity.md` | five table cases written against the resolver's signature |
| `cmd/ark/session_identity_test.go` | those cases + the call-site guard → becomes a node-declaration check |
| `.claude/skills/bloodhound/SKILL.md` | the `printenv` step — **still needed for the secretary's tube**, no longer for identity; easy to over-delete |
| `.claude/agents/ark-recall-agent.md` | only if the open question above is answered yes |
| R3309–R3311 | retire the flag clauses; keep the env-authoritative reasoning |

**Completion test** (step 7): could an agent reading only `specs/` and `design/`
be led back to a decorative `--session` flag? If yes, a trap remains.
