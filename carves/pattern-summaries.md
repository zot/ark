# Pattern summaries

Bill keeps a collection of personal engineering patterns, and every session
starts with each pattern's name and summary in its context. A pattern's body
is read only when something sends the reader to it, so the summary decides
whether a pattern is ever reached for. A summary that describes less than its
body, or something else, leaves the body right and unreachable.

Opened 2026-10-28, from ark work. Copy-on-write configuration and a config
reload running off the DB actor (windows-support 8.5, actor-discipline Item 8)
were answered by ideas already in the collection, Monadic Wrapper and Context
Coupling, which the session reached for only when Bill named them. Both
summaries pointed away from what their bodies say. A history record as much as
a work list: the patterns themselves live outside this repository.

## Status

- [x] ~~**Item 1 — the actor-turn pattern.**~~ **LANDED (fossil `5a432197`,
      2026-10-28.)** Named Hall Pass (by Daneel), with its links. See Item 1.
- [x] ~~**Item 2 — measure summary/body drift across the collection.**~~ **LANDED (2026-10-28 — `#189`.)** A measurement, not a rewrite. See Item 2.
- [ ] **Item 3 — give the 41 summaries what they lack.** **OPEN (not queued.)**
      Long-term: a few at a time, now and then, until the list is done (Bill,
      2026-10-28). See Item 3.

## Parts

**Item 1.** A new pattern: the handle an actor's closure receives is a
context-coupled object whose context is the actor's turn. Given a type of its
own, it cannot be used to write a synchronous call back into its own actor, so
re-entry, which deadlocks, cannot be written. Closed when the closure returns, it
fails loudly if it escaped into a goroutine, a field or a return value, instead
of reading guarded state off the actor. A value that must be read synchronously
on both sides is published immutable and read from a leaf micro-actor that calls
no other actor (actor-discipline Item 8's vending actor). It builds on Monadic
Wrapper (the turn rides as the receiver) and Context Coupling (the turn has a
lifetime, as a transaction does), and connects to Closure Actor, Sewing and
Baton.

Installing it means three files in the collection: the new pattern, a section in
Context Coupling ("an actor's turn is a context") with its summary rewritten to
match its body, and Monadic Wrapper's summary widened to name its second job (the
operation's only door to shared state). Plus one-line back-links in Monadic
Wrapper, Closure Actor and Sewing.

**Item 2.** For each pattern, does its summary name the idea, the act and the
moment its body describes, or something narrower or different? Two instances
found 2026-10-28. Context Coupling's summary describes context carried
implicitly through closures and globals, while its body describes a derived
object carrying its context (a record and its transaction). Monadic Wrapper's
names only its pipeline-configuration job, not its use as an access discipline.
Report per pattern (matches, narrower, different) with the body sentence that
shows it. Bill decides the rewrites.

**Item 3.** The measurement (Item 2, 2026-10-28) found 6 summaries narrower
than their bodies and 39 that name no moment for reaching for the pattern; four
are both, so 41 patterns need work. Each line says what that summary lacks and
how long it is now. "Narrower" means the body has a main part or a second job the
summary omits (the measurement report quotes it); "moment" means the summary says
what the pattern is but not when to reach for it.

The balance (Bill, 2026-10-28): every summary is in every session's context, so
prefer the shorter wording when it carries the same meaning, and never trade
meaning away to save words. More text is not catastrophic, since newer models
handle larger contexts better, but it still matters for now. Replace rather than
add: drop description the body already carries and spend the words on the idea
and the moment. Tick a line when its rewrite is in the collection.

- [ ] `alibi-stamp` — moment, 41 words
- [ ] `anlp` — moment, 14 words
- [ ] `baby-food` — moment, 26 words
- [ ] `batteries-included` — moment, 46 words
- [ ] `call-button` — moment, 45 words
- [ ] `closure-actor` — moment, 22 words
- [ ] `code-as-sketchpad` — narrower + moment, 31 words
- [ ] `conversation-transcript` — moment, 10 words
- [ ] `crank-handle` — moment, 32 words
- [ ] `discernment-gated-authoring` — moment, 18 words
- [ ] `fixer` — moment, 28 words
- [ ] `fossil-local-storage` — narrower, 15 words
- [ ] `fumble-log` — moment, 26 words
- [ ] `greased-pig` — moment, 19 words
- [ ] `heirloom-database` — moment, 46 words
- [ ] `hermetic-seal` — narrower + moment, 21 words
- [ ] `librarian-search` — moment, 38 words
- [ ] `lock-flap` — moment, 26 words
- [ ] `lost-wax` — moment, 135 words
- [ ] `lotto-tube` — narrower + moment, 25 words
- [ ] `personal-notes` — moment, 18 words
- [ ] `personal-patterns` — moment, 119 words
- [ ] `project-frictionless` — moment, 30 words (a project/résumé note, not a technique: does it need a moment at all?)
- [ ] `project-humble-master` — moment, 15 words (a project/résumé note, not a technique: does it need a moment at all?)
- [ ] `project-leisure` — moment, 18 words (a project/résumé note, not a technique: does it need a moment at all?)
- [ ] `project-mini-spec` — moment, 17 words (a project/résumé note, not a technique: does it need a moment at all?)
- [ ] `query-crystallization` — moment, 38 words
- [ ] `resume-tailoring` — moment, 12 words (a project/résumé note, not a technique: does it need a moment at all?)
- [ ] `scratch-directory` — moment, 14 words
- [ ] `secretary` — moment, 23 words
- [ ] `self-contained-tools` — narrower + moment, 44 words
- [ ] `shoulder-the-fat` — moment, 115 words
- [ ] `sidecar-agent` — moment, 23 words
- [ ] `solarian-viewer` — moment, 22 words
- [ ] `soviet-supermarket` — moment, 22 words
- [ ] `stencil` — moment, 24 words
- [ ] `straycat` — narrower, 18 words
- [ ] `sublooper` — moment, 34 words
- [ ] `text-generation-guidelines` — moment, 23 words
- [ ] `turnstile` — moment, 43 words
- [ ] `watermark` — moment, 46 words
