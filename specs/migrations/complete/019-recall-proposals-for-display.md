# Migration: recall proposals for display (discernment-gated authoring)

**Status:** in-flight (opened 2026-07-11). PENDING #36. Reverses the *autonomous
producer* landed by migration 018 (Pass B+C); the record-family substrate that
018 built survives intact. Design model:
[.scratch/RECALL-SOURCE-TAGS.md](../../.scratch/RECALL-SOURCE-TAGS.md). Decided
with Bill 2026-07-10 (no autonomous authoring) + 2026-07-11 (this-call-only
first; minimal scope).

## Problem (state A)

Migration 018 made the recall `--propose` pass an **autonomous author**: for
each surviving statistical candidate, `runDerivationPass` authors an
`@ext-candidate` file tag via `DB.CandidateExtTag`, stamps RF freshness, and
synchronously reindexes each touched mirror so the RC record derives and
`enrichProposedTags` surfaces it in the same call (R3068, R3072, R3076). Curation
accrues passively — "every recall call leaves curation footprints"
(derived-tags.md thesis).

Two layers in the recall path lack the discernment that durable authoring
demands:

- **the watcher** — blind cosine statistics; no conversational context;
- **the recall secretary** — a weak, context-isolated Haiku.

Neither should write durable `@ext-candidate`s into the corpus. Authority must
match discernment (companion to Haiku-delegation-bounds and the Secretary
pattern). The autonomous pass writes them anyway, on cosine alone.

A second gap: the live conversation drives recall as the *query* but never earns
proposals of its own. The source seed is excluded by A66 self-exclusion; the
recent-N turns are a transcript with no chunk IDs. The conversation enters the
hypergraph's query, never its candidate set (`scoresMap`).

## Target (state B)

The `--propose` pass **computes proposals for display only**. The sole author of
durable `@ext-candidate`s is a *discerning approver*: the **calling agent** (full
live-conversation context) via `ark ext candidate` (R3075), or — when no agent is
connected (Tag Forge, #37) — the **human**. The watcher and secretary only
*propose* into the ephemeral tmp:// curation/result doc.

Concretely:

- **The pass computes, returns, authors nothing.** `runDerivationPass` keeps its
  candidate computation unchanged (cosine vs ED+EV, top-N, floor, already-on and
  net-rejected filters — R2670–R2672, R2742, R2911). It drops the write+
  materialize half: no `CandidateExtTag`, no RF stamp, no `syncOnePath` reindex.
  It returns the per-chunk computed proposals transiently for this call.
- **Enrich reads the transient compute.** `enrichProposedTags` populates each
  surfaced chunk's `ProposedTags`/`ProposedTagScores` from this call's computed
  proposals, not from `Store.DerivedProposals`. Ordering is unchanged
  (chunk-EC ↔ tag similarity desc; `selectCandidates` already returns that order).
- **RF dies.** RF's only writer was the pass. Removing the write forces removing
  the read-skip too — leftover RF stamps from prior autonomous runs would else
  wrongly skip chunks and show no proposals. The pass no longer touches RF.
- **The conversation earns proposals (#36 core).** The recall call gains an
  optional live-conversation chunk-ID set (source seed + recent-N turn chunk IDs),
  defaulted empty. The watcher populates it; the bloodhound seed leaves it empty.
  These chunks fold into the compute with A66 self-exclusion **bypassed** (they
  are the query, but we *want* proposals on them) and marked `tag-only` (R2869),
  so they earn proposals but are never surfaced back to the reader (redundant with
  the reader's live context).

### What 018's substrate keeps (unchanged)

- **RC/RJ record family** — still derived by `collectIndexExtPlans` (R3061) from
  the `@ext-candidate`/`@ext-judgment` mirror lines the *calling agent* authors.
  Keying (R3058–R3060), lifecycle (R3064), in-memory maps (R3065/R3066), `@count`
  materialization (R3074/R3075) all stand.
- **`Store.DerivedProposals`** — survives as the **forge-facing reader** (#37
  wires it); only the recall-path call from `enrichProposedTags` is removed.
- **accept/reject** — `ark ext accept/reject` and `Store.AcceptDerived`/
  `RejectDerived` (R3069/R3071) operate on the mirror files, untouched. The human
  gate (candidate → `@ext`) is unchanged.
- **`ark ext candidate`** (R3075) — the calling agent's write verb, unchanged; it
  is now the *only* producer of `@ext-candidate`s.

## Requirements

New (next R = **R3079**):

- **R3079** — the `--propose` pass is compute-for-display: it computes candidates
  (R2670–R2672, R2742, R2911) and returns them transiently for the current call;
  it authors no `@ext-candidate`, writes no RC/RF, and runs no synchronous
  materialization. Supersedes R3068, R3076.
- **R3080** — `enrichProposedTags` populates each surfaced chunk's proposals from
  R3079's transient computed set (similarity-desc), not from
  `Store.DerivedProposals`. `Store.DerivedProposals` is retained as the
  forge-facing reader (#37). Supersedes R3067's recall-path read.
- **R3081** — the calling agent is the sole author of `@ext-candidate`s (via
  `ark ext candidate`, R3075); the recall pass and the recall secretary surface
  proposals only, authoring nothing durable. (Discernment-gated authoring.)
- **R3082** (#36 core) — the recall call accepts an optional live-conversation
  chunk-ID set (source seed + recent-N turn chunk IDs), watcher-populated,
  empty for the bloodhound seed. These chunks fold into the R3079 compute with
  A66 self-exclusion bypassed and `tag-only` set (R2869), so they earn proposals
  but are never surfaced (R2872).

Reworded in place (identity persists, effect changes):

- **R2667** — `ark connections recall --propose` runs the compute-for-display
  derivation pass and surfaces **this call's** computed proposals per surfaced
  chunk in the result stencil (R2684–R2686); it persists nothing. `--propose`
  does not change which chunks appear in the surfaced output (`-all` still
  controls that).

Retired:

- **R3068** → R3079 — pass no longer authors `@ext-candidate`.
- **R3076** → R3079 — no authoring ⇒ no synchronous materialization; enrich reads
  the transient compute.
- **R3067** → R3080 — recall-path enrich reads the transient compute, not
  `candidateSourcesByChunk`/`DerivedProposals` (which survive for the forge).
- **R3072** → – — RF removed from the pass (was "RF unchanged / skip fresh").
- **R2666** → – — RF record class no longer written.
- **R2669** → – — RF read/skip/write removed.
- **R2675** → – — pass writes no RC/RF, so no batched RC+RF write.
- **R2682** → – — RF lazy cleanup moot once RF is unwritten.

Unchanged (called out to prevent over-retirement):

- **R2668** — the pass keeps `KeepTagless=true` internally (kept minimal — see
  *Scope decisions*). R2670–R2672, R2742, R2911 (candidate computation);
  R3058–R3066, R3069, R3071, R3074, R3075 (018 substrate + verbs); R2676 (no-model
  skip); R2746 (watcher surfaces tagless).

## Scope decisions (for Bill — confirm before build)

1. **RF teardown: dormant now, full teardown later (recommended).** This pass
   removes the pass's RF *usage* (read + write). It leaves the `Store`
   RF methods (`ReadDerivedFreshness`/`WriteDerivedFreshness`), the `RF` record
   class in record-formats.md, the `ark status -db` RF label (R3078), and the
   `ark connections clean -all` RF wipe (R2744) **defined but dormant**, and banks
   the full teardown (methods + record-format + label) as a follow-on O-gap. Keeps
   #36 focused on the reversal, not a record-format teardown. *Alternative:* rip
   RF out entirely now (wider diff, touches record-formats.md + status-db).
2. **KeepTagless kept (recommended).** R2668 unchanged — don't perturb the
   `scoresMap`/2×2/funnel path. With the RF freshness skip gone, the pass now
   recomputes cosine for the full scored set every `--propose` call (no cache).
   For recall latencies (~tens of chunks, not a tight loop) this is fine (spec's
   own ~5 ms/chunk cold ⇒ ~100–200 ms). Banked as an O-gap optimization: align the
   compute set to surfaced-chunks + injected-conversation-chunks later if it bites.
3. **This-call-only proposals (decided 2026-07-11).** The recall doc shows this
   call's fresh proposals; it does **not** read already-authored `@ext-candidate`s
   to suppress re-proposals. De-dup-vs-authored is a later refinement if
   re-proposal noise shows up.

## Reconciliation (supersede-at-source)

`derived-tags.md` is the steady-state owner; its whole thesis (passive automatic
curation — "every recall call leaves curation footprints"; "Writing proposals";
"Freshness check"; the RF class; the state-A lifecycle diagram) describes state A
and must be rewritten to state B on completion. Also sweep: `recall.md`,
`bloodhound.md`, `find-connections*.md`, `record-formats.md` (RF), `cli-commands.md`
(`--propose` semantics), `config.md` (min_propose_similarity still applies), the
crc cards (crc-Librarian, crc-Store, RecallWatcher, RecallOpts) and
`seq-derived-tags.md` (retire the author/materialize steps, add the injection +
compute-for-display steps). Completion test: could an agent reading only specs +
design be led to wire the autonomous author back in? If yes, a trap remains.

## Test strategy

- `recall --propose` on a corpus with ED records surfaces computed proposals per
  surfaced chunk **and writes no RC/RF records** (assert the index is unchanged).
- A second `recall --propose` computes the same proposals (no freshness skip, no
  persisted state to diverge).
- Injected conversation chunks earn proposals (tag-only) and never appear in the
  surfaced result.
- `--propose` without `[embedding] model` → recall result unchanged, no proposals,
  no error (R2676).
- The already-attached / ext-exclusion / net-rejected filters still drop
  candidates (R2671/R2672/R3070) — computation is unchanged.
- `ark ext candidate`/`accept`/`reject` still author/transition mirror lines and
  derive RC/RJ on reindex (018 path intact) — regression guard that the substrate
  survived.
