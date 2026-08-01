# Actor delivery: the front door to the closure actor

*Working name. The problem is "what happens between calling `svc` and the
closure running," and that wants a better handle than this one.*

The closure actor is ark's primary concurrency mechanism, and the guarantee the
rest of the codebase leans on is that a closure posted after another closure
**runs** after it. That guarantee does not currently hold. This carve covers
restoring it, and the redesign that turns out to be the right way to do so.

## The defect

`svc.go`'s fire-and-forget post is:

```go
func svc(ch chan func(), fn func()) {
	go func() { ch <- fn }()
}
```

Every post spawns a goroutine to perform the send. Two posts from the same
goroutine therefore race, and can arrive in either order. Delivery order is
whatever the scheduler picks.

That is not a cosmetic problem. Serialization is the entire point of a closure
actor, and **Actor Queue Ordering** — post the follow-up behind the message
rather than synchronizing against it — is a pattern used throughout this
codebase precisely because it converts an ordering requirement into a queue
position. When the queue does not preserve position, every use of that pattern
is relying on something that is not true.

It also costs more than it looks. A post that cannot be delivered immediately
parks a goroutine holding a 2–8 KB stack, and those accumulate without bound
when the actor is behind. The backlog is invisible rather than absent, and when
it finally drains it drains in arbitrary order.

### Where it bites

The defect needs two posts from **one** goroutine whose relative order matters.
A single post is unaffected, and two posts of interchangeable work are unaffected
in practice.

The clearest live instance is the bloodhound dispatch loop
(`recall_watcher.go:589`), which allocates a monotonic hunt id and posts a
dispatch per watermark found in one batch of assistant output:

```go
for _, req := range scanBloodhounds(newBytes) {
	bid := w.nextBloodhoundLocked(sessionID)
	r, sid := req, sessionID
	svc(w.jobs, func() { w.dispatchBloodhound(sid, bid, r) })
}
```

Ids are allocated in order and dispatched in order, and delivery order is
unspecified, so `<B2>` can dispatch before `<B1>`. It fires whenever one
assistant turn carries more than one watermark. Whether out-of-order dispatch is
*harmful* has not been established, and this carve does not claim it is; what is
established is that the code is written in the shape that assumes ordering.

The watcher's `indexPaths` followed by `reconcile` is the other candidate pair,
and also unestablished as to harm.

**A wrong turn, recorded because it is instructive.** This section first claimed
that R346 was never implemented, on the strength of the comment at
`server.go:818` — "a second reconcile requested while one runs queues behind it
on the DB actor (FIFO) rather than being dropped, so it runs after (R346)". The
requirement itself says only that the second reconcile *waits for the current one
to finish, then runs*, and that holds: serialization is intact, and two
reconciles are interchangeable so their arrival order is immaterial. The false
part is the parenthetical "(FIFO)", which describes a property the channel has
and the posting path does not.

The mistake was reading a requirement through a comment about it instead of
reading the requirement. It is the same failure the ordering bug itself is made
of — a claim that stayed on the page after it stopped being exactly true — and it
is worth keeping here because "the docs say X is guaranteed" was the evidence, in
both cases, that nobody went and checked.

### How it got this way

The cause is one line further down. `runSvc` executes each closure **inline** in
the loop goroutine, so a slow closure (a `Sync`'d search, say) stops the loop
receiving. Senders then back up, and escaping the send into a goroutine is the
obvious way to stop callers blocking on it.

So the goroutine wrapper is a symptom. It was buying one real property — a post
from inside a running closure does not deadlock — at the cost of the ordering
guarantee. The redesign below keeps that property and stops paying for it.

*(The shape was modelled on ui-engine, which has the same defect. See Item 2.)*

## The design

**DECIDED (Bill, 2026-07-31.)** `ChanSvc` becomes a struct rather than a bare
channel. The loop goroutine stops running work and does only bookkeeping.

```
ChanSvc: channel, running bool, queue []func()

loop (one goroutine):    receive a closure, run it immediately

Svc(arg):                ch <- func(){ Accept(arg) }      // direct send, no goroutine
Accept(arg):             if running { queue arg } else { launch(arg) }
launch(arg):             running = true
                         go { arg(); ch <- next }
next():                  if queue nonempty { launch(dequeue()) } else { running = false }
```

With an empty queue a closure runs immediately. With a full one, each completion
triggers the next until they are gone. The work runs in its own goroutine, so
the central loop is only ever a slice operation and a `go` statement.

### Why it is correct

**Ordering.** `Svc` sends directly on the calling goroutine, so two posts from
one goroutine land in channel order, `Accept` sees them in that order, and the
queue is FIFO. The `next`-versus-`Svc` race is safe in both directions: if `Svc`
lands first, `Accept` sees `running` and queues, and `next` then dequeues it; if
`next` lands first it clears `running`, and the following `Accept` launches at
once.

**`running` needs no synchronization.** `Accept` and `next` both execute in the
loop goroutine and nowhere else, so the flag has exactly one reader and one
writer, both serialized by the loop.

**No deadlock is reachable.** Nothing the loop goroutine runs ever sends to its
own channel, and neither `Accept` nor `next` can block. The loop is therefore
always returning to the receive, so a send can stall only transiently. This is
what restores the property the goroutine wrapper was protecting: `Svc` from
inside a running closure is safe again, because the closure is not running in
the loop.

**Panics.** No recover. An unrecovered panic in the work goroutine terminates the
process, matching `runSvc`'s current `log.Fatalf` behaviour, and a closure that
wants to survive its own panics recovers internally — as the write actor already
does. Choosing not to recover also removes a hazard rather than guarding one: a
recover that forgot to send `next` would leave `running` true forever, queueing
every subsequent closure with no error anywhere. There is no such state if the
process is gone.

### What it deliberately does not change

**Serialization.** One work goroutine at a time, gated by `running`. A slow
closure still delays every closure behind it. What the redesign buys is that
*posting* never blocks, not that work overlaps. It is not a throughput change and
should not be sold as one.

**The rule against synchronous calls from inside a closure.** `Sync` from a
running closure still deadlocks, for a new reason: the nested post is queued
because `running` is true, and the queued closure can only run when the active
one finishes, which is waiting on it. The rule in `specs/architecture.md`
survives verbatim.

That last point is the one most likely to bite, because the redesign makes
fire-and-forget re-entrant while synchronous stays fatal. "I can call `Do` from
inside a closure now, so `Sync` must be fine too" is an easy inference and a
wrong one. The tempting fix — having `Accept` detect the nested case and run it
immediately — is re-entrancy, which is the thing actors exist to prevent, since
it would let the nested closure observe the outer one's half-finished mutations.
The deadlock is the correct outcome.

**The operation-object discipline is the audit surface for that rule**, and its
reach is currently partial. `substrateOp` holds its read view as a private field
rather than an embedded type, so a method on the operation cannot drift into an
actor call by accident; reaching `op.db` is deliberate and greppable. But search,
recall normalization, and fetch-payload assembly are still bare `fts` readers
(`specs/db-concurrency.md`), so the audit only bites where operations exist.
Any spec sentence stating the rule must state that reach too. A rule that reads
as universal while holding on two code paths stops people checking the rest.

Finishing that migration is separate, pre-existing tech debt and is **not** part
of this carve. The two meet at exactly one question, "may I call `Sync` from
here?", and nowhere else: `ChanSvc` governs how closures reach the actor,
operations govern off-actor reads through a private copy.

### A sweep that turned out to be unnecessary

Before the redesign was on the table, the obvious minimal fix was a plain
blocking send. That would have required first proving no caller of `DB.Do`,
`reconcile`, `indexPaths`, or the watcher's posts ever sits on the actor it
targets, because a blocking send to your own full queue self-deadlocks — and a
deadlock there presents as the whole server hanging with no error, the worst
diagnostic shape available.

The redesign removes the need for that proof. The loop always drains, so a send
from anywhere is safe, including from inside a running closure. Recording this
because the sweep was recommended and then obviated, and someone re-reading the
minimal fix should know why it was not taken.

For the record, the six call sites were checked and all are off-actor anyway:
`db.go:517`/`:532` (write goroutine), `server.go:883` (an `enqueueWrite` closure,
so the write goroutine), `recall_watcher.go:574` (a timer goroutine), `:593`
(`OnAppend`, the indexer goroutine), and `db.go:435` (`DB.Do`, reached from the
watcher).

## The split

~~**Item 1 — `ChanSvc` and the three actors** (`/mini-spec`).~~ **LANDED
2026-07-31** as R3344–R3350, with `crc-ChanSvc.md` and `test-ChanSvc.md` as new
artifacts. The ordering tests were verified to *fail* under the old spawned
sender before being accepted — re-injecting `go func(){ ch <- … }()` scrambles
delivery visibly (`[0 3 1 2 10 4 …]`), so the guard is demonstrably awake rather
than merely green. Gaps banked: **O192** (post-after-stop is now a silent drop
or a hang where it used to panic) and **O191** (the operation-object audit
surface reaches only part of the read paths). **DECIDED (Bill, 2026-07-31): the
channel is one slot deep**, on diagnostic grounds rather than capacity ones — a
deeper channel would absorb the first N posts to a wrongly-held loop and surface
the stall somewhere other than its cause. That supersedes the capacity question
in Item 3 below.
The struct and its delivery guarantee as new requirements, then migrate
`db.svc`, the recall watcher's `w.jobs`, and the pty host's `h.jobs`. All three
share the `svc` / `svcSync` / `svcSyncVoid` helpers, so this is a single change
rather than a staged one; four tests assign raw channels (`db.svc = make(chan
func(), 16)`) and move to the struct with it.

Two deliverables that are not mechanical and should not be skipped:

- **An ordering test.** This property was argued once, quietly lost, and stayed
  lost. Nothing currently fails if someone re-wraps the send. Something must.
- **The spec pass.** `specs/architecture.md` and `specs/db-concurrency.md` gain
  the sync/async asymmetry and the operation discipline's actual reach.

R346 becomes true for the first time as a consequence, so it wants the ordering
test pointed at it explicitly rather than being assumed to follow.

**The guarantee's first consumer landed the same day.** Reconcile coalescing
(R3351) makes "at most one active, one pending" out of two closures in the queue:
a gate closure that decides, and the scan closure it posts behind itself. The
pending mark clears when the scan *begins*, and expressing that at all requires
the decision and the scan to be separate queue entries. So the very next piece of
work built directly on ordered delivery — and would have been silently wrong the
day before. Recorded here because it is the evidence that this carve's guarantee
is load-bearing rather than theoretical.

**Item 2 — tell ui-engine** (`ark-messenger`).
The defect was modelled from there, so its actor has the same missing guarantee.
Cross-project message, not a change made from this repo.

**Item 3 — cost** (deferred; `/mini-spec` if taken). *The capacity half is
settled — see Item 1's DECIDED. What remains is cost.*
Every closure now pays a goroutine spawn and a scheduling hop where fast ones
used to run inline. Go recycles `g` structs and their stacks on per-P free lists,
so a spawn is a few hundred nanoseconds and does not hand the collector a fresh
stack each time, but "cheap" is not "free" and nobody has measured this actor's
closure mix. Related: with the loop now draining fast, the channel capacities
(`db.svc` is 8, the other two are 16) are probably wrong in some direction, and
the internal queue is unbounded where the channel was not. Whether an unbounded
queue wants a depth warning is open.

## Open questions

- **Channel capacity**, now that the loop cannot be held. A very small buffer may
  be sufficient, since the queue absorbs the backlog.
- **Should the internal queue be bounded, or report depth?** It replaces
  unbounded parked goroutines with an unbounded slice, which is strictly cheaper
  but still unbounded.
- **Is the goroutine-per-closure cost acceptable on the hot path**, or does the
  DB actor's closure mix skew small enough to want an inline fast path? An inline
  path reintroduces head-of-line blocking, so the answer is probably "accept,"
  but it should be measured rather than assumed.
- **A better name**, for the carve and for the guarantee itself.

## Where decisions go as parts land

The delivery guarantee, the no-recover choice, and the sync/async asymmetry all
belong in `design/requirements.md` and `specs/architecture.md` when Item 1 lands,
with the operation-discipline reach going to `specs/db-concurrency.md`. This
document then points at them rather than restating them.
