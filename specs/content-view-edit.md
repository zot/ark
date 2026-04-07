# Content View/Edit Toggle

Upgrade `/content/` for markdown files from edit-only (CodeMirror 6)
to a read-first experience with on-demand editing.
Language: Go (server-side rendering), TypeScript (client-side toggle).
Environment: ark UI server.

## Context

Currently `/content/PATH` for markdown files loads an HTML shell that
immediately creates a CM6 editor. This is heavy for just reading a
file. The new behavior renders markdown server-side via goldmark
(already a dependency) and provides a toggle button to switch into
an ink-mde editor when editing is needed.

## Read View (default)

`/content/PATH` for markdown files returns an HTML page with the
markdown rendered to HTML by goldmark on the server. The rendered
HTML appears in a scrollable content area.

Links and images in the rendered HTML are rewritten so they work
through ark's content routes:

- Relative image `src` → `/raw/BASEDIR/src` (serves the image
  through the raw endpoint)
- Relative link `href` ending in `.md` → `/content/BASEDIR/href`
  (navigates to another rendered markdown page)
- Absolute paths and external URLs are left unchanged

BASEDIR is the directory portion of the requested file's path.

A pencil icon button sits at the upper right of the page. Clicking
it switches to Edit View.

## Edit View

Clicking the pencil button:

1. Fetches the raw markdown from `/fetch/PATH`
2. Creates an ink-mde editor instance with ark extensions (tag
   parser, tag widgets, tag completion, search blocks — same
   extensions as the CM6 editor but composed via ink-mde plugins)
3. Replaces the rendered content area with the editor
4. The pencil button becomes an eye icon

The ink-mde spike (`markdown-editor/src/ink-spike.ts`) already
demonstrates composing ark extensions with ink-mde. This needs to
be integrated into the main bundle alongside the existing CM6 editor.

The editor wires to the same HostAPI endpoints as the current CM6
shell: `/search/grouped`, `/tags/complete`, `/tags/values`,
`/file/save`, `/tags/set`.

Ctrl+S saves via the HostAPI.

## Returning to Read View

Clicking the eye button:

1. If the document has been modified since last save, prompts
   "Save changes?" with Save / Discard options
2. If Save: saves via HostAPI, then reloads the page (gets fresh
   goldmark rendering from server)
3. If Discard: reloads the page without saving
4. If not dirty: reloads the page

Reloading is the simplest correct approach — it guarantees the
goldmark rendering reflects the saved state.

## Bundle Changes

The `ark-markdown-editor.js` bundle needs to export both:

- `createArkEditor` (existing CM6 editor, used by Frictionless app)
- `createInkArkEditor` (ink-mde editor with ark extensions)

The `/content/` HTML shell loads the bundle and calls
`createInkArkEditor` when the user clicks the pencil button.

## Tag Line Rendering

Tag lines (`@name: value`) must end with two trailing spaces so
markdown renderers produce line breaks. This applies everywhere:
GitHub, goldmark, any viewer.

- `TagBlock.Render()` appends two spaces before the newline.
- `ParseTagBlock` trims trailing spaces from values on parse to
  prevent accumulation across parse→render round-trips.
- `NormalizeTagLines(data)` normalizes any tag line in arbitrary
  content to end with exactly two trailing spaces.
- Three call sites: `handleSave` (on write to disk),
  `renderMarkdownForContent` (before goldmark, safety net for
  hand-edited files), and the editor JS (before loading into
  ink-mde — marks dirty if content changed).

## Template Externalization

The content HTML shells (`content-markdown.html`,
`content-plain.html`) live in `~/.ark/html/` as Go
`html/template` files. They are read from disk on each request so
CSS changes take effect immediately on reload — no binary rebuild.

Templates include the full theme stack (base + all theme CSS
files) and set the active theme class from localStorage, matching
the main ark app.

## Non-markdown files

No change — non-markdown files still get the `<pre>` presentation.
