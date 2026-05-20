# Nano: sessions

## Why local persistence

nano.py relied on OpenAI's Responses API to remember the conversation
server-side; a session was just a stored response ID. Ollama has no
such notion — every `/api/chat` call must include the full message
history. To preserve the `-c` (continue) and `-s` (pick) features
without a server, nano stores the full message log on local disk.

## File format

Sessions live in a single JSON file. The default path is
`~/.ark/nano-sessions.json` (inside ark's home directory); library
users override it via `Nano.SessionsPath`. The file holds an array of
`NanoSession` objects:

```json
[
  {
    "label": "find the bug in auth.go",
    "cwd": "/home/me/work/svc",
    "ts": 1715890123,
    "messages": [
      {"role": "system",    "content": "You are Nano..."},
      {"role": "user",      "content": "find the bug in auth.go"},
      {"role": "assistant", "content": "", "tool_calls": [...]},
      {"role": "tool",      "content": "$ cat auth.go\nexit 0\n..."},
      {"role": "assistant", "content": "Here's what I found..."}
    ]
  }
]
```

Fields:
- `label` — the prompt that started the session, truncated to 80
  characters. Used as the display name in the picker.
- `cwd` — absolute path of the working directory when the session was
  saved. Used by `-c`/`-s` to filter sessions to the current project.
- `ts` — Unix epoch seconds at last save. Used to compute the
  "Nm/Nh/Nd ago" hint in the picker.
- `messages` — the full chat history, including the system prompt,
  tool calls, and tool results.

## Save semantics

On every save:
- Any existing session with the same `label` and `cwd` is replaced
  rather than duplicated.
- The file is capped at the most recent 50 sessions; older entries
  are dropped.
- The file is written with mode 0600 so other users on the machine
  cannot read another user's shell history.

## Continue (`-c`)

Loads the most recently saved session in the current working
directory. Errors with `no sessions in this directory` when there are
none.

## Pick (`-s`)

Lists up to ten most recent sessions in the current working
directory, each with a numeric index and a relative-time hint. Reads
a digit from stdin and resumes the chosen session.

## KeepHistory opt-in for library users

The library defaults `KeepHistory` to false so embedding nano in
another program doesn't quietly write to disk. The CLI explicitly
sets it to true because session resume is a feature it ships.

## Distinction from nano.py's and nano-go's files

Three sessions files in the same lineage do not collide:

- nano.py uses `~/.nano_sessions.json` with `{id, label, cwd, ts}`
  (OpenAI response IDs).
- Standalone nano-go uses `~/.nano-go_sessions.json` with
  `{label, cwd, ts, messages}`.
- Ark's embedded nano uses `~/.ark/nano-sessions.json`, same shape as
  standalone nano-go.

A user with both standalone nano-go and `ark nano` on the same
machine sees two independent histories — by design.
