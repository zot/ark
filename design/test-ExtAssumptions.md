# Test Design: linked-`@ext` assumptions
**Source:** crc-ExtMap.md

Covers the premise records, the three verification tiers, and the sewn
retrieval. Most of these run against a fake LinkTable and a zero-value
ExtMap; the ones exercising re-resolution need the ExtMap maps but not a
server.

**Several of these must be verified to fail before being accepted.** They
assert properties that hold by default — an unchanged fileid, a routing
that happens to still be right — so a test written after the mechanism
lands passes on its first run and proves nothing about whether it can
detect the mechanism's absence. Each such case names below what to break.

## Test: an unchanged fileid does no work

**Purpose:** the recorded premise turns verification into a comparison
(R3382). This is the common path, and the reason the always-recomputing
tier is affordable at all.
**Input:** a routing whose target resolves to the same fileid as recorded;
retrieve.
**Expected:** no X-record write, no ExtMap map mutation, nothing posted to
the write actor. Asserting *absence* of writes, via a counting fake.
**Refs:** crc-ExtMap.md, seq-ext-tag-retrieval.md#1.4.1

## Test: a changed fileid triggers re-resolution through the existing path

**Purpose:** R3388 — one implementation, not two. A parallel updater would
drift, and the drift shows as tags routing correctly through one path and
not the other.
**Input:** repoint a tracked link so the target resolves to a different
fileid; retrieve.
**Expected:** `applyReresolve` runs, and X records, `targetToChunk`,
`chunkToTargets`, `fileidToTvids`, `extByAnchor`, `virtualTagCount` and
`routedTagsByTvidExt` all move together.
**Refs:** crc-ExtMap.md, seq-ext-tag-retrieval.md#1.4.2

## Test: an unresolvable target is unresolved, never last-known-good

**Purpose:** R3386, and the accuracy priority — slow is bounded, wrong is
not.
**Input:** a routing whose target becomes unresolvable (intermediate
removed); retrieve.
**Expected:** the routing is reported unresolved and counted in
`ext-unresolved`. Asserting specifically that the previously-recorded
fileid is *not* returned.
**Fire alarm:** replace the fail-closed branch with the recorded value and
confirm this test goes red. Without that check the test passes whether the
code fails closed or open, because the recorded value is the correct answer
right up until the moment it isn't.
**Refs:** crc-ExtMap.md, seq-ext-tag-retrieval.md#1.4.3

## Test: single-hop invalidates on a change to the link entry

**Purpose:** the cheap tier, and the dependency the whole scheme rests on
(R3384). The refresh path today classifies a directory symlink as a
non-directory, matches it against file patterns, and drops it — so the
event already fires and nothing acts on it.
**Input:** a single-hop tracked link feeding a routing; repoint it; retrieve.
**Expected:** the association is recomputed and reflects the new target.
**Fire alarm:** make the refresh path ignore directory-symlink entries and
confirm this goes red. This is the test that would have caught the original
defect, and the property is invisible when absent — the association simply
keeps returning its old, once-correct answer.
**Refs:** crc-ExtMap.md, crc-Scanner.md, seq-ext-tag-retrieval.md#1.2.1

## Test: internal multi-hop is dirtied by any file change and cleared on retrieval

**Purpose:** the conservative tier (R3384). Precise invalidation here would
need a hop-to-dependents reverse index; dirtying a handful is cheaper than
maintaining one.
**Input:** an internal multi-hop routing; an unrelated file change; retrieve
twice.
**Expected:** first retrieval recomputes and clears dirty; second does not
recompute.
**Refs:** crc-ExtMap.md, seq-ext-tag-retrieval.md#1.2.2

## Test: external multi-hop recomputes on every retrieval

**Purpose:** the uncacheable tier (R3384) — no event can reach it.
**Input:** a routing whose chain leaves every source; retrieve twice with
no intervening change.
**Expected:** resolution runs both times. The recorded fileid still
suppresses X-record work when unchanged, so recomputing the *resolution* is
not the same as redoing the *routing*.
**Refs:** crc-ExtMap.md, seq-ext-tag-retrieval.md#1.2.3

## Test: an anchor key written through a symlink still matches

**Purpose:** R3367 — the originating `@ext` defect. The key stays the
absolutized-but-unresolved text the user wrote; the lookup tries the
indexed file's canonical path *and* its alias addresses.
**Input:** an `@ext` whose TARGET names a path through a tracked link;
index the target file at its canonical path.
**Expected:** the routing fires.
**Fire alarm:** drop the alias-address arm of the lookup and confirm this
goes red — with only the canonical arm, a routing written through a link
never fires, and a routing that never fires is indistinguishable from one
nobody wrote.
**Refs:** crc-ExtMap.md, crc-LinkTable.md, seq-link-resolve.md#2

## Test: a repointed link heals on the next scan

**Purpose:** the property that made the unresolved-key design acceptable —
bounded, self-healing staleness rather than a permanent wrong answer needing
`ark rebuild`.
**Input:** an `@ext` routing through a link; repoint the link; rescan; retrieve.
**Expected:** the routing follows the new target.
**Refs:** crc-ExtMap.md, crc-LinkTable.md

## Test: a retrieval that mutates does not block on the write

**Purpose:** R3387 — the mutation is posted, never awaited, so the read
path stays fast while the answer stays correct.
**Input:** a retrieval that finds a changed fileid, against a write actor
that does not run.
**Expected:** the response is produced from the freshly resolved value and
returns; it does not wait for the write.
**Refs:** crc-ExtMap.md, seq-ext-tag-retrieval.md#1.4.2

## Test: the continuation runs after the mutation, not before

**Purpose:** the sewn operation's ordering dependency (R3387, and R3344 —
R3350 beneath it).
**Input:** a retrieval that posts a re-resolution and then a continuation.
**Expected:** the continuation observes the mutation's effects.
**Fire alarm:** re-introduce the spawned post in the closure actor and
confirm this goes red. It is the same injection that scrambled delivery to
`[0 3 1 2 10 4 …]` when the guarantee was restored, and it is what makes
this design's dependency on that guarantee a tested fact rather than a
comment.
**Refs:** crc-ExtMap.md, crc-ChanSvc.md, seq-ext-tag-retrieval.md
