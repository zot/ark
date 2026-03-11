# Ark Plan
As we implent things, we'll add them to the spec/* files, either in main.md or other appropriately named files and then check them off here when they're implemented.

Check OBSERVATIONS.md and specs/main.md for context

See [VISION.md](VISION.md) for the big picture: session model,
orchestrator, pubsub, why no MCP server.

## V2 — Model-Free

No embedding model required. Tags + FTS give fully functional
recall on any hardware.

- [x] Chunk retrieval — CLI option to return chunk text instead of ranges
  - `ark search --chunks` — emit chunk text (JSONL)
  - `ark search --files` — emit full file content for matches
  - Enables permission end-run: if it's indexed, ark can emit it
  - Works with FTS and tag search, no model needed
- [x] Tag tracking — track @tags as files are ingested
  - Tags are `@word:` — the trailing colon is required (disambiguates from emails/mentions)
  - 'T' [tagname] [count] in ark subdatabase
  - 'F' [fileid] [tag] [count]
  - Tag vocabulary file: `~/.ark/tags.md`
    - Format: `@tag: name -- description`
    - Indexed like any other file — ark finds definitions by content
    - New tags emerge by use; this file documents what they mean
    - `ark init` creates a starter tags.md with the format documented
  - subcommands:
    - ark tag list — all known tags with counts
    - ark tag counts <tag>... — total count per tag
    - ark tag files <tag>... — filename and size per file
      - `--context` shows each occurrence with tag to end of line
      - includes tag definitions from tags.md alongside usage
- [x] JSONL chunker strategy — index conversation logs
  - microfts2 strategy: each newline-delimited record is one chunk
  - Enables indexing Claude conversation JSONL files
  - Registered as `chat-jsonl` strategy in `ark init`
- [x] Recall agent / /ark skill
  - `/ark` skill is a thin pointer — just says "spawn the ark agent"
  - `.claude/agents/ark.md` carries the CLI reference and output formats
  - Agent only loads when invoked, saving context in every other session
  - Runs on haiku for cheap, fast queries
- [x] Fetch — return full file contents by path
  - `ark fetch <path>` — emit file content for any indexed file
  - Adding a file to ark implies read-approval; using fetch
    side-steps other permission gates
  - Agent with access to `~/.ark/ark` can view any indexed file
  - HTTP: `GET /fetch?path=<path>` or `POST /fetch` with path in body
- [x] Init seeds from existing ark.toml
  - If ark.toml exists, read case_insensitive/aliases from it
    instead of requiring flags — enables clean "delete DB, re-init, scan"
  - If ark.toml doesn't exist, write one with case_insensitive
    so it's complete for next time
- [x] Server lifecycle
  - `ark serve` already starts the server (foreground). One global server
    per machine — first `ark serve` wins, all agents connect to it
  - `ark serve` exits 0 if server is already running (intent: "make sure
    it's up" — already up means success). Message on stderr for humans.
  - `ark stop` — read PID file, verify process exists and is ark
    (handles pid rollover), send SIGTERM, poll until process exits.
    Returns 1 if process doesn't die within timeout.
    `-f` flag sends SIGKILL instead.
  - Server signal handling: catch SIGTERM, close socket, close DB, exit 0
  - PID file is never removed by the server process itself — stale pidfile
    is safe because `ark stop` verifies before killing. Remove the current
    `defer os.Remove(pidPath)` in server.go
  - `ark status` already reports server running/not — no changes needed
  - Self-contained management in ~/.ark, no systemd

## V2.5 — Smoother maneuvering
### Go
- [ ] register mcp:search_grouped(), mcp:open() on Lua mcp table (app is in-process, no HTTP)
- [ ] remove HTTP endpoints: POST /search/grouped, POST /open, GET /indexing
- [ ] R438-R439: browser count — needs ui-engine counter + flib passthrough (D6, request `flib-browser-count-6b3c3b6a`)
- [ ] Chunker interface in microfts2 (extracted from @ark-file design work)
  - Chunkers do two jobs: produce chunks AND retrieve text by range
  - Currently only production is in the public API (`ChunkFunc`)
  - Retrieval is scattered (inline re-chunking in `filterResults`, ark's `FillChunks`)
  - Define `Chunker` interface, `FuncChunker` adapter, clean up internal call sites
  - See .scratch/ARK-FILE.md for full design notes
- [x] `ark search` and `ark fetch` use local LMDB reads
  - Both always use withDB — server doesn't re-index before searching,
    so proxy adds latency without value for read-only ops
  - Fetch: 2.3s → 0.4s (also fixed O(n) StaleFiles scan → O(1) CheckFile)
  - Search: skips HTTP round-trip + JSON encode/decode
  - `ark chunks` already correct (withDB, no proxy)
- [x] ark search allows multiple --regex and --except-regex options (microfts2 WithRegexFilter/WithExceptRegex ready, ark CLI not wired)
- [ ] Parallel rebuild/refresh
  - LMDB supports concurrent readers; chunking + trigram extraction per file is independent
  - Worker pool: N goroutines read+chunk+extract, single writer goroutine serializes LMDB puts
  - Biggest win on rebuild (all files) and first scan (hundreds of files)
- [ ] Move `defaultTagsContent` from db.go to `install/tags.md`
  - Bundle it via zip-graft like other assets
  - `ark init` extracts from bundle with `bundle.ReadFile`, falls back to hardcoded seed
  - Editable, diffable, part of the source tree
- [ ] Refactor: move `set-tags`/`get-tags`/`check` from `ark message` to `ark tag`
  - `ark tag set FILE TAG VALUE [TAG VALUE ...]` — generic tag block I/O
  - `ark tag get FILE [TAG ...]` — generic tag block read
  - `ark tag check FILE [HEADING ...]` — validate structure, optional heading args flag stray headings
  - `ark message check` wraps `ark tag check` with message-specific heading list (terser crank-handle)
  - Update ark skill, librarian agent, Franklin agent references when this lands
- [ ] Append-detection tag correctness
  - `AppendFile` runs `ExtractTags(newBytes)` where newBytes starts at old FileLength
  - A tag split across the boundary (`@t` in old, `ag: value` in new) is silently lost
  - In practice unlikely (appends land at line boundaries) but not impossible
  - Fix: back up from the split point to the previous newline, scan from there
  - Also: markdown chunker's `WithReplaceFrom` replaces the last chunk, but tag
    extraction doesn't re-scan that replaced region — only the new bytes
  - Audit and add tests for boundary-split tags
- [x] `ark tag defs` — fast tag definition lookup
  - D[tagname][fileid] records in LMDB, extracted at index time
  - `ark tag defs` — default: deduplicated, sorted alphabetically
  - `ark tag defs --path` — with provenance, lexically sorted
  - `ark tag defs TAG...` — filter to specific tags
  - 18ms cold-start (was 3.7s via `ark tag files --context`)
- [x] `ark chunks` — context expansion around search hits
  - `ark chunks <path> <range> [-before N] [-after N]`
  - Returns target chunk plus N neighboring chunks (JSONL output)
  - microfts2 stores chunks sequentially per file (C records) — scan for target, return neighbors
  - Enables the librarian's "flip pages" research loop: search → hit → expand context → decide
  - Small feature, high leverage for agent research quality
  - See librarian/specs/chunk-context.md
- [x] `ark message` — structured CLI for cross-project messaging (core)
  - Haiku-safe: models say *what*, command handles *how* (tag placement, file format)
  - Two lifecycles: `@status` (work state) and `@msg` (delivery state)
    - `@status`: open, in-progress, done, declined
    - `@msg`: new, read, acting, closed
    - A notification about completed work: `@status:done @msg:new`
    - A request acknowledged but not started: `@status:open @msg:read`
  - [x] `ark message new-request --from PROJECT --to PROJECT --issue "..." FILE`
  - [x] `ark message new-response --from PROJECT --to PROJECT FILE`
  - [x] `ark message set-tags FILE TAG VALUE [TAG VALUE...]`
  - [x] `ark message get-tags FILE [TAG...]`
  - [x] `ark message check FILE`
  - all just applications of tags + file structure — no new storage
  - librarian skips `@msg:closed` by default — keeps the archive out of search noise
- [x] `ark message` — remaining subcommands (Franklin needs these)
  - `ark message ack FILE` — mark `@msg:read` (I saw it)
  - `ark message close FILE` — mark `@msg:closed` (done with this message)
  - `ark message inbox [--project PROJECT]` — list messages where `@msg` is not `closed`
    - default scope: messages to the current project
    - `@msg:new` items shown first

### Lua / CSS (app UI)
- [ ] wire grouped results, click handler, preview display
  - in directory sources look like `~/xE2x80xA6crank-handle.md`
  - preview text should render if we know how
    - markdown with goldmark (rendered server-side in Go, delivered as HTML)
    - tokens should be highlighted
    - JSON uses pretty printing unless the output exceeds a certain length
- [ ] re-indexing spinners — poll mcp:indexing(), update every 250ms with setInterval?
- [ ] MCP event pulse indicator (R423-R428, A13)
  - 9-dot app grid button pulses while waiting for Claude response
  - Count in tooltip. No permanent screen real estate

### ui-engine TypeScript (upstream)
- [ ] R418: page should reconnect on reload (A14)
- [ ] R421-R422: second tab detection, "use the other tab" message (A15)

### Other
- [ ] ensure this info is well represented in the readme: The ark is long-term memory for Claude: vector + trigram DB indexing everything we talk about and all projects. Carries memory across context compaction. MEMORY.md and personal patterns are a keyhole; the ark gives actual recall — not just what Bill told me to remember, but what we actually did. Every debugging session, design decision, and correction.
- [ ] ark-librarian: persona vs rules experiment
  - 4 agents (persona-only, rules-only, both, Bob the Skull), 18 queries, blind evaluation
  - Expand corpus first: add HollowStuff, Leisure, dice as sources
  - Measures token cost, result quality, and Evil Bob (confident wrongness) resistance
  - See librarian/specs/experiment.md for full design
  - Determines whether the librarian persona adds real value on Haiku
- [ ] franklin: personal assistant agent (daily narrowing, commitment tracking)
  - Two personas over same data: Librarian (findability) vs Franklin (actionability)
  - Basic Franklin v0: inbox report (`@msg:new`), waiting-for, open items per project
  - "Today" mechanism — how the daily list is stored (tag? file? session state?)
  - Session continuity — notes-to-self for cross-session state
  - Done ritual — acknowledge completion, not just track what's open
  - Weekly review (broad) vs daily standup (narrow) — two rhythms
  - See franklin/ directory for specs, design, REVIEW.md
- [ ] ark skill: search agent and other Haiku agents
  - keep in mind that is a Haiku agent when you tell it what you need
  - Consider using ad hoc crank-handle if required
- [ ] ark skill bootstrap: check for open project messages at session start
  - After server start and tag load:
    1. Librarian checks inbox (`@msg:new` messages targeting this project)
    2. Franklin reports: unread count, stale items, waiting-for status
  - Report to user before proceeding
  - Depends on: functional ark-librarian, basic franklin agent
- [ ] when finished with V2.5
  - integrate items in specs/v2.5-enhancements.md into their appropriate spec files and remove v2.5-enhancements.md

### Done

- [x] ark.toml sources should have `strategies` as an optional field
  - instead of `strategy`
  - Amends the global list
- [x] `ark rebuild` command
  - close db, make a new one, re-scan.
- [x] `ark init` --no-setup doesn't seem to actually nuke the db, gotta remove it first
- [x] change `ark ui browser` -> `ark ui open`
- [x] SearchGrouped — R403-R409 (grouped results, preview rendering, token highlight)
- [x] click-to-open — R410-R412 (xdg-open/open)
- [x] mcp:indexing() — R413-R416 (Go function on Lua mcp table)
- [x] ReloadUIEngine — R417, R419-R420 (shutdown → rebuild with saved port → restart)
- [x] POST /ui/reload + cmdUIReload — D5 resolved (flib.Config.Port fix)
- [x] handleStatus enriches GET /status — R437, R440-R442 (UIRunning, UIPort, UIIndexing)
- [x] cmdUIStatus + printStatus UI line
- [x] ark install — R429-R436 (starts server, symlinks skill+agent, crank-handle for CLAUDE.md)
- [x] Frictionless management app
  - file tree with 3-state selection
    - offer "gitignore" in the exclude pattern picklist if there's a gitignore file in that dir
  - Offer to index claude things: adds to file tree (below)
    - claude projects in a pick-list
    - chat history
    - memories
- [x] Export microfts2 chunker functions (eliminate exec-per-file)
  - [x] AddStrategyFunc alongside AddStrategy
  - [x] Built-in chunkers call Go functions directly
- [x] Config mutation CLI commands
  - `ark config add-source <dir> --strategy <name>` — add a source directory
  - `ark config add-include <pattern> [--source <dir>]` — add an include pattern
  - `ark config add-exclude <pattern> [--source <dir>]` — add an exclude pattern
  - `ark config remove-pattern <pattern> [--source <dir>]` — remove a pattern
  - `ark config show-why <path>` — explain why a file is included/excluded/unresolved
  - The Frictionless app shells out to these instead of editing ark.toml directly
- [x] Search enhancements
  - `--like-file <path>` — find files with similar content (FTS density scoring)
    - Read file content, use as query with density scoring
  - `--about-file <path>` — find files semantically similar (vector search, V4)
    - Deferred until vector support lands
  - Both flags together: combine FTS + vector scores (same merge as SearchCombined)
  - `--tags` — only output tags extracted from matching chunks
- [x] FTS tuning tools
  - `ark grams <query>` — show which trigrams a query produces, which
    are active at the current cutoff, and the frequency of each
  - `ark config set-cutoff <percentile>` — adjust the search cutoff
    (rebuilds the active set). Default 50; higher values include more
    common trigrams (better recall on small corpora, slower on large)
  - Frictionless app: cutoff slider, trigram inspector for queries
  - Agent can tune cutoff based on corpus size and search results
- [x] `ark config set-embed-cmd <command>` — patch vector search into
  an FTS-only database. Existing files need `ark refresh` to generate
  embeddings.
- [x] Internal: LMDB cursor scan helper
  - `scanPrefix(txn, dbi, prefix, fn)` — 7 scan loops in store.go consolidated
- [x] Internal: CLI command helper
  - `withDB` — 18 of 20 Open/Close blocks consolidated (fetch kept separate: conditional open with loop)
- [x] Internal: reduce redundant file reads
  - `AddFile` / `RefreshFile` use `AddFileWithContent`/`ReindexWithContent`
    — microfts2 returns content, eliminating second disk read
  - `ftsKeyCache` caches CheckFile and FileInfoByID across FTS result iteration
- [x] Internal: non-blocking startup reconciliation
  - Server blocks on scan+refresh before serving. Move to background
    goroutine so status queries work immediately
- [x] Glob sources
  - Config is intent, DB is state
    - ark.toml: globs, patterns, strategies — what you *want*
    - LMDB: files, chunks, trigrams, vectors — what *is*
    - Optional config snapshot in DB for change detection only
  - Source `dir` in ark.toml can be a glob: `~/.claude/projects/*/memory`
  - Globs are directives, not records — DB only has concrete directories
  - `ark sources check` — lightweight reconciliation command
    - Expands globs from config, diffs against DB sources
    - New dirs: adds as sources. Missing dirs: flags as MIA
    - No file scanning or indexing — just directory listing against globs
    - Cheap enough to run on every `ark serve` startup
  - Removing a concrete source managed by a glob is an error
    — change the glob to change what it manages
  - Removing a glob from config orphans its resolved sources
    — `ark sources check` detects and reports orphans for cleanup
  - fsnotify watches each resolved directory individually
  - UI: glob shown as group header, resolved sources underneath
    — remove button on the glob, not individual children
- [x] Global strategy mapping
  - Top-level `strategies` table in ark.toml: glob pattern → strategy name
  - Source `strategy` field becomes fallback when no pattern matches
  - Path-prefixed globs for specificity: `docs/**.md` beats `*.md`
  - Longest matching pattern wins (poor-man's specificity)
  - Example: `strategies = {"*.md" = "markdown", "*.jsonl" = "chat-jsonl"}`
- [x] File logging
  - Server logs to `~/.ark/logs/ark.log` instead of (or in addition to) stderr
  - Log rotation or size cap so it doesn't grow unbounded
  - CLI commands that cold-start don't need logging — server only
- [x] Embedded UI engine
  - Full Frictionless stack via `flib` facade: MCP tools, `mcp` Lua global, /api/* endpoints
  - `ark serve` starts ark API (unix socket) + ui-engine (HTTP port) in one process
  - Frictionless API routes mounted on ark's mux via `RegisterAPI` — no separate MCP port
  - Two listeners total: unix socket (ark + Frictionless API) and HTTP port (browser)
  - `~/.ark/` is the unified home: html/, lua/, viewdefs/, apps/ alongside data.mdb, ark.toml, ark.sock
  - `ark ui` — full subcommand parity with `.ui/mcp`:
    audit, browser, checkpoint, display, event, linkapp, patterns,
    progress, run, state, status, theme, update, variables
  - `ark ui checkpoint` — full fossil checkpoint management (save/list/rollback/diff/clear/baseline/count/update/local)
    - Crank-handle: if fossil not at ~/.claude/bin/fossil, outputs download instructions
  - UI skills use `{cmd}` placeholder — CLAUDE.md declares the command per project
  - Bundle commands: `ark bundle` (build-time graft), `ark ls/cat/cp` (runtime extraction)
- [x] `ark ui install` — install UI skills into current claude project
  - Symlinks skills from `~/.ark/skills/` to `.claude/skills/`
  - Crank-handle prompt for CLAUDE.md `{cmd}` declaration
- [x] `ark init` extracts bundled UI assets
  - Extracts html/, apps/ark/, patterns/, themes/ from zip-grafted binary
  - Pure filesystem operation — no server needed (bootstrap)
  - Subsequent updates via `ark ui install` (hash-based conflict detection)
- [x] Makefile: asset pipeline
  - Build frictionless, extract its bundled assets into cache/
  - Layer ark's own assets (apps/ark/)
  - Build ark binary, graft cache as zip appendix

## V3 — Proactive

@note: agentic graph DB? It's not like current ones but it works...

Ark surfaces things to you without being asked. The conversation
itself is the query — no explicit "remind me" needed.

Every Claude session with /ark gets its own persistent session ID
and its own JSONL watcher. Reminders and inspiration are per-session,
driven by that session's conversation context. All sessions share the
same index — one brain, many streams of consciousness.

- [ ] Ark messaging
  - subscribe to regex `@to-project:.*\bPROJECT\b`
- [ ] Orchestrator architecture
  - Your Daneel — the main session, the one you talk to
  - Launches subagents for heavy work; they query ark, write back, die
  - Only the orchestrator has MCP — it operates the personal software
  - Converges with the embedded UI engine: one process, one binary
  - Does not need to compact nearly as often
- [ ] Time-decay scoring (we store timestamps, scoring comes later)
  - makes it practical rather than just a big archive
  - Recent patterns weighted heavily, old ones available but not dominant
  - Living memory, not a filing cabinet.
  - allows from-to queries
- [ ] Haiku listener agent dispatches in-memory requests to other agents, potentially spawns
  - escalation: weaker agents can ask the dispatcher for help. It can spawn other agents.
  - some agent types:
    - secretarial: update/search calendars
- [ ] JSONL watcher @note: this is partly done already
  - ark server watches `~/.claude/projects/*/` for new/appended JSONL files
  - On each new turn (line appended), extract the text content
  - Fire it as a search query against the full index (FTS, and vector if available)
  - Also run `@[\w-]+:` regex against results above a quality threshold
  - Scrape all tags from qualifying matches
  - JSONL record types (observed):
    - `type: user` with message content → human typed something
    - `type: user` with `tool_result` content → tool completed
    - `type: user` with `tool_result`, `is_error: true`, "rejected" → permission denied
    - `type: assistant` with `tool_use` entries → Claude requested tool(s)
    - `type: assistant` without `tool_use` → Claude spoke (text only)
    - `type: progress` → streaming progress
    - `type: system` → system messages
    - `type: file-history-snapshot` → file tracking (ignore)
  - Session state detection from JSONL tail:
    1. Last record `assistant` with no `tool_use` → idle. Daydream candidate.
    2. Last record `assistant` with `tool_use` → blocked on tool/permission.
    3. Last record `user` with real content → Claude is responding.
    4. Last record `user` with `tool_result` → Claude is mid-work.
    - Only state 1 triggers daydream idle timer
    - States 2-4 suppress daydreaming
- [ ] Inspiration engine
  - Per-session tag queue: each session has its own queue of discovered tags
  - Winnowing: diff scraped tags against tags already sent to that session —
    only queue tags the session hasn't seen yet
  - The JSONL filename (or a session ID) identifies which session triggered
    the search — tags are only sent to the session that produced the hits
- [ ] Long-poll subscription (`/subscribe`)
  - `GET /subscribe?session=<id>` — blocks until tags are queued or timeout
  - Returns queued tags as JSON, then clears them from the queue
  - Grace period TTL: if a session doesn't poll within N minutes, drop its queue
  - Track JSONL file position per watcher so reconnects don't replay old turns
- [ ] `/inspiration` skill (client side)
  - Runs `ark subscribe --session $SESSION_ID` as a background task
  - The CLI command does the long-poll HTTP request to the server
  - On return: prints the new tags and exits
  - `run_in_background` exit notification pokes the agent to check output
  - Agent sees new tags, gains context from the knowledge base without
    anyone asking for it
- [ ] Daydreaming
  - When a session has been idle after a Claude turn (long gap
    between turns), ark stochastically selects from recent tags
    and sends them unprompted. Only triggers after Claude's turn,
    not the user's — a user turn means Claude is busy responding
  - Same mechanism as inspiration: `ark listen` exits with output,
    `run_in_background` notification pokes Claude, Claude surfaces
    the idea to the user ("hey, I just had a thought...")
  - Stochastic selection is key — deterministic would be predictable
    and annoying. Weighted-random from recent tags makes each
    daydream a surprise, occasionally brilliant
  - Idle threshold is configurable — too short is intrusive, too
    long defeats the purpose
  - User can dismiss ("not now") — same as how humans handle
    intrusive thoughts from the unconscious
  - Output format: one short nudge sentence + a few tags
    - Nudge prompts (randomly selected):
      - "Any decisions worth tagging from this session?"
      - "Anything learned here that a future session should know?"
      - "Any connections worth noting?"
    - Tags: weighted-random selection from recent matches, like
      fortune cookie lucky numbers. Material to act on.
    - The nudge gives a reason to act, the tags give material.
      Neither works as well alone.
  - Risk: see ~/work/daneel/ark-unconscious-failure-modes-20260305.md
    — self-reinforcing loops, narrative personas as defense
- [ ] Temporary documents (`tmp://`) — ephemeral, in-memory, searchable
  - A session can index content without writing a file to disk
  - URI scheme: `tmp://SESSIONID/human-readable-name`
    (e.g. `tmp://abc123/scoring-notes`)
  - **Pure in-memory** — never touches LMDB. Trigrams, tags, content
    all held in RAM data structures. Searched alongside the real index
    but the database is never mutated. Program dies → they're gone.
  - microfts2 searches tmp:// documents via an in-memory overlay —
    same query interface, results merge with disk-backed results
  - `--tmp` flag on search/tag subcommands includes tmp:// documents
    in output (excluded by default? or included by default? TBD)
  - **Mutable** — sessions can edit tmp:// documents. Update content,
    re-chunk, re-tag, all in memory. Multiple agents can read and
    write the same tmp:// document — ad hoc blackboard / tuplespace.
    Agents brainstorm by editing a shared tmp:// doc, each seeing
    the others' contributions via tag-pattern subscriptions.
  - Subscription by tag pattern: a session registers interest in
    tag patterns (e.g. `@decision:*`, `@pattern:actor*`). When any
    document is indexed (tmp:// or real) and its tags match a
    pattern, ark publishes to the subscribing session. Sessions
    must act on notifications when they arrive — the content won't
    survive to be found later.
  - This is the inter-session channel — one session tags a decision,
    another session that subscribed to `@decision:` learns about it
    instantly without polling
  - Lifecycle: tmp:// documents exist as long as their session is
    alive. Session dies → gone. If it's worth keeping, write a file.
  - Complements the @ephemeral and @burn tags from tags.md
- [ ] `ark context` — session-awareness for agents
  - Reports: fresh connect vs continuation, time-of-day transitions
    (morning, noon, afternoon, evening, night), session duration
  - Enables Franklin to know whether the user just arrived or has
    been working — "good morning, 3 new messages" vs picking up
    where we left off
  - Without this, agents can't distinguish `@msg:read` (saw it last
    session) from `@msg:read` (saw it five minutes ago in this session)
  - Time of day can come from timed ark events (see below) rather
    than needing a hook
  - UserPromptSubmit hook turns a crank-handle to get pending events —
    appropriate for things like current time-of-day that don't need
    to fire between chat messages
  - Background event notification for things that do need to interrupt
- [ ] Timed events — event scheduling via tagged content
  - Tags carry timing information: `@at: 9:00`, `@every: morning`,
    `@after: 2h`, `@cron: 0 9 * * *` (syntax TBD)
  - Ark server evaluates schedules, fires events through the same
    subscription/notification channel as tag-pattern subscriptions
  - Events are tagged content — they go through the same pipeline
    as everything else. No separate event system.
  - User can blacklist directories, files, or individual events —
    sanity valve. `@mute: true` on a file silences its events.
    Blacklist in ark.toml for broader suppression.
  - Useful for Franklin (daily standup, weekly review), other agents,
    and cross-project coordination
  - Time-of-day transitions (morning, noon, afternoon, evening, night)
    are just timed events with well-known tags — not special-cased
- [ ] JSONL knowledge prospecting
  - Idle Haiku agents mine JSONLs chunk by chunk — tiny prospectors
  - External tag files point back to specific JSONL chunks:
    `@decision: use LMDB` referencing the conversation where it happened
  - Mined watermark per JSONL — fully prospected files skipped until
    they grow. Append detection (Phase C) says where to resume.
  - Prospectors externalize *tags*, not full text. Reduces noise
    without duplicating content. `ark chunks` provides context
    when someone finds a tag and wants the surrounding conversation.
  - Only tags actually *used*, not mentioned in examples — even
    Haiku can distinguish "this is a decision" from "this is
    someone talking about how decisions are tagged"
  - Gradually builds a curated tagged graph over the conversation
    archive. Search hits clean tag files instead of standing in
    front of the JSONL false-positive machine gun.
- [ ] Summarizing (skill)
  - Summarize content with separate summary files that use tags
  - Storage: `~/.ark/summaries/medusa-name/SUMMARY-FILE-1.md`
  - Summaries are indexed like any other file — searchable, tagged

### Applications of tmp:// documents

- **Agent blackboard** — multiple agents read/write a shared tmp:// doc,
  each seeing others' contributions via tag subscriptions. Ad hoc
  tuplespace that dies when the work is done.
- **Message staging** — draft a request/response in tmp://, review tags
  with `ark message get-tags`, edit with `set-tags`, promote to disk
  when ready. Filesystem stays clean until commitment.
- **Ephemeral context** — session indexes working notes, intermediate
  analysis, scratch thoughts. Searchable alongside the real index
  during the session, gone after. No cleanup, no orphan files.
- **Live state / UI backing** — tmp:// as an in-memory data model for
  state too transient for disk: progress bars, agent status, task
  queues. Agent tags `@progress: 47/100 indexing`, UI subscribes to
  `@progress:`, renders live. No file I/O, no debounce, no polling.
  Multi-agent dashboard: each agent's tmp:// doc has `@agent-status:`,
  orchestrator UI subscribes across all docs. Same subscription
  mechanism could back a TUI (`ark watch --tags progress`).
- **Monitoring of things** — tags hold values connected to a program's
  or system's live state. As things change, ark sends pings to
  subscribed agents (event compression, not raw firehose).
  - Agent writes `@cpu-load: 73%` or `@build-status: passing` to a
    tmp:// doc. Ark detects the tag value changed, notifies subscribers.
  - "Latest value wins" — intermediate updates are compressed, only
    the current state matters (c.f. incremental-search pattern in
    ~/work/frictionless/install/patterns/incremental-search.md)
  - Subscribers see tag-change events, not polling results. The
    subscription says *what* you care about, ark handles *when*.
  - Monitor.jl parallel: programs as database shards, value-change
    detection, transport-agnostic pub-sub
    (c.f. ~/work/bill/document-as-shared-memory-20260309.md)

## V4 — With-Model

Add vector search for users with the hardware. Batteries included.

- In-process embedding via gollama
  - Load nomic-embed-text at server startup, hold in memory
  - EmbedFunc in microvec alongside EmbedCmd (no exec)
  - Cold-start CLI loads model on demand (slower, acceptable)
- `--about-file <path>` — vector side of file similarity search
  - Chunk the query file, embed each chunk, search with each, merge results
  - Or average chunk embeddings for a single search (loses specificity)
  - Combines with `--like-file` FTS scores when both flags present
- Model distribution — fetch from HuggingFace on `ark init`
  - nomic-embed-text-v1.5.Q8_0.gguf (~140MB, one-time download)
  - Store in ~/.ark/models/
  - Model-free operation remains fully supported without download
- @manifest files for per-directory indexing rules
  - whenever ingesting a file, check it for @manifest entries
  - track files included specifically from @manifest entries
    because the entries "manage" them
  - maybe a record: 'M' [manifest] [dependent]
  - need format for @manifest — a markdown list might work well here

## AOT: Go methods for slow Lua in the ark app
  - Profile the ark Frictionless app, identify slow Lua methods
  - Rewrite hot paths as Go methods registered on the view
  - Keep original Lua in the file as a comment so users can
    uncomment and modify without touching Go
  - Balance: Go for performance, Lua stays as the customization surface
  - Handles both ark projects (`~/.ark/ark ui`) and Frictionless projects (`.ui/mcp`)

## Database
- [ ] `ark grow` — resize LMDB map when it fills up
  - Slurp from old env, spew to new with larger map, swap
  - Map size as init argument (default 2GB)
  - Avoids needing a large default that wastes sparse file space

## Chunking Strategies
- [x] Markdown-aware chunker — paragraph-based splitting in microfts2
  - `MarkdownChunkFunc`: splits on blank lines and heading transitions
  - Heading + following paragraph form one chunk
  - Blank lines excluded from chunk content
  - Registered as `markdown` strategy, replaces `lines` for `*.md`

## Content Extraction — Indexing Beyond Text

Ark indexes text. Many valuable files aren't text. The strategy:
extract text from binary formats, index the extraction. The source
file stays on disk; ark indexes the extracted text and attributes
results back to the original file.

Each extractor is a chunking strategy or a pre-processing step
that feeds into one. Build these incrementally — each new format
is independent.

- [ ] PDF — extract text with pdftotext or similar
  - Most common non-text format in knowledge work
  - Page numbers map to chunk boundaries naturally
  - Scanned PDFs need OCR (tesseract) — separate, harder problem
- [ ] Office documents — .docx, .xlsx, .pptx
  - All are ZIP archives containing XML
  - .docx: extract paragraph text from word/document.xml
  - .xlsx: extract cell text, sheet by sheet (each sheet = chunk?)
  - .pptx: extract slide text, one slide per chunk
  - Go libraries exist (unidoc, excelize) or just unzip + parse XML
- [ ] Image metadata — EXIF comments, IPTC captions, XMP descriptions
  - Not OCR — just the text metadata already embedded in the file
  - Lightweight: read EXIF/IPTC/XMP tags, index as a single chunk
  - Good for photos with descriptions, screenshots with annotations
- [ ] OCR — images and scanned PDFs
  - tesseract via exec (like chunking strategies)
  - Heavier dependency, optional — only if tesseract is installed
  - Quality varies; extracted text may be noisy
  - Lower priority than metadata extraction
- [ ] HTML — strip tags, index text content
  - Saved web pages, exported bookmarks, local documentation
  - Go's html.Parse or simple regex stripping
- [ ] Email — .eml / .mbox
  - Extract subject, from, body text
  - Thread structure could map to chunks
  - Lower priority — most people's email isn't on disk

The pattern: each extractor is a function that takes a file path
and returns text + chunk offsets. Same interface as chunking strategies.
`ark init` registers extractors for installed tools (pdftotext,
tesseract, etc.) and skips those that aren't available.

### Performance: first scan matters most
A new user's first scan may hit hundreds of PDFs/docs before they
can try anything. Exec-per-file is the bottleneck.
- **In-process where possible** — Go libraries for Office formats
  (excelize, xml parse for docx/pptx) eliminate exec entirely.
  PDF text extraction in pure Go (pdfcpu) avoids pdftotext dependency.
- **Batch invocation** — many external tools accept a list of files
  in one call, amortizing process startup and library initialization.
  Some also parallelize internally (tesseract `--parallel`, LibreOffice
  batch convert). Design the strategy interface to support batch:
  pass N file paths, get back N results. Let the tool handle its
  own concurrency rather than wrapping it in ours.
- **Parallel exec** — for tools that don't batch natively, fan out
  N workers. Files are independent; the only shared resource is
  LMDB writes (already serialized by transactions).
- **Progressive availability** — index text files first (instant),
  then extract binary formats in background. User can search
  immediately while PDFs are still processing.

## Usability — Making It Work for Everyone

The index and engine are infrastructure. The behavior that makes ark
useful lives in skills, the tag vocabulary, and opinionated defaults.
An unskilled user installs ark and their assistant should just start
building their zettelkasten. No training, no configuration ritual.

### Zero-config onboarding
- [ ] `ark init` creates everything needed: DB, config, tags.md, starter
  skill. The user runs one command. From then on, the assistant knows
  what to do.
- [ ] First-run experience: the assistant indexes the user's existing
  notes/docs/code. No "add source" step — the skill walks the user
  through what to index conversationally.

### Tag-as-you-go
- [ ] The assistant tags naturally during work, not as a separate step.
  Decisions get `@decision:` when made. Patterns get `@pattern:` when
  recognized. Questions get `@question:` when opened.
- [ ] Tags are embedded in the files the assistant is already writing —
  code comments, markdown notes, commit messages. Not a separate
  "tagging" action.
- [ ] The starter tags.md teaches the vocabulary by example. Each tag
  has a one-line description and the skill tells the assistant *when*
  to use it, not just what it means.

### Notes-as-you-go
- [ ] When something important comes up in conversation that doesn't
  belong in code, the assistant writes a note to `~/.ark/notes/`,
  tagged. The user never says "remember this" — the assistant
  recognizes what's worth keeping.
- [ ] Note format: markdown, one topic per file, tagged inline.
  Filename is topic-YYYYMMDD.md (suffix timestamp).
- [ ] What's worth a note: decisions and their reasoning, things
  learned through debugging, connections between systems, answers
  to questions that took effort to find.
- [ ] What's NOT worth a note: anything already captured in code,
  commits, or existing docs. No duplication.

### Search-before-answering
- [ ] The assistant checks ark before answering questions about the
  user's systems, preferences, or past decisions. The skill makes
  this the default behavior — not a special mode.
- [ ] CLAUDE.md bootstrap seeds context at session start so the
  assistant arrives already oriented.

### Model-tiered skills
- [ ] Opus skill: principles and taste. Internalizes when to tag,
  what's worth a note, when to search. Narrative identity works.
- [ ] Sonnet skill: explicit checklists and prompts. Sonnet can't
  hold narrative or make judgment calls about taste — it needs
  scaffolding. The skill tells it exactly what to do and when:
  - "Before answering questions about the user's code, run
    `ark search --chunks <topic>`"
  - "After completing a task, check: was a decision made? See
    `ark tag files decision` for examples, then add `@decision:`"
  - Step-by-step, externalized judgment. The CLI becomes the
    scaffolding that compensates for what the model lacks.
- [ ] `ark decide <phase>` — crank-handle command for weaker models.
  State machine lives in ark, not in the model. Each call returns
  a self-contained prompt: what to do, what command to run, what
  to look for. The assistant executes, feeds result back, cranks
  again. Sonnet/Haiku become capable executors because sequencing
  intelligence is externalized.
  - The skill is tiny: "After each task, run `ark decide check`.
    Do what it says."
  - Phases: scan, tag, note, review, idle — ark tracks where the
    session is and what needs attention
  - Output is a structured prompt, not data — designed to be
    followed by a model with no additional context

### Encoding taste
- [ ] The hard problem: when to tag, what's worth a note, what's
  noise. This can't be rules — it has to be principles and examples.
- [ ] The skill encodes this as: "tag decisions when the alternative
  was non-obvious. Tag patterns when you see the same approach twice.
  Write a note when you'd want this information in a future session
  but it won't survive in any existing file."
- [ ] Over time the user's tags.md grows with their own vocabulary.
  The starter set is a seed, not a taxonomy.

## Deferred

### `@ark-file:` tag on indexed files
Synthetic chunk prepended to every file: `@ark-file: <path>`. Makes filenames
searchable via FTS, gives tag-based file listing (`--tags ark-file`).

**Why deferred:**
- ark already has file metadata (`ark files`, `--filter-files`, `--exclude-files`)
- Per-file subscriptions can pattern-match on paths directly — no tag needed
- Synthetic chunk is alone, so `--regex` AND patterns can't cross from `@ark-file:`
  to content chunks — the composability that makes it useful doesn't actually work
  with the more powerful search operators
- "Everything is tags" taken one step too far — this is metadata, not content

**Still useful for:** subscriptions, FTS filename search if `--filter-files` proves
insufficient. Revisit if those needs materialize.

Design notes: .scratch/ARK-FILE.md (includes Chunker interface work, which was
extracted as a standalone item in V2.5)

## Future
- Multiple ark instances — separate databases for work/personal hygiene
  - Each instance is independent: own LMDB, config, socket
  - `--dir` flag already supports this; future work is convenience (naming, switching, discovery)
- LLM-driven tag extraction
- Secondary "arkive" database for cold storage
