# Chat Transcript

Display conversation transcripts from Claude Code JSONL session logs.
Language: Go. Environment: ark CLI (no server needed).

## ark chats

```
ark chats GLOB [--with-tools] [--thinking] [--all] [--wrap NAME] [--line-length N]
```

Reads Claude Code JSONL conversation logs and renders them as
human-readable transcripts. GLOB matches against file basenames
in `~/.claude/projects/` directories.

Each user turn is introduced with `❯`, each assistant turn with `●`.
Continuation lines within a turn are indented by 2 spaces. Text is
word-wrapped at `--line-length` (default 100).

`--with-tools` shows tool calls inline as `⚙ ToolName summary`.
Tool input is summarized — the most useful field (command, file_path,
pattern, prompt, etc.) is shown, truncated at 80 chars.

`--thinking` shows assistant chain-of-thought (thinking) blocks inline
as `✻ ...`, word-wrapped like any turn. Off by default — thinking is
verbose — but the corpus already indexes it (the chat-jsonl chunker
extracts both `text` and `thinking`), so this only restores display
parity with what search already sees.

`--all` is a convenience for a complete transcript: it turns on
`--with-tools`, `--thinking`, and `--sidechain` together.

`--wrap NAME` surrounds the output with `<NAME>...</NAME>` tags,
useful for embedding transcripts in prompts.

Sidechain messages (subagent traffic) are filtered out by default;
`--sidechain` (or `--all`) includes them.
