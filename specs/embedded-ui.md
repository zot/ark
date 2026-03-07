# Embedded UI Engine

Ark bundles the Frictionless ui-engine as a Go library. One binary,
one process, serves everything: the ark API, the browser UI, and the
Frictionless MCP protocol for the orchestrator session.

Language: Go. Environment: Linux (primary), macOS.

## One binary

The ark binary imports `github.com/zot/ui-engine/cli` as a Go
dependency. No separate frictionless binary is needed. The user
downloads one thing, runs one command, gets everything.

## Unified home directory

`~/.ark/` is the home for everything — database, config, and UI
assets side by side:

```
~/.ark/
├── data.mdb, lock.mdb     # LMDB
├── ark.toml                # ark config
├── ark.sock                # ark API socket
├── logs/                   # server logs
├── tags.md                 # tag vocabulary
├── html/                   # ui-engine static assets
├── lua/                    # ui-engine lua (symlinks to apps)
├── viewdefs/               # ui-engine viewdefs (symlinks to apps)
├── apps/                   # frictionless apps
│   └── ark/                # the ark management app
├── ui-port                 # ui-engine HTTP port
└── mcp-port                # ui-engine MCP port
```

The ui-engine's `Server.Dir` points to `~/.ark/`. The ui-engine
creates and manages `ui-port`, `mcp-port`, and the standard
Frictionless directory structure. Ark's own files (data.mdb,
ark.toml, ark.sock, logs/) coexist without collision.

## Three listeners

`ark serve` starts three listeners in one process:

1. **Unix socket** (`ark.sock`) — the ark API. Search, index, tags,
   config. Every session uses this via the `ark` CLI.

2. **HTTP port** (written to `ui-port`) — the ui-engine browser
   server. Serves HTML, handles WebSocket connections for live UI
   updates. The user's browser connects here.

3. **MCP port** (written to `mcp-port`) — the Frictionless ui-engine
   protocol. The orchestrator session connects here to drive the UI.
   This is not an ark MCP server — it's the ui-engine's own protocol,
   embedded.

## Server lifecycle

The ark API server starts first (bind socket, open DB, begin
reconciliation). Then the ui-engine server starts alongside it.

On shutdown (SIGTERM/SIGINT), both servers shut down gracefully.
The ui-engine server shuts down first, then the ark API server
closes the database.

If the ui-engine fails to start (port conflict, missing assets),
the ark API server continues running. The UI is optional — ark
works without it. Log the error and carry on.

## ark ui command

`ark ui` with no subcommand opens the browser to the UI. It reads
`ui-port` and opens the URL. If the server is not running, it
reports that.

`ark ui` also serves as the gateway for all UI operations,
replacing the `.ui/mcp` shell script:

- `ark ui run '<lua>'` — execute Lua code in the UI session
- `ark ui display <app>` — display an app in the browser
- `ark ui event` — wait for next UI event (long-poll, 120s timeout)
- `ark ui checkpoint <cmd> <app> [msg]` — manage app checkpoints
- `ark ui audit <app>` — run code quality audit
- `ark ui status` — ui-engine server status
- `ark ui browser` — open browser to current session

All subcommands read `mcp-port` from `~/.ark/` and communicate
via HTTP. Skills and agents use `~/.ark/ark ui run '...'` — the
same binary they already have approved, no separate script.

## ark install populates UI assets

`ark install` sets up `~/.ark/` with the UI assets: html/, the ark
app in apps/ark/, and the linkapp/mcp scripts. The standard
Frictionless directory structure is created. This is part of the
existing `ark install` bootstrap — the UI assets are one more thing
it sets up.

The ark app (apps/ark/) ships embedded in the binary via `//go:embed`.
`ark install` extracts it to `~/.ark/apps/ark/` and runs linkapp to
wire up lua/ and viewdefs/ symlinks.

The ui-engine's static site (html/) also ships embedded. Extracted
to `~/.ark/html/` on install. Updates to the ark binary bring new
UI assets — `ark install` refreshes them.

## No MCP server for ark

Ark is accessed via the CLI. The embedded ui-engine has its own
mcp-port for Frictionless's protocol, but ark itself does not
register as an MCP server. See VISION.md for the rationale.

Agents drive the UI via `ark ui` subcommands — no separate shell
script, no MCP registration. `~/.ark/ark ui run '...'` works from
any agent depth, any session.
