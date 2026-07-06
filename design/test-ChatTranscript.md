# Test Design: Chat Transcript

**Source:** specs/chat-transcript.md

Covers the `ark chats` renderer's content-block handling. Renders to stdout, so
tests capture it via an `os.Pipe` swap of `os.Stdout` (output stays well under the
pipe buffer).

## Test: --thinking gates the chain-of-thought
**Purpose:** R3035 — an assistant `thinking` block renders only with `--thinking`
(marked `✻`), while a `text` block always renders. Guards the display-parity fix:
the corpus already indexes thinking, so the renderer must be able to show it.
**Input:** a one-line JSONL file with an assistant record whose content array holds
a `{"type":"thinking",…}` block ("SECRETTHOUGHT") and a `{"type":"text",…}` block
("VISIBLETEXT"); `renderChat` called with `withThinking` false, then true.
**Expected:** default — text present, thinking absent; with `--thinking` — thinking
present and carries the `✻` marker.
**Refs:** crc-CLI.md

## Test: the urfave `chats` node declares the flags (wiring Sentry)
**Purpose:** R3035 — guard the migration bug the render test can't see: a flag
declared only on the legacy `cmdChats` flag set but not on the urfave node is
rejected ("flag provided but not defined") before `cmdChats` runs. Assert the node
declares `--thinking` / `--all`.
**Input:** find the `chats` command in `flatCommands()`; collect its flag names.
**Expected:** the set includes `thinking`, `all`, `with-tools`.
**Refs:** crc-CLITree.md, crc-CLI.md

