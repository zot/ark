# ChanSvc
**Requirements:** R986, R3344-3350, R3607

The closure actor itself, as a reusable component rather than a DB
implementation detail. Serializes access to whatever state its owner protects,
and guarantees that closures posted by one goroutine run in the order that
goroutine posted them. Three owners use it: `DB` (`db.svc`), `RecallWatcher`
(`w.jobs`), and `PtyHost` (`h.jobs`).

Its distinguishing property is a **split between keeping order and doing work**.
The loop goroutine only ever manipulates the queue and the in-flight flag; every
closure runs in a goroutine of its own. That split is what lets the post be a
direct send, and the direct send is what makes ordering hold.

## Knows
- ch: the channel closures arrive on. **One slot** — a doorway, not the buffer; the queue behind it absorbs bursts (R3347). The depth is chosen for diagnosis rather than throughput: the loop does only O(1) work, so depth would buy a poster nothing but a skipped rendezvous, while a deeper channel would swallow the first N posts to a wrongly-held loop and surface the stall later and elsewhere. One slot blocks the next poster at the site.
- queue: closures waiting for the in-flight one to finish. Unbounded, and touched only on the loop goroutine, so it needs no synchronization (R3346, R3347).
- running: whether a closure is in flight. Written and read only on the loop goroutine, which is why it is a plain bool rather than an atomic (R3346).

## Does
- next pops its queue through popFront, so a run closure is not kept alive by the backing array (R3607).
- loop: receive a closure and run it immediately. Every closure it receives is bookkeeping (`accept` or `next`), never caller work, so the loop is never held longer than a queue operation and is effectively always at the receive (R3346). This is what makes a direct send viable, and running caller work here instead is precisely what made escaping the send tempting in the first place.
- Svc(fn) / SvcSync(fn) / SvcSyncVoid(fn): post fn. The send is performed **on the calling goroutine** — never handed to a spawned sender, which would let two posts race and surrender ordering (R3344, R3345). Both post kinds enter here, so ordering spans them (R3344). The synchronous forms additionally block the caller on fn's completion.
- accept(fn) (loop goroutine): if a closure is in flight, append fn to the queue; otherwise launch it (R3346).
- launch(fn) (loop goroutine): mark running, then spawn the goroutine that runs fn and, on completion, posts `next` back. **No recover** — a panicking closure terminates the process (R3348), which is deliberate and also removes the failure mode a central recover would create: forgetting to clear `running` would queue every later closure forever, with no symptom.
- next() (loop goroutine): dequeue and launch if anything is waiting, else clear running. Posting this from the work goroutine rather than calling it is what keeps every mutation of `queue`/`running` on the one goroutine.

## Collaborators
- its owner (DB, RecallWatcher, PtyHost): holds the ChanSvc and posts closures over its own state. Connected-to — ChanSvc knows nothing about what it protects, which is what makes it reusable across three unrelated owners.
- operation objects (`substrateOp`, `recallOp`): not a runtime collaborator but the **audit surface** for R3349's rule against synchronous calls from inside a closure. Go has no dynamically-scoped values, so an operation threaded as the receiver is that scoped value made lexical. Reach is partial — see gap O191.

## Notes
**Fire-and-forget from inside a running closure is safe; synchronous is not**
(R3349). The first works because the closure runs off the loop, so the post
simply queues behind it. The second cannot: the nested post queues because a
closure is in flight, and that closure is blocked waiting for the nested one.
Inferring the second from the first is natural and wrong, which is why both are
stated. Making `accept` detect the nested case and run it inline is rejected —
that is re-entrancy, and preventing an inner closure from observing an outer
one's half-finished mutations is the whole purpose of the actor.

## Sequences
- seq-write-actor.md
