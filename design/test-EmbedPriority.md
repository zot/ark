# Test Design: EmbedPriority
**Requirements:** R3577
**Source:** crc-Librarian.md

The batch embedding pass orders files tag-bearing first, then authored content,
then conversation logs (R1611). Whether a file is a conversation log is read from
the strategy it was indexed under, not from its suffix (R3577).

## Test: a file is a conversation log by its indexed strategy
**Purpose:** `embedPriority` gives 2 only to a `.jsonl` file indexed as `chat-jsonl`;
a `.jsonl` file indexed under another strategy is authored content (1), and a
tag-bearing file is 0 whatever its strategy (R1611, R3577).
**Input:** three indexed files: `a.jsonl` under `chat-jsonl`, `b.jsonl` and `c.md`
under `line`; `c.md` and then `a.jsonl` passed as tag-bearing.
**Expected:** 2, 1, 0, 0. The fixture checks `DB.FileStrategy` reports the strategies it
indexed, so a strategy that did not stick fails as a fixture problem.
**Alarm:** 1
**Fire alarm:** classify by `.jsonl` suffix alone; `b.jsonl` comes back 2.
**Inject:** librarian.go:embedPriority
**Pulled:** 2026-10-24 — rang (`.jsonl` under another strategy: embedPriority = 2, want 1)
