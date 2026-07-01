# Verbose Flags

Ark gains global verbose flags (`-v`, `-vv`, `-vvv`, `-vvvv`) matching
the frictionless convention from `ui-engine/internal/config`.

## Behavior

`-v` through `-vvvv` set a verbosity level (1–4) parsed globally
before subcommand dispatch, the same way `--dir` is handled today.
Both stacked (`-vvv`) and repeated (`-v -v -v`) forms work.

Levels are a graded dial — each higher level is strictly more verbose,
since `Logv(level, …)` emits whenever the global verbosity is at least
`level`. There is no fixed per-level category; a call site chooses a
level by how much detail it carries. Current usage:

- **1 (-v):** coarsest tier — high-level indexing/refresh milestones
  (full refresh, orphan cleanup, PDF page progress).
- **2 (-vv):** fine tier — per-file refresh/append decisions and
  recall-watcher activity.
- **3 (-vvv):** deeper tier for finer operational detail (e.g. variable
  operations); available through the same gate, used as call sites opt in.
- **4 (-vvvv):** deepest tier for the fullest detail (values, chunk
  content).

## Global flag, not per-subcommand

Verbosity is stripped from the argument list before subcommand
dispatch. Every subcommand inherits the level without declaring its
own flag.

## Logging helper

A package-level `Logv(level int, format string, args ...any)` function
checks the global verbosity and emits `[v1] msg` style output via
`log.Printf`, matching the frictionless `Config.Log()` format.

## Server pass-through

When the server starts, it receives the verbosity level via
`ServeOpts`. The server's `setupLogging` and all server-side log
sites use the same level. The Frictionless ui-engine config already
has its own verbosity (`cfg.Logging.Verbosity`); ark's level is
propagated to it when ark starts the embedded UI.
