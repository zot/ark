# CLI Proxy/Local Dispatch

How every DB-touching `ark` CLI command reaches the index.
Language: Go. Environment: ark CLI + API server (unix socket).

## Why

The index is single-process (one bbolt file lock). Since the
LMDB→BBolt migration a CLI command that opens the index directly
blocks forever while `ark serve` holds the lock — under LMDB
multiple processes could map the file concurrently, under bbolt
they cannot. So a command must reach the index through the server
when one is running, and open it directly only when none is.

## Central dispatch — `proxyOrLocal(proxy, local)`

A single helper is the dispatch point for every DB-touching command:

- When a server is reachable (its unix socket accepts a
  connection), the command runs `proxy` against the server.
- When no server is reachable, the command opens the index locally
  with a bounded lock wait and runs `local`.

`proxy` may be absent. A maintenance or diagnostic command that
needs exclusive local access and has no server endpoint passes no
proxy; when a server holds the index such a command fails fast with
a clear "stop the server with `ark stop`" message instead of
hanging. (`withExclusiveDB` is the convenience wrapper for these.)

## Stubborn through a bounce

A dropped connection to the server is treated as a wait condition,
not an error (the Stubborn Plumbing principle). If a proxied request
fails at the transport level (the server is down or restarting),
the command waits and retries until a stubborn window elapses,
rather than surfacing an error the moment the server blinks. An
application error returned by a live server (an HTTP non-200) is a
real error and is reported at once — only transport failures are
retried.

## On failure, recheck liveness

A bounded local open lets the no-server path fail instead of hang.
If the local open times out on the lock — something holds the index
though no server answered the dial, e.g. a server coming up between
the liveness check and the open, or a bounce in progress — the
command rechecks server liveness and re-dispatches (to the server
if it is now up, or retries the local open), surfacing a real error
only after the stubborn window closes. A server that crashed
releases its lock on death, so the local open then simply succeeds.

## Lock timeout

The bounded local open is enabled by a lock timeout on the index
open (microfts2 `Options.Timeout` → `bbolt.Options.Timeout`).
`ark.OpenWithTimeout` threads it; the server's own startup open uses
a zero timeout (block until available) since it is the index owner.

## Server endpoints behind the proxy

Commands that work concurrently with a running server proxy to a
matching endpoint:

- `chunks` → `POST /chunks` — chunk content by chunkID, by
  `path:range`, or by chat sub-chunk anchor (the same resolution the
  CLI does locally).
- `grams` → `POST /grams` — decoded trigram document-frequency counts.
- `schedule tags` → `POST /schedule/tags` — the schedule-tags summary
  lines (`--values` includes per-event last-fire detail).
- `tag values` → `POST /tags/values` with a `files` flag selecting the
  file-resolved variant; the flagless form is unchanged for the editor.

`fetch` proxies to the existing `POST /fetch` (ordinary paths) as
well as `tmp://` paths.

Commands that fail fast when a server holds the index (no proxy):
`embed text`, `embed bench`, `embed validate`, `config recover`.
