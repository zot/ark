# Subscriber-presence check

Language: Go. Environment: ark CLI binary + server.

A small pubsub query that answers "is anyone listening for this tag
right now?" — used by producers of expensive artifacts (recall
curation docs, recall result docs) to skip work when no consumer is
present. The guiding line: if the lights aren't on, there's nobody
home.

See also: [pubsub.md](pubsub.md) (subscription registry — this query
reads from it), [simple-recall.md](simple-recall.md) (the two
producers that gate their writes on this query).

## The query

```go
db.SubscriberCount(tagName string, tagValue string) int
```

Returns the number of currently-registered subscriptions whose
predicate would accept `(tagName, tagValue)` if such a tag were
published right now. The semantics match the existing publish-time
predicate match in `pubsub.go` — same name normalization, same
value-mode handling, same file-filter logic (file filters are
ignored for this query: the question is "could anyone receive
this?", not "would this specific file pass each subscriber's
filter?").

The registry is in-memory; the query is O(subscriptions).
Subscriptions whose session has expired (per the TTL reaping
behavior in [pubsub.md](pubsub.md)) are not counted — the reaper
removes them before the count runs, or the query treats them as
absent.

`SubscriberCount` returns zero when no session is subscribed. A
non-zero return means at least one live session would receive an
event with this `(tagName, tagValue)` pair if it were published.

### CLI surface

```
ark subscribers --tag TAG [--quiet]
```

`--tag` accepts the sigil syntax used elsewhere (see
[file-tag-filter.md](file-tag-filter.md)). With a name-only tag
(`--tag chime-45m`), the query asks "would anyone receive *any*
event with this name?". With a value form (`--tag ark-recall-result=abc123`),
the query asks "would anyone receive an event with this exact
(name, value) pair?".

Default output: the integer count on stdout, followed by a newline.

`--quiet`: no stdout output. Exit code carries the answer — `0` if
the count is non-zero, `1` if zero. This is the shell-pipeline
form, designed for `if ark subscribers --quiet --tag X; then ...`.

Server required: the subscription registry is server-only state.

## Consumers

Two producers gate writes on this query.

### Recall watcher

The recall watcher (`recall_watcher.go`, [simple-recall.md](simple-recall.md))
writes a curation doc to `tmp://ARK-RECALL/curation-<session>-<fire>`
when a fire produces at least one section that clears
`[recall].min_similarity` (R2753). Before the write, the watcher
queries `SubscriberCount("ark-recall-curate", "<originating-session-uuid>")`.
If the count is zero, the watcher skips the curation doc, clears
`pendingChunks` as it would on a normal completion, and writes a
`outcome: "no-subscriber"` record to `recall.jsonl`. No fire is
counted as missed — there was simply nobody to deliver to.

### Recall agent's `close`

`ark connections recall close` writes the result doc to
`tmp://ARK-RECALL/result-<session>-<fire>` when at least one
`surface` or `recommend` item was added during the fire (R2758).
Before the write, the handler queries
`SubscriberCount("ark-recall-result", "<originating-session-uuid>")`.
If the count is zero, the handler skips the result doc write, still
sweeps the curation doc and orphan curation docs per R2758, and
writes a `outcome: "no-subscriber"` record to `recall.jsonl` (the
same outcome string as the watcher's skip path — the assistant was
not listening at close time).

The cleanup side of `close` (curation doc removal, orphan sweep,
monitoring log append) runs regardless of subscriber presence.
The check gates only the result-doc *write*, not the cleanup.

## What this spec deliberately does not require

- A subscription-change notification stream. The check is
  point-in-time. A producer that wants to react when a subscriber
  arrives must poll.
- File-filter or value-pattern resolution. The check ignores
  `-files` rows on subscriptions — they
  would require knowing the file path the would-be event came
  from, which the producer often doesn't have at decision time.
  Over-counting (a subscription whose filter would reject the
  event is still counted as present) is acceptable: the failure
  mode is "we did the work but the event was filtered out", which
  is the v1 behavior anyway.
- A way for the agent's `surface` / `recommend` calls to query
  subscriber presence per call. The check happens once at
  `close` time; if no subscriber is present then, the entire
  batch is dropped. Mid-fire subscriber departure is not a case
  worth optimizing.
- An `ark subscribers --list` form that enumerates which sessions
  match. `--list` already exists on `ark subscribe` (see
  [pubsub.md](pubsub.md)) for the registry view; this command's
  job is the boolean / count answer.
