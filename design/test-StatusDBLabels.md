# Test Design: StatusDB record-class labels
**Source:** crc-DB.md

## Test: arkLabels covers every shown record class
**Purpose:** Guard R3078 — the `arkLabels` allowlist must label every ark-bucket
record class in `specs/record-formats.md` except no-status-display classes (`S`),
so `ark status -db` never silently drops a class. `buildRecordCounts` iterates the
map, so an unlabeled class is invisible in the listing.
**Input:** The canonical ark prefix set from `specs/record-formats.md` minus `S`,
compared against the package-level `arkLabels` map. Pure — no DB, no fixtures.
**Expected:** Every canonical prefix has an entry in `arkLabels`, and `arkLabels`
carries no extra/stale entries (exact count match). A missing class fails with a
message pointing at `record-formats.md`.
**Refs:** crc-DB.md, specs/record-formats.md, specs/status-db.md
