# Test Design: TranscriptReader
**Source:** crc-TranscriptReader.md

## Test: records are translated from the shapes Claude Code writes
**Purpose:** R3554, R3555, R3558, R3560. Fixtures are lines in the shapes
measured 2026-10-22: an assistant record with text, thinking, a tool_use carrying
a shell command, and usage; a user record carrying a tool_result; a user record
with string content; a `system`/`turn_duration` record; an `attachment` record.
**Expected:** each used record has the right kind, blocks in order, tool name,
call id and command, context fill = cache-creation + cache-read, the line
number; the attachment is not used.
**Refs:** crc-TranscriptReader.md
**Code:** transcript_test.go
**Fire alarm:** compute the context fill from cache-read tokens alone; the usage check reads 24000 instead of 25000.
**Inject:** transcript.go:ParseTranscriptLine
**Pulled:** 2026-10-22 — rang: context fill 24000, want 25000

## Test: speaker precedence
**Purpose:** R3556. A `peer` record that also carries `isMeta` is a subagent
report, not a skill injection; a record with no origin and tool results is a
tool-result carrier; `isMeta` with no origin is a skill; no origin, no
`isMeta`, string content is unmarked; only `human` is person-typed.
**Refs:** crc-TranscriptReader.md
**Code:** transcript_test.go
**Fire alarm:** check `isMeta` before `origin`; the agent report reads as a skill.
**Inject:** transcript.go:speakerOf
**Pulled:** 2026-10-22 — rang: the peer record read as speaker 5 (skill), want 2

## Test: narration is its own kind
**Purpose:** R3559. A thinking block whose signature carries the narration label
reads as narration; one without reads as thinking; neither reads as text.
**Refs:** crc-TranscriptReader.md
**Code:** transcript_test.go
**Fire alarm:** make `isNarration` match the base64 text instead of decoding; the label at an unaligned offset is missed.
**Inject:** transcript.go:isNarration
**Pulled:** 2026-10-22 — rang: the narration block read as thinking (kind 1, want 2)

## Test: reading survives bad lines and counts what it could not place
**Purpose:** R3561, R3565. A file with a malformed line, an unknown record type
and an unknown block kind between good records.
**Expected:** every good record arrives in order; the stats count one malformed
line, the unknown type by name and the unknown block kind by name; the unknown
block appears as `BlockUnknown` with its type name.
**Refs:** crc-TranscriptReader.md
**Code:** transcript_test.go
**Fire alarm:** stop counting malformed lines; the stats check reads 0 malformed.
**Inject:** transcript.go:ParseTranscriptLine
**Pulled:** 2026-10-22 — rang: stats showed Malformed 0

## Test: nothing else decodes transcripts
**Purpose:** R3553. A source check over ark's non-test Go files: no file other
than transcript.go names Claude Code's record fields as JSON (`json:"message"`,
`"tool_use"`, `"tool_result"`, `"isSidechain"`, `cache_read_input_tokens`).
Generic names such as `"message"` are deliberately not checked: an HTTP body and
the nano agent's chat API both use it, and a check that fires on unrelated code
gets ignored. Comments are not checked. The test first proves it can see the
names, in transcript.go itself, so a broken scan fails rather than passing.
**Refs:** crc-TranscriptReader.md
**Code:** transcript_test.go
**Fire alarm:** add a struct with a `json:"isSidechain"` field to chats.go; the test names the file and line.
**Inject:** chats.go
**Pulled:** 2026-10-23 — rang: "decodes Claude Code's transcript format outside the reader: chats.go:400"

## Test: the chat chunker's rules
**Purpose:** R3567, R240, R241, R1507. The chunker on lines in the shapes Claude
Code writes. Before it moved onto the reader (2026-10-22) nothing tested its text
rules: injecting "index tool results" or "drop thinking" turned nothing red.
**Input:** a person turn with a timestamp; an assistant turn with thinking, an
empty signed thinking block, narration, text and a tool call; a tool-result turn;
an attachment; a partial trailing line.
**Expected:** three chunks: the person's turn (role human, timestamp set), the
assistant's thinking, narration and text one per line, thinking and narration
in their voice tags and the empty block absent (role assistant, R3608), and the
partial line raw with no role. No chunk for the tool result or the attachment, and no tool
command anywhere.
**Refs:** crc-DB.md, crc-TranscriptReader.md
**Code:** chat_chunker_test.go
**Fire alarm:** index tool-result text; an extra chunk appears.
**Inject:** db.go:chatChunkText
**Pulled:** 2026-10-25 — rang: TestChatChunkerText "chunks:" mismatch (re-pulled after the voice tags rewrote chatChunkText)
**Fire alarm:** drop thinking from chunk text; the assistant chunk loses "weighing".
**Inject:** db.go:chatChunkText
**Pulled:** 2026-10-25 — rang: TestChatChunkerText "chunks:" mismatch (re-pulled after the voice tags rewrote chatChunkText)
**Fire alarm:** treat narration as plain text, untagged; the assistant chunk
loses `<narration>said it</narration>`.
**Inject:** db.go:chatChunkText
**Pulled:** 2026-10-25 — rang: TestChatChunkerText "chunks:" mismatch
**Fire alarm:** stop indexing a partial trailing line; its chunk disappears.
**Inject:** db.go:JSONLChunkFunc
**Pulled:** 2026-10-22 — rang: TestChatChunkerText failed

## Test: the chat chunk's role
**Purpose:** R1507, R3556. A chunk's role from the reader's record: an agent
report carries isMeta and stays "skill"; a person's mid-turn message and a task
notification also carry isMeta and are "human"; an unused record has no role.
**Input:** user, assistant, skill, agent-report, mid-turn, task-notification,
system and progress lines; assistant lines whose nested blocks also say `type`.
**Expected:** human, assistant, skill, skill, human, human, "", "", assistant,
assistant.
**Refs:** crc-DB.md, crc-TranscriptReader.md
**Code:** role_test.go
**Fire alarm:** map only skills to "skill"; the agent report reads "human".
**Inject:** db.go:chatChunkRole
**Pulled:** 2026-10-22 — rang: the peer line's role came back human
**Fire alarm:** make `TurnUser` the zero value again; an unused record reads as a user turn, so the partial line gets role "human".
**Inject:** transcript.go:TranscriptKind
**Pulled:** 2026-10-22 — rang: TestChatChunkerText "a partial line has no role, got human", and TestChatChunkRole
