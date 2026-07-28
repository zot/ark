# Test Design: Source Pairing

**Source:** specs/chat-transcript.md

Covers source-name resolution (R3292) and the `logs_for` pairing (R3288–R3290).
All of it is pure logic over a zero-value `Config` — no DB, no server, no
fixtures — so the decision rules are pinned cheaply and the expensive-to-reach
paths stay out of the tests entirely.

The label/turn-count reader (R3284, R3286) needs a file but not an index, so it
tests against `t.TempDir()`.

## Test: a source name is its directory's last segment
**Purpose:** R3292 — establishes the naming rule other tests rely on, and pins
that a trailing separator does not change the answer (`/a/b/` is still `b`).
**Input:** `SourceName` over `/home/deck/work/ark`, `/home/deck/work/ark/`, and
a bare `ark`.
**Expected:** `ark` in all three cases.
**Refs:** crc-Config.md

## Test: resolution prefers the full dir over the last segment
**Purpose:** R3292 — `dir` stays the source's identity and the short form is a
convenience, so an exact `dir` match must win. Guards against the short form
quietly becoming a second identity.
**Input:** a Config with sources `/w/ark` and `/other/ark`; resolve the exact
string `/w/ark`.
**Expected:** the `/w/ark` source, with no ambiguity error — even though the
last segment `ark` is ambiguous.
**Refs:** crc-Config.md

## Test: an ambiguous short name errors and names every candidate
**Purpose:** R3292 — a silent pick would pair a chat source with the wrong
project, and the failure would be invisible. The error must carry both dirs, or
the operator cannot act on it.
**Input:** a Config with `/w/ark` and `/other/ark`; resolve `ark`.
**Expected:** an `*ErrAmbiguousSource` whose `Candidates` holds both dirs.
**Refs:** crc-Config.md

## Test: an unknown name errors rather than returning a zero source
**Purpose:** R3292 — a zero `Source{}` would silently list nothing, which reads
identically to "this project has no conversations."
**Input:** resolve `nosuch` against a Config holding one source.
**Expected:** a non-nil error.
**Refs:** crc-Config.md

## Test: logs_for is matched by resolved identity, not by string
**Purpose:** R3288, R3292 — `logs_for = "ark"` and
`logs_for = "/home/deck/work/ark"` are the same declaration, so `--for` must
find the chat source either way. A string comparison would make the two forms
behave differently and the difference would only show up as a missing panel.
**Input:** two chat sources logging the same code source, one declaring the
short name and one the full dir; `SourcesLoggingFor("ark")`.
**Expected:** both chat sources returned.
**Refs:** crc-Config.md

## Test: a dangling logs_for is skipped by lookup, reported by pair
**Purpose:** R3290 — "a logs_for pointing at nothing is worse than an absent
one." Lookup must not fail wholesale because one source is misconfigured, but
the broken pairing must be *reported* somewhere or it stays invisible forever.
**Input:** a Config where one chat source declares `logs_for = "ghost"`;
`SourcesLoggingFor` for a valid name, then `ProposeLogsFor`.
**Expected:** lookup returns only the valid pairing; `ProposeLogsFor().Dangling`
carries the ghost entry.
**Refs:** crc-Config.md

## Test: the heuristic proposes only for sources it matches
**Purpose:** R3290 — proposals are for unpaired **chat** sources. An ordinary
code source never gets a `logs_for`, so reporting it as an unmatched proposal
would put a permanent complaint about every project directory into the output
of a verb meant to be re-run.
**Input:** a Config with a code source `/w/proj` and a chat source
`~/.claude/projects/-w-proj`; `ProposeLogsFor`.
**Expected:** exactly one proposal, pairing the chat source to `proj`; the code
source appears nowhere in the result.
**Refs:** crc-Config.md

## Test: an already-paired source is left untouched
**Purpose:** R3290 — re-running must be safe and must never overwrite a
hand-corrected pairing.
**Input:** the same Config with the chat source already carrying a (valid)
`logs_for` naming the other source.
**Expected:** no proposals.
**Refs:** crc-Config.md

## Test: SetLogsFor refuses a name that resolves to nothing
**Purpose:** R3290 — the write path is where "never written" is enforced, so a
dangling value must be impossible to introduce through the supported route even
if a caller proposes one.
**Input:** `SetLogsFor` with a name matching no source.
**Expected:** an error, and the source's `LogsFor` unchanged.
**Refs:** crc-Config.md

## Test: the label is the first user turn, sidechain excluded
**Purpose:** R3284, R3286 — the label is the whole point of the list verb, and a
session's opening line is the human's, not a subagent's. Also pins the turn
count, which is what distinguishes a two-message stub from a day's work.
**Input:** a JSONL file whose first record is a sidechain user turn, followed by
a real user turn, an assistant turn, and a second user turn.
**Expected:** the label is the *real* first user turn; turns is 3 (the sidechain
record counted in neither).
**Refs:** crc-CLI.md

## Test: the mcp: option tables parse through the production parsers
**Purpose:** R3287, R3291 — pins the marshalling both Lua bindings depend on,
including that `for` (a Lua keyword, written `["for"]=`) lands under that name,
and that an absent options table means "unnarrowed" rather than an error.
Deliberately drives `chatSessionOptionsFromLua` / `chatRenderOptionsFromLua`
themselves rather than a hand-rebuilt copy: a mirrored mapping keeps passing
while the real binding drifts, which is how a dropped `extmap` stayed green for
three months.
**Input:** a real `lua.LState`; `lua.LNil` and a non-table value, then tables
carrying `source`/`for`/`limit` and `withTools`/`lineLength`.
**Expected:** nil/non-table yield the zero options; populated tables map field
for field; an unset `lineLength` falls back to `DefaultChatLineLength` rather
than to a zero width that would wrap every line to nothing.
**Refs:** crc-Server.md

## Test: a long label is flattened to one line and clipped
**Purpose:** R3284 — a row that wraps or spans paragraphs stops being a table of
contents. Guards both the newline flattening and the rune-safe clip.
**Input:** a user turn holding several hundred characters across two paragraphs,
including multi-byte runes.
**Expected:** one line, no newlines, no longer than the label cap plus its
ellipsis, and valid UTF-8 (no split rune).
**Refs:** crc-CLI.md
