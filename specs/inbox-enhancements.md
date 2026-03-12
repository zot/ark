# Inbox Enhancements

Language: Go. Environment: CLI (part of the `ark` binary).

The `ark message inbox` command shows incoming messages filtered by
`@to-project`. Agents (especially Hermes on Haiku) need three
additional capabilities to avoid shell gymnastics with grep/wc/cut
pipelines.

## --from flag

```
ark message inbox --from PROJECT
```

Filters messages where `@from-project` matches PROJECT. Composable
with `--project` — when both are given, a message must match both
filters (intersection, not union). When only `--from` is given,
`--project` is unconstrained.

This gives the outgoing view: "what did this project send?"

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
