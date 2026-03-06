# Ark Index Manager

@note: add interactive searching -- this is the user's google and yahoo on all their stuff and all of the assistant's stuff. Frictionless chat and other events links to the AI partner directly
@note: need to have a way to show unresolved files deep in the trees. Ark CLI should help with this. `ark unresolved` has "path" right now but it should be <pattern>

Visual frontend for ark's source configuration. Shows what's indexed,
what's excluded, and what's unaccounted for — merged into one tree so
discrepancies are immediately visible.

## Architecture

**Lua + ark CLI:** The app shells out to `ark` commands for all
operations. If we discover missing CLI support during development,
that's the app doing its job — we go add it to ark.

## Source List

Left panel. Shows configured source directories from ark.toml plus
an "Add Source" control.

Each source shows:
- Directory path
- Strategy name (markdown, lines, etc.)
- File counts: included / excluded / unresolved

Clicking a source shows its file tree in the right panel.

### Add Source

Button or form to add a new source directory:
- Directory path (text input)
- Strategy picker (from registered strategies)
- Writes to ark.toml

### Claude Integration

Quick-add buttons for common Claude paths:
- **Claude Projects** — pick-list of directories under `~/.claude/projects/`
- **Chat History** — Claude's conversation logs
- **Memories** — Claude's memory files

These pre-fill the "Add Source" form with the right path and a
sensible default strategy.

## File Tree

Right panel. Shows the actual filesystem merged with ark's index
state. Every file and directory shows one of three states:

- **Included** (green check) — matches an include pattern, will be indexed
- **Excluded** (red X) — matches an exclude pattern, skipped
- **Unresolved** (gray ?) — matches nothing, ark won't touch it

### Merged View

The tree shows files from two sources overlaid:
- Files that exist on disk (from walking the source directory)
- Files that are indexed but missing from disk

Visual treatment:
- Normal text — file exists on disk and is accounted for (included or excluded)
- *Italic* — indexed in ark but missing from disk (ghost file)
- **Bold** — exists on disk but not accounted for (unresolved)

### Example Tree

```
~/work/myproject/
├── [✓] src/                      ← collapsed by default (fully included)
│   ├── [✓] main.go                  (visible when expanded)
│   ├── [✓] server.go
│   └── [?] utils_test.go        ← unresolved (bold)
├── [✗] .git/                     ← collapsed (fully excluded)
├── [✗] node_modules/             ← collapsed (fully excluded)
├── [✓] README.md
├── [✓] design.md
├── [?] scratch.txt               ← unresolved (bold)
└── [✓] api-notes.md              ← missing (italic)
```

Directory states roll up from children:
- All children included → directory shows [✓]
- All children excluded → directory shows [✗]
- Mixed or has unresolved → directory shows [~]

### Expanding and Collapsing

All directories are expandable regardless of state. A fully-included
`[✓]` directory can be expanded to carve out exceptions — flip any
descendant to `[✗]` to exclude it from the blanket include.

**Default collapse:** Fully-included and fully-excluded directories
start collapsed. Mixed directories (`[~]`) start expanded so
discrepancies are immediately visible.

**"Collapse resolved" button:** Collapses all fully-included and
fully-excluded directories in one click, cleaning up the view to
show only the things that need attention.

### Lazy Loading

The tree does not generate all nodes at once. Children are loaded
in batches when a directory is expanded — keeps the DOM clean while
balancing against round-trip latency. Directories that have never
been expanded have no child nodes until the user opens them.

### Changing State

Clicking a file's state indicator cycles: include → exclude → unresolved.

Clicking a directory's state indicator applies to all unresolved
children (does not override existing explicit rules).

**Pattern generation:** When the user changes a file's state, the app
generates the simplest pattern that covers it:
- Single file → `filename`
- Directory → `dirname/`
- All children → `dirname/**`

**Exception patterns:** Each directory node has a combobox with
common exception patterns. If the directory is included, the
patterns create prefixed excludes. If excluded, they create
prefixed includes. The last option is "type a glob" for custom
patterns.

Example for an included `src/` directory:
```
├── [✓] src/ [ ▾ exceptions    ]
│              ├─ *_test.go
│              ├─ *.generated.*
│              ├─ vendor/**
│              └─ type a glob...
```

The selected pattern is automatically prefixed by the node's path
(e.g. `src/*_test.go`).

Expanding the directory still works — you can combine exception
patterns with individual overrides on children.

**Under the hood:** The three states map directly to ark.toml
patterns:
- Include → add an include pattern for this file/directory
- Exclude → add an exclude pattern for this file/directory
- No pattern → remove any pattern covering this file

The UI shows the *effective* state (a file may be included because
a parent glob matches it), but clicking always operates on
explicit patterns.

### Why Tooltips

Every state indicator has a tooltip explaining *why* the file has
that state:
- "Included by pattern: *.md"
- "Excluded by .gitignore: node_modules/"
- "Excluded by pattern: *_test.go (source: src/)"
- "No matching pattern"

This makes the rule resolution transparent without cluttering the
tree itself.

### Ignore File Integration

If a directory contains a `.gitignore` or `.arkignore`, the tree
node shows a checkbox for whether to honor it (checked by default).
When honored, the ignore file's patterns fold into the tree — files
excluded by an ignore rule show as `[✗]` with a tooltip indicating
the source (e.g. "Excluded by .gitignore: *.log").

## Status Bar

Bottom of the app. Shows:
- Total files: included / excluded / unresolved / missing
- Last scan timestamp
- Server status (running or not)
