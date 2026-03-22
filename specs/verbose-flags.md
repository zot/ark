# Verbose Flags

Ark gains global verbose flags (`-v`, `-vv`, `-vvv`, `-vvvv`) matching
the frictionless convention from `ui-engine/internal/config`.

## Behavior

`-v` through `-vvvv` set a verbosity level (1–4) parsed globally
before subcommand dispatch, the same way `--dir` is handled today.
Both stacked (`-vvv`) and repeated (`-v -v -v`) forms work.

Levels:

- **1 (-v):** connections, server lifecycle events
- **2 (-vv):** protocol messages, HTTP requests
- **3 (-vvv):** variable operations, indexing detail
- **4 (-vvvv):** full values, chunk content

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
