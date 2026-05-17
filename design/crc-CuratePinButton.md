# CuratePinButton
**Requirements:** R2415, R2416, R2417, R2418, R2419, R2420, R2421, R2423, R2424, R2425

Inline-script button injected into every `<div class="ark-chunk">`
AND every `<ark-curate-region>` within the content view. Toggles
between unpinned (outlined) and pinned (filled) state by calling
`POST /curation/pin` / `POST /curation/dismiss`. Lives entirely in
the `content-markdown.html` and `content-plain.html` template
scripts — no compiled bundle, no custom element registration.

## Knows
- chunkID — read from the parent `div.ark-chunk` `data-chunkid` attr
- fileID — read from the parent `div.ark-chunk` `data-fileid` attr
- pinned state — kept on the button as `aria-pressed="true|false"`
- page path — derived once from `location.pathname` (strip the
  `/content/` prefix, URL-decoded)
- pinned-on-load snapshot — Set<chunkID> built from
  `GET /curation/pinned` during `DOMContentLoaded`

## Does
- on `DOMContentLoaded`: fetch `GET /curation/pinned`, build a
  Set of pinned chunk IDs; for every element matching
  `div.ark-chunk[data-chunkid], ark-curate-region[chunkid]` in
  the document, prepend a `<button class="ark-curate-pin">`
  with `aria-pressed="true"` when the chunk's id is in the
  set, otherwise `aria-pressed="false"`. Reads the chunkid via
  `el.dataset.chunkid` for the div case and
  `el.getAttribute('chunkid')` for the region case. Failed
  fetch leaves every button at `aria-pressed="false"`. (R2416,
  R2425)
- on click when `aria-pressed="false"`: POST `/curation/pin`
  with `{chunkID, fileID, path}` from the data attributes plus
  the resolved path; on 2xx, set `aria-pressed="true"`. (R2417)
- on click when `aria-pressed="true"`: POST `/curation/dismiss`
  with `{chunkID}`; on 2xx, set `aria-pressed="false"`. (R2417)
- on non-2xx response: leave visual state unchanged and log the
  error via `console.warn` (display-only feature; never breaks
  the page). (R2417, R2420)
- positioning: absolute, upper-left corner of the chunk div
  with a small inset (`top: 0.25em; left: 0.25em`); the chunk
  div gains `position: relative` via the same stylesheet. (R2418)
- visual: a single inline SVG glyph (`<svg viewBox="0 0 16 16">`)
  with a CSS class that flips `fill: currentColor` on the
  pinned state and `fill: none` on the unpinned state. Color
  drives off `--term-accent` (pinned) and `--term-text-dim`
  (unpinned). Print stylesheets hide it
  (`@media print { .ark-curate-pin { display: none; } }`). (R2418)
- handles PDF chunks via the `<ark-curate-region>` selector
  branch: the server emits one region per chunk with a `rect`
  inside its page's `<pdf-chunk>`; the region positions
  absolutely at the chunk's rect via the PdfChunkElement's
  `positionRegions` pass; the pin button sits at the region's
  upper-left, the region itself reveals a hover outline.
  Salvage chunks (no rect) fall through to the regular
  `<div class="ark-chunk">` wrapper that the salvage path
  already emits. (R2419, R2421, R2423, R2424)

## Collaborators
- Server (Go): provides `POST /curation/pin`, `POST /curation/dismiss`,
  and `GET /curation/pinned`. Provides the `data-chunkid` /
  `data-fileid` attributes that the button reads.
- Curation (Go): writes the underlying pinned slice; the
  workshop and the pin button both see the same state through
  the same endpoints.

## Sequences
(none yet — interaction is per-click, fully captured in the
endpoints' specs)
