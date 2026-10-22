# Notify

Ark notices things nobody is watching for: a scheduled check that failed, a
format that drifted, a pool that paused itself. Today there is nowhere for that
to go. A scheduled event publishes to nobody (see
[scheduling.md](scheduling.md)), and a problem ark detects in the background
waits until someone happens to run the right command. This carve gives ark a way
to raise an alert that reaches the person through whichever agent they are
talking to, says what their options are, and stays quiet once it has been heard.

Opened by Bill, 2026-10-22.

## Status

- [ ] **Item 1 — the alert store.** **OPEN (not queued.)** `/mini-spec`. See
      Item 1.
- [ ] **Item 2 — `ark notify`.** **OPEN (not queued.)** `/mini-spec`. See Item 2.
- [ ] **Item 3 — reaching the person, once per session.** **OPEN (not queued.)**
      The delivery channel is an open fork. See Item 3.
- [ ] **Item 4 — Luhmann relays.** **OPEN (not queued.)** Luhmann skill. See
      Item 4.
- [ ] **Item 5 — the UI mirror.** **OPEN (not queued.)** `/ui-fast`. See Item 5.
- [ ] **Item 6 — the first tenant: a transcript format sentry.** **OPEN (not
      queued.)** Needs Items 1 and 2, and the transcript reader. See Item 6.

## The design as decided

DECIDED (Bill, 2026-10-22), the shape:

- **Alerts live in `~/.ark/alerts.md`, managed with ark tags.** Text on disk,
  readable and greppable, versioned with the rest of `~/.ark`.
- **The checks are deterministic and token-free.** Ark itself subscribes to a
  scheduled tag, runs the check, and writes the alert. No language model is
  involved in deciding whether something is wrong.
- **Luhmann is the majordomo.** It hears new alerts and tells the person; it
  does not perform the checks.
- **The person's agent presents the options**: silence it, defer it for a while,
  or address it. Once it has been addressed, the agent offers to silence it.
- **Each alert reaches a given session once.**
- **The UI mirrors the alerts.**

## Item 1

`~/.ark/alerts.md`, one tagged block per alert, written through ark's tag verbs
(and so through the write actor) rather than by hand-editing.

**One alert per condition, not per occurrence.** A check that fails every night
updates one alert's last-seen date and count; it does not add thirty. So each
alert has a stable key naming the condition (for example
`transcript-format/narration`), plus a state (open, deferred, addressed,
silenced), first and last seen, a count, where it came from, a one-line summary,
and a deferred-until date when deferred. A silenced alert that fires again is a
question for the design: stay silent, or reopen. Not settled.

Why one per condition: a channel that repeats itself is a channel people learn
to skip, and then it reports nothing (the cry-wolf pattern).

## Item 2

`ark notify`, baby food by default:

- `list`: open alerts, and deferred ones whose date has passed.
- `raise KEY --summary ...`: create or refresh an alert (used by ark's own
  checks, and available to a person or a script).
- `silence KEY`, `defer KEY --until ...`, `address KEY`.

`ark notify` belongs in `specs/cli-commands.md` like every other verb.

## Item 3

The alert has to reach **the person's agent**, once per session, and never a
subagent.

**What ark can know about the caller (measured).** Ark identifies the calling
session from `$CLAUDE_CODE_SESSION_ID`. A subagent reports its **parent's**
session id, and nothing a subagent can read tells it apart from its parent
(`specs/subagent-environment.md`). So "once per session" keyed on the caller's
session would be consumed by whichever ark call in that session happened first,
and secretaries call `ark connections recall next` about every 90 seconds. A
secretary would take every alert before the person's agent ever saw one.

**The open fork: which channel carries the alert.**

- *A tail on ark's baby-food output.* Every ark response would carry the open
  alerts. Reaches the agent wherever it is, but it cannot tell a subagent from
  its parent, and telling weak models to ignore alerts is the kind of
  instruction they drop.
- *A `UserPromptSubmit` hook.* Fires only in the person's own session, once per
  prompt they type, and injects the open alerts with their options. Subagents
  never see it, by construction rather than by instruction, and "once per
  session" then means once for the person. Needs the hook installed where ark
  sets up a project.

Recommended: the hook, keyed per session, with a one-line fallback in baby-food
output ("2 open alerts — `ark notify list`") that is safe for anyone to see.
Bill's call.

**The label (Bill, 2026-10-22).** Whatever the channel, an alert set beside a
command's own output opens with `**btw:**`, so the reading agent knows at once
that it is not the answer to its command. The baby-food fallback line is a btw
too. This is the Marginalia pattern: a note set beside a page's own text,
from something other than what the page is about, short, delivered once, and
meant for the principal reader.

## Item 4

When Luhmann is seated, it hears a new alert (pubsub on the alert tag) and tells
the person in its own session, with the same options. It only relays: the check
and the alert are ark's. When Luhmann is not seated, Item 3 still delivers.

## Item 5

The Frictionless app shows open alerts and their options, and acting on one
there does what `ark notify` does.

## Item 6

The first tenant, and the reason the carve exists today. Ark's transcript
reader ([specs/transcript-reader.md](../specs/transcript-reader.md)) depends on
Claude Code's undocumented format. Two kinds of drift:

- **Additions** — a new record type or block kind. The reader counts what it
  could not place while it reads, continuously and for free.
- **Disappearances** — counting cannot see these. If Anthropic renames the
  `narration` label in a thinking block's signature, narration blocks do not
  fail anything; they quietly read as thinking. A scheduled check runs the reader
  over recent transcripts and compares the share of narration among thinking
  blocks with its usual level (about 20% on 2026-10-22). If it falls to zero
  while signed thinking blocks keep appearing, it raises
  `transcript-format/narration`.

Ark subscribes to the check's own scheduled tag, so this is also the first real
use of ark's scheduler, whose fires today publish to nobody.

## Overlaps

- **[scheduling.md](scheduling.md)** — Item 6 is the first thing to listen to a
  scheduled fire, which is what that carve's Item 5 ("what it takes for Bill to
  use it") is asking about.
- **[observability.md](observability.md)** — the state document could list open
  alerts beside seats and hunts.
- **[bloodhound-fix.md](bloodhound-fix.md)** — Item 8 there builds the transcript
  reader that Item 6 watches.
