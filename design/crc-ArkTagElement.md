# ArkTagElement
**Requirements:** R1476, R1477, R1478, R1479, R1480, R1481, R1482, R1483, R1484, R1490, R1491, R1492, R1493, R1494, R1497, R1498, R1512

Custom element (`<ark-tag>`) that renders an interactive tag widget
in read-only content (goldmark HTML and plain-text `<pre>` blocks).
No shadow DOM — inherits host theme CSS.

## Knows
- tag name (from `<name>` child element text)
- tag value (from `<value>` child element text)
- whether an inline search panel is currently open
- the currently open panel element (static, shared across all instances)

## Does
- renders styled tag: CSS `content` generates `@` on `name::before` and `:` on `name::after`; name colored `--term-accent-bright`, value colored `--term-success`, punctuation colored `--term-text`
- degrades to readable plain text without JS: `TAG VALUE`
- on click: closes any existing inline `<ark-search>` panel, creates a new `<ark-search>` element, sets its `tag` and `value` properties, inserts it after the tag's parent block element, sets `api` from `document.arkSearchAPI`
- on click when own panel is already open: removes the panel (toggle)
- dispatches bubbling `ark-tag-click` custom event with `detail: { name, value }`
- shows pointer cursor on hover

## Collaborators
- ArkSearchElement: created on click for inline search panel
- SearchAPI: obtained from `document.arkSearchAPI`, passed to created ArkSearchElement
- Server (Go): wraps tag patterns in `<ark-tag>` elements during content rendering (R1485-R1489)

## Sequences
- seq-ark-tag-click.md
