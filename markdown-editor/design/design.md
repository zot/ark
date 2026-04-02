# Markdown Viewer/Editor — Design

A CodeMirror 6 component that renders markdown with interactive ark
tag support. Built as a set of CM6 extensions that compose with
`@codemirror/lang-markdown`.

## Intent

Provide the viewer component described in specs/viewer.md — the
foundation layer for ark's illuminated documents. Tag widgets,
completion, ark-search blocks, and read/edit toggle, packaged as
a standalone JS component any host can embed.

## Cross-cutting Concerns

### Host API Boundary
All ark communication flows through the HostAPI interface (R1–R3).
The viewer imports nothing from ark or Frictionless. Every CRC card
that needs ark data receives it via HostAPI injection at construction.

### Packaging (R4)
Built assets (JS bundle, CSS) are placed in ~/.ark/html/ by the
build process. No npm runtime dependency. This is a build/install
concern, not a runtime component — no CRC card needed.

### Read/Edit Mode
A shared EditorState field tracks the current mode (R23–R24). All
extensions that produce interactive widgets check this field —
widgets are active in read mode, standard CM6 editing in edit mode.

## Artifacts

### CRC Cards
- [x] crc-ArkTagParser.md → `src/ark-tag-parser.ts`
- [x] crc-TagWidget.md → `src/tag-widget.ts`
- [x] crc-TagCompletion.md → `src/tag-completion.ts`
- [x] crc-ArkSearchBlock.md → `src/ark-search-block.ts`
- [x] crc-SearchResultView.md → `src/search-result-view.ts`
- [x] crc-HostAPI.md → `src/host-api.ts`
- [x] crc-ModeToggle.md → `src/mode-toggle.ts`

### Sequences
- [x] seq-tag-click.md → `src/tag-widget.ts`, `src/search-result-view.ts`
- [x] seq-tag-completion.md → `src/tag-completion.ts`
- [x] seq-ark-search-render.md → `src/ark-search-block.ts`, `src/search-result-view.ts`
- [x] seq-mode-toggle.md → `src/mode-toggle.ts`, `src/ark-search-block.ts`
- [x] seq-save.md → `src/mode-toggle.ts`

## Gaps

(populated after implementation)

- [ ] O1: Tag search panel UI not yet implemented (TagSearchWidget fires search but has no panel to display results)
- [ ] O2: innerHTML for non-markdown previews — sanitize if content is untrusted
- [ ] O3: No search deduplication/cancellation for in-flight requests
- [ ] A1: R4 (packaging) is a build concern — documented in design.md cross-cutting, no CRC card needed