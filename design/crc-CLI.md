# CLI
**Requirements:** R29, R71, R72, R73, R74, R75, R76, R77, R78, R79, R80, R81, R82, R83, R84, R85, R86, R87, R88, R108, R109, R110, R131, R139, R140, R141, R142, R143, R144, R145, R146, R147, R159, R161, R166, R169, R170, R172, R174, R165, R173, R178, R179, R180, R181, R182, R183, R185, R189, R196, R197, R198, R199, R201, R230, R232, R233, R234, R256, R273, R274, R275, R276, R277, R278, R279, R280, R281, R259, R260, R282, R283, R284, R285, R286, R287, R288, R289, R290, R291, R292, R293, R295, R297, R298, R299, R300, R301, R302, R304, R305, R306, R307, R308, R309, R310, R311, R312, R313, R314, R315, R316, R317, R318, R323, R324, R325, R326, R327, R328, R329, R330, R331, R332, R333, R334, R335, R336, R337

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
- Each subcommand: init, setup, add, remove, scan, refresh, search, serve,
  status, files, stale, missing, dismiss, config, unresolved, resolve,
  tag (with sub-subcommands: list, counts, files),
  ui (with sub-subcommands: run, display, event, checkpoint, audit,
  status, browser, install),
  bundle, ls, cat, cp
- cmdConfig: dispatches to show (default), add-source, remove-source,
  add-include, add-exclude, remove-pattern, show-why sub-subcommands.
  Config subcommands with positional args + optional flags use
  reorderArgs() to ensure flags are parsed before positional args
  (Go's flag package stops at first non-flag argument).
- cmdSearch: adds --chunks and --files flags (mutually exclusive),
  outputs JSONL when either is set. --wrap <name> wraps output in
  XML tags of that name. --like-file <path> uses file content as
  FTS density query (mutually exclusive with --contains/--regex).
  --tags outputs extracted tag names instead of chunk content.
  --filter/--except for content-based filtering,
  --filter-files/--exclude-files for path-based filtering.
  Replaces --source/--not-source.
- --wrap: parameterized context wrapper. Escapes closing tag in content.
  Convention: "memory" for experience, "knowledge" for facts.
- cmdTag: dispatches to list/counts/files sub-subcommands,
  --context flag on files sub-subcommand
- cmdFetch: verify indexed, output raw file content to stdout.
  --wrap <name> wraps output in XML tags.
- cmdServe: if server already running, print message to stderr, exit 0
- cmdStop: read PID file, verify process is ark, send SIGTERM (or
  SIGKILL with -f), poll until exit. Exit 1 on timeout.
- cmdSourcesCheck: expand globs, add new dirs, report MIA/orphan.
  Output: +/−/? prefix per line. Proxies to server if running.
- cmdUI: gateway for all UI operations. Reads mcp-port/ui-port from
  dbPath. No subcommand → open browser. Subcommands:
  run, display, event, checkpoint, audit, status, browser.
  Each subcommand sends HTTP requests to the mcp-port.
  Replaces the `.ui/mcp` shell script — one binary, no separate script.
- cmdSetup: extract bundled UI assets to dbPath using
  bundle.ExtractBundle, run linkapp, install global skills
  (~/.claude/skills/ark/, ~/.claude/skills/ui/) and agent
  (~/.claude/agents/ark.md) from bundled assets. Idempotent.
- cmdInit: seed case_insensitive/aliases from ark.toml if present;
  CLI flags override seeded values. Runs setup first if ~/.ark/
  not bootstrapped (no html/ dir). --no-setup skips setup.
  --if-needed skips DB creation when data.mdb already exists.
- cmdUIInstall: single entry point for per-project setup. Runs
  init --if-needed internally. Creates symlinks in project
  .claude/skills/ pointing to ~/.ark/skills/. Prints crank-handle
  prompt for CLAUDE.md bootstrap line.
- cmdBundle: graft a directory onto a binary as a zip appendix.
  Calls bundle.CreateBundle(src, dir, output). Build-time command.
- cmdLs: list embedded assets. Calls bundle.ListFilesWithInfo,
  prints one file per line (symlinks show target). Exit 1 if not bundled.
- cmdCat: print an embedded file to stdout. Calls bundle.ReadFile.
  Exit 1 if not bundled.
- cmdCp: extract embedded files matching a glob to a directory.
  Matches against basename and full path. Preserves permissions,
  recreates symlinks. Exit 1 if not bundled or no matches.

## Collaborators
- Server: proxy target when server is running
- DB: direct access for cold-start operations

## Sequences
- seq-cli-dispatch.md
