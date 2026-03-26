# Tag Pubsub

Language: Go. Environment: CLI + server (part of the `ark` binary).

Agents and sessions subscribe to tag patterns. When indexed content
produces matching tags, ark delivers notifications through a long-poll
channel. Output is markdown crank handles — self-contained prompts
that tell the agent what happened and how to act on it.

See .scratch/PUBSUB.md for the full brainstorm.

## Subscribe

`ark subscribe` registers interest in tags for a session.

```bash
# subscribe to all status changes
ark subscribe --session $ID --tag status

# subscribe with value filter (RE2 regex on tag content)
ark subscribe --session $ID --tag to-project --value 'ark'

# exclude files from matching (prevents infinite loops)
ark subscribe --session $ID --tag status \
  --except-files '/home/deck/.claude/**/*.jsonl'
```

Each `--tag` is one subscription entry. Optional `--value REGEX`
filters on the tag's content. No `--value` means match any value.

Tag names are normalized: a leading `@` and trailing `:` are
stripped so that `--tag @status:`, `--tag @status`, and `--tag status`
all resolve to the tag name `status`. Users naturally type the
`@tag:` form they see in files — the CLI shouldn't punish that.

Multiple `--tag` flags in one command create multiple subscriptions
(OR — any match fires). Each subscription is independent.

File filters parallel search flags:
- `--filter-files GLOB` — only match tags in files matching the glob
- `--except-files GLOB` — never match tags in files matching the glob

Both are checked at publish time before enqueue. They compose the
same way as in search: `--filter-files` sets the scope,
`--except-files` carves out exceptions within it. Example:
`--filter-files '~/.claude/projects/**/*.jsonl'` watches only
conversation logs; adding `--except-files '**/c9f6bd1d*.jsonl'`
excludes your own session's log from that set.

### Cancel

```bash
# cancel ALL subscriptions for this session
ark subscribe --session $ID --cancel

# cancel subscriptions for a specific tag
ark subscribe --session $ID --cancel --tag dm

# cancel only subscriptions matching a specific value
ark subscribe --session $ID --cancel --tag dm --value 'abc123'
```

Bare `--cancel` wipes all subscriptions for the session — clean
slate on reconnect. `--cancel --tag TAG` removes all subscriptions
for that tag. Adding `--value VAL` narrows: only cancels
subscriptions whose value regex would match VAL.

## Listen

`ark listen` long-polls for notifications and outputs markdown
crank handles.

```bash
ark listen --session $ID [--timeout 120]
```

Blocks until events are queued or timeout expires. On events,
outputs markdown separated by `---` dividers. Each event is a
descriptive phrase followed by file references the agent can act on.

For full file contents: provides the `ark fetch` command.
For chunks: provides `ark chunks FILE LOCATION -after COUNT`.

The agent reads the markdown, decides what matters, acts or ignores.
Sequencing intelligence is in ark (what to push, when); judgment is
in the model (what to do about it).

After output, the queue is drained. The agent loops back to listen.

## List

`ark subscribe --list` shows active sessions and their subscriptions.
`ark subscribe --list --session ID` shows subscriptions for one session.

Output is tab-separated:
```
session	tag	value_regex	filter_files	except_files	hits	drops
```

## Stats

`ark subscribe --stats` shows aggregate hit/drop counts across all
sessions. `ark subscribe --stats --session ID` shows stats for one
session. Hits = events successfully enqueued. Drops = events lost
because the queue was full.

Stats are in-memory, reset on server restart.

## Muting

`@mute: true` in a file silences all pubsub events from that file.
The publisher checks for it before matching subscriptions — no
events fire, no watchdog findings, nothing. The file is still
indexed and searchable; it just doesn't trigger notifications.

Useful for silencing a file temporarily without deleting its tags
or removing it from the index. Remove `@mute: true` to resume.

## Publish

Publishing is implicit — it happens in the existing tag extraction
path. When tags are extracted during add, refresh, or append, each
tag is checked against the subscription registry. On match: check
value regex (if any), check file exclusions, then enqueue a
notification for the subscribing session.

The publish hook goes in `Indexer.AppendFile` (after
`store.AppendTags`, ~indexer.go:352) and in `prepareRefresh`
(~indexer.go:155). No new extraction — just matching against what
was already extracted.

## Subscription Registry

In-memory. Dies with server. Agents re-subscribe on connect.

Per-session data:
- List of subscriptions: (tag name, optional value regex, file exclusions)
- Notification channel: bounded, lossy (non-blocking send, drop if full)
- Last-listen timestamp: for TTL reaping

### TTL Reaping

If a session hasn't called listen within the TTL (default 10
minutes), the server drops its subscriptions and drains its queue.
Prevents memory leak from dead agents.

The listen call resets the timer. A well-behaved agent in a
long-poll loop never expires.

Reaper runs on a server ticker (once per minute, scan the map,
drop stale entries).

### Queue Depth

Bounded channel per session. Default 100 events. If full, new
events are dropped — pubsub is notification, not reliable delivery.
Agents that need completeness can also poll the inbox.

### Self-notification

A session does not receive notifications about its own writes
by default. You know what you just did.

## Event Scheduler

Ark maintains a priority queue sorted by next-fire time and a
single `time.Timer` for the head of the queue.

When the timer fires: deliver the event as a crank handle through
listen, pop the entry, if recurring compute the next occurrence
and re-enqueue, reset the timer to the new head.

### Scheduling as a Subscription Property

There are no predefined scheduling tags. Any tag can be scheduled
or recurring — the subscriber declares the behavior:

```bash
# treat @dentist: values as one-shot scheduled events
ark subscribe --session $ID --scheduled --tag dentist

# treat @standup: values as recurring events
ark subscribe --session $ID --recurring --tag standup

# treat @birthday: values as annually recurring events
ark subscribe --session $ID --recurring --tag birthday
```

When a subscription has `--scheduled` or `--recurring`, the pubsub
system:
1. Scans the index for existing files with that tag
2. Parses the date/recurrence from each tag's value
3. Adds entries to the event scheduler, bound to this subscription
4. When the event fires, delivers through this session's listen

This means the tag vocabulary is entirely user-defined. `@standup:`,
`@forecast:`, `@sprint-review:`, `@rent-due:` — whatever tags the
user writes, the subscriber decides which are timed events.

### Time Value Format

Tag values for scheduled/recurring subscriptions follow this
grammar:

**One-shot** (`--scheduled`):
```
@dentist: 2026-04-15 09:00 cleaning
@release: 2026-12-25
```
DATE formats: `YYYY-MM-DD HH:MM`, `YYYY-MM-DD` (defaults 09:00).
`MM-DD` (annual — next occurrence of that month-day).
Past one-shot events are ignored.

**Recurring** (`--recurring`):
```
@standup: every Monday at 09:00
@checkin: every weekday at 08:30
@retro: every 3rd Monday
@rent: every 15th of the month
@chime: every 15m
@review: starting on 2026-04-01 every Tuesday at 10:00 ending on 2026-12-31
@bookclub: every 2nd Saturday of the month
@birthday: 04-15 Mom
```

Full grammar:
```
[starting [on|at] DATE] every [ORDINAL] PERIOD [at HH:MM]
  [ending [on|at] DATE] [DESCRIPTION]
```

ORDINAL (optional): `second`, `third`, ... `tenth`, or `1st`,
`2nd`, `3rd`, ... `365th`.

PERIOD: one of:
- **Duration:** `Nm` (minutes), `Nh` (hours)
- **Day name:** `Sunday`, `Monday`, ... `Saturday`
- **Day class:** `weekday` (Mon-Fri), `weekend` (Sat-Sun), `day`
- **Scope:** `of the week`, `of the month`, `of the year`

When no time is given, defaults to 09:00. When no start date,
starts immediately. When no end date, recurs indefinitely.

Annual shorthand: a bare `MM-DD` value (like `04-15 Mom`) is
treated as annually recurring — fires at 09:00 on that date
each year.

**Structured payloads.** The tag value carries the schedule;
the rest of the chunk is the payload. Markdown code fences work
naturally:

```markdown
@forecast: 2026-08
\`\`\`toml
[weather."0900"]
humidity = "35%"
\`\`\`
```

The tag gets you to the chunk. The code fence is the content.
No new data format needed.

### Stopping a Recurring Event

```
@ended: [REASON]
```

Must be in the **same chunk** as the recurring tag — same
paragraph in markdown, same comment block in code. When the
scheduler reads a chunk containing both the subscribed tag and
`@ended:`, it skips the event entirely.

```
@standup: every Monday at 09:00
@ended: team dissolved 2026-06-15
```

The `@ended:` tag is searchable — `ark search --tags ended` finds
all stopped events and why.

### Scheduling Mechanics

Only the *next* occurrence of a recurring event lives in the queue.
When it fires, compute the following occurrence and re-enqueue. No
cycles of cycles — just "when's the next one?" after each fire.

Scheduled events are per-subscription. Different sessions can
subscribe to different tags as scheduled/recurring. The scheduler
fires to the subscribing session, not broadcast.

At startup, when a `--scheduled` or `--recurring` subscription is
registered, the system scans for existing tags via `TagContext`
and populates the queue. New tags arriving after subscription
are picked up in the normal Publish path — the publish hook
checks for scheduled/recurring subscriptions and feeds the
scheduler.

### Quarter Chimes

A built-in recurring event: every 15 minutes, push a chime with
the full date, day of week, and time of day. One line of markdown.
Gives agents temporal awareness without asking. Implemented as
`@recurring: every 15m` internally.

### Variable-date Holidays

A Lua function in init.lua computes variable-date holidays (Easter
computus, lunar calendar) at startup and writes them to a tmp://
file with `@event:` tags. The scheduler picks them up through the
same tag-scanning path as everything else.

### Push Records

In-memory set of (event-id, session-id) pairs. Prevents duplicate
delivery within a server lifetime. Server restart clears it;
startup re-scan fires anything due that hasn't been delivered.

## Relationship to tmp:// Documents

Pubsub and tmp:// compose naturally:

- An agent writes a tmp:// doc with tags → pubsub fires for those
  tags → subscribing agents read the pre-computed result
- AppendChunks on tmp:// docs means each append fires pubsub for
  new tags — agents see incremental updates, not batch dumps
- Agent-to-agent chat: each agent appends to its own
  `tmp://SESSIONID/chat-with-PARTNER` file, subscribes to
  `@dm: SELF`. The half-thread model from ARK-MESSAGING.md,
  ephemeral and in-memory

These are usage patterns, not implementation requirements. Pubsub
doesn't depend on tmp:// and vice versa.

### Error aggregation via tmp://

Subsystems that encounter non-fatal errors (unparseable dates, stale
refs, missing files) append tagged diagnostics to tmp:// error files:

```
tmp://errors/scheduling
  @error: cannot parse recurring spec for @standup: "evry monday" in ~/notes/cal.md
  @error: cannot parse scheduled date for @dentist: "next week" in ~/notes/appts.md
```

Each append is a chunk — searchable, subscribable. Franklin can
subscribe to `@error:` and surface problems in the morning briefing
instead of hoping someone tails the log.

Uses `ark add --append --content CONTENT tmp://PATH` — same command
as tmp:// creation, with `--append` to add content without replacing.
(Depends on tmp:// append support — `--append` flag on `ark add`.)

### Unsubscribed tag watchdog

The publisher already iterates all tags in a file. Tags that match
no subscription are silently dropped — but some of those drops are
interesting:

- **Schedulable orphans.** A tag whose value parses as a date or
  recurrence spec but has no `--scheduled` or `--recurring`
  subscription and no `@ended:` in the chunk. Someone wrote a
  time-tagged entry but nobody's listening. Append to
  `tmp://watchdog/unsubscribed-schedules`.

- **Near-miss typos.** A tag whose name is within edit distance 1-2
  of a subscribed tag name. `@standups:` when `@standup:` is
  subscribed. `@stndup:` for the same. Append to
  `tmp://watchdog/possible-typos`.

Franklin subscribes to `@watchdog:` and surfaces these in the
morning briefing: "Found 2 tags that look like they should be
scheduled but aren't, and 1 possible typo." The cost is one
Levenshtein check per unmatched tag per publish — negligible
since the subscribed set is small.
