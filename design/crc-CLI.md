# CLI
**Requirements:** R29, R71, R72, R73, R74, R75, R76, R77, R78, R79, R80, R81, R82, R83, R84, R85, R86, R87, R88, R108, R109, R110, R131, R139, R140, R141, R142, R143, R144, R145, R146, R147, R159, R161, R166, R169, R170, R172, R174, R165, R173, R178, R179, R180, R181, R182, R183, R185, R189, R196, R197, R198, R199, R201, R230, R232, R233, R234, R256, R273, R274, R275, R276, R277, R278, R279, R280, R281, R259, R260, R282, R283, R284, R285, R286, R287, R288, R289, R290, R291, R292, R293, R295, R297, R298, R299, R300, R301, R302, R304, R305, R306, R307, R308, R309, R310, R311, R312, R313, R314, R315, R316, R317, R318, R323, R324, R325, R326, R327, R328, R329, R330, R331, R332, R333, R334, R335, R336, R337, R370, R371, R396, R397, R398, R399, R400, R401, R402, R429, R430, R431, R432, R433, R434, R435, R436, R437, R442, R450, R451, R452, R453, R454, R455, R456, R457, R458, R462, R464, R466, R467, R468, R469, R470, R471, R477, R479, R480, R481, R482, R483, R484, R485, R486, R487, R488, R489, R490, R491, R492, R493, R494, R495, R496, R497, R498, R499, R500, R501, R506, R507, R508, R509, R510, R512, R513, R514, R515, R516, R525, R526, R527, R528, R529, R530, R531, R532, R533, R534, R535, R536, R537, R538, R539, R540, R547, R548, R549, R550, R551, R552, R553, R554, R555, R556, R557, R558, R559, R560, R561, R562, R572, R573, R579, R580, R581, R582, R583, R584, R585, R590, R591, R592, R594, R599, R605, R606, R607, R608, R609, R610, R611, R612, R613, R614, R615, R616, R639, R654, R655, R656

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
- Each subcommand: init, setup, rebuild, add, remove, scan, refresh, search,
  serve, status, files, stale, missing, dismiss, config, unresolved, resolve,
  tag (with sub-subcommands: list, counts, files, set, get, check),
  ui (with sub-subcommands: run, display, event, checkpoint, audit,
  status, open, install),
  bundle, ls, cat, cp, chunks,
  message (with sub-subcommands: new-request, new-response, set-tags,
  get-tags, check — set-tags/get-tags alias ark tag set/get)
- cmdRebuild: refuse if server running. Delete data.mdb and lock.mdb,
  re-run init (reading settings from ark.toml), then scan.
- cmdConfig: dispatches to show (default), add-source, remove-source,
  add-include, add-exclude, remove-pattern, show-why sub-subcommands.
  Config subcommands with positional args + optional flags use
  reorderArgs() to ensure flags are parsed before positional args
  (Go's flag package stops at first non-flag argument).
- cmdSearch: adds --session NAME flag (implies server proxy),
  --chunks and --files flags (mutually exclusive),
  outputs JSONL when either is set. --wrap <name> wraps output in
  XML tags of that name. --like-file <path> uses file content as
  FTS density query (mutually exclusive with --contains/--regex).
  --tags outputs extracted tag names instead of chunk content.
  --score <mode> selects scoring strategy: auto (default), coverage,
  density. Unknown values produce error and exit.
  --multi runs all four strategies (coverage, density, overlap, bm25)
  via SearchMulti and merges results. Mutually exclusive with --score.
  Does not apply to --regex, --about, or --like-file.
  --proximity enables post-search proximity reranking on top 2*k
  candidates. Composes with any search mode including --multi.
  --filter/--except for content-based filtering,
  --filter-files/--exclude-files for path-based filtering,
  --filter-file-tags/--exclude-file-tags for tag-based filtering.
  Replaces --source/--not-source.
- --wrap: parameterized context wrapper. Escapes closing tag in content.
  Convention: "memory" for experience, "knowledge" for facts.
- cmdTag: dispatches to list/counts/files/defs/set/get/check sub-subcommands,
  --context flag on files sub-subcommand.
  set/get/check are generic tag block operations (no DB needed).
  check accepts optional heading arguments for allowed-heading validation.
- cmdTagDefs: query D records from LMDB. Optional tag name args filter.
  Default: `tagname description`, deduplicated, sorted alphabetically.
  --path: `path tagname description`, lexically sorted, not deduplicated,
  spaces in paths backslash-escaped.
  Uses server proxy when available, falls back to withDB.
- cmdFetch: verify indexed, output raw file content to stdout.
  --wrap <name> wraps output in XML tags.
- cmdChunks: context expansion around search hits. Takes path, range,
  optional -before/-after counts. Calls microfts2.GetChunks() via
  withDB. Outputs JSONL (one object per chunk: path, range, content,
  index). Supports --wrap for XML wrapping.
- cmdServe: if server already running, print message to stderr, exit 0
- cmdStop: read PID file, verify process is ark, send SIGTERM (or
  SIGKILL with -f), poll until exit. Exit 1 on timeout.
- cmdSourcesCheck: expand globs, add new dirs, report MIA/orphan.
  Output: +/−/? prefix per line. Proxies to server if running.
- cmdUI: gateway for all UI operations. Reads mcp-port/ui-port from
  dbPath. No subcommand → open browser. Subcommands:
  run, display, event, checkpoint, audit, status, open, reload, install.
  Each subcommand sends HTTP requests to the mcp-port.
  Replaces the `.ui/mcp` shell script — one binary, no separate script.
- cmdUIStatus: output "ui: running (port N)" + browser count + indexing
  state. When UI not running: "ui: not available". Queries server
  via GET /status for UI fields.
- cmdSetup: extract bundled UI assets to dbPath using
  bundle.ExtractBundle, run linkapp, install global skills
  (~/.claude/skills/ark/, ~/.claude/skills/ui/) and agent
  (~/.claude/agents/ark.md) from bundled assets. Idempotent.
- cmdInit: seed case_insensitive/aliases from ark.toml if present;
  CLI flags override seeded values. Runs setup first if ~/.ark/
  not bootstrapped (no html/ dir). --no-setup skips setup.
  --if-needed skips DB creation when data.mdb already exists.
  Without --if-needed, removes existing data.mdb and lock.mdb
  before creating a fresh database.
- cmdUIInstall: single entry point for per-project setup. Runs
  init --if-needed internally. Starts server if not running.
  Creates .claude/skills/ and .claude/agents/ dirs if needed.
  Creates symlinks in .claude/skills/ pointing to ~/.ark/skills/,
  symlink for .claude/agents/ark.md pointing to ~/.ark/agents/ark.md.
  Symlinks are idempotent — re-running updates existing ones.
  Prints crank-handle prompt for CLAUDE.md bootstrap line.
- cmdInstall: alias for cmdUIInstall (`ark install` = `ark ui install`).
- cmdBundle: graft a directory onto a binary as a zip appendix.
  Calls bundle.CreateBundle(src, dir, output). Build-time command.
- cmdLs: list embedded assets. Calls bundle.ListFilesWithInfo,
  prints one file per line (symlinks show target). Exit 1 if not bundled.
- cmdCat: print an embedded file to stdout. Calls bundle.ReadFile.
  Exit 1 if not bundled.
- cmdCp: extract embedded files matching a glob to a directory.
  Matches against basename and full path. Preserves permissions,
  recreates symlinks. Exit 1 if not bundled or no matches.

- cmdMessage: dispatches to new-request, new-response, set-tags,
  get-tags, check, inbox sub-subcommands. Most operate on plain files
  via TagBlock — no server, no DB. Exception: inbox requires the database.
- cmdMessageSetTags: alias for cmdTagSet. Help text points to `ark tag set`.
- cmdMessageGetTags: alias for cmdTagGet. Help text points to `ark tag get`.
- cmdMessageNewRequest: validate FILE doesn't exist, build TagBlock
  from flags (ark-request=ID, status=open), render with heading scaffold.
  If stdin is not a terminal, read body until lone `.` on a line and
  append after scaffold. Write file.
- cmdMessageNewResponse: same pattern as new-request with ark-response tags.
  Default status is "accepted" (response = ack). Stdin body reading
  works identically.
- readStdinBody(): if stdin is not a terminal, read lines until lone `.`
  or EOF. Returns the collected body text (may be empty).
- cmdMessageCheck: wrapper that calls cmdTagCheck with the standard
  message heading list. Crank-handle for agents — terser than passing headings.
- cmdMessageInbox: query DB for files with @status tag via TagFiles,
  filter to requests/ paths, read each file's TagBlock.
  Flags: --project (filter @to-project), --from (filter @from-project),
  --all (include completed/done/denied), --include-archived (include archived),
  --counts (output status counts instead of rows).
  Default: filter out completed/done/denied and archived.
  Sort @status:open first then by path, output tab-separated summary lines.
  With --counts: output status\tcount lines sorted alphabetically.
  Summary field uses `@issue` for requests, `ark-response:<id>` for responses.

- cmdVec: dispatches to bench, bench-search sub-subcommands.
- cmdVecBench: load GGUF model via gollama, pull N chunks from LMDB
  (sequential or random), embed each in-process, report per-chunk
  timings (duration, byte length) and summary stats (min/max/mean/
  median/total, chunks/sec). Model load time reported separately.
  Read-only, uses withDB.
- cmdVecBenchSearch: load model, embed query, brute-force cosine
  against stored vectors. Report vector count and search time.

## Collaborators
- Server: proxy target when server is running
- DB: direct access for cold-start operations
- TagBlock: tag block parsing and manipulation for message subcommands

## Sequences
- seq-cli-dispatch.md
- seq-message.md
