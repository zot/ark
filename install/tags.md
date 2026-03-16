# Tags

Ark zettelkasten tags. Write them anywhere — code comments,
markdown, brainstorms, memory files. Ark finds them by content.
No registry enforces this list. New tags emerge by use.

Tag definitions (`@tag: name description`) can appear in any
indexed file, not just this one. They must be at the start of a
line — indented or mid-line `@tag:` is ignored. `ark tag defs`
shows all definitions from all sources.

## Core tags

@tag: tag -- the description of a tag

@tag: connection -- A relationship between two ideas, patterns, or systems. Format: `@connection: thing A = thing B`

@tag: pattern -- A recurring approach or solution. Name it.

@tag: personal-pattern -- A pattern from the personal patterns library (~/.claude/personal/patterns/). Used in code comments and design docs to link back to named patterns
@note: review existing @pattern entries and change to @personal-pattern where appropriate

@tag: decision -- A choice that was made and why. Captures the "why" so future sessions don't relitigate.

@tag: question -- An open question. Unanswered. Searchable so we remember to answer it.

@tag: learned -- Something confirmed through experience, not just theorized.

@tag: project -- Which project something relates to.

@tag: manifest -- Indexing rules for a directory. Ark finds these by tag and uses them to decide what to index.

@tag: ephemeral -- Content that should leave the index when no longer relevant. The file stays on disk, ark just drops it. For scratch notes, planning docs, session-specific memory files.

@tag: burn -- Consume and destroy. Once read/processed, remove from index and delete the file. True temporary notes.

@tag: project-idea -- A potential future project or exploration worth pursuing. Emerged from current work but not yet started.

@tag: ark-request -- A cross-project request. Format: `@ark-request: <short-name>`. See ARK-MESSAGING.md in ark for the full convention.

@tag: ark-response -- A response to a cross-project request. Format: `@ark-response: <id>`. Lives in the responding project's requests/ directory.

@tag: ark-request-sent -- Audit trail: links a planning item to the request it generated. Format: `@ark-request-sent: requests/foo.md`.

@tag: ark-request-ref -- Citation: references a request from any file. Format: `@ark-request-ref: <path-or-id>`.

@tag: ark-response-ref -- Citation: references a response from any file. Format: `@ark-response-ref: <path-or-id>`.

@tag: from-project -- The project making a request. Format: `@from-project: <name>`.

@tag: to-project -- The project receiving a request. Format: `@to-project: <name>`. Can list multiple projects.

@tag: issue -- An open issue, unresolved. Name it, describe it. Searchable across all projects — ad hoc Jira via ark.

@tag: status -- Lifecycle state of a request or work item. Values: open, accepted, in-progress, completed, denied, future. Response file existence = ack. Format: `@status: open`.

@tag: obsolete-req -- A requirement superseded by a newer one. Prefix the R# line in requirements.md. Keeps the old number stable (no renumbering) while marking it as replaced. Format: `@obsolete-req: R457 -- superseded by R607`.

@tag: waiting-for -- Something sent to another project or person that hasn't come back. Format: `@waiting-for: project-name` or `@waiting-for: person`. Franklin uses this to track what's in someone else's court.

@tag: reopened -- A completed request that turned out incomplete. Format: `@reopened: 2026-03-09 -- reason`. Search with `ark search --tags reopened`.

@tag: resolved -- A reopened issue that has been fixed. Format: `@resolved: 2026-03-09 -- what was done`.

@tag: warning -- A known hazard. Not a bug, not a task — a thing to be careful about. Surfaces when working in the area.

@tag: note -- An observation worth preserving. Not actionable, just worth knowing.

@tag: ark -- Topic tag for ark-related content. Use with subtopics: `@ark: tags, tuplespace` or `@ark: indexing, symlinks`.

Any tag is a potential reminder. The background reminder system
uses vector + FTS regex (`@[a-z]+:`) to find tagged content
matching the current conversation. No special reminder tag
needed — every tagged line is automatically a reminder candidate.

## Usage

In markdown, bare:
```
@connection: recall agent context isolation = closure-actor private state
```

In code, inside block comments so the tag starts on its own line:
```go
/*
@pattern: closure-actor
@decision: use LMDB for index — single writer, crash safe
*/
```
