# CLI
**Requirements:** R29, R71, R72, R73, R74, R75, R76, R77, R78, R79, R80, R81, R82, R83, R84, R85, R86, R87, R88

Command-line interface. Parses flags, detects running server,
dispatches operations via proxy or cold-start.

## Knows
- dbPath: string — database directory (from -db flag, default ~/.ark/)
- command: string — subcommand name
- flags: parsed flag values

## Does
- Main(): parse command and flags, dispatch to subcommand handler
- DetectServer(dbPath): try connecting to Unix socket in dbPath,
  return connection or nil
- Proxy(conn, request): forward operation to running server as HTTP,
  return JSON response
- ColdStart(dbPath): open DB directly, execute operation, close
- CleanStaleSocket(dbPath): if connect fails, remove socket file
- Each subcommand: init, add, remove, scan, refresh, search, serve,
  status, files, stale, missing, dismiss, config, unresolved, resolve

## Collaborators
- Server: proxy target when server is running
- DB: direct access for cold-start operations

## Sequences
- seq-cli-dispatch.md
