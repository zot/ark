# TranscriptReader
**Requirements:** R3553, R3554, R3555, R3556, R3557, R3558, R3559, R3560, R3561, R3562, R3563, R3564, R3565, R3566

The one translator from Claude Code's JSONL transcript format into ark's own
types (Lingua Franca). Lives in `transcript.go`, package `ark`; nothing else in
ark decodes a transcript record.

## Knows
- TranscriptRecord: one record ark uses.
  - Kind: `TurnUser`, `TurnAssistant`, `TurnEnd`.
  - Speaker (user turns): `SpeakerHuman`, `SpeakerAgentReport`,
    `SpeakerTaskNotification`, `SpeakerToolResults`, `SpeakerSkill`,
    `SpeakerUnmarked`, decided in that order of precedence (R3556).
  - Sidechain bool; Time (zero if absent); Cwd (empty if absent).
  - Blocks []TranscriptBlock, in order.
  - Usage *TranscriptUsage (assistant turns only).
  - Line int: 1-based line in its file, the reference back to the source (R3562).
- TranscriptBlock: Kind (`BlockText`, `BlockThinking`, `BlockNarration`,
  `BlockToolCall`, `BlockToolResult`, `BlockUnknown`), Text, Tool, CallID,
  Input (map), Command (Input's `command` when it is a string), TypeName (the
  unrecognized type, for `BlockUnknown`).
- TranscriptUsage: Context (cache-creation + cache-read input tokens, the R2777
  value), Input, Output.
- TranscriptStats: counts of unknown record types and unknown block kinds by
  name, and of malformed lines (R3565).

## Does
- ParseTranscriptLine(line, lineNo) (TranscriptRecord, used bool): translate one
  line. `used` is false for a record type ark does not use (R3554) and for a
  malformed line; the caller's stats say which.
- ReadTranscript(path, fn) (TranscriptStats, error): every used record of a file,
  in order, to fn; fn returns false to stop. A malformed or unused line is
  skipped and counted, never fatal (R3561). The error is only the file's.
- ReadTranscriptData(data, fn) TranscriptStats: the same over bytes already in
  memory (the chunker, append scans).
- Record helpers, so consumers never walk blocks for the common cases:
  `Text()` (text blocks joined), `ToolCalls()`, and `IsPersonTyped()`
  (SpeakerHuman).
- Narration: a thinking block whose signature carries the `narration` label
  (R3559). The only place in ark that knows how narration is marked.
- Nothing about policy: whether a consumer uses thinking or narration is its own
  choice (R3563).

## Collaborators
Consumers, each reading TranscriptRecord instead of JSON:
- JSONLChunker (`chat-jsonl`, db.go): chunk text, role, timestamp per line.
  Keeps a byte-level fast path only inside this card, with a corpus parity test
  (R3564).
- RecallWatcher: user-turn and turn-end signals; assistant text and cwd for
  `<BLOODHOUND>` and recall-tag directives.
- RecallAgentBuilder: the recent-conversation excerpt (R3557); a subagent's
  context fill and token totals.
- Chats: `ark chats` rendering and session labels.
- LuhmannCLI: `inspect-exit`'s close detection and tokens at close.
- LuhmannSend: the send window's turn-end signal.
- The format sentry (notify carve Item 6) for R3566.

## Sequences
- None; each consumer's own sequence calls ReadTranscript or
  ReadTranscriptData where it decoded JSON before.
