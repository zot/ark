# Transcript Reader

Language: Go. One section of ark reads Claude Code's JSONL transcripts, and it
translates each record into ark's own types. Nothing else in ark decodes Claude
Code's JSON.

```
[transcript] -> [ark's types] <- [the rest of ark]      not      [transcript] <- [the rest of ark]
```

## Why there is one reader

Ark reads Claude Code transcripts (`~/.claude/projects/<project>/<session>.jsonl`
and subagent transcripts under `<session>/subagents/`) for many jobs: indexing
chat as a corpus, recall's conversation injection, recognizing `<BLOODHOUND>`
watermarks, rendering `ark chats`, labelling chat sessions, measuring a
secretary's context, and classifying how a secretary exited. The format is
Anthropic's, undocumented, and it changes.

Measured 2026-10-22, before this spec: twelve functions decoded transcript
records in five different styles, and they disagreed. One read fields at the top
level of a record that Claude Code nests under `message`, so it never saw what it
was looking for and reported every secretary exit as a crash. They also treated
`thinking` blocks four different ways.

So the format stops here. A change to Claude Code's format is a change to this
reader and nothing else; the rest of ark reads ark's types and never learns the
format changed. This is the Lingua Franca pattern, applied to the one foreign
format ark reads in many places.

## The format, as measured

What the reader has to understand, measured 2026-10-22 over 60 recent
transcripts (22,609 records). It is a snapshot, not a contract, which is the
reason this reader exists.

- **Records ark uses.** `user` and `assistant` records, and `system` records with
  subtype `turn_duration` (the end of a turn). Sixteen other record types appear
  (`attachment`, `api-request`, `mode`, `ai-title`, `file-history-snapshot`,
  `queue-operation`, `last-prompt` and others). Ark uses none of them, and the
  reader drops them.
- **Common top-level fields.** `type`, `timestamp`, `isSidechain`, `cwd`,
  `sessionId`, `uuid`, `parentUuid`, `message`; on some user records `origin`
  and `isMeta`.
- **`message`.** `role` and `content` on every message; `usage` on assistant
  messages.
- **`content`** is either a string or a list of blocks. Block types seen:
  `text`, `tool_use`, `tool_result`, `thinking`, and `tool_addition`.
- **`thinking` comes in two kinds.** A real thinking block, and a **narration**
  block: assistant text that Claude Code recorded only as a paraphrase in a
  thinking block, recognizable by its `signature`, which carries the label
  `narration`. 563 narration blocks against 2,250 real thinking blocks in the
  sample. The text the assistant actually wrote is not in the transcript at all.
- **`origin.kind`** on user records: `human`, `peer`, `coordinator`,
  `task-notification`.
- **`usage`**: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`,
  `cache_read_input_tokens`, and others ark does not use.
- **Not present:** `isError`, `error` and `isApiErrorMessage` never appear at the
  top level of a record.

## Ark's types

Named for what ark does with each thing, not after Claude Code's field names.

**A record** is one transcript line that ark uses:

- **kind**: a user turn, an assistant turn, or a turn end. Every other record is
  dropped.
- **speaker**, for a user turn, decided in this order: by its origin, a
  **human**, an **agent report** (another agent's message relayed in: Claude
  Code's `peer` origin, a subagent's hand-back delivered into its parent's
  conversation, or its `coordinator` origin, a coordinating session's message),
  or a **task notification**; otherwise a **carrier of tool results** when that is what it
  holds; otherwise a **skill injection** (`isMeta`); otherwise **unmarked**. The
  order matters: every agent report also carries `isMeta` (measured
  2026-10-22), so reading `isMeta` first would mistake reports for skills. An origin kind the
  reader does not know is counted, then falls through to the content rules. Only a
  human speaker is something a person typed. Recall's conversation excerpt
  leaves agent reports out, because recall reminds the agent of what it and
  the person discussed, and the agent's own summary to the person already
  carries a report's substance (Bill, 2026-10-22).
- **sidechain**: whether the record belongs to a subagent's side conversation.
- **time** and **working directory**, when the record has them.
- **blocks**, in order.
- **usage**, for an assistant turn: the context fill (cache creation plus cache
  read, the number the context gate acts on), input tokens and output tokens.
- **where it came from**: the line number in its file, so any reader can go back
  to the source for what the record dropped.

**A block** is one of:

- **text**: words the assistant or the person wrote. A string `content` is one
  text block.
- **thinking**: the assistant's own reasoning.
- **narration**: a paraphrase standing in for text the assistant wrote but the
  transcript did not keep. Kept apart from thinking, so no reader takes a
  paraphrase for the original or for reasoning.
- **tool call**: the tool's name, its call id, and its input, with a shell
  command available directly.
- **tool result**: the call id it answers and its text.
- **unknown**: a block type the reader does not recognize, named and otherwise
  empty, so a new block type is visible rather than silently lost.

## Reading

- One line at a time, or a whole file in order. A malformed line, or a record
  type ark does not use, is skipped and never stops the read.
- The reader is lossy on purpose: ark consumes transcripts and never writes one,
  so the types carry only what ark uses. The line number keeps it lossless by
  reference.
- Whether to use thinking or narration is each consumer's decision, not the
  reader's. The reader reports which is which.

## The fast path

The chat chunker scans transcript bytes directly for speed. If measurement shows
it still needs to, the scanner stays, but inside this reader: it produces the
same types, and a test runs both paths over the real corpus and requires them
to agree. No fast path lives anywhere else.

## Noticing format drift

The format is not a contract, so the reader watches it:

- **While reading**, it counts what it could not place: record types, block kinds
  and user-turn origin kinds it does not know, and malformed lines. These counts are
  available to report, so an addition to the format is visible the day it
  appears.
- **On a schedule**, a check catches what counting cannot: something that stops
  appearing. The case that matters is narration. If the label that marks a
  narration block changes, those blocks read as thinking and nothing fails, so
  the check compares the share of narration among thinking blocks with its usual
  level and raises an alert when it drops to nothing. How alerts reach the person
  is the notify carve's (`carves/notify.md`): set beside the output of whatever
  the person's agent ran next, under a `btw:` label (the Marginalia pattern).

## Not in scope

- How transcripts are rendered: [chat-transcript.md](chat-transcript.md).
- What the chat chunker puts in chunk text, which is what search shows people
  and agents: [chunkers.md](chunkers.md).
- Finding the transcript for a session or a subagent nonce: path lookup, not
  parsing.
