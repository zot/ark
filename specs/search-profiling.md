# Search Profiling

Search is the hot path. When investigating performance, you need
CPU and memory profiles from real queries against real data.

## Flags

`ark search` gains three flags:

- `--cpuprofile FILE` — write a Go pprof CPU profile covering the
  entire search operation (DB open through result output).
- `--memprofile FILE` — write a Go pprof heap profile after the
  search completes (after GC, before exit).
- `--trace FILE` — write a Go execution trace covering the entire
  search operation. View with `go tool trace` or load directly in
  Chrome DevTools (chrome://tracing). Captures goroutine scheduling,
  syscalls, GC pauses, and blocked time — the full picture for
  I/O-bound workloads where CPU profiling shows nothing.

All three flags are optional and independent. When set, the output
is written to the named file.

## Scope

Profiling wraps the full search command — DB open, query, scoring,
output. This captures the real cost distribution, not just the
search function in isolation.

Only on the `search` subcommand. Other commands can be added later
if needed.
