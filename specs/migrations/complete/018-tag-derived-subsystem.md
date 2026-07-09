# Migration: the tag-derived record subsystem (RC/RJ re-key + producer inversion)

**Status:** in-flight. Folds PENDING #22 Pass B and Pass C into one pass
(Bill, 2026-07-08: "fold C into B" — one clean RC/RJ shape, no staged
coexistence). Authoritative model:
[.scratch/TAG-ENRICHMENT.md](../../.scratch/TAG-ENRICHMENT.md) "SETTLED MODEL
(2026-07-08)". Predecessor: Pass A (commit `e327ca9`) landed the `@ext-candidate`
/ `@ext-judgment` file-tag authoring verbs.

## Problem (state A)

Two record classes carry irreplaceable curation signal yet live only in the
disposable bbolt index, keyed to a chunk that can vanish under them:

- **RC** (recall candidate) — `"RC" + chunkid + tagname → tally`. Written by
  `runDerivationPass` (the statistical `--propose` pass), read by
  `enrichProposedTags` / `DerivedProposals`.
- **RJ** (recall judgment, signed v3) — `"RJ" + chunkid + tagname → score +
  nanos`. Written by `Store.RejectDerived`, the veto half of the accept loop.

Three consequences fall out of that shape:

1. **Stranded on re-chunk (gap #4).** RC/RJ are keyed by chunkid, but the
   chunkid-orphan callback (`cleanupOrphans`) cleans EC, F/V/T, and `@ext`
   X-records — never RC/RJ/RF. An edit that re-chunks a target orphans the old
   chunkid and mints a new one; the judgment is stranded on a dead chunkid and
   the new chunk carries none. `record-formats.md` claims RF orphans are cleaned
   via the callback; that is doc drift (unimplemented).
2. **Judgment is disposable.** RJ is the most expensive artifact in the system
   (human/agent discernment) and it lives only in the rebuildable index,
   violating ark's file-is-truth / index-is-disposable principle.
3. **The accept loop is open.** `AcceptDerived` exists but is unwired; `ark ext
   add` applies a tag without retiring the RC that proposed it. The pool only
   grows or is vetoed.

Pass A gave us the durable, file-backed source of truth — `@ext-candidate` and
`@ext-judgment` tags in the mirror tree — plus the verbs that author them. But
the indexer does not yet *derive* anything from them: `collectIndexExtPlans`
(indexer.go:431) skips every tag that is not `@ext`, so the two new classes
index as ordinary tags and produce no derived records.

## Target (state B)

The tag-derived record subsystem: X, RC, RJ are one family, each derived from a
source-of-truth tag, keyed identically, riding one lifecycle.

| Source tag        | Derived record | Value                              |
|-------------------|----------------|------------------------------------|
| `@ext`            | X              | packed routed_tvids (+ live V edge)|
| `@ext-candidate`  | RC             | tally (deferred; written `1`)      |
| `@ext-judgment`   | RJ             | signed-varint score + 8-byte nanos |

- **Uniform key:** `<class-letter> + source_tvid + target_chunkid`. RC/RJ shed
  the old `chunkid + tagname` key entirely.
- **Tagname and value are not stored** in RC/RJ — both are recovered from the
  source tvid: `TvidMap.Resolve(source_tvid)` → the `@ext-candidate` /
  `@ext-judgment` value → `ParseExtTarget` → the routed tag (and value). Exactly
  how X avoids keying by its routed tag.
- **RC/RJ are lighter than X.** Only `@ext` writes the routed tag's live V edge
  (`V[cuisine][tex-mex]`) and bumps `virtualTagCount`. Candidate and judgment
  write their derived record *instead of* the edge, never the edge itself. That
  one line is the entire proposed-vs-committed-vs-judged distinction. RC/RJ are
  persistent-only (no overlay branch this pass — see Open Questions).
- **Shared lifecycle, inherited from the `@ext` machinery:** index-time
  derivation (the generalized `collectIndexExtPlans` → `ExtMap`), re-resolution
  on reindex (`ReresolveOnReindex`), source-side cleanup on orphan
  (`CleanupSource` via the source F-record tvid trailer), rebuild at startup.
  Keying by the source tvid is what dissolves gap #4: RC/RJ re-resolve and clean
  up through the same path as X, so they are no longer islands keyed to a target
  chunk that can vanish.

`ExtMap` generalizes from "the `@ext` router" to "the tag-derived routing map,"
branching only at the write step by class.

### Mutable counts live in `@count` (file-backed, not a bbolt counter)

The repetition tally (RC) and the graded judgment magnitude (RJ) are mutable
accumulators. They live in an `@count` field on the file line and are
materialized into the derived record's value:

- `@ext-candidate: TARGET @insight: "why" @tag: v @count: 3` → RC tally `3`.
- `@ext-judgment: TARGET @tag: @count: -2` → RJ score `-2` (net-rejected twice).
  The field is **signed** so one axis carries both directions: negative is the
  rejection magnitude, positive is the reinforcement/popularity tally that a
  future producer (seam 3b) writes. A `@count` reaching `0` removes the judgment
  (absent ≡ neutral).

`@count` is a reserved metadata field — `ParseExtTarget` drops it from the
routed-tag list (as it does `@insight`) but **keeps it in the tag's value
string**. So the outer tag's V record mirrors the file line faithfully: the index
is a performance mirror of the source, never a filtered view (Bill, 2026-07-08).
The consequence is accepted: because `@count` is part of the tvid-bearing value, a
bump changes the source tvid, and the source-chunk reindex cleans the old-count
derived record (`CleanupSource`) and derives the new one with the new count. The
count survives the churn because the file line is the source of truth; bbolt RC/RJ
hold only a cached copy.

A bump is a read-modify-write on the mirror line, run as one closure-actor
operation (`svc`, R986) so concurrent producers cannot lose an update.
`CandidateExtTag` on an exact-identity duplicate increments `@count` (Pass A's
no-op flips to a bump); `RejectExtTag` creates or decrements the signed `@count`.

### In-memory maps (reads move off bbolt scans)

Two derived maps, maintained fresh on index / reindex / cleanup and rebuilt at
startup, in the pattern of `routedTagsByTvidExt`:

- **`candidateSourcesByChunk[target_chunkid] → []source_tvid`** — the reverse
  lookup "candidates proposed for chunk C." Replaces the RC bbolt prefix scan.
  `enrichProposedTags` / `DerivedProposals` read this, then `Resolve` +
  `ParseExtTarget` each source tvid to recover `(tagname, value)`.
- **`rejectByChunk[target_chunkid][tagname] → score`** — the reject filter's
  question is "is tag T net-rejected on chunk C." With RJ keyed
  `source_tvid + chunkid`, that is not a direct key lookup, so the subsystem
  keeps this map; "is T rejected on C" becomes one hit with the negative-score
  check riding along. No bbolt touch on the hot proposal path.

### Producer inversion (the folded-in Pass C)

Every producer authors file tags; bbolt RC/RJ demote to a derived index of them.

- **`runDerivationPass`** stops writing bbolt RC directly. Its surviving
  statistical candidates are authored as `@ext-candidate` file tags through a
  single producer path (the same mirror authoring Pass A built for `ark ext
  candidate`); the indexer then derives RC. A bare RC (tvid + chunkid, no
  tag/value) is meaningless without a source line, so authoring is the only
  option once RC is tvid-keyed. To keep proposals visible in the same
  `--propose` call (not only after the async watcher pass), the pass then
  **synchronously materializes**: still on the actor, it reindexes each distinct
  touched mirror once via `DB.syncOnePath`, deriving the RC before
  `enrichProposedTags` reads it. Embedding stays deferred, so the sync is just
  the FTS + tag + derive of tiny mirror files.
- **`Store.RejectDerived`** stops writing bbolt RJ directly and authors an
  `@ext-judgment` file tag (negative score on reject; the reinforce direction is
  schema-ready but has no producer — seam 3b).
- **The reject filter** (skip already-rejected `(chunk, tag)`) lifts out of the
  statistical pass and reads `rejectByChunk`, so every producer inherits it.
- **The accept loop closes by construction.** `ark ext accept` (Pass A) rewrites
  `@ext-candidate` → `@ext`; on reindex the RC derivation drops and the X + V
  edge lands. Committing a tag consumes its candidate with no separate
  `AcceptDerived` bbolt dance. `Store.AcceptDerived` is **re-homed to the mirror
  path** — it delegates to `DB.AcceptExtTag` rather than deleting RC and
  attaching directly — surviving as the file-backed accept primitive the forge
  wiring will call (symmetric with the inverted `Store.RejectDerived`).

### The freshness gate stays

RF (`"RF" + chunkid → max ED serial`) is a pure skip-optimization for the
statistical scoring pass and is legitimately disposable. It keeps its chunkid
key and its role: `runDerivationPass` still computes what to propose and still
skips fresh chunks; only what it does with a survivor changes (author a file tag,
not write bbolt RC). RD / RM (session-scoped throttles) are untouched.

## Migration mechanics

The old RC/RJ key shape (`chunkid + tagname`) is structurally incompatible with
the new (`source_tvid + target_chunkid`) — a prefix scan for one would
mis-hit the other. There is no in-place re-key:

1. **Clear** the old RC/RJ records. Per `record-formats.md`, the RJ v-bump is
   already handled by `ark connections clean -all -checkpoint` + rebuild; the
   same wipe clears RC. Old records carry no recoverable source tvid, so they
   cannot be salvaged into the new shape — they re-derive from the file tags
   (`@ext-candidate` / `@ext-judgment`) that now back them.
2. **Rebuild** repopulates X, RC, RJ and the in-memory maps from the mirror-tree
   file tags via the generalized index path.
3. **Bump** the record-format doc (RC/RJ key + value, the in-memory maps, the
   dissolved gap-#4 cleanup claim).

No user-authored `@ext-candidate` / `@ext-judgment` exist in the corpus at
migration time beyond Pass A's own test fixtures, so the clear-and-rederive
loses no durable signal. Legacy bbolt RC/RJ that predate the file-tag backing
are the disposable half by definition (state A stored them nowhere else).

## Specs to fold into on completion (state B is current truth)

- `record-formats.md` — RC/RJ key + value shapes; the two in-memory maps; retire
  the "RF orphan cleaned via callback" claim or make it true; note X/RC/RJ as one
  keyed family.
- `derived-tags.md` — the derivation pass now authors `@ext-candidate` instead of
  writing RC; the accept/reject Store API section (`AcceptDerived` and
  `RejectDerived` both re-homed to author via the mirror path — `DB.AcceptExtTag` /
  `DB.RejectExtTag`); the RC-as-tally storage section.
- `at-ext-storage.md` — `ExtMap` is the tag-derived map, not the `@ext`-only
  router; the class branch at the write step.
- `simple-recall.md` — RJ producer path (file-backed) and the reject-filter map.
- `curation-workshop-primitives.md` — accept closes the loop by construction.

## Resolved during design (2026-07-08, Bill)

- **Mutable-count home = `@count` file field** (not a bbolt counter, not a
  deferral). See "Mutable counts live in `@count`" above. The tvid churn on bump
  is accepted in exchange for a faithful V mirror. This keeps the RC tally and RJ
  magnitude, so `DerivedProposals` orders by tally again.
- **No peel.** The count stays in the indexed value; V mirrors the file
  byte-for-byte. The index is for performance and must reflect the source.

## Open questions (banked, not solved this pass)

- **Overlay (`tmp://`) candidates.** X supports overlay routings
  (`overlayRoutings` / `overlayValues`). RC/RJ are persistent-only this pass;
  proposing a candidate onto a `tmp://` chunk is out of scope until a use case
  appears.

## Out of scope (separate items)

- **#22 Layer 2** — the recall watcher injecting live-conversation chunks
  (source seed + recent N turns) as `tag-only` candidates so the conversation
  enters the hypergraph. This pass builds the subsystem that Layer 2 will
  produce through; the watcher wiring is its own pass.
  ([.scratch/RECALL-SOURCE-TAGS.md](../../.scratch/RECALL-SOURCE-TAGS.md)).
- **Forge harvest UI** — surfacing RC proposals for deliberate review (#9, #12).
- **Reinforcement producer** — the positive `@ext-judgment` direction (seam 3b,
  #18).
- **The one-door stencil for weak-model producers** — levels 2/3 (secretary,
  parent agent) filling a proposal stencil. This pass inverts the level-1
  statistical producer and the reject verb; the stencil rides the same authoring
  path later.
