# Messaging fixes

Cross-project messages are created by `ark-messenger`, a Haiku agent sealed by
a PreToolUse hook (`.claude/skills/ark/hermes-guard.sh`) that allows only
`~/.ark/ark` commands and Read under `requests/`. On 2026-10-22 Frictionless
reported that a messenger told it "Status: completed" for a response it never
wrote. Ark had seen the same failure the night before: a messenger reported a
path it had not checked, because the guard blocked its reads.

The guard is not what makes these reports false. What fails is everything
around it: the guard refuses without saying why, the CLI has no verb for one
job messengers are routinely given, and the writing verbs print nothing a
report could quote. So the messenger fills the silence with narrative. This
carve gathers the fixes.

Opened by Bill, 2026-10-22.

## Status

- [ ] **Item 1 — the guard explains each denial and gates on the guide.** **OPEN (#149.)**
      Hook and skill-file edits, no skill. See Item 1.
- [ ] **Item 2 — a body verb and a confirmation stanza.** **OPEN (#150.)** `/mini-spec`. See Item 2.
- [ ] **Item 3 — sweep the trajectory for messaging stragglers.** **OPEN (#151.)** No skill. See Item 3.
- [ ] **Item 4 — delegate messaging to Luhmann's pool when it is running.** **OPEN, not queued.**
      Design not settled. See Item 4.

## Evidence

**The guard keeps a fumble log.** Each call it allows or denies is appended,
with its full hook input, to `/tmp/allowed` or `/tmp/denied` (the literal
`/tmp`, so a reboot clears it). Measured 2026-10-22: 15 denials since boot, all
from Frictionless's messengers, of four kinds.

| denied call | count | what it shows |
|---|---|---|
| reading the scratchpad body file it was handed (`cat`, `ls`, Read) | 9 | It had been asked to set a response to completed **and** append a file's text. `ark message` has no verb that changes a body, so it hunted for another route. |
| `grep` / `find` in Frictionless's code | 3 | Checking claims against the code. Out of scope; denying it is right. |
| bare `ark --help` | 2 | The guard allows only the `~/.ark/ark` prefix. On Linux a bare `ark` is KDE's archiver. |
| `cat requests/…` | 2 | Read on `requests/` is allowed and `cat` is not. |

All fifteen got the same reply: *"BLOCKED. Run this command exactly:
`~/.ark/ark fetch --wrap knowledge ~/.ark/skills/ark/hermes-messaging.md`"*.
That reply is right for a first call, where the denial *is* the onboarding
(the fumble-onboarding pattern). After onboarding it still refuses correctly but
explains nothing. One of Frictionless's messengers had already run the fetch and
was told to run it again.

**Three incidents, one cause.** Each was read at the time as something else.

- *2026-06-24, microfts2.* A session concluded that the guard was "over-broad"
  because it blocked a correct response. Its own transcript shows what was
  blocked: a Write (denied by design, since bodies go through `--content-file`)
  and a bare `ark message new-response`. The guard was right twice and said
  neither thing.
- *2026-08-01, ark.* A messenger reported, confidently and coherently, that the
  server was down and bootstrap was deadlocked. The server had been up for 40
  minutes. It had read the guard's first-call denial as a fact about the world.
- *2026-10-22, Frictionless.* The blocked-success report above.

**Two older complaints, folded in here.** The roadmap carried two lines with
no detail: the messenger "has a lot of trouble getting messages" and "often
can't find responses". Neither records a symptom. Their likely causes are all
accounted for: the Hermes bootstrap fetched a symlinked path ark did not index
and got a 404 (fixed 2026-08-03), the guard's unexplained denials (Item 1), and
the missing body verb (Item 2). Closed against those; if the complaint
survives Items 1 and 2, it comes back here with a symptom attached.

**What was tried before, so it is not re-proposed.** The prefix allowlist for
`~/.ark/ark` (2026-04-22, after the guard blocked the CLI itself);
`--content-file` and `--issue-file`, so the messenger never retypes a payload,
and the structural denial of `serve`/`stop`/`restart` (2026-06-10); baby-food
output from the messaging tools (#128, 2026-09-17). None of them touched the
denial text, the missing verb, or the shape of the messenger's report.

## Item 1

The guard's denial names the reason and the alternative, one line per case:

- bare `ark` → use `~/.ark/ark`;
- `cat`/`head`/`ls` of a `requests/` file → use Read;
- reading a body file it was handed → it never needs to; `--content-file`
  reads it;
- `grep`/`find`/code reads → not the messenger's job; report back to the
  caller;
- anything else → the general refusal.

The onboarding instruction stays, for the first denial in a session only. The
hook input carries the session id, so the hook can tell.

**The guide gate.** Until a session has run the guide fetch (`~/.ark/ark fetch
--wrap knowledge ~/.ark/skills/ark/hermes-messaging.md`), the guard denies every
call, and the denial names that exact command. Ark-only and guide-first then
hold by construction: the first call the guard allows is always the guide, so
the messenger never has to remember either rule. The same session-id tracking
serves both. The agent's `SessionStart` prompt hook, which was meant to load the
guide, goes. Measured 2026-10-24: in both messenger runs that day the transcript
shows no hook output before the first tool call, and both first calls were
non-ark shell commands. A subagent's lifecycle hooks do not fire, so the hook
reads as a mechanism and is none.

Apart from the gate, changes to the guard's allow rules are out of scope: the
allow rules held in every incident above.

`hermes-messaging.md` should say the same things in the same words, so the
denial and the skill file agree.

## Item 2

Two changes to `ark message`, one pass:

- **A body verb.** Something like `ark message set-body FILE --content-file F`,
  with append as an option, so "move this response to completed with this
  text" is one command. `ARK-MESSAGING.md` already tells responders to append
  what changed as work progresses, and no verb can do it today. That sentence
  is either made true by this verb or rewritten.
- **A confirmation stanza.** `new-request`, `new-response`, `set-tags` and the
  body verb end with a fixed baby-food block (the path written, the status
  set, what changed). Measured 2026-10-22: `new-response` prints nothing at
  all. The messenger's instructions then say to return that stanza verbatim
  as its report. A run that wrote nothing has no stanza to quote, so it cannot
  claim a status it never set.

`specs/cli-commands.md` and `specs/messaging.md` follow.

## Item 3

Messaging problems have been noted in several places over the months and never
gathered. Before Items 1 and 2 are scoped as final, sweep the queue, the
current-step notes, the roadmap and the planning scratch for messaging-related
loose ends. Each one becomes a part here or is closed with a reason.
DECIDED (Bill, 2026-10-22): once a straggler's content is fully in this carve,
it is removed from the trajectory file it came from, leaving a one-line note
that points here, so the problem lives in one place and cannot fall through
the cracks. Known starting points: a queue heading about delegating messaging
to Luhmann when it is running, and a roadmap line saying the messenger "has a
lot of trouble getting messages" with no detail. Done when every hit has been
folded in or closed and its source replaced by the note.

**Progress, 2026-10-26.** Swept the queue, the current-step notes, the roadmap,
and the problem-shaped planning scratch. Folded in: the Luhmann heading (Item 4)
and the two vague roadmap lines (Evidence). Closed, each checked against today's
code:

- the guard blocking `~/.ark/ark` itself: the prefix is allowlisted
  (`hermes-guard.sh`);
- `message inbox --project` not showing outgoing messages: it does, measured;
- the inbox never reading `FileTagValues`: `readInboxFields` calls it;
- `message inbox` swallowing the server's error: it goes through
  `proxyOrLocal` now (R3398);
- the messenger stuffing bodies into invented tags: stdin body support landed;
- checking open messages at session start: the `/ark` bootstrap's messenger
  sweep does it.

Still to decide (Bill): two research ideas on the roadmap (a reality-check
review of messaging, and a persona-versus-rules experiment for Hermes), a stray
self-addressed request file in the planning scratch, and whether the
bootstrap-fix note in the current-step notes becomes a pointer. The early
messaging design notes were triaged by name and date, not read.

## Item 4

A known starting point from the queue (undated, before 2026-10-22). Make an
`ark messenger` command for high-level messaging, and point the `/ark` skill's
messaging section at it. When Luhmann is running, the command delegates to its
agent pool; when it is not, it launches an agent and delegates. The recall
agent (`ark-recall-agent.md`) learns messaging too.

Unsettled, so recorded rather than designed: how this sits with
the rule that ark never proactively starts Claude
([managed-pty.md](../specs/managed-pty.md)) — "launch an agent" must be an explicit
user action, never a fallback the CLI takes on its own — and whether Items 1
and 2 shrink the need. A messenger that cannot misreport (Item 2's stanza) may
make delegation a cost question rather than a correctness one.
