# Test Design: RecallWatcher
**Source:** crc-RecallWatcher.md

Covers the turn-boundary watcher in isolation. Substrate
behavior (`Librarian.Recall` plus `--propose`) is exercised
under test-Recall.md and test-DerivedTags.md; these cases stub
or pin the substrate so the watcher's own state-machine and
composition logic are what's under test.

The CLI surface — `ark message dm --from-service` and the
multi-recipient/subject grammar — is covered by the
`TestComposeDM_*` cases in `cmd/ark/main_test.go` (R2716-R2727).

## Test: watcher_disabled_emits_nothing
**Purpose:** R2688 — master switch must gate every emit
**Input:** ark.toml with `[recall].enabled = false`; OnAppend
called with a JSONL append containing a `turn_duration` record.
**Expected:** no timer armed; no DM emitted; no log lines from
the watcher.
**Refs:** crc-RecallWatcher.md, seq-recall-watcher.md#2

## Test: turn_duration_arms_timer
**Purpose:** R2731, R2734 — turn_duration arms the debounce
timer.
**Input:** `[recall]` enabled; OnAppend with `newBytes`
containing one `{"type":"system","subtype":"turn_duration",...}`
line and one assistant chunk added.
**Expected:** `sessions[sid].pendingTimer != nil`;
`pendingChunks == [c]`; no DM emitted yet (timer hasn't fired).
**Refs:** seq-recall-watcher.md#2

## Test: user_record_disarms_without_clearing_pending
**Purpose:** R2732, R2733 — user record cancels timer but
preserves pendingChunks.
**Input:** Sequence of OnAppend calls:
1. assistant chunk + turn_duration line → timer armed,
   pendingChunks=[c1]
2. user chunk → timer disarmed
**Expected:** `pendingTimer == nil`; `pendingChunks == [c1]`;
no DM emitted.

## Test: multi_turn_accumulation
**Purpose:** R2733 — pendingChunks roll forward across
disarms.
**Input:**
1. assistant chunk c1 + turn_duration
2. user chunk c2 (disarm)
3. assistant chunk c3 + turn_duration
4. user chunk c4 (disarm)
5. assistant chunk c5 + turn_duration
6. wait `activation_delay` seconds
**Expected:** one DM emitted; section count corresponds to
chunks {c1, c2, c3, c4, c5} that pass the similarity gate;
`pendingChunks` cleared after fire.

## Test: timer_expiry_fires_pipeline
**Purpose:** R2735, R2736 — timer expiry runs Recall per
chunk and clears pending.
**Input:** turn_duration arms timer with 1s
`activation_delay`; pendingChunks = [c1, c2] with embeddings
that produce strong matches in the corpus; wait 1.2s.
**Expected:** one DM emitted at `tmp://ARK-RECALL/dm-<sid>`;
two `## Recalled for chunk <ID>` sections (one per input);
`pendingChunks` is empty after fire.

## Test: similarity_gate_drops_section
**Purpose:** R2708, R2739 — sections below threshold drop.
**Input:** `[recall].min_similarity = 0.99`; pendingChunks =
[c1, c2] where c1's top recall is 0.99+ and c2's is 0.5.
**Expected:** DM has exactly one `## Recalled for chunk c1`
section; c2's section omitted; pendingChunks cleared.

## Test: all_sections_drop_no_dm
**Purpose:** R2739, R2740 — empty fire emits no DM.
**Input:** `[recall].min_similarity = 0.99`; pendingChunks =
[c1, c2], both below threshold.
**Expected:** no DM appears at
`tmp://ARK-RECALL/dm-<sid>`; pendingChunks cleared; log line
shows `fired sections-emitted=0`.

## Test: per_chunk_recall_isolation
**Purpose:** R2736, R2737 — separate Recall per input,
discrimination preserved.
**Input:** pendingChunks = [c1, c2] where c1's content matches
chunks A/B/C and c2's content matches chunks D/E/F (disjoint
corpus regions).
**Expected:** section 1 lists A/B/C; section 2 lists D/E/F.
No cross-contamination from per-input-max smearing.

## Test: section_header_excerpt_bounded
**Purpose:** R2738 — section header excerpt cap.
**Input:** input chunk text is 4 KB of prose.
**Expected:** the blockquoted excerpt under
`## Recalled for chunk <ID>` is ≤ 200 chars; UTF-8 boundaries
preserved.

## Test: chunks_per_dm_caps_section_size
**Purpose:** R2692 — per-section top-K cap.
**Input:** `[recall].chunks_per_dm = 2`; one input chunk
matches at least 5 corpus chunks above threshold.
**Expected:** that input's section lists exactly 2 chunks,
ordered by `@chunk-score` desc.

## Test: propose_passthrough_in_sections
**Purpose:** R2706 — `@chunk-proposed-tags` per section.
**Input:** `[recall].propose = true`; surfaced chunks have RC
records.
**Expected:** each section's recalled chunks include
`@chunk-proposed-tags` lines where applicable.

## Test: mark_on_send_writes_RD_across_sections
**Purpose:** R2711, R2740 — RD writes span every surfaced
chunk in every section.
**Input:** Fire with 2 sections, each with 2 recalled chunks
carrying tags.
**Expected:** RD records exist for `(session, tag, value)`
for every tag of every surfaced chunk; counts match
`fired discussed=N` log.

## Test: derived_candidates_not_marked
**Purpose:** R2712 — `@chunk-proposed-tags` doesn't drive RD.
**Input:** Fire with a section whose chunks carry
`@chunk-proposed-tags: foo` not otherwise attached.
**Expected:** no RD record for `(session, foo, *)`.

## Test: source_whitelist_filters
**Purpose:** R2693, R2696, R2741 — non-listed sources
ignored.
**Input:** `[recall].sources = ["/path/A"]`; OnAppend from
`/path/B` source (also chat-jsonl) with a turn_duration.
**Expected:** no timer armed for that session; no
accumulation either (source-qualification rejected upstream).

## Test: non_chat_jsonl_source_skipped
**Purpose:** R2696, R2741 — strategy gate.
**Input:** OnAppend with strategy=markdown carrying a
`turn_duration`-shaped line.
**Expected:** no timer armed; no accumulation.

## Test: curation_candidate_tag_only_marker
**Purpose:** R2869 — own-session candidate renders the
`- tag-only: true` marker (recommend-only, never surface);
non-own-session candidates omit it.
**Input:** `RecallCurationBuilder` for session `S`; one
`Candidate(..., tagOnly=true)` (own-session JSONL chunk) and one
`Candidate(..., tagOnly=false)` (external file).
**Expected:** the doc body carries exactly one `- tag-only: true`
line, sitting under the own-session candidate's `## Candidate:`
H2, not the external one.
**Refs:** crc-RecallAgentBuilder.md, crc-RecallWatcher.md
(implemented as `TestRecallCurationBuilder_TagOnly`).

## Test: cold_start_no_backfill
**Purpose:** R2698 — go-forward only.
**Input:** Start `ark serve` with a `chat-jsonl` source that
has many existing committed chunks; no new appends arrive.
**Expected:** no DMs, no timers; `sessions` map remains empty.

## Test: live_config_reload
**Purpose:** R2695 — config reload propagates without
restart.
**Input:** Start with `enabled = false`; rewrite ark.toml to
`enabled = true` and `activation_delay = 1`; trigger a turn
boundary; wait 1.2s.
**Expected:** DM emitted; subsequent OnAppend respects the
new `activation_delay`.

## Test: turn_duration_resets_existing_timer
**Purpose:** R2734 — fresh turn_duration replaces deadline.
**Input:** turn_duration arms timer at T+0 with
activation_delay=15s; another turn_duration arrives at T+5
(unusual but possible if the chunker yields one record per
turn).
**Expected:** the second arming replaces the first; fire
happens at T+20, not T+15.

## Test: log_shape
**Purpose:** R2713 — observability.
**Input:** A sequence producing one `armed`, one `disarmed`,
one `armed` (re-armed), one `fired` decision.
**Expected:** four structured log lines with the documented
fields (session, pendingChunks count, decision-specific
fields).
