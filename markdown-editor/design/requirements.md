# Requirements

## Feature: Markdown Viewer/Editor Component
**Source:** specs/viewer.md

### Host Integration
- **R1:** The viewer is a standalone CM6 component with no dependency on Frictionless or host view framework
- **R2:** The host passes an API object at construction with: search, tagComplete, tagValueComplete, save, navigate, setTags
- **R3:** The viewer never calls ark directly — the host adapts its own transport (HTTP or in-process Lua) to the API interface
- **R4:** Built assets (JS bundle, CSS) are placed in ~/.ark/html/ — no npm runtime dependency

### Tag Parsing
- **R5:** Tags (`@word: value`) in the document are detected by a Lezer markdown parser extension and produce typed AST nodes
- **R6:** (inferred) The tag parser must not conflict with email addresses or other `@` usage — the `@word:` pattern (word chars + colon) is the disambiguator

### Tag Widgets
- **R7:** Any tag: click opens a search panel below the line, search field shows the full tag text pre-selected, user can read results or type to refine
- **R8:** Schedule tags: date picker widget for the value
- **R9:** Status tags: dropdown with known values (open, accepted, in-progress, completed, denied, future)
- **R10:** Ack tags: gap-detection helpers
- **R11:** Widgets render inline or as line decorations using CM6 WidgetType

### Tag Completion
- **R12:** `@` at the start of a word triggers tag name completion from the index (D records via tagComplete)
- **R13:** After the colon in `@tagname:`, triggers value completion from the tag index (via tagValueComplete)

### ark-search Code Blocks
- **R14:** Fenced code blocks with `ark-search` language tag render as live search result panels
- **R15:** Three view modes cycle on click: both (source + results), results only, src only
- **R16:** Default mode order is both,results,src — initial display is the first in the list. ark-search blocks inside search results default to src,both,results (source first, no search fires until user clicks through)
- **R17:** Code fence accepts optional `mode=` attribute to restrict and order available modes (e.g. `mode=results` for read-only search)
- **R18:** Edit mode always enables all three modes regardless of the mode attribute
- **R19:** Markdown results render in read-only CM6 instances with tag widgets active; non-markdown results use pre-rendered HTML
- **R20:** Search results include complete raw chunk content (full indexed chunk, not hit context), content type, and pre-rendered HTML
- **R21:** Click a result to navigate (via host navigate call)
- **R22:** Edit the query in both/src mode, results update live

### Read/Edit Mode
- **R23:** Default mode is read-only with markdown rendered and widgets active
- **R24:** Toggle to edit mode for full text editing
- **R25:** On save: call save(path, content), host re-indexes
- **R26:** Tag edits can use setTags for atomic tag block updates
