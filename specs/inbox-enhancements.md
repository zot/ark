# Inbox Enhancements

Language: Go. Environment: CLI (part of the `ark` binary).

The `ark message inbox` command shows messages relating to a project.
Agents (especially Hermes on Haiku) need additional capabilities to
avoid shell gymnastics with grep/wc/cut pipelines.

## --project flag (either side)

```
ark message inbox --project PROJECT
```

Filters messages where either `@to-project` OR `@from-project`
matches PROJECT. This is the "everything involving PROJECT" view —
both inbound traffic (responses, incoming requests) and outbound
traffic (this project's outgoing requests still waiting on a reply).

When a tighter view is needed, `--to` and `--from` filter each
direction independently.

## --to flag

```
ark message inbox --to PROJECT
```

Filters messages where `@to-project` matches PROJECT. This is the
strict "incoming for PROJECT" view: requests targeted at PROJECT
plus responses to PROJECT's outgoing requests. `--to` is what the
old `--project` flag did before it was broadened.

## --from flag

```
ark message inbox --from PROJECT
```

Filters messages where `@from-project` matches PROJECT. This is the
outgoing view: "what did PROJECT send?"

Composable with `--project` and `--to` — when given together, a
message must match every filter (intersection, not union). `--from
ark --to frictionless` selects only the ark→frictionless slice.

## --all flag

```
ark message inbox --all
```

Includes messages with any `@status` value, including completed, done,
and denied. Without `--all`, current behavior is preserved (these
statuses are filtered out). Archived messages (`@archived: true`) are
still excluded unless `--include-archived` is also given.

Composable with `--project`, `--from`, and `--counts`.

## --include-archived flag

```
ark message inbox --include-archived
```

Includes messages with `@archived: true`. Without this flag, archived
messages are excluded (current behavior). Composable with all other
flags.

## --counts flag

```
ark message inbox --counts
```

Instead of individual message rows, outputs one line per status value
with the count of messages matching that status:

```
open	12
accepted	2
future	1
```

Tab-separated, sorted alphabetically by status name. Composable with
`--project`, `--from`, `--all`, and `--include-archived` — the counts
reflect whatever the other filters select.

When `--counts` is used without `--all`, only non-completed statuses
appear (because completed/done/denied messages are already filtered
out before counting).
