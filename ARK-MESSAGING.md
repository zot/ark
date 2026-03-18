# Ark Cross-Project Messaging

## Version 3 2026-0318

How ark projects communicate through tagged files that ark indexes
and connects automatically.

## Model

A conversation is like a GitHub issue. One project opens a request
(the issue). Other projects join by creating response files (comments).
Each file is a **half-thread** — the complete record of one project's
participation, growing over time as the conversation progresses.

- One request, potentially many responses (one per participating project)
- Each participant owns exactly one file, appends over time
- The conversation is the unit, not any single exchange
- Tags on each file are like GitHub labels — they drive status,
  filtering, and dashboard views

## Cardinal Rule

**Always write to YOUR project's `requests/` directory.** Never
modify files in another project's directory. A request FROM ark
TO microfts2 lives in `ark/requests/`. A response FROM microfts2
TO ark lives in `microfts2/requests/`.

This is load-bearing. It means:
- No cross-project file writes, ever
- Each project owns its own files completely
- Acknowledgment happens by creating a response, not modifying the request

## Convention

### Requests (the issue)

A requesting project creates a file in its own `requests/` directory:

```
requests/<short-name>.md
```

Use the bare name. If the name already exists, append `-<session8>`
(first 8 chars of Claude session ID) to disambiguate.

Use `ark message new-request` to create (never hand-write tag blocks):

```bash
~/.ark/ark message new-request \
  --from my-project --to target-project \
  --issue "short description" \
  requests/chunker-interface.md
```

The file uses tags for discoverability:

```markdown
@ark-request: chunker-interface
@from-project: ark
@to-project: microfts2
@status: open
@issue: Define Chunker interface for per-chunk attributes

# chunker-interface

What's needed and why. Include enough context for the target
project to act without reading the requesting project's code.
```

The request file is the requester's half-thread. Over time, the
requester appends clarifications, additional context, or revised
requirements. The `@status` tag reflects the requester's current
state, not a single moment.

### Responses (joining the conversation)

Any project can join a conversation by creating a response file in
its own `requests/`:

```
requests/RESP-<request-id>.md
```

One request can have many responses — one per participating project.
Each response is that project's complete half-thread.

Use `ark message new-response` to create:

```bash
~/.ark/ark message new-response \
  --from my-project --to requesting-project \
  --request original-request-id \
  requests/RESP-original-request-id.md
```

```markdown
@ark-response: chunker-interface
@from-project: microfts2
@to-project: ark
@status: accepted

# RESP chunker-interface

What was done. Reference commits, files, or API surfaces.
```

Over time, the responder appends progress notes, questions for
clarification, or delivery details. The file grows; the `@status`
tag reflects the current state.

### Response as acknowledgment

**The response file's existence is the ack.** Creating a response
with `@status: accepted` means "I saw your request, I'll join this
conversation." No need to modify the sender's file. No cross-project
writes.

Response status progression:
1. `accepted` — I saw it, I'll do it
2. `in-progress` — working on it
3. `completed` — here's the result

The sender checks whether their request was seen by searching for
responses to their request ID.

### Multi-project conversations

When a request involves multiple projects:

```
ark/requests/chunker-interface.md          ← the issue (ark)
microfts2/requests/RESP-chunker-interface.md   ← microfts2's thread
mini-spec/requests/RESP-chunker-interface.md   ← mini-spec's thread
```

Each response file has its own `@status` and `@request-handled:`
bookmark. The conversation's overall state is the fusion of all
participants' statuses — like a GitHub issue where multiple
assignees work independently.

### Cross-status tracking (bookmarks)

Each participant tracks what it has **dealt with** from the others:

- `@response-handled:` on a request — the most advanced response
  status the requester has processed
- `@request-handled:` on a response — the request status the
  responder has processed

With multiple responders, the requester's `@response-handled:`
reflects the overall conversation state — the fusion of all
participants. When any responder moves ahead, the bookmark goes
stale and the reminder fires.

**The search index is the eyes, the handled tag is the hands.**
There is no separate "observed" tag. Franklin computes the delta at
query time — look at each counterpart's current `@status` via search,
compare to the local `@*-handled:` tag, and surface the gap.

Absent or stale `@*-handled:` means "I haven't acted on this yet."
The handled tag is the bookmark — it marks your place. The gap
between where the bookmark is and where the counterpart has moved
to is the reminder.

**Hermes never updates any status tag.** Hermes carries messages.
The owning project's session decides when obligations are discharged.

Example lifecycle:

1. **Ark** creates request `flibertygibbet.md`: `@status: open`.
   No `@response-handled` — no response exists.

2. **Microfts2** sees the request but has other priorities. Creates
   `RESP-flibertygibbet.md` with `@status: accepted` to acknowledge
   receipt. Does not set `@request-handled` — hasn't done the work.

3. **Ark** sees the response exists with `accepted`. Sets
   `@response-handled: accepted` — nothing to integrate yet, so
   no work is deferred.

4. **Microfts2** is reminded in a later session (no `@request-handled`
   on its response = unfinished business). Does the work. Updates its
   response to `@status: completed` and sets `@request-handled: open`
   — it has dealt with the open request.

5. **Ark** sees the response is now `completed` but has other
   priorities. Does **not** update `@response-handled` — integration
   work is pending. The stale tag keeps the reminder alive.

6. **Ark** in a later session is reminded again. Integrates the
   changes. Updates `@response-handled: completed` and
   `@status: completed` — both obligations discharged.

The rule: **update the handled tag when you've discharged your
obligations for that state.** If the state change implies work you
haven't done, leave it stale on purpose.

### Self-messages

For notes-to-self (same project as sender and receiver), a single
file with `@status` is sufficient. No response file needed —
just update status on the original: `open` → `completed`.

## Status Values

One lifecycle, clear progression:

- **open** — needs attention
- **accepted** — acknowledged, will act
- **in-progress** — being worked on
- **completed** — finished
- **denied** — won't do (response body explains why)
- **future** — not yet actionable, parked for later

### Archiving

`@archived: true` is separate from status. Any message can be archived
regardless of its status. Inbox excludes archived messages by default.
Searches can exclude with `--exclude-file-tags archived`.

```bash
# Archive a completed request
~/.ark/ark tag set requests/old-request.md archived true

# Find archived messages
~/.ark/ark search --filter-file-tags archived --filter-files '**/requests/*'
```

## IDs

Format: `<short-name>` — the bare descriptive name. Only append
`-<session8>` (first 8 chars of Claude session ID) if the name
already exists. The `@ark-request:` / `@ark-response:` tag says what it
is. The requesting project owns the ID.

## Tags

- `@ark-request: <id>` — a cross-project request
- `@ark-response: <id>` — a response to a request
- `@from-project: <name>` — who's asking
- `@to-project: <name>` — who's answering
- `@issue:` — short description (also used as card name in dashboard)
- `@status: <value>` — lifecycle state (open, accepted, in-progress, completed, denied, future)
- `@status-date: <date>` — when status last changed (set automatically by `ark message`)
- `@response-handled: <value>` — on requests: the response status the sender has dealt with
- `@request-handled: <value>` — on responses: the request status the responder has dealt with
- `@comment: <slug-id> <subject>` — thread comment (heading-level); ID is first word, subject is rest
- `@reply-to: <project>[:<comment-id>]` — what this comment responds to; bare project = initial entry
- `@reopened: <date> -- <reason>` — request was completed but incomplete
- `@resolved: <date> -- <description>` — reopened issue was fixed

## Finding Conversations

```bash
# Inbox: requests to me with no response from me yet
# (implementation TBD — needs join logic in ark message inbox)

# All requests to a project
~/.ark/ark search --exclude-files '*.jsonl' --regex '@to-project:.*\bPROJECT\b'

# Responses to a specific request
~/.ark/ark search --exclude-files '*.jsonl' --regex '@ark-response:.*REQUEST-ID'

# All tags co-occurring with a request
~/.ark/ark search --exclude-files '*.jsonl' --tags REQUEST-ID

# Waiting-for: my requests with no response yet
~/.ark/ark search --exclude-files '*.jsonl' \
  --regex '@from-project:.*\bMY-PROJECT\b' --regex '@ark-request:'
# then check which have matching @ark-response: files

# Needs attention: requests where response changed since I last handled it
# Franklin computes this: search for counterpart's @status, compare to
# local @response-handled. Gap = unfinished business.
```

Both request and response files surface together — ark connects
them through content, not directory structure.

## Reopening

When a completed request turns out to be incomplete, change
`@status:` back to `open` and add a `@reopened:` tag with the
date and reason:

```markdown
@status: open
@reopened: 2026-03-09 -- field added but Start() ignores it
```

The `@reopened:` tag is searchable — `ark search --tags reopened`
finds all requests that needed a second pass.

When the reopened issue is fixed, add `@resolved:` with the date
and description:

```markdown
@resolved: 2026-03-09 -- fixed in mcp.Server.Start()
```

## Project View (Kanban)

Each conversation appears once on the board. **Column placement is
the request's `@status`.** The requester owns the issue and decides
the overall state — just as a GitHub issue author closes the issue,
not the assignees.

Response statuses appear as chips on the card: `PROJECT:status`,
shown only when a participant's bookmark is stale (behind the
counterpart). A clean card means everyone is current. One or two
chips means someone owes work.

```
┌──────────────────────────┐
│ ark:open  microfts2:done │  ← stale bookmarks only
│ Chunker interface        │
│ ark → microfts2          │
└──────────────────────────┘
```

```bash
# All open work
~/.ark/ark search --exclude-files '*.jsonl' --regex '@status:.*\bopen\b'

# Everything in progress
~/.ark/ark search --exclude-files '*.jsonl' --regex '@status:.*\bin-progress\b'

# Completed items
~/.ark/ark search --exclude-files '*.jsonl' --regex '@status:.*\bcompleted\b'
```

```
 Future       Open          Accepted      In-Progress   Completed
 ──────       ────          ────────      ───────────   ─────────
 tag+value    chunker       chunk attrs                 JSONL unwrap
 index        interface                                 ack/close
              CLI help                                  inbox
```

Franklin uses this to build the daily view — what's open, what's
waiting, what got done.

## Thread Content

Files are half-threads — they grow over time. The initial body is
the issue description (requests) or first response (responses).
Subsequent entries use structured comments:

```markdown
# @comment: slug-id Short subject line
@reply-to: PROJECT
or
@reply-to: PROJECT:comment-id

Comment text. Markdown, as much as needed.
```

- **`@comment:`** — heading-level tag. The first word is the comment
  ID (a mnemonic slug), the rest is the subject line. IDs are local
  to the file; globally addressed as `PROJECT:comment-id`.
- **`@reply-to:`** — what this comment responds to. Bare `PROJECT`
  replies to the initial entry. `PROJECT:comment-id` replies to a
  specific comment in that project's file.

The initial body has no `@comment:` tag — it's the root, addressed
by the project name alone.

### Example thread

In ark's request file:
```markdown
@ark-request: chunker-interface
@status: open
...

# chunker-interface

We need a Chunker interface with per-chunk attributes.
[]Pair not map[string]string.

# @comment: clarify-pair-format Pair format details
@reply-to: microfts2:need-attr-format

Each Pair is {Key string, Value string}. Duplicate keys allowed.
See design/design.md Chunk CRC card.
```

In microfts2's response file:
```markdown
@ark-response: chunker-interface
@status: in-progress
...

# RESP chunker-interface

Accepted. Will implement Chunker interface.

# @comment: need-attr-format What format for chunk attributes?
@reply-to: ark

Implementing the chunker but need to know — are attrs
key=value pairs or structured JSON?
```

### Chunking

The markdown chunker splits on blank lines, so only the first
paragraph of a comment stays grouped with its `@comment:` /
`@reply-to:` tags. Short comments (typical for agent exchanges)
work fine. If multi-paragraph comments cause search to lose the
tag association, a message-aware chunker that treats each
`# @comment:` block as a single chunk would fix it.

### Content expectations

Responses should always contain at least a one-line summary of
what was done. An empty response body with `@status: accepted`
is valid as an initial ack, but as work progresses the responder
appends what changed — commits, files, API surfaces. Franklin
needs this to distinguish "completed" from "completed but nobody
wrote down what happened."

The requester appends too: clarifications when asked, revised
requirements when scope changes, integration notes when consuming
a response. The file is the complete record of one side's
participation.
