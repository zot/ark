# CLI
**Requirements:** R29, R71, R72, R73, R74, R75, R76, R77, R78, R79, R80, R81, R82, R83, R84, R85, R86, R87, R88, R108, R109, R110, R131, R139, R140, R141, R142, R143, R144, R145, R146, R147, R159, R161, R166, R169, R170, R172, R174, R165, R173, R178, R179, R180, R181, R182, R183, R185, R189, R196, R197, R198, R199, R201

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
  status, files, stale, missing, dismiss, config, unresolved, resolve,
  tag (with sub-subcommands: list, counts, files)
- cmdConfig: dispatches to show (default), add-source, remove-source,
  add-include, add-exclude, remove-pattern, show-why sub-subcommands
- cmdSearch: adds --chunks and --files flags (mutually exclusive),
  outputs JSONL when either is set. --wrap <name> wraps output in
  XML tags of that name. --like-file <path> uses file content as
  FTS density query (mutually exclusive with --contains/--regex).
  --tags outputs extracted tag names instead of chunk content.
- --wrap: parameterized context wrapper. Escapes closing tag in content.
  Convention: "memory" for experience, "knowledge" for facts.
- cmdTag: dispatches to list/counts/files sub-subcommands,
  --context flag on files sub-subcommand
- cmdFetch: verify indexed, output raw file content to stdout.
  --wrap <name> wraps output in XML tags.
- cmdServe: if server already running, print message to stderr, exit 0
- cmdStop: read PID file, verify process is ark, send SIGTERM (or
  SIGKILL with -f), poll until exit. Exit 1 on timeout.
- cmdInit: seed case_insensitive/aliases from ark.toml if present;
  CLI flags override seeded values
- cmdSourcesCheck: expand globs, add new dirs, report MIA/orphan.
  Output: +/−/? prefix per line. Proxies to server if running.

## Collaborators
- Server: proxy target when server is running
- DB: direct access for cold-start operations

## Sequences
- seq-cli-dispatch.md
