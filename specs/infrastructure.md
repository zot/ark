# Infrastructure — Cluster 3

Operational improvements: reload stability, event visibility,
project bootstrap, and status enrichment.

Language: Go (server), Lua (UI). Environment: Linux (primary), macOS.

## ark ui reload — port persistence

`ark ui reload` restarts the ui-engine without changing the HTTP
port. Currently a reload picks a new random port, which breaks
the browser connection and forces the user to re-open.

Reload should:
- Stop the current ui-engine instance
- Start a new one on the same port
- The browser page should reconnect automatically via the existing
  WebSocket reconnect logic in ui-engine

This may require changes in Frictionless (`flib`) or ui-engine to
accept a preferred port on restart. If the port is busy (shouldn't
be — we just stopped), fall back to a new port and log a warning.

Second browser tab: if a second WebSocket connection arrives while
one is already active, the UI should show a message: "there is
already a connection — use the other tab instead." This prevents
confused state from two tabs driving the same session.

## MCP event pulse indicator

The 9-dot app grid button in the Frictionless status bar should
pulse while the MCP shell is waiting for Claude to respond to a
tool call. This replaces raw event count text that currently
overlaps content.

- Pulse animation on the grid button (CSS class toggle)
- Tooltip shows pending event count
- No permanent screen real estate consumed
- Pulse stops when event resolves (Claude responds or timeout)

This is a Lua + CSS change in the ark app and/or Frictionless
status bar. The event state already exists in the MCP shell —
this just visualizes it.

## ark install — project bootstrap

`ark install` (alias for `ark ui install`) is the single command
to connect a project to ark. Run in any project directory:

1. **Bootstrap server.** Run `ark init --if-needed` internally.
   If `~/.ark/` doesn't exist, set up everything. If it does,
   no-op. Start server if not running.

2. **Symlink skills.** Create symlinks in the project's
   `.claude/skills/` pointing to `~/.ark/skills/`. Includes
   the ark skill (search/recall) and the UI skills (ui, ui-fast,
   etc.). Symlinks, not copies — upstream updates flow through.

3. **Symlink agent.** Create symlink for `.claude/agents/ark.md`
   pointing to `~/.ark/agents/ark.md`.

4. **Crank-handle output.** Print a prompt instructing Claude to
   add `load /ark first` to the project's CLAUDE.md. The binary
   cannot edit CLAUDE.md intelligently; the crank-handle prompt
   is designed for any model tier to follow.

The README-driven install flow: user tells Claude "install ark",
Claude checks for `~/.ark/ark`, downloads if missing, runs
`~/.ark/ark install`. One chain, self-resolving.

## UI status endpoint — connected browsers

`ark ui status` and `GET /status` should report browser connection
state:

- Number of connected browsers (WebSocket connections)
- Replace the current session count (always 1 for embedded) with
  browser count

Server-side: `mcp:status()` in Lua already returns status info.
Ark wraps it via `WithLua` to add ark-specific fields, or queries
the ui-engine directly for connection count.

The `ark ui status` CLI command should output:
```
ui: running (port 8080)
browsers: 1
indexing: false
```

When no UI is running:
```
ui: not available
```
