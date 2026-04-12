# Sequence: ark-tag click to inline search

Shows what happens when a user clicks an `<ark-tag>` element in a
read-only content page (goldmark or plain text).

## Participants
- User
- ArkTagElement — the clicked `<ark-tag>` custom element
- ArkSearchElement — inline search panel created on demand
- SearchAPI — server communication interface

## Flow

```
User                ArkTagElement         ArkSearchElement       SearchAPI
  |                      |                      |                     |
  |--- click ----------->|                      |                     |
  |                      |                      |                     |
  |                      |-- check: own panel    |                     |
  |                      |   already open?       |                     |
  |                      |                      |                     |
  |        [if own panel open: remove it, done]  |                     |
  |                      |                      |                     |
  |        [if other panel open: remove it]      |                     |
  |                      |                      |                     |
  |                      |-- dispatch            |                     |
  |                      |   ark-tag-click       |                     |
  |                      |   {name, value}       |                     |
  |                      |                      |                     |
  |                      |-- get document        |                     |
  |                      |   .arkSearchAPI       |                     |
  |                      |                      |                     |
  |                      |-- create <ark-search> |                     |
  |                      |   set tag, value, api |                     |
  |                      |                      |                     |
  |                      |-- insert after        |                     |
  |                      |   parent block el     |                     |
  |                      |                      |                     |
  |                      |-- store ref as        |                     |
  |                      |   active panel        |                     |
  |                      |                      |                     |
  |                      |               [connectedCallback]          |
  |                      |                      |                     |
  |                      |                      |-- search(query) --->|
  |                      |                      |                     |
  |                      |                      |<--- results --------|
  |                      |                      |                     |
  |                      |                      |-- render results    |
  |                      |                      |                     |
```

## Notes

- R1482: Only one panel at a time. The static `activePanel` reference
  tracks which `<ark-tag>` has an open panel. Opening a new one removes
  the previous.
- R1483: `document.arkSearchAPI` is set by the content page script
  (both `content-markdown.html` and `content-plain.html`).
- R1484: The `ark-tag-click` event fires before panel creation —
  external listeners can suppress or augment the behavior.
- R1493: This flow never occurs inside CM6. The server only wraps
  tags in the read-view content div (`#content`) or plain-text `<pre>`.
