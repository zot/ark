# Carve: the Tag Forge as a review console

**Opened** 2026-07-25 · **Status** open — the Go substrate landed, the UI has
not started · **Items** #67 (landed, `aea5e9e`), #9, #68, #69, #70

The Tag Forge is where a person works on their tags. This carve works out how
it also becomes where they *review* what agents have proposed, and how that job
splits into pieces one session can hold. It was opened because PENDING #9's own
note declared the item stale as written.

Blurry-first: the doc sharpens only the edges concrete work has forced, and
deliberately leaves the rest blurry.

Lineage, in the project's private working notes: FORGE-UPDATE.md §"The sidecar
is obsolete" (what killed the original shape of #9), TAG-ENRICHMENT.md gap 2
(the review queue was designed in 2026-07 and never built), and
CONTEXTUAL-RECALL.md:237 (the original intent, where the Forge surfaces
high-tally proposals first and a human always approves).

@forge-review-console: the Tag Forge presents the ext-candidate ledger for human accept/reject/edit

## Where #9 came from, and what killed it

#9 was written when proposals arrived from the **ark-connections
sidecar**: the app called `mcp.findConnections`, a Haiku consumer read
`tmp://connections/<id>.md`, posted results back, and curation.lua
parsed `## Themes` / `## Shared Tag Candidates` into widgets the human
staged, reverted, and accepted in a batch.

Every piece of that is gone. #37 rerouted the forge through the
ext-candidate machinery and deleted the Find Connections button plus the
staging loop; #65 replaced the approval shims with real one-shot hooks.
The sidecar's role went to the luhmann secretary pool. So the forge
today has a working trigger and no ammunition: widgets fire correctly,
but the only thing that fills one is a human typing into it.

## What is real today (post-#37, post-#65)

**Write side — complete.** `ark ext candidate|accept|reject|add|set|
remove`, four accept cells (internal/external × add/replace) with
degrade, internal-remove, durable signed judgments. From Lua:
`mcp.extAccept(targetSpec, tag, value, opts)` and
`mcp.extRemove(targetSpec, tag, opts)`, both resolving TARGET specs
server-side on the DB actor.

**Ledger — durable and consuming.** Candidates live as
`@ext-candidate` lines in mirror files, carrying `insight`, `@count`
tally, disposition, and the replace token. Accept rewrites the line to
`@ext`; reject rewrites it to `@ext-judgment` with a signed `@count`.
So *pending* is not a computed state: the set of pending proposals is
literally the set of candidate lines on disk.

**Derived read substrate — built, unexposed.** `@ext-candidate` → RC
records (#22 Pass B+C). `Store.DerivedProposals(chunkID)` reads a
chunk's proposals through the extmap; `ScanAllDerivedCandidates` walks
the corpus. Neither has a CLI verb or a Lua binding.

**Forge UI — pinned cards with loaded-gun widgets.** Canonical pinned
set is Go-side (`sys.curation.pinned`, stale pins pruned at load since
#65). Each `Ark.PinnedChunk` carries a `_widgets` stack of
`Ark.ProposalWidget`s; each widget is one tag operation with fire ahead
of its fields. Tag authoring is fully detached from CM6, which now
serves content edits only.

## What the console needs that does not exist

1. **A pending-candidate query, exposed.** CLI verb plus Lua binding.
   Substrate exists both ways (per-chunk via `DerivedProposals`,
   corpus-wide via `ScanAllDerivedCandidates`), plus the unresolved set
   in `ExtMap.unresolvedTargets`; nothing surfaces any of it.
2. **`mcp.extReject`.** #65 landed accept and remove. Reject is a
   distinct verb with its own ledger effect (signed judgment), and a
   review console without a reject gesture is a rubber stamp.

Everything else the console needs, it already has.

## The carve

### 1. Which ledger does the console read?

**Correcting an earlier draft of this doc.** It argued the console must
walk mirror-file lines because RC records go stale, citing
TAG-ENRICHMENT gap 4. That gap note (2026-07-07) predates the work that
fixed it, and the underlying chunk model in the argument was wrong.

The right model of chunk identity (Bill, 2026-07-25): chunks are
**deduplicated by hash and may occur in more than one file**, so
editing a file does not necessarily replace a chunk — it can simply add
one. An altered chunk that kept its tags and values (each identified by
tvid) mints a new chunk whose ID joins the V records of every one of
those tvids. The lifecycle axis that matters for derived records is
therefore the **tvid**, not the chunkID:

> When a tvid disappears, every associated record must be changed or
> removed, depending on where the tvid sits. **In the key (remove):**
> EV, RC, RJ, V, X. **In the value (change):** F, X.

Against that model, RC holds up. Verified in code:

- **Source axis.** A candidate's `source_tvid` is the tvid of the
  `@ext-candidate` line's (tag, value) pair, so each distinct line has
  its own. Accept and reject both rewrite the line, retiring the tvid;
  `ExtMap.cleanupDerivedSource` (R3064) strikes that source's RC/RJ
  records and reverse-lookup entries. No phantom pending rows.
- **Target axis.** `ExtMap.applyDerivedReresolve` (R3062–R3064, R3074)
  re-resolves a candidate when its **target file reindexes**, writing
  RC for newly-resolved targets and deleting for vanished ones. RC
  follows the TARGET spec's resolution rather than depending on chunkID
  stability, which is what makes chunk churn a non-issue.

**Revised lean: read RC.** It is a maintained materialized view of the
pending set — a prefix scan instead of a file walk, and `Store.
DerivedProposals(chunkID)` already serves the per-chunk shape.

**The real coverage hole is different: unresolved targets.** A
candidate whose TARGET currently resolves to nothing has no RC record
at all (`ExtMap.unresolvedTargets` tracks those tvids separately). An
RC-only query silently hides them, and they are the rows the human most
needs, since a proposal that cannot find its target is a proposal
nobody will ever judge. Two causes, both worth surfacing: a typo in the
TARGET when the candidate was authored, and a corpus change that moved
the content out from under a once-good target.

**DECIDED (Bill, 2026-07-25): plug the hole with a zero target id.**
Write `RC[source_tvid][0]` for a candidate that resolves to nothing, so
unresolved proposals ride the same prefix scan as resolved ones and no
console can accidentally omit them. Zero is safe as a sentinel — it is
already the not-found chunkID elsewhere (`db.go:1806`,
`server.go:3967`) — but the pass should confirm nothing allocates chunk
0 before relying on it.

Touch points for that change: the initial resolve and the reresolve
both already know when a target set comes back empty
(`ExtMap.unresolvedTargets` is set at extmap.go:599, :927, :1252 and
cleared at :675, :913, :1041, :1138, :1308). `applyDerivedReresolve`
deletes RC when `newScoped` empties; under the zero-target design it
writes `RC[tvid][0]` instead, and clears it when a real target
resolves.

Open sub-question: does the query need per-chunk *and* corpus-wide
shapes, or does corpus-wide with a target filter cover both?

Unverified, noted in passing: I could find no per-tvid retirement sweep
for **EV** (only backfill-missing and the bulk `DropEmbeddings`), so a
retired tvid's embedding may linger. Space leak rather than a
correctness bug, since tvids are never reused. Worth a separate look,
not this pass.

### 2. Scope — pinned-set review, or corpus inbox?

The pinned set is the human's working set. The ledger is corpus-wide.
An agent that proposes a tag on a chunk the human never pinned is
invisible under a per-card model, which is the whole point of agents
authoring cross-session.

Three shapes, not mutually exclusive:

- **(a) Per-card.** Candidates for a pinned chunk hydrate that card's
  widget stack. Cheapest, fits the existing UI exactly, misses
  everything unpinned.
- **(b) Inbox panel.** A corpus-wide pending list, ranked, with
  accept/reject/edit per row. New surface. Answers "what is waiting for
  me."
- **(c) Inbox that pins.** Rows in the inbox; acting on one pins its
  chunk and hydrates a card, so review runs through the machinery that
  already works.

**DECIDED (Bill, 2026-07-25): (a) then (c).** (a) is nearly free once
the query exists and proves the hydration path end to end. (c) reuses
the card as the review unit rather than building a second accept
surface. Whether (b) exists as its own thing or is just (c)'s left rail
is a layout question, not an architecture one.

**And it is a tab, not a view.** The Tag Forge is *where the user works
on their tags*, so the pending queue and the rejected-proposals list
belong inside it as tabs rather than as sibling views in the shell.
That settles #12's home too (see below).

### 3. Row → widget hydration

A candidate line carries exactly what a `ProposalWidget` holds: tag,
value, disposition, replace token, plus `insight` and `@count` that the
widget has no field for. If a row prefills a widget, review becomes
"the gun arrives loaded; adjust and fire," and machine proposals and
hand-typed ones travel the same path.

The open question was what an *edited* hydrated widget should do to the
line it came from. Left alone, the machinery leaks: accept matches on
`(target, tag, value)`, so firing an edited value commits a new
candidate and the original sits pending with nobody to judge it.

**DECIDED (Bill, 2026-07-25): two kinds of accept.** A widget's work is
tentative until the user accepts it, and the candidate line is now the
natural home for tentative work, so a widget fires one of two ways:

- **Amend** — rewrite the `@ext-candidate` line to match the widget.
  The proposal stays pending, revised. No orphan, and the human's
  half-finished curation becomes durable without being committed.
- **Commit** — today's accept. Candidate becomes `@ext` or an inline
  tag, with the positive judgment.

This generalizes past hydrated rows: a hand-typed widget can amend into
existence, which makes "save my thinking, decide later" a first-class
gesture instead of a lost draft. Every widget grows the same pair.

Machinery gap this opens: **there is no amend verb.** `ark ext
candidate` writes by identity and bumps `@count` on an exact repeat, so
amending means retiring the old line and writing the new one as a
single operation. Goes in the Go pass alongside the query and
`mcp.extReject`.

Open, for the pass to settle: does an amended line carry its `@count`
tally forward or reset (the tally measures agreement on *that*
proposal, and the human just changed the proposal), and is `insight`
editable by the human or reserved to whoever authored it? Also still
wanted regardless: a read-only insight and tally display on hydrated
widgets, since the human is judging the why.

### 4. Ranking

CONTEXTUAL-RECALL's intent was "high-tally proposals first." `@count`
is on the line already. Recency and insight presence are the other
obvious axes. Cheap to defer: ship with tally-descending, revisit when
there is a real ledger to rank.

**The part that is not deferrable belongs to #67:** where ordering
lives. A corpus-wide query that can outgrow a screenful has to rank and
limit server-side, because the caller cannot sort what it never
fetched. That ties to the per-chunk-vs-corpus shape in carve 1, so
settle both together. Zero-target rows need explicit handling either
way — an unresolved candidate must not sink to the bottom of a tally
sort, since it is the row most likely to need a human.

### 5. What stays blurry

**The supply chain.** How the luhmann secretary pool authors proposals,
what triggers it, whether pool output lands as `@ext-candidate` lines
or something else. The console reads the ledger regardless of who fills
it, and agents can author through the CLI today. Carve when the pool's
shape is in front of us.

## Falls out of this carve

**O112's premise is dead.** The gap reads "apps/ark/curation.lua still
parses legacy `## Themes` / `## Shared Tag Candidates` sections." It
does not — #37/#65 left zero references. So the migration O112 waits on
has already happened by demolition. What remains is the *server* half:
R2597's dual emission in `connections.go:642`, which is the dead
sidecar's code. O112 should be re-evaluated as part of the
connections-sidecar teardown (#26's widening question), not as UI work.

**TAG-ENRICHMENT gap 4 is stale and should be marked so.** It reads
"RC/RJ/RF not updated on chunk replacement (verified)" as of
2026-07-07, but the ext family grew its own maintenance since:
`cleanupDerivedSource` on the source axis, `applyDerivedReresolve` on
the target axis. RF was not checked here and may still be genuinely
uncovered. Someone reading that doc today draws a wrong conclusion
about RC and RJ, which is exactly what happened while writing this one.

**#12 overlap — resolved.** The rejected-proposals view (revisit / zero
/ bump the RJ counter) is this console with a different filter, and
since the forge is where tag work happens, it lands as a sibling tab
rather than its own item. #12's monitor-view half is unrelated and
stays where it is.

**RC doc drift.** RC was DB-only before `@ext-candidate` existed, and
the reshaping left false prose behind. Two confirmed already:
`specs/recall.md`:196 says `--propose` "persist[s] surviving candidates
as RC records" and `specs/simple-recall.md`:184 says RC records "accrue
on every per-chunk Recall call" — both contradict R3079, where the pass
is compute-only and the *calling agent* authors durable candidates
(crc-Librarian.md:305). Twenty spec and design files mention RC;
`derived-tags.md` and `crc-Store.md` read as already reconciled, so
this is an audit with a handful of real corrections, not a rewrite.
Own PENDING item, since correcting spec prose is requirement
reconciliation.

## Pending split (settled)

`#9` keeps its ID and becomes the UI item; the Go work goes above it.

1. ~~**#67 `/mini-spec`** — expose the ledger.~~ **DONE 2026-07-26**, git
   `aea5e9e` / fossil `01c46fb8`. R3252–R3262: `Store.PendingCandidates` with
   server-side ranking, the `RC[tvid][0]` unattached sentinel,
   `DB.AmendExtCandidate`, `ark ext pending` / `ark ext amend`,
   `GET /ext/pending` / `POST /ext/amend`, and
   `mcp.extReject`/`extAmend`/`extPending`. Verified live end to end.
   **Two open questions from carve 3 were settled by implementation:** an
   amended line does **not** carry its `@count` forward (a new identity starts
   at 1; a collision merges into the existing line's tally), and `insight` **is**
   editable by the reviewer, but silence preserves the author's rather than
   discarding it.
   **Not verified: the three Lua bindings** — reaching a registered binding
   needs `srv.uiRuntime`. Banked as O174; #9 is what will exercise them.
   The first live test of this machinery found three unrelated defects,
   recorded in the private note EXT-FIRST-LIGHT-20260726.md.

   **Where these decisions live now.** `design/requirements.md` carries them as
   requirements: R3254 the zero-target sentinel, R3253 the server-side ranking,
   R3255 the amend identity-and-tally rule. The requirements say what the system
   does; this carve says why it was chosen and what was rejected on the way.
2. **#9 `/ui-thorough`** — the review console as a Tag Forge tab.
   Hydration into the widget stack, amend/commit pair, the pending and
   rejected tabs, ranking.

Everything else in the old #9 body (ranked candidates, substrate
badges, turbo affordances, the `## Proposals` body-section migration)
is either absorbed above or belongs to O112's re-evaluation.

Spun out as their own items, all `/mini-spec`:

- **#68** — RC doc-drift audit (above).
- **#69** — tvid and chunkid cleanup audit across the record classes,
  against the invariant in carve 1. Covers what TAG-ENRICHMENT gap 4
  half-saw, including RF, which was never re-checked.
- **#70** — EV cleanup on tvid removal.
