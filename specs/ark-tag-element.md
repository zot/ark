# Ark Tag Element

Interactive `<ark-tag>` web component for rendering tag widgets in
all file types. Tags become clickable, styled elements in both
goldmark-rendered markdown and plain-text `<pre>` blocks.
Language: TypeScript (component), Go (post-processing).

## Problem

Tag widgets currently only appear in the CM6 editor — they're
CodeMirror decorations created by `tag-widget.ts`. The goldmark
read view and plain-text content pages render tags as flat text
with no interactivity. Files with tags should show interactive
tag widgets everywhere, not just in edit mode.

## The Component

`<ark-tag>` is a custom element (no shadow DOM — inherits host
theme CSS). It renders an interactive tag with styled punctuation,
tag name, and value:

```html
<ark-tag><name>ark-request</name> <value>flib-port-7d28514c</value></ark-tag>
```

CSS `content` properties generate the `@` prefix and `:` suffix
on the `<name>` element. The markup carries only the semantic
parts. Without JS loaded, the element reads as plain text:
`ark-request flib-port-7d28514c`.

### Styling

Uses `--term-*` CSS variables for theme compatibility:
- `@` and `:` punctuation: `--term-text` (adapts to light/dark)
- Tag name: `--term-accent-bright` (orange in LCARS)
- Tag value: `--term-success` (green in LCARS)
- Cursor on hover to indicate clickability

### Click Behavior

Clicking the element toggles an inline `<ark-search>` panel
directly below the tag in the document flow — the same pattern
as CM6's `TagSearchWidget` play button, but using plain DOM
insertion instead of editor decorations.

The element also dispatches a bubbling custom event for any
external listeners:

```js
new CustomEvent('ark-tag-click', {
  bubbles: true,
  detail: { name, value }
})
```

### Inline Search Panel

When clicked, the `<ark-tag>` element:

1. Creates an `<ark-search>` element
2. Sets its `tag` and `value` properties from the tag's content
3. Inserts it after the tag (or after the parent block element)
4. Clicking again or the panel's close button removes it

The `<ark-tag>` element needs access to a `SearchAPI` to pass
to the search panel. The content page provides this by setting
a shared API reference that `<ark-tag>` elements can find — either
a global, a property on a parent element, or a DOM-level
convention like `document.arkSearchAPI`.

Only one inline panel should be open at a time per content page.
Opening a new one closes the previous.

## Server-Side Post-Processing

The Go server wraps `@tag: value` patterns in `<ark-tag>` elements
before templating. This is a single post-processing pass applied
to both content paths:

1. **Markdown path**: applied to the goldmark HTML output. Tags
   appear inside `<p>` elements as rendered text — the regex
   matches tag patterns in the HTML text content.

2. **Plain-text path**: applied to the HTML-escaped `<pre>` content.
   Tags appear as escaped text — the regex matches after escaping.

The post-processing function takes rendered HTML and returns HTML
with tag patterns wrapped in `<ark-tag>` elements. It must not
match inside HTML attributes or existing element content where
the pattern appears coincidentally.

### Tag Pattern

The tag regex matches `@name:` followed by value text to end of
line (or `<br>` in goldmark output). Tag names follow ark's
definition: `[a-zA-Z][\w.-]*`. The value is everything after the
colon and optional whitespace, trimmed.

Tags that are already inside an `<ark-tag>` element are skipped
(idempotency for any future double-processing path).

## Script Loading

The `<ark-tag>` component needs its JavaScript loaded in content
pages. Options:

- Inline the component definition in both HTML templates (small
  enough — the element is simple)
- Or bundle as a tiny standalone JS file served alongside the
  content page

The component definition should be minimal — just the custom
element registration, CSS content rules, and click handler. No
imports, no build step required if inlined.

## Content Page Integration

### content-markdown.html

The goldmark read view (`#content` div) shows rendered HTML with
tags wrapped in `<ark-tag>` elements. Clicking a tag opens an
inline `<ark-search>` panel below it. When the CM6 editor is
active, the read view is hidden — CM6's own tag widgets handle
interactivity there.

The page sets `document.arkSearchAPI` to the `api` object so
`<ark-tag>` elements can find it. The `<ark-search>` element
and its CSS are already available on this page (loaded for the
editor path).

### content-plain.html

Plain-text `<pre>` blocks have tags wrapped in `<ark-tag>` elements.
Same inline search panel on click. This page needs the
`<ark-search>` element script loaded — either inline or as a
separate bundle import.

## Scope Boundary

`<ark-tag>` is for non-editable content only: goldmark read views,
plain-text `<pre>` blocks, and any future read-only rendering path.
It must never appear inside the CM6 editor. CM6 manages its own
tag decorations (`TagSearchWidget`, `StatusWidget`, completion)
through CodeMirror's state and decoration system, which maintains
editability of the underlying document text. Custom elements in
the CM6 DOM would break that contract.

The two systems are independent:
- **CM6 editor**: `tag-widget.ts` decorations (editable)
- **Read views**: `<ark-tag>` custom elements (interactive, not editable)
