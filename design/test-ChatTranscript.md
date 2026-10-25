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

## Test: an existing file path resolves directly
**Purpose:** R3242 — an argument naming an existing file resolves to that file,
with no index lookup and no `~/.claude/projects/` walk, so any on-disk JSONL
renders. Guards the #66 contract: existence is the only test.
**Input:** a JSONL file written under `t.TempDir()` (outside any projects dir);
`resolveChatFiles` called with its absolute path.
**Expected:** the returned list is exactly that path.
**Refs:** crc-CLI.md

## Test: non-file arguments fall back to the basename glob
**Purpose:** R1050, R3243 — an argument that is not an existing file is treated
as a basename glob against the projects walk; an argument yielding nothing
leaves the result empty (caller warns and exits 1 only when the union is empty).
**Input:** `resolveChatFiles` called with a pattern matching nothing (the walk
reads `$HOME` at call time, so `t.Setenv("HOME", t.TempDir())` makes it
hermetic), then with a mix: one existing file plus one no-match pattern.
**Expected:** first call returns empty; second returns just the existing file,
in argument order.
**Refs:** crc-CLI.md

## Test: the urfave `chats` node declares the flags (wiring Sentry)
**Purpose:** R3035 — guard the migration bug the render test can't see: a flag
declared only on the legacy `cmdChats` flag set but not on the urfave node is
rejected ("flag provided but not defined") before `cmdChats` runs. Assert the node
declares `--thinking` / `--all`.
**Input:** find the `chats` command in `flatCommands()`; collect its flag names.
**Expected:** the set includes `thinking`, `all`, `with-tools`.
**Refs:** crc-CLITree.md, crc-CLI.md


## Test: the renderer's output, option by option
**Purpose:** R1045, R1047, R1049, R3035. The exact output for one transcript
holding every case the renderer distinguishes, now read through the transcript
reader (R3553). Before that move only `--thinking` was tested; on 2026-10-22,
injecting a space join of a user turn's lines, a dropped sidechain filter and an
unconditional tool line turned nothing red.
**Input:** a person turn; a sidechain turn; an assistant turn with text,
thinking and a tool call; a tool-result turn; a turn end; a person turn of two
text blocks.
**Expected:** by default the two person turns and the assistant text, the
two-block turn on two lines; `--with-tools` adds `⚙ Bash ls -la`; `--all` adds the
sidechain turn; thinking and tools never appear by default.
**Refs:** crc-CLI.md, crc-TranscriptReader.md
**Code:** chats_render_test.go
**Fire alarm:** join a user turn's text blocks with a space; the default render differs.
**Inject:** chats.go:chatText
**Pulled:** 2026-10-22 — rang: default render differs
**Fire alarm:** drop the sidechain filter; the default render shows SIDECHAIN.
**Inject:** chats.go:RenderChatLines
**Pulled:** 2026-10-25 — rang: default render differs (re-pulled after R3610 edited RenderChatLines)
**Fire alarm:** show tool calls without `--with-tools`; "tool line shown without --with-tools".
**Inject:** chats.go:RenderChatLines
**Pulled:** 2026-10-25 — rang: default render differs (the comparison fails before the tool-line check; re-pulled after R3610)

## Test: a session's turn count
**Purpose:** a chat session's turn count is every user and assistant record
outside sidechains, tool-result carriers included and turn ends not. The label
and the sidechain rule are TestReadChatLabel's.
**Input:** the renderer's transcript.
**Expected:** 4 turns.
**Refs:** crc-CLI.md, crc-TranscriptReader.md
**Code:** chats_render_test.go
**Fire alarm:** count turn ends as turns; the count reads 5.
**Inject:** chats.go:readChatLabel
**Pulled:** 2026-10-22 — rang: turns = 5, want 4

## Test: narration renders with its own mark
**Purpose:** R3610: with `--thinking`, narration shows as `≈ ...`, thinking keeps `✻ ...`
**Input:** an assistant record with a thinking block and a narration-signed block
**Expected:** `✻ PONDERING` and `≈ PARAPHRASED` in the output
**Refs:** crc-CLI.md
**Code:** cmd/ark/chats_test.go
**Fire alarm:** render narration with `✻`; red is "narration should carry the ≈ marker"
**Inject:** chats.go:RenderChatLines
**Pulled:** 2026-10-25 — rang ("narration should carry the ≈ marker"; got ✻ PARAPHRASED)
