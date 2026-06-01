# Migration: RJ → signed Recall Judgment edge record

Language: Go. Environment: built-in subsystem of `ark serve`,
`store.go` record layer + `recall.go` propose pass, configured via
`[recall]` in `ark.toml`. No new CLI surface.

This is the **first build seam of the Recall Secretary** (ARK-STATE
item 1). The settled design lives in
[`.scratch/SECRETARY-NEW.md`](../../.scratch/SECRETARY-NEW.md); this
spec carves only the foundational data record that "everything else
sits on top." It is a record-format migration (RJ value v2 → v3)
plus the bidirectional Store primitive the secretary will consume.

## Problem (state A)

Today the `RJ` record (Recall reJection) is a **one-directional,
reject-only** signal:

- Key: `"RJ"` + chunkid varint + tagname (R2665).
- Value: `varint(counter) + 8-byte BE unix nanos` (R2764). The
  counter only ever increments — one `Store.RejectDerived` call per
  rejection (R2680). Existence means "rejected" (R2673); the counter
  feeds two suppression ceilings (R2765/R2766).
- Sticky: no decay, no reinforcement, no un-reject (R2683).
- Scoped to **derived proposals** (RC candidates) only — there is no
  per-edge state for a tag that is already *attached* to a chunk.

The Recall Secretary needs more than reject-or-not. Its design (see
SECRETARY-NEW.md, "Pruning the garden") calls for **one signed
per-(chunk, tag) edge record** where:

- **Rejection is the negative tail of a single signed axis.** A
  useful edge is reinforced (score rises); one that drifts out of use
  is decayed (score falls); sustained negative signal is what the old
  reject counter measured.
- **The axis is bidirectional.** The secretary increments the edge
  when it keeps a tag-driven surface and decrements when it drops one
  — pruning rides on curation it is already doing. Hysteresis (the
  counter the record already carries) keeps one cheap misjudgment
  from flipping a good edge.
- **It applies to attached tags, not just proposals.** Pruning the
  garden operates on live hyperedges (F/V records), which the
  reject-only RJ never covered. The key shape (`chunkid + tagname`)
  already supports any tag, attached or proposed — no key change is
  needed, only a semantic and value-shape generalization.

A reject-only counter cannot represent "this edge is currently more
relevant than before." Unifying rejection and relevance into one
signed figure is the carving.

## Solution (state B): the Recall Judgment record

The `RJ` record class is reframed as the **Recall Judgment** edge:
one signed relevance figure per `(chunkid, tagname)`. The two RJ
letters are retained (no key-shape change, no migration of key
bytes); "reJection" becomes the record's *negative tail*.

### Record format (v3)

- **Key:** unchanged — `"RJ"` + chunkid varint + tagname. Still
  mirrors RC exactly.
- **Value:** `signed-varint(score) + 8-byte BE unix nanos`.
  - `score` is a signed integer, zigzag-encoded via Go's
    `binary.PutVarint` / `binary.Varint`.
  - `score < 0` → net-rejected. The magnitude `-score` reproduces the
    old reject counter exactly: N rejections with no reinforcement →
    score `-N`.
  - `score > 0` → reinforced (a `(chunk, tag)` edge a judge kept).
  - `score == 0` → neutral, equivalent to record-absent. A write that
    lands a score back at 0 may store the record or delete it; readers
    treat absent and 0 identically.
  - The trailing 8-byte big-endian unix-nanos timestamp records the
    **most recent adjustment**, enabling decay-on-read as a future
    knob (not implemented this pass).

### Store API (the bidirectional primitive)

- `Store.AdjustJudgment(txn, chunkID, tagname, delta int64)
  (newScore int64, err error)` — the single read-modify-write
  primitive. Reads the current score (absent = 0), adds `delta`,
  stamps the timestamp to NOW, writes the v3 value, returns the new
  score. Positive delta reinforces, negative decays/rejects. Runs
  inside the caller's txn.
- `Store.ReadJudgment(txn, chunkID, tagname) (score int64,
  present bool, err error)` — reads the signed score. Absent →
  `(0, false, nil)`. A malformed value is treated conservatively as
  rejected (`score < 0`, present) so a ceiling=0 caller never
  re-proposes a corrupt edge.
- `Store.RejectDerived(chunkID, tagname)` is **reimplemented** on the
  primitive: in one txn, delete `RC[chunkid+tagname]` then
  `AdjustJudgment(..., -1)`. It returns the rejection magnitude
  (`-newScore` when negative, else 0) so existing callers that
  expected the old `uint64` counter keep working unchanged. Until a
  reinforcement producer exists (the secretary, a later seam), every
  edge starts at 0 and only `RejectDerived` writes it, so a sequence
  of rejections yields scores `-1, -2, -3, …` — **bit-for-bit
  identical** to the old monotonic counter.
- `Store.HasDerivedRejection(txn, chunkID, tagname)
  (rejected bool, magnitude uint64, err error)` is **reimplemented**
  on `ReadJudgment`: `rejected = present && score < 0`,
  `magnitude = max(0, -score)`. Same signature, same meaning for the
  existing propose/mention readers.

### Readers (behavior-preserving)

The two existing consumers read the magnitude exactly as before — the
old "counter" is now "rejection magnitude" (`-score`):

- **Propose pass** (R2673/R2765): a candidate is suppressed when
  `score < 0` and (`reject_propose_ceiling == 0` ⇒ any net rejection
  suppresses; else `magnitude >= reject_propose_ceiling`).
- **Assistant mention path** (R2766): silent when
  `magnitude >= reject_mention_ceiling`.

The `reject_propose_ceiling` / `reject_mention_ceiling` config keys
(R2767/R2768) are unchanged in name and default; only their
description gains the signed framing (the ceiling is compared against
the negative magnitude).

### Migration (clean-reset, explicit + checkpointed)

The v3 value shape is structurally indistinguishable from the v2
shape (both are a leading varint + 8 nanos), so v2 and v3 cannot
coexist. There is **no automatic DB.Open drop**: the migration is the
operator running

```
ark connections clean -all -checkpoint
```

which wipes RJ (the `-all` scope) after `-checkpoint` advances the
session JSONLs to current EOF and the Fossil checkpoint makes the
wipe recoverable. The next reject/reinforce cycle rewrites RJ in v3
shape. Running the v3 binary against un-cleared pre-v3 RJ records is
out of contract — the same discipline already stated for RJ's v1 → v2
transition (R2764) and for old binaries on new-format data (Schema
Version Protocol).

## What this migration does NOT do

- **No RD-family sibling.** The per-(session, chunk) surface-cooldown
  / match-history signal (no tagname) is a *separate* RD-family record
  and a separate carve — the next seam, not this one.
- **No reinforcement producer.** `AdjustJudgment` with a positive
  delta ships as substrate but has no in-tree caller until the
  secretary lands. The bidirectional primitive is the deliverable;
  who drives it is later.
- **No pruning thresholds.** The "fade-from-results" and
  "suggest-removal" negative thresholds (and their `[recall]` config
  knobs) are consumed by the secretary/assistant, which don't exist
  yet. Deferred with their consumer.
- **No decay.** The timestamp is stored so decay-on-read is *possible*
  later; whether and how fast the score decays toward 0 is a future
  knob. No decay math this pass.
- **No strikeout UI.** Rendering struck-through arc-tags below the
  prune threshold is downstream `/ui` work.
- **No secretary agent, no pipeline rewiring.** The topology change
  (per-session secretary replacing the shared recall daemon, the two
  presence gates, content-bundled curation docs) is later seams.

## Test strategy

- **v3 round-trip** — `AdjustJudgment(+1)` then `ReadJudgment` returns
  score 1; `AdjustJudgment(-1)` twice from absent returns scores
  -1 then -2; encode/decode preserves both sign and timestamp.
- **Reject parity** — three `RejectDerived` calls on the same
  `(chunk, tag)` yield score -3; `HasDerivedRejection` reports
  `rejected=true, magnitude=3` — matching the old R2764 counter test.
- **Reinforcement hysteresis** — from score +2, one `RejectDerived`
  yields +1 (still `rejected=false`), demonstrating the signed axis is
  bidirectional and a reinforced edge survives a single decrement.
- **Neutral = absent** — `ReadJudgment` on an untouched edge returns
  `(0, false)`; a score driven back to 0 reads identically to absent.
- **Propose ceiling** — `reject_propose_ceiling = 2`, score -2 →
  propose pass suppresses; score -1 → propose pass surfaces (parity
  with the existing R2765 test, expressed in signed terms).
- **Mention ceiling** — `reject_mention_ceiling = 5`, score -5 →
  mention path silent (parity with R2766).
- **Malformed value** — a non-decodable RJ value reads as
  `rejected=true, magnitude=0` so a ceiling=0 caller never
  re-proposes it.
- **clean -all wipes RJ** — unchanged; the existing
  `ark connections clean -all` test continues to pass (RJ records,
  now v3, are removed).

## On completion (folding plan)

When the implementation lands, this migration is complete and
`specs/` must describe state B as the present:

1. **`specs/simple-recall.md`** — replace the "RJ record format (with
   rejection counter)" section with a "Recall Judgment record (signed
   per-edge relevance)" section: v3 value, signed semantics, the
   `AdjustJudgment` / `ReadJudgment` primitive, attached-tag
   applicability, and the clean-reset migration note.
2. **`specs/record-formats.md`** — update the prefix-inventory `RJ`
   row and the "RJ — Recall reJection" subsection to "RJ — Recall
   Judgment": value `signed-varint(score) + 8-byte BE unix nanos`,
   signed semantics, negative tail = rejection.
3. **`specs/config.md`** — reword `reject_propose_ceiling` /
   `reject_mention_ceiling` meanings in signed terms (ceiling compared
   against `-score`); keys and defaults unchanged.
4. **Retire** `R2764` (v2 value format) → the new v3 Rn. Reword R2683
   (sticky / no reinforcement) — the axis is now bidirectional; there
   is still no manual un-reject verb, but reinforcement raises the
   score. Update R2665's value clause to v3 (key clause unchanged).
5. **`ark status -db`** — confirm the RJ record listing still renders
   (the decode is internal; the count is unaffected).
6. `minispec update migration-complete recall-judgment`.
