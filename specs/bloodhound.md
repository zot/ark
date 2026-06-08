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

## Precondition: recall must be running

The bloodhound is the warm path, and warmth comes from recall already standing
(`/recall` running its secretary + the assistant's `listen`). Recognition lives
in the watcher and is therefore subject to the **same activation gate** as
ambient recall: a session is processed only while *both* the secretary
(`@ark-recall-curate=<S>`) and a result consumer (`@ark-recall-result=<S>`) are
subscribed. When recall is off, no watermark is recognized — the assistant's
fallback is the ephemeral `ark-searcher` Hermes spawn (an `ark-search`-skill
concern, not this service's). No new config knob: the bloodhound is active
exactly when the watcher is (`[recall].enabled` + both ends subscribed).

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
- Header `@ark-recall-curate: <S>` — the same *tag* the secretary already
  subscribes to, so the doc rides the existing tube. The namespace files the
  doc; the **tag** drives delivery — the two are orthogonal, so no new
  subscription is needed.
- Body: the **search crank handle** (SHERLOCK.md "Build-step 2") with the raw
  payload pasted verbatim under a `## Search task <cookie>` first line. The crank
  handle is self-contained — it carries the CLI craft (scope→targets,
  match-the-matcher, the chat two-pass, the tune loop, the `want` emit-branches)
  so the weak agent executes without planning.
- `next` distinguishes the two doc kinds by **namespace** (it scans both and
  prioritizes `ARK-BLOODHOUND/`); the `## Search task` first line tells the
  *agent* it's running a search rather than curating. Nothing about the tube
  (the tag) changes.

The activation-gate backstop applies as it does for curation: if the consumer
dropped, the task is not written.

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
  search verbs the crank handle uses — `ark search …`, `ark chunks …`, and the
  read-only `tag` / `files` / `grams` lookups. `Read`, `Write`, `Edit`,
  network, and every mutating verb stay denied as a class. Each denial's stderr
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
finding answers a search the assistant *specifically called for*. It rides the
same **`@ark-recall-result=<S>` channel** and the same `listen` (the assistant's
one inbound lane), but nothing is *reused* at the doc level: like recall result
docs, findings are their own docs that **pile up** on the channel and are
drained one at a time — the channel is shared, the docs are not. Because a
finding is a *response to a request*, it must tie back to that request: the
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
  `@ark-recall-result: <S>` iff any finding was added (else silent-close),
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
of finding items. Both carry `@ark-recall-result=<S>`, so one `listen` drains
both — the namespace files them apart, the shared tag delivers them together.

```
@ark-recall-result: <S>

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
- **No new tube or subscription.** Bloodhound rides `@ark-recall-curate` /
  `@ark-recall-result`; the assistant adds no new `listen`.
- **No watcher interaction with the ambient fire.** Recognition is orthogonal
  to arm/cancel/fire; it neither triggers nor suppresses a curation pass.
- **No new-tag invention, no RJ writes** — inherited non-goals from
  simple-recall.md; the bloodhound only searches and reports.

## Test strategy

- **Recognition** — `OnAppend` fed `newBytes` containing an assistant line with
  `<BLOODHOUND>…</BLOODHOUND>` writes `tmp://ARK-BLOODHOUND/task-<S>-<B>` with the
  crank handle wrapping the captured payload; an append with no watermark writes
  none.
- **Multi-line payload** — a watermark whose payload spans lines is captured
  whole (DOTALL).
- **Once-only** — the same watermark across two separate appends yields two
  tasks; within one append, one.
- **Independence from fire** — a watermark with no `turn_duration` still
  dispatches a task; it does not arm or cancel `pendingTimer`, and does not
  alter `pendingChunks`.
- **Activation gate** — with no `@ark-recall-result=<S>` subscriber, a watermark
  writes no task doc (gate parity with curation).
- **Dispatch priority** — with both a pending curation doc and a pending
  bloodhound doc, `next` returns the bloodhound doc first; lowest id within kind.
- **Seal** — the secretary running `ark search` / `ark chunks` is permitted;
  `Read` of an arbitrary path, `ark fetch`, and mutating verbs are still denied,
  and the denial stderr names the loop driver.
- **finding → result** — a `-loc` finding then `close` emits a `## Finding:`
  result doc tagged `@ark-recall-result=<S>`; `-answer` carries synthesized
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
