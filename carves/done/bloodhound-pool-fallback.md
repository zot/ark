# Bloodhound: two doors to the same pool

**Problem.** Ark has two ways to run a directed hunt — an in-conversation
`<BLOODHOUND>` watermark and the `ark bloodhound search` CLI — and only one of
them could reach Luhmann's pool of warm secretaries. A session with Luhmann
awake and secretaries idle a few feet away still had to spawn its own Haiku
that duplicated them.

Opened 2026-07-27. Promoted from `.scratch/` 2026-07-28 on spawning its second
item.

| Part | Status |
|---|---|
| ~~#80 — split recognition from service; pool fallback; not-served report~~ | **Landed** 2026-07-28, git `4c69e33` |
| ~~#84 — a reaped watermark hunt is still silent~~ | **Landed** 2026-07-28 — R3293 |
| ~~#83 — `/bloodhound` skill still calls the secretary spawn a precondition~~ | **Landed** 2026-07-28 |

**Closed 2026-07-28.** All three parts landed; the ordering constraint below was
honoured (#84 before #83), so the silence was fixed before the path was
advertised.

**Where the decisions went.** Behavior is canonical in `specs/bloodhound.md`
("Serving a watermark", "Unservable — report, never drop" and its
"Accepted, then stranded") and `specs/bloodhound-cli.md` ("Tag proposals are
origin-dependent"), as **R3279–R3283 and R3293**. The `/bloodhound` skill carries
the operator-facing half. This document is the reasoning trail and the split —
read the specs for what the system does.

---

## What was wrong

`<BLOODHOUND>` recognition required a secretary subscribed to **its own
session's** work tube. Three lines held it:

- `bloodhoundEnabled()` = `secretaryPresent(sessionID)` **AND** an
  `ark-bloodhound-result` subscriber.
- `secretaryPresent()` = `SubscriberCount("ark-secretary-work", sessionID) > 0`
  — session-scoped.
- `OnAppend` returned early when the secretary was absent, discarding the
  watermark *before* dispatch was ever considered.

So the two doors were separate, and only one reached the pool:

| door | reached the pool? | who curates |
|---|---|---|
| `<BLOODHOUND>` watermark | **no** — session-local secretary only | session secretary |
| `ark bloodhound search` | yes (S2 Fixer → S1 Luhmann seat) | secretary **+ Luhmann** |
| `ark bloodhound search --raw` | yes | secretary only; **caller curates** |

**The second defect was worse than the first.** Because the watermark was
dropped at the gate with no signal, an unservable hunt was indistinguishable
from a hunt that found nothing. An assistant blocked on `listen` waits forever
on a request nobody holds. That is the `try-the-handle` failure shape: an
apparently-locked door with nothing reporting that it is locked.

## How it was found

Bill launched Luhmann and said *"we don't need the secretary — hopefully ark
routes your request to luhmann."* It did not. Two watermarks were emitted after
the session secretary was killed; neither produced a finding, and
`ark luhmann status` stayed at `0 pool secretary(ies)` throughout. Reading the
gate explained why, and the reasonable expectation behind the remark turned out
to be a better design than what was implemented.

## Decisions

- **DECIDED (Bill, 2026-07-27):** the watermark path should reach the pool. The
  per-session secretary becomes an optimization, not a precondition.
- **DECIDED (Daneel, 2026-07-27, ratified by the spec):** separate *recognition*
  from *service*. Recognition gates on the bloodhound-result subscription alone;
  who serves a recognized hunt is a later, independent choice. Conflating them
  is what made a staffed pool worthless to a watermark **and** made silence the
  failure mode. → R3279/R3280.
- **DECIDED:** a pool-served watermark is always **raw**, returning to the
  emitting session rather than through Luhmann. Not a shortcut — a watermark is
  by construction emitted by an assistant holding the live context that provoked
  it, so it *is* the curate-in-your-own-context case, and Luhmann's
  separate-context judgment would be both the wrong judge and a serial step on
  the latency-sensitive path. → R3281.
- **DECIDED:** an unservable hunt **reports**; silence is the one unacceptable
  outcome. → R3282.
- **DECIDED (Bill, 2026-07-28):** tag proposals are origin-dependent —
  session-delegated hunts propose, CLI-delegated ones stay findings-only,
  because a script has no assistant to winnow proposals. → R3283.

## What the work turned up

**Proposals on the pool path were broken, not merely unwanted.**
`RecommendItem` had no branch for a request-doc cookie, so a proposal fell
through to `openResult`, which has no in-flight curation doc for a bare request
id and fails. The generalizable shape, now recorded in `specs/bloodhound-cli.md`:
**an accumulator and its flush must be keyed the same way**, or items land where
nothing drains them.

**A claim was corrected by testing it.** That defect was first written up — in
the spec, the requirement, and a code comment — as a *silent* drop. Reverting
the fix to confirm the new test actually went red produced the real error
message and corrected the claim before it shipped in a public spec. Cost: one
command. This is the `sleeping-sentry` discipline paying for itself immediately.

**Pool growth is operator-driven, not demand-driven.** Observed 2026-07-27: the
pool sat at `0 pool secretary(ies)` before *and* after a CLI hunt that
nonetheless returned findings, and only reached 2 once Bill grew it through
Luhmann's seat. A fallback that assumes an idle pool will grow itself would find
none — which is why `poolCanServe()` checks for room to grow rather than
assuming it.

## How the open parts closed

**Order constraint, honoured: #84 before #83.** The strand defect was unreachable
in practice while the `/bloodhound` skill still instructed a spawn, because
nobody took the pool path. #83 is exactly what makes #84 reachable, so landing
the documentation first would have converted a latent bug into a live one. Both
landed the same day, in that order.

### #84 — a reaped watermark hunt was still silent → **R3293**

R3282 covered "cannot be served at dispatch", not "accepted, then stranded".
`sweepRequests` reaped a pending request past `request_ttl_seconds` and notified
nobody — correct for a CLI hunt whose client hit `--timeout` and exited, wrong
for a **watermark** hunt whose emitting session is alive and blocked on `listen`.
So the silence this carve exists to eliminate had been *moved* rather than
removed: from the gate to the reaper, and harder to spot there because the hunt
appeared to be accepted.

The fix keys on **who is still waiting**. A watermark-origin reap emits the same
not-served finding through a shared writer (`emitNotServedReason`), with a reason
naming the strand rather than a dispatch-time condition — all of those held when
the hunt was accepted, so naming one would misdescribe it. A CLI reap still
reports to nobody, because nobody is listening.

- **DECIDED (Bill, 2026-07-28):** `poolCanServe()` **keeps** counting *room to
  grow* as servable. The tightening was tempting — refusing unless a secretary is
  actually free makes the strand impossible — but this carve's own observation
  refutes it: the pool read 0 secretaries before *and* after a hunt that
  nonetheless returned findings. Fail-fast would refuse hunts that demonstrably
  work, trading a rare 3-minute wait for a common outright refusal.
- **DECIDED (Bill, 2026-07-28):** a stranded **watermark** is reaped on a
  deadline shorter than the CLI TTL (`bloodhoundStrandTTL`, 180s). The 900s TTL
  is tuned for a client already gone, where reaping late costs nothing; a blocked
  assistant pays for every second. It stays above the 60s re-issue threshold so a
  recovered stand-up has room to land and a *slow* orchestrator is not mistaken
  for an absent one. A named constant rather than a config key — no operator has
  a reason to turn it.

**Verified by making the test fail.** The new test was run against a disabled
fix, and went red with the exact symptom (`no report for a reaped watermark — the
session waits forever`). Same `sleeping-sentry` discipline that corrected the
"silent drop" claim during #80, applied on purpose this time rather than by luck.

### #83 — the `/bloodhound` skill understated the machinery

The skill presented spawning a session secretary as **step 1, a precondition**.
Since R3279/R3280 it is an optimization, so the skill described less than the
system does — which is how a capability quietly goes unused.

The repair was more than wording: **the steps were in the wrong order.** `listen`
is the actual precondition (recognition gates on the bloodhound-result
subscription alone), so it became step 1 and the spawn became step 2, marked
optional with what skipping it costs — scheduling latency and a shared secretary
— and what it does *not* cost, the tag recommends (R3283). The frontmatter
description was carrying the same false promise and was rewritten too, since that
is the line the loader reads.

One thing the pass had to be careful **not** to over-generalize: ambient recall
(`/recall`) genuinely does still need a session-local secretary — R2949's arming
gate drops ambient state when `@ark-secretary-work` is absent, and the pool
cannot stand in. The skill now says so where a reader would otherwise conclude
the spawn is never needed.

## Interim workaround (still valid for callers outside a session)

`ark bloodhound search --raw --want passages …` — the pool hunts, the caller
curates. Verified 2026-07-27; returns a markdown locator list.
