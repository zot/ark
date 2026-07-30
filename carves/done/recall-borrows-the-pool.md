# Ambient recall should borrow the pool too

**Problem.** Turning on ambient recall costs a session its own Haiku secretary,
held for the whole session and idle most of it. Directed search stopped needing
one when the watermark path learned to borrow Luhmann's pool; ambient never did,
so the more expensive of the two levels is the one still paying per-session.

Opened 2026-07-28.

| Part | Status |
|---|---|
| ~~#86 — ambient reaches the pool; `ark bloodhound listen [--recall]`~~ | **Landed** 2026-07-28 — R3294–R3296, T272 |
| ~~#87 — `/bloodhound` and `/recall` describe the new surface~~ | **Landed** 2026-07-29 |
| ~~#88 — measure what the pool actually saves~~ | **Landed** 2026-07-29 — not measurable yet; idle-burn result below |
| ~~#90 — ambient is invisible to the pool, in both directions~~ | **Landed** 2026-07-29 — R3298–R3304, T273 |

**Precedent.** This is the same conflation
[bloodhound-pool-fallback.md](bloodhound-pool-fallback.md)
found and fixed on the *directed* gate, still in place on the ambient one. That
carve is the argument for this one: the split has already been made once, the
machinery it needed already exists, and what it cost is on record.

---

## The gates sit ten lines apart, and only one was fixed

In `recall_watcher.go`:

- `bloodhoundEnabled` (~437) — the bloodhound-result subscription **alone**,
  carrying an explicit comment that it is *"Deliberately NOT conjoined with
  secretaryPresent"*, because "a session without its own secretary can still be
  served by Luhmann's pool" (R3279).
- `ambientEnabled` (~467) — `secretaryPresent(sessionID) && ` the recall-result
  subscription. The conjunction #80 removed, still here.

The teardown at ~492 states the asymmetry outright: a missing secretary tears
down ambient state but must **not** end the append, "because bloodhound
recognition survives a missing session secretary."

**Why the justification looks solid and is not.** R2949 says ambient "genuinely
needs a session-local secretary," and the code comment says ambient needs *a
worker*. Both are true. Neither establishes that the worker must be
**session-local** — that step is assumed, and it is precisely the step #80
identified as the conflation: *recognition* and *service* are separate
decisions, and who serves is chooseable later.

## The machinery is already there

Ambient's `fire()` builds a curation doc and addresses it to
`@ark-secretary-work: <sessionID>`, which the session's own secretary drains.
The pool path addresses the **same tube** at `@ark-secretary-work=<composite>`
(`<luhmann-session>-<nonce>`) via the Fixer's baton flip. Same mechanism,
different addressee — so serving ambient from the pool is a routing choice, not
new infrastructure.

### Confirmed 2026-07-28 — and most of it already exists

The trace above was flagged as design-granularity only. Confirming it found the
addressing correct **and four of the listed pieces already implemented**:

- **Directed already preempts ambient — R2939.** `RecallNext`'s dispatch loop
  serves a pending bloodhound task, then a CLI hunt, then a curation doc. Bill's
  decision below confirms existing behavior rather than requesting new work, and
  it keeps holding for a pool secretary with no extra code.
- **Returns are already origin-routed.** `recall close` keys on the **fire
  token** `<session>-<fire>`, so the originating session travels inside the work
  item. Whoever curates it, the result goes home. No `relayRawResult` analogue is
  needed.
- **Cross-session dispatch already exists.** `lowestPendingCuration(target)`
  filters by session *only when `target != ""`* (R2889, so a per-session
  secretary cannot steal another session's work). Passing `""` — the "legacy
  bare-curate path" — dispatches any session's doc. That is exactly pool
  behavior.
- **No-drop queueing already exists.** That same function skips a doc whose
  session has no `ark-recall-result` subscriber, and the comment states the
  intent: "the work is deferred, never discarded." Which is the decision below,
  already built.

**So what actually remains is small:** the gate conjunct in `ambientEnabled`, the
ambient teardown that fires when no session secretary is present, a way for a
pool secretary to say "I may take any session's curation", the CLI verb, and
superseding R2949.

**Landed 2026-07-28.** Smaller than the split below predicted, because of the
four already-built pieces above. What changed: `ambientEnabled` lost its
`secretaryPresent` conjunct (R3294, retiring R2949 as T272); the OnAppend
teardown went with its premise, since the recall-result gate below it already
drops lingering state on the condition that actually means "stop accumulating";
`recall next --pool` declares the role (R3295); `ark bloodhound listen
[--recall]` names the verb where the concept lives, delegating to the existing
implementation so the two spellings cannot drift (R3296). One correctness catch
along the way: the conversation injected into a curation doc must follow the
**doc's** session, not the caller's — for a pool secretary the caller's is a
composite with no conversation, which would have handed the curator no context
exactly when judging fit matters most. **Gap O133 discharged** — its assertion
(the gates are independent of the secretary) is now written, and verified red by
restoring the conjunct.

**The one real design choice** was the third: a pool secretary passes its composite
as `session`, which makes `targetSession != ""` and filters out every other
session's docs. It needs to keep the composite for *directed* routing
(`lowestPendingCLIHunt`) while using `""` for *curation* lookup.

## Decisions

- **DECIDED (Bill, 2026-07-28):** ambient recall no longer requires a
  session-local secretary. It borrows the pool, for the economy of scale — one
  warm pool serving many mostly-idle sessions, instead of an idle Haiku per
  session.
- **DECIDED (Bill, 2026-07-28):** **directed preempts ambient.** Intuitive, and
  the asymmetry earns it: a watermark has an assistant blocked on `listen`,
  while nothing waits on a recollection.
- **DECIDED (Bill, 2026-07-28):** **a late recollection is still delivered, not
  dropped.** The proposal to drop under load was rejected, and rightly: a user
  who turned recall on is interested in the results even when they are pokey.
  So this is a *queueing* problem, not a deadline problem — ambient yields to
  directed and arrives when it arrives.
- **DECIDED (Bill, 2026-07-28):** `--recall` is a **flag on `listen`**, not a
  separate verb, since `/recall` includes `/bloodhound` anyway. The verb moves to
  `ark bloodhound listen`, where the concept already lives.

## What the shape buys

One rule instead of two. Today "is directed on?" is answered by a subscription
and "is ambient on?" by a subscription *and* a subagent. After this, both are
"did you subscribe" — `@ark-bloodhound-result` for directed, and
`@ark-recall-result` on top for ambient. The CLI says the same thing in one
verb and one flag.

It also puts the command where the concept is. `ark connections recall listen`
performs the *bloodhound* subscription; `ark bloodhound listen` stops that
disagreement.

## The split

**#86 — ambient reaches the pool, and the CLI says so** (`/mini-spec`). The gate
and the verb land together or the flag has nothing to enable. Drop the
`secretaryPresent` conjunct from `ambientEnabled`; give `fire()`'s dispatch the
same "no session secretary → try the pool" branch `dispatchBloodhound` has;
implement directed-preempts-ambient; add `ark bloodhound listen [--recall]` and
decide what happens to `ark connections recall listen` (alias or retire).
**Supersede at source:** R2949's "ambient genuinely needs a session-local
secretary" must be retired or reworded, or a future agent reads it as current
intent and restores the conjunct — the revert trap, with a requirement behind it.

**#87 — the two skills describe the new surface** (docs). **Landed 2026-07-29**,
and it reached four files rather than two: the "you need a secretary" claim had
also settled into `/ark-search` ("a secretary spawned and running its loop") and
`/ark` ("sets up the secretary and `listen`"). Neither was named here, because
this carve tracked the claim by where it was *first* written rather than by
where it had spread. Worth remembering next time a sentence has to be corrected
twice: grep the claim, not the file list. `/bloodhound` gains the
new verb; `/recall` loses the spawn as a precondition, which is the *second* time
that sentence has had to be corrected — it was #83's whole content for directed,
and `/recall` inherits the same claim. Note that #83 deliberately left
"ambient still needs a session-local secretary" **in** `/bloodhound`; that line
becomes wrong the moment #86 lands and must go with it.

**#88 — measure what the pool actually saves** (investigation). The economy-of-
scale case is sound in principle, but it is a premise, and this project keeps
being right to measure those. Feeds #74, where the question is what ark may
honestly claim; a number is worth more there than an argument.

## #90 — ambient is invisible to the pool, in both directions

**Landed 2026-07-29** — R3298–R3304, R3295 retired as T273. Ambient is a
separate `recall` pool class with its own roster and limits; the watcher routes
each fired doc by the ladder below and stamps occupancy by doing so; the
stand-up crank handle dictates `--class`, which is what the `--pool` version
could never do. Tests in `ambient_routing_test.go` (nine cases,
`test-AmbientRouting.md`), verified red by removing the occupancy stamp. Two
gaps banked: **O178** (the shared pool primitives still live in the file named
for the bloodhound CLI) and **O179** (the conversation-injection assertion needs
real JSONL fixtures). What the fix did *not* need is below — most of the
machinery was already there, wearing one class's name.

Found 2026-07-29, while checking what #87 could honestly say. #86 landed the
gate and the verb; what it did not land is any point at which ambient and the
pool actually meet. Three separate findings, one shape.

**Nothing ever passes `--pool`.** R3295's flag is plumbed end to end (CLI →
`/connections/recall/next` → `RecallNext`), and `RecallNext` uses it correctly:
`curationTarget = ""` when pool, so the lookup crosses sessions. But Luhmann's
stand-up spawns `ark-recall-agent` with a composite `Session:` and no role
(`.claude/skills/luhmann/SKILL.md`), and that agent's loop verb is written
`recall next --session <S> <NONCE>`. Even a secretary launched by hand with the
flag would lose it on its first turn: all four crank-handle builders
(`recall_next.go` `recallDocPrompt` / `recallKeepalivePrompt` /
`recallSearchTaskPrompt`, plus the redial in `cmd/ark/main.go`) format
`recall next --session %s %d` with no role term. With a composite session and no
`--pool`, `lowestPendingCuration(composite)` filters to that composite's own
docs, of which there are none — ambient docs are addressed to real session IDs.
So a session that skips its own secretary fires curation docs the pool cannot
see. They queue, per the no-drop decision, and nothing is coming for them.

**Serving ambient does not mark a secretary busy.** `busy` is set exactly where
the watcher *routes a hunt* — `pump()` in `recall_watcher_cli.go`, which picks a
free secretary, stamps `busy`, and re-addresses the doc to that secretary's
`@ark-secretary-work=<composite>` tube via `enhanceRequestDoc` — and cleared on
its return. Ambient is not routed at all: the secretary finds it itself through
`lowestPendingCuration`, which touches neither `busy` nor `idleSince`. The
pruner (R3019) retires any not-busy secretary idle past `cooldown_seconds`, so a
secretary that has served recollections steadily for ten minutes reads as idle
and gets a stop directive.

**An ambient fire never asks for a secretary.** `fire()` has no pool interaction
anywhere in its 179 lines. Stand-up directives are pushed only from the two
bloodhound paths (`handleCLIRequest`, and the watermark route).

Together the second and third mean the pool is not merely unreachable but
unsustainable: Luhmann seated, ambient on, no watermarks for one cooldown, and
the pool decays to zero with nothing to restaff it. Fixing only the `--pool`
caller gap would buy a pool that empties itself ten minutes later.

**Why #87 waits on this.** The docs pass exists to tell a session it may skip
its own secretary. That sentence is false until ambient and the pool meet, and
writing it would put the third incorrect claim about who serves ambient into the
same two skills — after #83 corrected the first and #86's landing left the
second.

### The dispatch ladder

**DECIDED (Bill, 2026-07-29):** who serves a fired curation doc is decided in
three rules — the session's own secretary if it has one; else Luhmann's pool if
a Luhmann is seated; else an error delivered to the session rather than silence.
A running secretary is *sufficient* for the switch: the decision is made from
what is standing, not from anything a secretary says about itself. How that is
implemented is the next section, and it is what retires the role question
altogether.

### The mechanism: ambient is routed, not fished for

**DECIDED (Bill, 2026-07-29):** the watcher **routes** a fired curation doc to a
chosen secretary's identifying nonce, and the watcher manages the pool. Ambient
stops being pull-with-a-filter and becomes push-with-an-address, which is what
the CLI hunt path already does — `pump()` picks a free secretary, marks it
`busy`, and re-addresses the doc to `@ark-secretary-work=<composite>`. Ambient
takes the same door: pick the session's own secretary if one is standing, else a
free pool secretary, and address the doc there.

This is the earlier "same mechanism, different addressee" observation finally
made literal, and it collapses all three findings above into one change:

- **The dispatch race disappears.** A doc arrives addressed, so no secretary can
  take work meant for another. The pull-side filter this carve first proposed
  (have the pool skip sessions with a local secretary) is unnecessary.
- **The role question dissolves.** `--pool` existed so a secretary could widen
  its own lookup across sessions. Addressed work needs no lookup, so there is no
  role to declare or infer, and nothing for the crank handle to echo. R3295 is
  superseded rather than relocated.
- **Occupancy fixes itself.** `busy` is stamped by the routing step, so ambient
  marks occupancy by construction instead of needing its own bookkeeping. The
  pruner then sees a curating secretary as working, and `PoolBusy` sees a
  saturated pool, which is what makes stand-up fire.

The last one is worth stating as a general lesson, because it is why the bug
existed: ambient bypassed the routing step, and everything the routing step
happens to record went unrecorded with it. Occupancy was not forgotten so much
as never reached.

### Ambient gets its own class

**DECIDED (Bill, 2026-07-29):** ambient is a **separate pool class**, not a new
customer of `class.bloodhound`. The reason is not modularity: the pool is a
class-scoped resource everywhere it appears — `cliPool`, `bloodhoundPoolClass`,
`class.bloodhound.pool_max`, `@ark-bloodhound-cli-return` — and #86 declared
ambient a user of it without making ambient a member of anything. Giving it a
class is what makes the structure carry the concept.

This is the whole finding, stated properly. The earlier framing here — that
occupancy went unrecorded because ambient bypassed the routing step — described
the symptom. The cause is that "the pool" was a bloodhound-class thing in the
design and in the code while the spec had quietly given it a second customer,
and no traceability link can go red for that: nothing went unimplemented. R3294
landed, R3295 landed, both traced, `minispec validate` green. See
[.scratch/SYSTEMS-DOC-20260729.md](../../.scratch/SYSTEMS-DOC-20260729.md) (#91)
for the general form and the mini-spec work that would make it mechanically
visible.

**What separate classes buy.** Preemption stops being the only protection.
Ambient cannot consume a slot a directed hunt needs, so "directed preempts
ambient" narrows to a dispatch-order rule within a class rather than a defence
against starvation across two workloads. `pool_max` and `cooldown_seconds` tune
independently for arrival rates that differ by orders of magnitude — a
deliberate watermark against every turn boundary of every enabled session. And
the consequence an earlier draft of this section asked us to accept (a secretary
mid-curation being unavailable to a hunt) simply does not arise.

**What they cost** is more standing Haiku secretaries, which cuts against the
economy-of-scale argument this carve was built on. That is now a question with a
number attached rather than an argument, and it belongs to #88.

**Rule 2 is the watcher's existing autoscale, per class.** Grow when
`pickFreeLocked() == nil && len(secretaries) < poolMax` (the `needStandUp`
condition the two bloodhound paths already use), shrink through `pruneLoop`.
Both already read their limits from `class.<NAME>`, so the machinery is
class-agnostic; what it has never had is a second class.

**Rule 3 confirmed (Bill, 2026-07-29):** the report goes to *the agent that
wants the result* — the requesting session, through the consumer loop it is
already blocked on. That is `ark bloodhound listen [--recall]`; there is no
`bloodhound next` verb, and none is needed, since `listen --recall` already
carries both subscriptions. A custom tube stays available if `listen` turns out
not to suffice, but nothing yet suggests it won't.

This amends the ambient-reap question below rather than contradicting it. That
question says a **late** recollection needs no report because nobody is blocked,
and that stays right. Unservable is a different state, and worth reporting
because the remedy belongs to the assistant — spawn a local secretary, or tell
the user no Luhmann is seated.

*Settled in the `/mini-spec` pass:* an unservable-ambient report is a
**`## Unservable:`** item on `@ark-recall-result`, a third result kind alongside
`## Surface:` and `## Recommend:`. Not a `## Finding:`, which is the directed
channel's (R3293) — the two levels' failures land on the channels their
successes would have, so a consumer never has to cross-reference to learn which
of its requests died. Documented in `/recall` as the one result kind that
demands a decision rather than a mention.

## #88 — what the pool saves cannot be measured yet; here is what can

**Measured 2026-07-29** from 75 secretary transcripts (`~/.claude/projects/*/*/
subagents/`, descriptions matching `secretary loop nonce`) spanning 2026-05-30
to 2026-07-29 — 64.7 hours of standing time, 3,737 classified turns, 138.3M
context tokens. Not from `~/.ark/monitoring/recall.jsonl`, whose token columns
are dead (see #92 below).

### The economy-of-scale premise is untested, not refuted

This carve opened by asserting a saving: *one warm pool serving many mostly-idle
sessions, instead of an idle Haiku per session.* The multiplier in that sentence
is the number of secretaries standing at once. Measured over the whole history:

| concurrent standing secretaries | share of standing time |
|---|---|
| 1 | **99.9%** |
| 2 | 0.1% |

Peak 2, mean 1.00.

**Read that as "no data", not "no saving".** The distinction is the whole
result. A concurrency of 1 does not show that pooling saves nothing; it shows
that **the pool has never been stressed**, so nothing here prices it either way.
Three reasons the sample cannot bear a scalability conclusion:

- **The feature is used infrequently** — 64.7 standing hours across two months,
  by one person (Bill, 2026-07-29: *"I use bloodhound and recall infrequently"*).
- **The barrier was the thing being measured.** Every hour in that table predates
  #86/#90; turning ambient recall on **required** spawning and supervising your
  own subagent. That friction is exactly what suppressed the concurrency, so
  using the concurrency to judge the change that removes the friction is
  circular.
- **No load has ever been applied.** Nothing has ever driven several sessions
  through ambient recall at once, deliberately or otherwise.

**ASSESSED (Bill, 2026-07-29):** the data is nowhere near the quality needed to
judge scalability, and the design call was intuition. Recorded as intuition
rather than dressed up as a measurement — which is the honest state, and better
than a number that only looks like evidence.

So what #88 actually establishes is narrower and worth keeping: the pool's
*stated* justification was an untested premise, so **#74 must not claim a cost
win** — not because the claim is false, but because nothing here supports it.
What is defensible today is the friction argument: ambient recall became
something a session switches on with one subscription instead of a subagent it
must supervise.

Testing the economy claim needs data this corpus does not contain: either
deliberate load across several concurrent sessions, or ordinary usage rising far
above today's. **#92 blocks both** — with `ContextTokens` dead, even a stressed
pool would report zeros. This table is the pre-change baseline to measure
against when that becomes possible.

### The real inefficiency is idle polling, and it is large

Independent of pooling, and the more actionable result:

| bucket | turns | context tokens | share |
|---|---|---|---|
| **idle** (keepalive, server-unreachable redial) | 2,403 | 82.6M | **59.8%** |
| work (curation docs, surface/recommend/close, directed tasks) | 841 | 38.6M | 27.9% |
| waste (guard blocks, permission denials, bad flags) | 109 | 3.3M | 2.4% |
| unclassified | 384 | 13.8M | 10.0% |

**~1.28M context tokens per standing-hour is spent doing nothing**, against
~2.14M total. Six tokens in ten buy a keepalive. The cause is structural, not a
bug: `recallNextKeepalive` returns every 90s to stay under the harness's
foreground-Bash auto-background threshold, and each return re-sends the
secretary's whole accumulated context.

### In money, it is small — and the token share overstates it badly

**99% of those idle tokens are cache *reads*, billed at roughly a tenth of base
input.** Priced at Haiku 4.5 rates ($1/$5 per MTok, cache read 0.1×, 5-minute
cache write 1.25×):

| | per standing-hour |
|---|---|
| idle polling | **$0.16** |
| all secretary activity | **$0.57** |

The entire two-month history — every recall and bloodhound secretary, 64.7
standing hours — comes to about **$37, of which ~$11 was idle polling.**

So the 60% figure is a true statement about *tokens* and a misleading one about
*cost*, and it should never be quoted without the second table. Six-tenths of
eleven dollars over two months is not a number that justifies a design change.
Carry the cost figures to #74, not the token share.

### Do not "fix" this by polling less often

The obvious reading — 90s is too frequent, lengthen it — is **wrong, and would
make it worse.** A subagent's prompt cache has a **5-minute TTL** (verified
below), so a poll interval above five minutes pays a cache *write* at 1.25×
instead of a read at 0.1× — over twelve times more per poll. The 90s keepalive
is already near cache-optimal given that it must poll at all, and its interval
is pinned from the other side by the harness's ~120s foreground-Bash threshold.

The waste is **polling at all while idle**, not the interval. The only lever
that removes it is retiring the secretary — see #94.

### Method notes, so the numbers can be re-derived or disputed

- Turns are classified by the **tool result** that followed each assistant turn;
  cost is the `cache_read + cache_creation` of the turn that produced it.
- `(Bash completed with no output)` — 664 turns, the largest single fingerprint —
  is **work**, not idle: it is `surface` / `recommend` / `close` succeeding
  silently. Counting it as idle (the first pass did) overstated the idle share.
- 10% remains unclassified, so idle is a floor of ~60% and work a ceiling of
  ~28%.
- Concurrency is computed by sweeping lifetime start/end events; a lifetime is
  first-to-last timestamp in a transcript.
- **Costs are priced at Haiku 4.5 rates** ($1.00/$5.00 per MTok), cache read at
  0.1× base input and 5-minute cache write at 1.25×, per the current pricing
  reference rather than from memory. An earlier draft of this section quoted
  token counts with no cost conversion at all, which read as far more alarming
  than the money warrants.
- **Cache TTL is 5 minutes for subagents and 1 hour for main sessions** —
  measured, not assumed, from the `usage.cache_creation` per-TTL breakdown the
  transcripts record: 25,417 subagent turns are 100% `ephemeral_5m`, 10,155
  main-session turns are 100% `ephemeral_1h`, with no crossover. That is what
  fixes the 1.25× write multiplier used above, and it is why a subagent has no
  cheap idle state: stop polling for five minutes and its cache is gone anyway.

## Open questions

- **Coalescing, which is not dropping.** With ambient yielding to directed, a
  talkative session can have several curation docs queued at once. Merging them
  before service would cut redundant work while still delivering everything —
  compatible with the no-drop decision, but a distinct question it does not
  settle.
- **The ambient reap.** A queued ambient doc that nothing ever serves cannot wait
  forever, or the queue grows without bound. Unlike a stranded watermark it needs
  **no report** (R3293 exists because someone is blocked; here nobody is), so the
  question is only what the deadline should be, and whether it differs from
  `request_ttl_seconds`. *Amended by #90's rule 3:* the no-report argument holds
  for a **late** doc, not an **unservable** one.
- **Pool sizing.** Ambient fires per turn boundary across every enabled session,
  a far higher arrival rate than deliberate watermarks. Preemption stops it
  starving directed hunts, but says nothing about whether `pool_max` defaults
  remain sensible once ambient is on the same pool. *Answered by implication in
  #90's rule 2* — ambient staffs the pool, so it also sizes it. Whether the
  default 3 survives that is what #88 should measure.
