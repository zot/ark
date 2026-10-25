# Test Design: Bloodhound hunt scoping
**Source:** crc-Librarian.md, crc-RecallWatcher.md, crc-RecallAgentBuilder.md, crc-Searcher.md

Covers the file-glob scope a directed hunt carries: how a glob is written on
the watermark, how it is anchored, where it filters, and how a wrong glob is
reported. The through-line is that a hunt has **two** scope-enforcement points
— the Go-side `Recall` seed and the secretary's own `ark search` calls — and
every test here exists to keep them from drifting apart.

## Test: AnchorGlobToDir
**Purpose:** the anchoring half of `-files` semantics (R3192, R3193)
**Input:** globs of each shape against a fixed project directory
**Expected:** `/`, `~`, and `tmp://` pass through; anything else joins the
directory; a trailing `/` survives the join; an empty glob and an empty
directory both leave the glob untouched rather than mangling it
**Refs:** crc-Searcher.md

## Test: AnchorGlobsToDir drops empty
**Purpose:** a stray `filter-files=""` must not become a glob matching nothing (R3185)
**Input:** a list containing empty and whitespace-only entries
**Expected:** only the real glob survives; a nil list stays nil

## Test: scopeAdmitsPath
**Purpose:** the positive-then-negative rule (R3185, R3192)
**Input:** one path against each combination of include/exclude globs
**Expected:** no globs admits; positives union (match any); an exclusion
rejects even a path that matched a positive

## Test: Recall filters at admission, not after ranking
**Purpose:** the push-down — the one behavior a post-filter cannot fake (R3186)
**Input:** a corpus where three out-of-scope files carry the whole query and
one in-scope file carries less of it, so the out-of-scope files occupy the
unscoped top-K; then `Recall` with `K:3` and a positive glob naming only the
in-scope directory
**Expected:** the in-scope chunk is returned. A post-filter (apply K, then
drop) returns zero here. The test **asserts the fixture first** — it fails if
the in-scope file ranks inside the unscoped top-3, since the assertion would
then pass under either design and prove nothing. The exclude direction is
checked in the same test.
**Note:** each file needs distinct content; identical content deduplicates to
a single chunk owned by the first file indexed, which silently defeats the
fixture.

**Limit:** with three out-of-scope files the fixture sits far inside every
leg's internal cap (50), so it cannot see a leg that cuts corpus-wide before
admission. The three per-leg tests below cover that (R3570).

## Test: trigram leg scoped before its cut
**Purpose:** the trigram leg searches within the scope rather than taking a
corpus-wide 50 and leaving the scope to admission (R3186, R3570)
**Input:** 60 out-of-scope files carrying the whole query, one in-scope file
carrying less of it; text input (no embeddings reach the trigram leg)
**Expected:** the leg's own unscoped search (`SearchFuzzy`, K 50) does not
reach the in-scope chunk (the fixture check); scoped Recall with `K:3` returns it
**Refs:** crc-Librarian.md
**Code:** recall_scope_test.go
**Fire alarm:** drop the `WithOnly` option from the trigram leg; red reads
"the trigram leg cut corpus-wide before the scope". (Red from below until
microfts2 restricted before its own top-k, O228.)
**Inject:** recall.go:Recall
**Pulled:** 2026-10-25 — rang (scoped recall returned 0 chunks without the in-scope one: the trigram leg cut corpus-wide before the scope)

## Test: meaning leg scoped before its cut
**Purpose:** the embedding walk skips out-of-scope chunks before scoring, so its
top 50 come from within the scope (R3186, R3570)
**Input:** a query chunk with a planted vector; 60 out-of-scope chunks whose
vectors lie closer to it than the in-scope chunk's; no shared text, so only the
meaning leg can reach the in-scope chunk
**Expected:** the leg's own unscoped walk (`SearchChunks`, K 50) does not reach
the in-scope chunk (the fixture check); scoped Recall with `K:3` returns it
**Refs:** crc-Librarian.md
**Code:** recall_scope_test.go
**Fire alarm:** stop setting `AboutRequest.Admit` on the scoped walk; red reads
"the meaning leg cut corpus-wide before the scope"
**Inject:** recall.go:Recall
**Pulled:** 2026-10-25 — rang (scoped recall returned 0 chunks without the in-scope one: the meaning leg cut corpus-wide before the scope)

## Test: tag axis scoped before its cut
**Purpose:** a tag value counts toward the axis's 50 only when it reaches an
in-scope chunk (R3186, R3570)
**Input:** a query chunk with a planted vector; 60 out-of-scope chunks each
carrying its own tag value whose EV lies closer than the in-scope chunk's value;
the in-scope chunk has no embedding and no shared text
**Expected:** scoped Recall with `K:3` returns the in-scope chunk. There is no
fixture check: the axis has no door of its own, and an unscoped Recall is capped
by its 2×2 allocation (about 4 × per-cell, whatever K asks), so it cannot show
the cut. The value ranks below all 60 by construction, and the alarm is the proof
**Refs:** crc-Librarian.md
**Code:** recall_scope_test.go
**Fire alarm:** drop the in-scope check from the value loop, so every value
counts toward the 50; red reads "the tag axis cut its values before the scope"
**Inject:** recall.go:Recall
**Pulled:** 2026-10-25 — rang (scoped recall returned 0 chunks without the in-scope one: the tag axis cut its values before the scope; `TestRecall_UnscopedNeverAdmitsFenced` also went red)

## Test: ScopeEmpty reports a wrong glob
**Purpose:** separate "your scope matched nothing" from "the corpus has
nothing" — both return zero chunks, so nothing else distinguishes them (R3188)
**Input:** three recalls — a positive glob matching no indexed file; a valid
glob with a query nothing matches; no scope at all
**Expected:** `ScopeEmpty` only in the first case

## Test: scope spares injected conversation chunks
**Purpose:** injected conversation context is the caller's own material, not a
search hit (R3187)
**Input:** the `TestRecall_Propose_InjectsConversationChunks` fixture plus an
`ExcludeFiles` glob naming the conversation file itself
**Expected:** the conversation chunk still surfaces and still earns its
proposal — a scope that reached the injection would drop it

## Test: parseBloodhoundAttrs
**Purpose:** the opening tag's attribute run (R3110, R3184, R3185, R3193)
**Input:** an empty run; repeated globs in both directions mixed with `notags`
in either order; an attribute this build does not know; an empty value; a glob
value containing the word `notags`
**Expected:** globs collect in order and anchor against the given directory
(`~` passing through, unanchored joining); `notags` is recognized only as a
bare word, never inside a value; an unknown attribute is ignored rather than
failing the match; an empty value is dropped

## Test: scanBloodhounds anchors per line
**Purpose:** each watermark anchors against the session that emitted it (R3193)
**Input:** JSONL assistant lines with differing `cwd` values, one `<BLOODHOUNDER>`
line, and one plain watermark
**Expected:** three requests (not four — the leading `\s` in the regex keeps
`<BLOODHOUNDER>` out); each hunt's globs anchored to its own line's `cwd`; the
plain watermark carries no scope

## Test: scopeDirective
**Purpose:** the secretary copies a finished string and never composes a glob
(R3189, R3194)
**Input:** an unscoped request; a request with two positives and one negative
**Expected:** unscoped renders nothing; otherwise `-with -files '<glob>'` per
positive and `-without -files '<glob>'` per negative, quoted, and wording that
tells the secretary to apply it to *every* search

## Test: seed distinguishes a wrong scope from an empty corpus
**Purpose:** the reader-facing half of R3188
**Input:** `renderBloodhoundSeed` with `ScopeEmpty` set, and with an ordinary
empty result
**Expected:** the scope note names the offending globs and says the scope was
the problem; the ordinary empty-seed note makes no such claim

## Test: buildSearchTask carries the scope
**Purpose:** the directive reaches the doc the secretary actually reads (R3189)
**Input:** a scoped request
**Expected:** the filter string appears, ordered directives → `## Recall seed`
→ crank handle

## Test: scopeOf
**Purpose:** the CLI request doc's repeatable scope metadata (R3191)
**Input:** a payload with several `filter-files:` lines, an `exclude-files:`
line, an empty-valued key, and a clue body
**Expected:** globs collected in order per direction, the empty one dropped,
and `clueOf` leaving no scope metadata in the text the seed searches

## Test: an unscoped recall never admits a fenced chunk
**Purpose:** the fence applies on every leg with no hunt scope (R3591)
**Input:** setupRecall with system_exclude naming a `fenced/` dir; the TagAxisScopedBeforeItsCut fixture with the outranking values placed in `fenced/` and the kept value outside it; Recall with no FilterFiles
**Expected:** the kept chunk returns and no `fenced/` chunk does, though the fenced values outrank it
**Refs:** crc-Librarian.md
**Code:** recall_scope_test.go
**Fire alarm:** build no scope when the hunt has no globs (the pre-#165 early return); red reads a fenced chunk in the result
**Inject:** recall.go:recallOp.resolveRecallScope
**Pulled:** 2026-10-25 — rang

## Test: a hunt scope pointing into the fence still yields nothing from it
**Purpose:** no scope lifts the fence (R3593)
**Input:** Recall with `FilterFiles: [fencedDir + "/**"]`
**Expected:** no chunks, and the result reports the scope as matching no admissible file (R3188's wrong-scope signal), not a silent widening
**Refs:** crc-Librarian.md
**Code:** recall_scope_test.go
**Fire alarm:** append the fence only when the hunt has no positive globs; red on a fenced chunk
**Inject:** recall.go:recallOp.resolveRecallScope
**Pulled:** 2026-10-25 — rang

## Test: the fence-only scope is a deny set
**Purpose:** an unscoped recall does not build an allow set over every file (R3591)
**Input:** resolveRecallScope with no globs and a one-file fence
**Expected:** the scope is active, `denied` holds the one fenced file, `only` is nil, and admission rejects only that file
**Refs:** crc-Librarian.md
**Code:** recall_scope_test.go
**Fire alarm:** none; this pins a cost decision, and its break (an allow set) changes no result.

## Test: the scope directive never carries -system
**Purpose:** a secretary's own searches cannot lift the fence (R3593)
**Input:** scopeDirective for a request with both filter and exclude globs, including one into `~/.ark/schedule/`
**Expected:** the string contains `-files` rows only and no `-system`
**Refs:** crc-RecallAgentBuilder.md
**Code:** recall_scope_test.go
**Fire alarm:** append `-system` to the directive; red names the flag
**Inject:** recall_agent_builder.go:scopeDirective
**Pulled:** 2026-10-25 — rang

## Not covered

- **End-to-end watermark → task doc with a scope.** The
  `OnAppend → jobs → dispatchBloodhound → task-doc` path is unit-covered
  piecewise on both sides of the seam but not end-to-end; the existing gap
  O132 already records this for the unscoped path and the scoped path inherits
  it rather than adding a second gap.
- **The secretary actually honoring the filter string.** That is weak-model
  behavior against a live agent, not a property of this code; the seam this
  code owns — that the string is complete, correct, and present in the doc —
  is covered above.
