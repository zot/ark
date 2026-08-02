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

## The bound is on every local open, not on one dispatcher

The rule above is a property of *the CLI opening the index*, not of the
dispatcher that happens to implement it. Commands that can answer without a
server also reach the index through a plain cold-start helper, and that helper
must bound its open by the same rule and for the same reason.

Saying it this narrowly once already cost us. The helper was written when the
store was LMDB, which permits several reader processes, so a command opening
the index beside a running server was ordinary and correct. The move to bbolt
replaced that with an exclusive single-process lock and turned the same call
into an unbounded wait — with nothing to notice, because the hazard only
appears when a command races a server for the lock. It surfaced eight days
later as a hang in one command, and the repair bounded *that dispatcher* while
the shared helper kept the old open. So the bound belongs to the act, and a
new local-open path is wrong the moment it does not have it.

**A lock timeout asks one more question before waiting: is a server answering
now?** The two cases behind "something holds the index" want opposite
responses. A server *coming up* releases nothing but will accept a dial
shortly, so waiting is right. A server already *running* owns the index for as
long as it lives, so waiting cannot help and the command should say so at
once — otherwise every such command pays the full stubborn window to reach a
failure that was certain from the first attempt.

## A failed proxy is not an absent server

Dispatch has three outcomes, not two: nothing answered the dial; a server
answered and served the request; a server answered and **refused** it. Only the
first is a reason to open the index.

Hand-rolled dispatch collapses the third into the first, because the natural
way to write it drops the error and keeps going —

```
if err := proxy(...); err == nil { print; return }
// falls through to the local open, server still up
```

— and that is wrong twice at once. The command opens the index while the server
is still holding it, so the user is told about a lock; and what the server
actually said, which is the only accurate account of the failure, is discarded
before anyone sees it. `ark search --regex '['` answered "a server is running
and holds the index" after two seconds, having already been told precisely what
was wrong with the request.

Transport failure looks like the case that justifies falling through, and it is
the one that justifies it least: a dropped connection means the server is
bouncing, so the right response is to wait for it (Stubborn Plumbing above) and
resume, not to answer from a cold index the server is about to reopen. The
central dispatcher draws all three distinctions; a command that dials for
itself draws none of them, and its correctness then depends on the failure
never happening.

## A successful proxy is the end of the road

A command that reached the server and got its answer must not then open the
index locally for the rest of that answer. The server owns the lock for as long
as it lives, so the second open cannot succeed — before the bound it hung, and
with the bound it reports a *lock* problem for data the server was in a position
to return. Either way the user never learns what was actually wrong.

The rule is about where the data comes from, not about how it is fetched. Any
part of a command's output that needs the index belongs to the server while one
is running. A command with a part the server cannot answer does not get to open
the index anyway: it either gains an endpoint for that part, or that part is
declared to require the server stopped, like any other exclusive-access
diagnostic.

Two commands were built the other way, and the second shows why the rule has to
be stated rather than left to each command. `status --chunks` printed its status
block from the server and then reached for the index for chunk statistics — half
the output, so the failure looked like a hang partway through. `schedule search`
returned its events and then reached for the index only to mark which tags were
suppressed, and because it also had no non-server path, its default output form
could not succeed in **either** state: with a server it hit the lock, without
one it refused at the door.

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
- `status --chunks` → `POST /status/chunks` — chunk size statistics for
  the file set the filter stack selects.
- `schedule search` → `POST /schedule/search`, whose events carry their
  own suppression state, so the CLI reads no config of its own to render
  the `[suppressed]` marker.
- `search` → `POST /search` — both output shapes (result entries, and the
  tag vocabulary under `--tags`).
- `message inbox` → `POST /inbox` — server-first so `tmp://` inbox
  messages, which live only in server memory, are visible.

`fetch` proxies to the existing `POST /fetch` (ordinary paths) as
well as `tmp://` paths.

Commands that fail fast when a server holds the index (no proxy):
`embed text`, `embed bench`, `embed validate`, `config recover`, and
`status --chunks --tokenize`. What they have in common is that they load
the embedding model for themselves — `--tokenize` only for its
vocabulary, but the model still has to be resident. Byte-mode
`--chunks` proxies normally; only the tokenizing form is exclusive.
