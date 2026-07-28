# Ambient recall should borrow the pool too

**Problem.** Turning on ambient recall costs a session its own Haiku secretary,
held for the whole session and idle most of it. Directed search stopped needing
one when the watermark path learned to borrow Luhmann's pool; ambient never did,
so the more expensive of the two levels is the one still paying per-session.

Opened 2026-07-28.

| Part | Status |
|---|---|
| #86 — ambient reaches the pool; `ark bloodhound listen [--recall]` | Open (`/mini-spec`) |
| #87 — `/bloodhound` and `/recall` describe the new surface | Open (docs) |
| #88 — measure what the pool actually saves | Open (investigation, feeds #74) |

**Precedent.** This is the same conflation
[carves/done/bloodhound-pool-fallback.md](done/bloodhound-pool-fallback.md)
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

*(Traced at design granularity, not exhaustively. The implementing session should
confirm the builder's addressing before relying on the size of this.)*

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

**#87 — the two skills describe the new surface** (docs). `/bloodhound` gains the
new verb; `/recall` loses the spawn as a precondition, which is the *second* time
that sentence has had to be corrected — it was #83's whole content for directed,
and `/recall` inherits the same claim. Note that #83 deliberately left
"ambient still needs a session-local secretary" **in** `/bloodhound`; that line
becomes wrong the moment #86 lands and must go with it.

**#88 — measure what the pool actually saves** (investigation). The economy-of-
scale case is sound in principle, but it is a premise, and this project keeps
being right to measure those. Feeds #74, where the question is what ark may
honestly claim; a number is worth more there than an argument.

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
  `request_ttl_seconds`.
- **Pool sizing.** Ambient fires per turn boundary across every enabled session,
  a far higher arrival rate than deliberate watermarks. Preemption stops it
  starving directed hunts, but says nothing about whether `pool_max` defaults
  remain sensible once ambient is on the same pool.
