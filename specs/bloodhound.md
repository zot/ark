# Bloodhound — directed search via the warm secretary

Language: Go (recall service additions) + the sealed Haiku secretary's
persona/guard. Environment: built-in subsystem of `ark serve`, on top of
Simple Recall ([simple-recall.md](simple-recall.md)).

Ambient recall is **push** — the corpus surfaces related material unasked.
The bloodhound is the **pull** counterpart: the assistant *directs* a search
down a chosen trail and a curated digest comes back. Both ride the **same warm
secretary and the same tube**; the bloodhound is not a second agent or a second
subscription. Design rationale, the assistant-side craft, and the search crank
handle live in [.scratch/SHERLOCK.md](../.scratch/SHERLOCK.md) and the
`ark-search` skill; this spec owns the recall-service behavior.

The whole delegation interface is **one watermark** the assistant emits in its
normal output:

```
<BLOODHOUND>did we ever discuss BM25 for recall? check the chat history — yes/no with the turn</BLOODHOUND>
```

No spawn, no tool call — the watcher already scans the conversation. The payload
is natural prose carrying four fields (clue · scope · depth · want) plus an
optional stop condition; the canonical `want`-words (answer / passages /
pointers / inventory / verdict) are the reliable anchor. The watermark
**displays to the user by design** — they follow the hunt.

## Precondition: the bloodhound's own subscription

The bloodhound is the warm path, and warmth comes from the secretary already
standing. But it has its **own opt-in**, independent of ambient recall: the
assistant subscribes to **`@ark-bloodhound-result=<S>`** (via `/bloodhound`'s
`listen`). Recognition + dispatch are gated on *both* the secretary
(`@ark-secretary-work=<S>`, established by its `next` loop) **and** that
bloodhound-result subscription — so the watcher recognizes a watermark and
produces a task exactly when someone is opted in to receive findings. This is
**decoupled** from ambient recall (which gates on `@ark-recall-result=<S>`,
its own subscription): a session can run the bloodhound (level 3) with no
ambient firehose (level 4). When neither the secretary nor the bloodhound-sub
is present, no watermark is recognized — the assistant's fallback is the
ephemeral `ark-searcher` Hermes spawn (an `ark-search`-skill concern, not this
service's). No config knob: the bloodhound is active exactly when its
subscription is.

## Recognition (watcher)

The watcher's per-append line scan
([seq-recall-watcher.md](../design/seq-recall-watcher.md) flow 1) gains a third
branch, independent of the turn-boundary arm/fire machinery:

- For each `type:"assistant"` line in `newBytes`, extract the text content
  blocks and match `<BLOODHOUND>(.*?)</BLOODHOUND>` (non-greedy, DOTALL so a
  multi-line payload is captured whole). Each match's captured group is one
  **bloodhound payload**.
- Recognition is deterministic — a regex, no language model. It is once-only by
  construction: `newBytes` is the newly-appended slice, so a given assistant
  line is scanned exactly once. Two identical watermarks are two requests.
- The branch does **not** arm, cancel, or interact with `pendingTimer` /
  `armReady` / `pendingChunks`. A watermark dispatches a search task on its own
  schedule; it neither triggers nor suppresses an ambient fire.
- The heavy step (writing the task doc) is posted to the watcher's jobs channel
  so `OnAppend` stays a fast line-scan on the indexer goroutine.

## The bloodhound task doc (on the tube)

For each recognized payload the watcher allocates a **bloodhound id** `<B>`
(a per-session monotonic counter, same family as the fire counter but its own
sequence) and writes a task doc onto the **curate tube** so the secretary's
`next` dispatches it:

- Path: `tmp://ARK-BLOODHOUND/task-<S>-<B>` — the bloodhound's **own tmp://
  namespace**, separate from recall's `ARK-RECALL/`. tmp:// path segments are
  namespaces; giving the bloodhound its own is what keeps its docs from ever
  colliding with recall's, with no naming tricks and recall's paths untouched.
- Header `@ark-secretary-work: <S>` — the same *tag* the secretary already
  subscribes to, so the doc rides the existing tube. The namespace files the
  doc; the **tag** drives delivery — the two are orthogonal, so no new
  subscription is needed.
- Body, in order: the `## Search task <cookie>` first line with the raw payload
  pasted verbatim; a **`## Recall seed`** block (below); then the **search crank
  handle** (SHERLOCK.md "Build-step 2"). The crank handle is self-contained — it
  carries the CLI craft (scope→targets, match-the-matcher, the chat two-pass, the
  tune loop, the `want` emit-branches) so the weak agent executes without
  planning; its first step reads the seed.
- `next` distinguishes the two doc kinds by **namespace** (it scans both and
  prioritizes `ARK-BLOODHOUND/`); the `## Search task` first line tells the
  *agent* it's running a search rather than curating. Nothing about the tube
  (the tag) changes.

The activation-gate backstop applies as it does for curation: if the consumer
dropped, the task is not written — and the seed search is not run.

### The Recall seed (hypergraph-aware start)

Before writing the task doc, the watcher runs the corpus's **deluxe combined
search** on the payload — `Librarian.Recall({Text: payload})`, the same
four-substrate combination (`VectorEC`, `TrigramEC`, `TagVector`, `TagTrigram`)
ambient recall's fire uses — and renders the top candidates into a `## Recall
seed` block placed between the `## Search task` payload and the crank handle.
Each candidate is one compact locator line — `<path>:<range> (<size>) <score>
[tags]` with a short excerpt — carrying the same `<path>:<range>` the crank
handle feeds to `ark chunks --wrap recall` (wrapped so the weak secretary
reads clean text, not JSON), and no chunkid on the wire.

**Per-idea seeding (multi-paragraph clue).** The seed splits the **clue** into
paragraphs — blank-line-separated, via the same markdown chunker the fire path
uses — and passes one `Recall` input per paragraph. `Recall` searches each and
**unions** their hits into one ranked set (its per-chunk score accumulator
already merges multiple inputs), so each distinct idea in a complex clue
contributes its own strong matches. A single-paragraph clue is one input —
identical to before, no regression. This matters because a single `Recall` over
a multi-idea clue builds one query embedding at the *centroid* of the ideas,
matching chunks near none of them; the vector substrate — the tag-axis reach
that is the seed's whole point — is diluted exactly when the clue is richest.
The seed budget scales with the idea count so each paragraph earns
representation rather than starving a fixed pool.

Only the **clue** is embedded as seed input; the `scope`/`depth`/`want` fields
shape the hunt (they drive the crank handle) but are directives, not search
ideas, so they are never folded into the seed search.

This is the point of the bloodhound-over-recall design. The subagent's own
tools reach only content search (FTS + Vector-EC via `ark search`); the
value→chunk **tag axis** (R2905/R2906) is reachable only through `Recall`.
Seeding the task doc gives every hunt a hypergraph-aware starting set it could
not assemble itself — a chunk can seed the hunt because its *tags* match the
clue even when its prose doesn't. The crank handle's first step reads the seed;
the agent runs its own searches only to widen the trail or when the seed is thin.

The seed search is **clue-only and session-agnostic**: the input is the clue
(split per paragraph), nothing else — not the `scope`/`depth`/`want` fields, and
not the conversation. The bloodhound is *pull* — the assistant has already
distilled its need into the clue, so the conversation is not folded back into the
search input. Folding it would pull the hunt off the trail; ambient
recall folds conversation only because its input *is* the conversation. The seed
runs with no discussed-tag exclusion and no derivation side-effects
(`RecallOpts.Session` and `.Propose` left off) — a directed hunt should see
every match, and the seed only reads. If the seed search finds nothing, the doc
carries an empty-seed note and the agent starts from its own searches.

## Dispatch — search ahead of recall (`next`)

`ark connections recall next --session <S> <N>` dispatches a session's pending
docs; the bloodhound adds one ordering rule:

- **Pending bloodhound docs dispatch before pending curation docs** for the
  session — interactive search is served ahead of ambient recall; recall
  backfills the idle time between searches. Within each kind, lowest id first.
- A dispatched bloodhound doc is returned like any doc: the body (the search
  crank handle) plus the close directive. Because the crank handle is in the
  body, `next` needs only to recognize the kind and frame the close verb with
  `<B>`; it does not re-author the instructions.
- Keepalive, context-gate, server-bounce redial, and the foreground window are
  unchanged — a bloodhound doc is just another dispatchable item on the tube.

## The secretary executes (seal + persona)

The sealed Haiku secretary gains the ability to *run* a search task:

- **Seal (guard script).** `recall-agent-guard.sh` permits the **read-only**
  verbs a directed hunt needs — `ark search …`, `ark chunks …`, `ark fetch …`
  (open any indexed file, with no path-approval friction — unlike `cat`, which
  only reaches user-approved paths), and the read-only lookups `ark files …`
  (locate by name) and `ark grams …` (trigram debug). `Read`, `Write`, `Edit`,
  network, and every other verb stay denied as a class — including `ark tag`,
  since bare `tag` would admit the mutating `tag set`. Each denial's stderr
  remains the runway (Fumble Onboarding).
- **Persona.** The secretary recognizes a `## Search task` doc and follows the
  crank handle in its body — it does not need the search *craft* in its persona
  (the crank handle is the craft). Its identity addition is one line: a search
  task is executed, not curated. The curation path is unchanged.
- **Minimap is an enhancement, not a requirement here.** The crank handle's
  explicit scope→targets mapping is enough to execute; loading `/minimap` at
  warm spawn (richer cross-layer navigation) stays a later step.

## The finding (return channel)

A finding is a **directed response** — unlike `surface` / `recommend`, which are
*ambient* (the recall agent volunteers them; the assistant never asked), a
finding answers a search the assistant *specifically called for*. It rides its
**own `@ark-bloodhound-result=<S>` channel** — distinct from recall's
`@ark-recall-result` — but the assistant's single `listen` subscribes to both,
so it's one inbound lane. Findings are their own docs that **pile up** on the
channel and are drained one at a time. Because a finding is a *response to a
request*, it must tie back to that request: the
`## Finding:` header **echoes the originating clue** (below), and the assistant
consumes it as the answer it asked for — folding it into its own reasoning —
rather than as a user-facing suggestion to gate.

The secretary emits results through a new builder verb, sibling to `surface` /
`recommend`:

```
ark connections recall finding <B> -loc <path>:<range> -note "<curated line>"
ark connections recall finding <B> -answer "<1–3 synthesized sentences>" -loc <path>:<range>
ark connections recall close <B> --nonce <N>
```

- **One item per call**, mirroring `surface` — keeps the weak agent's flag
  generation simple. The `want` governs *what* the agent emits, not new verbs:
  `passages` / `pointers` / `inventory` are repeated `-loc` findings (with or
  without `-note`); `answer` / `verdict` is a single `-answer` finding carrying
  the synthesized text plus its source `-loc`.
- For a `-loc` finding the server resolves content/size via `ChunkText(path,
  range)` exactly as `surface` does; no chunkid crosses the wire. **No
  own-session gate** — a directed search may legitimately point at a chunk in
  the requester's own session (unlike pushed recall, which suppresses
  own-session candidates).
- `close <cookie>` writes `tmp://ARK-BLOODHOUND/finding-<S>-<B>` tagged
  `@ark-bloodhound-result: <S>` iff any finding was added (else silent-close),
  removes the `tmp://ARK-BLOODHOUND/task-<S>-<B>` doc, and appends a monitoring
  record — the same close machinery curation uses. The bloodhound's own tmp://
  namespace (`ARK-BLOODHOUND/`, separate from `ARK-RECALL/`) means the two
  independent per-session counters can never collide on a path; the cookie
  carries a kind-marker so the shared `close`/`finding` verbs route to the
  bloodhound's namespace and its own in-flight maps.

### Result doc — the `## Finding:` H2

The result-doc *format* ([simple-recall.md](simple-recall.md) "Result doc
shape") gains a third H2 kind, `## Finding:`, alongside `## Surface:` /
`## Recommend:`. A given doc instance is one kind or the other: a recall fire's
`close` writes a `tmp://ARK-RECALL/result-<S>-<F>` doc of surface/recommend
items; a bloodhound `close` writes a `tmp://ARK-BLOODHOUND/finding-<S>-<B>` doc
of finding items. A recall result carries `@ark-recall-result=<S>`, a bloodhound
finding carries `@ark-bloodhound-result=<S>`; the assistant's `listen` subscribes
to both, so one lane drains both — distinct tags, distinct namespaces, one
consumer.

```
@ark-bloodhound-result: <S>

## Finding: <originating clue, echoed verbatim>

<answer text, when the want is answer/verdict>

- <path>:<range> (<size>) — <curated note>
- <path>:<range> (<size>) — <curated note>
```

The header echoes the **originating clue** — the assistant's own watermark
prose, which the server retains keyed by `<B>` when the watcher mints the task
and stamps into the header at `close`. That verbatim echo is the correlation
handle: the assistant recognizes *this is the answer to the question I asked*,
not an ambient hit it must guess the relevance of. The assistant's `listen`
returns the doc unchanged; it recognizes `## Finding:` (vs the recall H2s),
reads the digest, and **folds it into its own reasoning** (it asked) — distinct
from surface/recommend, which it gates on "should I show the *user* this?".
Delivery is async — the lead simply arrives a later turn.

## Recommend — the directed hunt also curates

A finding is *consumed and evaporates*; the corpus is no denser afterward. A
directed hunt can instead lay down a **persistent connection** — the
highest-value use of the corpus. So the bloodhound emits `## Recommend:`
connecting-tag proposals **alongside** `## Finding:`, promoting a query that
proved its worth into a tag on the chunks it surfaced. This is **Query
Crystallization**: search and tagging are the same act at different
time-horizons, and a query that keeps earning its keep gets promoted into a tag
so the corpus answers it for free thereafter.

**The query is the tag context.** The clue that directed the hunt is exactly
the signal a good tag needs: it steered the search *and* colors the proposed
value. A chunk surfaced by a clue about French-Indian fusion earns `@cuisine:
mostly French, influenced by Indian` — the value carries the query's angle. A
recommend on a directed search is therefore better warranted, and less
intrusive, than an ambient one: the bridge to the user's intent is explicit,
not guessed.

**The deposit lands on the found chunks.** The secretary is given the clue and
the seed chunks, never the current conversation, so it tags only what the hunt
surfaced. (Applying a tag to the *current context* is recall's move — recall
has no query, so its conversation context does that double duty. The bloodhound
is query-pure.)

The flow is **agent-driven** — two LLM passes, not the statistical
find-connections substrate:

1. The **seed** already carries each seed chunk's full tag set
   (`AllTagsForChunk`, the `[tags]` on every `## Recall seed` line), so the
   secretary sees the bridge vocabulary already near the query's angle and can
   avoid redundant proposals.
2. The **secretary** (Haiku) proposes connecting tags along the clue's angle
   for the chunks it holds — skipping a chunk's existing tags, refining an
   existing tag, or coining a new name (its agent definition runs `ark tag defs`
   at the top, so it knows the vocabulary's names *and meanings*). It emits each
   via `ark connections recall recommend`. New tag **names** are in scope; tag
   **definitions** are not (that stays #5, RP/RPE/RR). The secretary is
   **disposition-agnostic** — it does not choose internal versus external, and
   phrases a refinement in English ("replace the existing value with …").
3. `close` flushes the recommends into `ark-bloodhound-result` **alongside** the
   findings (see "Recommends ride the finding stream"). Today a `## Recommend:`
   fired during a directed hunt is dropped; routing it into the bloodhound
   stream is a core fix this feature makes.
4. The **calling assistant** reads the result, folds the finding in, and
   **winnows** the recommends at its discernment gate (`discussed-tags.md`) —
   dropping weak bridges, refining, adding its own. This second pass *is* the
   cap; no fixed N.
5. For each survivor the assistant **authors a durable candidate** — `ark ext
   candidate TARGET tag value --disposition <int|ext> [--replace]` — stamping
   the disposition (asked once per context, default external) and translating a
   secretary's English "replace" intent into `--replace`. The candidate is the
   durable async hand-off: the assistant accepts it on the user's OK, or it
   waits for the Tag Forge or a later `ark ext accept`.

**Proposals are inert.** A `## Recommend:` tag is written back-quoted (see
[simple-recall.md](simple-recall.md), "Result doc shape") so ark never indexes
the *proposal* as a live tag on the doc carrying it. The invariant must hold at
every indexed hop — secretary curation doc, result doc, session — which is the
**Watermark** pattern: the tag rides content already scanned, recognized by the
assistant, never acted on by the indexer.

### Recommends ride the finding stream

Two in-flight doc streams exist per session: the finding stream keyed by the
bloodhound cookie, and the ambient result stream keyed by the fire token. A
`## Recommend:` fired during a **directed** hunt must ride the **finding**
stream so `close` flushes it into `tmp://ARK-BLOODHOUND/finding-<S>-<B>`
alongside the `## Finding:` items, under the `@ark-bloodhound-result=<S>` tag
the assistant already listens on. Without this routing the directed recommend
is left on the ambient stream, which the bloodhound `close` never touches, and
the proposal is silently dropped.

### Opt-out — `<BLOODHOUND notags>`

A bare `<BLOODHOUND>clue</BLOODHOUND>` curates. `<BLOODHOUND notags>` suppresses
the whole tag apparatus for **that hunt**: the watcher omits the `[tags]` from
the seed lines and the crank handle tells the secretary to propose nothing, so
the hunt returns findings only. Plenty of directed searches are mere lookups
where a connection is clutter; `notags` is the escape hatch for them. The
ambient counterpart is `<RECALL notags/>` (see [simple-recall.md](simple-recall.md),
"Opt-out").

## Async only (sync deferred)

Delivery is **fire-and-forget**: the assistant emits the watermark, keeps
reasoning, and the finding surfaces through `listen` in a later turn — the truer
Sherlock. The opt-in **sync** path (watermark + an explicit block on the
specific `<B>` result, for "need it now") is a deliberate non-goal of this
slice; it would need the assistant to hold `<B>` and a blocking fetch keyed on
it. Recorded for a later seam.

## What this slice does not do

- **No sync/blocking delivery.** Async via `listen` only (above).
- **No `/minimap` at warm spawn.** The crank handle's scope mapping suffices;
  the richer navigation primer is a later step.
- **Own result subscription, shared input tube.** The bloodhound's *input*
  rides the shared `@ark-secretary-work` tube (one secretary `next` for all task
  types); its *output* gets its own `@ark-bloodhound-result` subscription, which
  the assistant's `listen` subscribes to and which gates recognition — decoupled
  from ambient recall's `@ark-recall-result` gate.
- **No watcher interaction with the ambient fire.** Recognition is orthogonal
  to arm/cancel/fire; it neither triggers nor suppresses a curation pass.
- **No new tag *definitions*, no secretary-authored RJ writes.** The bloodhound
  now recommends connecting tags (see "Recommend — the directed hunt also
  curates"): new tag *names* are in scope, but definitions stay #5 (RP/RPE/RR).
  The secretary only proposes; the calling assistant winnows and authors the
  durable candidate, so no RJ record is written by the secretary (unchanged from
  simple-recall.md).

## Test strategy

- **Recognition** — `OnAppend` fed `newBytes` containing an assistant line with
  `<BLOODHOUND>…</BLOODHOUND>` writes `tmp://ARK-BLOODHOUND/task-<S>-<B>` with the
  crank handle wrapping the captured payload; an append with no watermark writes
  none.
- **Recall seed** — a dispatched task doc carries a `## Recall seed` block
  between the `## Search task` payload and the crank handle, holding the top
  `Recall` candidates as `<path>:<range> (<size>) <score>` locator lines (no
  chunkid); a payload with no corpus match yields the empty-seed note and the
  task still dispatches.
- **Multi-line payload** — a watermark whose payload spans lines is captured
  whole (DOTALL).
- **Once-only** — the same watermark across two separate appends yields two
  tasks; within one append, one.
- **Independence from fire** — a watermark with no `turn_duration` still
  dispatches a task; it does not arm or cancel `pendingTimer`, and does not
  alter `pendingChunks`.
- **Bloodhound gate (decoupled)** — with no `@ark-bloodhound-result=<S>`
  subscriber, a watermark writes no task doc *even if* `@ark-recall-result` is
  subscribed; with the bloodhound-sub present, it does. Independent of the
  ambient gate.
- **Dispatch priority** — with both a pending curation doc and a pending
  bloodhound doc, `next` returns the bloodhound doc first; lowest id within kind.
- **Seal** — the secretary running `ark search` / `ark chunks` is permitted;
  `Read` of an arbitrary path, `ark fetch`, and mutating verbs are still denied,
  and the denial stderr names the loop driver.
- **finding → result** — a `-loc` finding then `close` emits a `## Finding:`
  result doc tagged `@ark-bloodhound-result=<S>`; `-answer` carries synthesized
  text; `close` with no prior finding is a silent-close that still removes the
  task doc and logs.
- **No own-session gate on finding** — a finding whose `-loc` resolves to the
  requester's own session is accepted (unlike `surface`).

## Sequencing

Depends on (all landed): Simple Recall watcher + secretary + builder verbs
([simple-recall.md](simple-recall.md)), the result-doc `listen` consumer, the
`ark-search` skill + finalized search crank handle (SHERLOCK.md "Build-step 2").

On landing, flip the `ark-search` skill's "forthcoming (2026-06-08)" marker on
the `<BLOODHOUND>` section — the path is now live.

Independent of: `ark-usage` trim and the `ark → /ark-search` wiring (step 4),
`/minimap` at warm spawn (step 4), and any sync-delivery seam.
