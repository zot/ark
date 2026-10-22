# Chat Transcript

Display conversation transcripts from Claude Code JSONL session logs.
Language: Go. Environment: ark CLI (no server needed).

The transcripts are read through [transcript-reader.md](transcript-reader.md),
the one part of ark that decodes Claude Code's JSON; this spec covers how they
are rendered.

## ark chats

```
ark chats PATH-OR-GLOB... [--with-tools] [--thinking] [--all] [--wrap NAME] [--line-length N]
```

Reads Claude Code JSONL conversation logs and renders them as
human-readable transcripts. Takes one or more positional arguments.
Each argument that names an existing file is rendered directly — any
JSONL file on disk works, whether or not it is indexed or lives under
`~/.claude/projects/`. An argument that is not an existing file is a
glob matched against file basenames in `~/.claude/projects/`
directories. Arguments that match nothing get a warning on stderr;
the command exits 1 only when no argument yields any file.

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

## Listing conversations — `ark chats list`

```
ark chats list [--source NAME] [--for NAME] [--limit N] [--json]
```

`ark chats` renders a conversation you can already name. This answers the
question that comes first: **what conversations exist, and which one do I
want?** Without it the only way in is `ls -lt` over a project directory, which
returns UUIDs and mtimes — so the conversation is chosen by guessing which
timestamp it was.

Each row identifies a session by something a human can read:

- **Label** — the session's **first user turn**, truncated. This is the whole
  point of the verb. A list of UUIDs is a prettier `ls`; a list of opening
  sentences is a table of contents.
- **Modified time**, newest first.
- **Turn count**, so a two-message stub is distinguishable from a day's work.
- **Path**, which is what `ark chats` takes, so the list composes with the
  renderer rather than replacing it.

Sessions come from the **index**, not a directory walk: chat directories are
already ark sources with `chat-jsonl` files, so this is a files-in-a-source
query the index answers. A JSONL on disk but not indexed does not appear —
consistent with every other list verb, and `ark chats PATH` still renders it.

`--source NAME` restricts to one chat source. `--for NAME` restricts to the
chat sources that log a **code** source (below). `--limit N` caps the rows
(default 20; newest wins). `--json` emits one object per line for programmatic
callers.

**Labels are read on demand, not cached.** The first user turn is at the head
of the file, so producing a label is a bounded read regardless of session
length. Measure before adding a record class: a cache here would introduce a
staleness class (an appended session whose label never changes) to save work
that may already be negligible.

## Pairing a chat source to the project it logs

A code source (`/home/deck/work/ark`) and the chat source recording work done
in it (`~/.claude/projects/-home-deck-work-ark`) describe the same project, and
nothing in ark records that. `[[source]].logs_for` does:

```toml
[[source]]
dir = "/home/deck/.claude/projects/-home-deck-work-ark"
logs_for = "ark"
```

The value names another source. It is **declarative and explicit** — greppable,
correctable, and meaningful for any log source, not only Claude Code's.

**Naming a source.** A source is identified by its `dir`, and its **name** is
that directory's last segment — `/home/deck/work/ark` is named `ark`. Anywhere
a source name is accepted (`logs_for`, `--source`, `--for`), the full `dir` is
accepted too and tried first, so the short form is a convenience rather than a
second identity. Two sources sharing a last segment make the short form
ambiguous; that is an error naming both candidates, not a silent pick, and the
full `dir` resolves it. Taking a path's last segment is not the vendor mangling
R3289 forbids — it derives a *label for a source ark already has*, whereas the
mangling reconstructs *which directory another program would have chosen*.

The pairing is *deliberately not computed at lookup time.* Claude Code derives
that directory name by mangling the cwd (`/home/deck/work/ark` →
`-home-deck-work-ark`), and reproducing that mangling inside ark would work
today while hardcoding one vendor's naming scheme into the query path, where
every caller inherits it and a change upstream breaks pairing silently. Worse,
it would only ever work for that one vendor.

**But the heuristic is still useful once**, as a populator rather than a rule:

```
ark sources pair [--auto] [--dry-run]
```

`--auto` proposes a `logs_for` for each unpaired chat source by applying the
mangling heuristic, prints what it matched, and writes the results into
`ark.toml` through the config path. So adoption costs one command, while the
vendor scheme lives in a one-shot helper you can delete — not in the lookup.
`--dry-run` prints the proposals without writing. A source that already carries
`logs_for` is left alone; a proposal matching no existing source is reported and
skipped rather than written, since a `logs_for` naming nothing is worse than an
absent one.

Pairing is what `--for` consumes, and what #71's UI needs to go from "I'm
looking at the ark project" to "here are its conversations."

## Rendering from the UI

The transcript renderer is reachable from Lua, so a Frictionless app can show a
conversation without shelling out: the session list and the rendered markdown
are both available in-process. The Lua surface is recorded in
[lua-api.md](lua-api.md); the rendering behavior is this spec's, and both
clients (CLI and UI) render through the same code so their output cannot drift.
