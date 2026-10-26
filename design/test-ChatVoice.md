# Test Design: ChatVoice
**Source:** crc-ChatVoice.md

## Test: segments follow the chunker's tag positions
**Purpose:** a tag opens or closes a segment only where the chunker writes one
**Input:** `<thinking>a\nb</thinking>\nsaid text\n<narration>para</narration>`, plus a plain line that quotes `` `<thinking>` `` mid-sentence and an unclosed `<thinking>tail`
**Expected:** segments thinking("a\nb"), text("said text …quotes `<thinking>` mid-sentence"), narration("para"), thinking("tail"); no tag text remains in any voiced segment
**Refs:** crc-ChatVoice.md
**Code:** chatvoice_test.go
**Fire alarm:** count a voice tag anywhere in a line, not only at line start / end; the quoting line splits into a thinking segment and red names the segment list
**Inject:** chatvoice.go:chatVoiceSegments
**Pulled:** 2026-10-26 — rang: `CutPrefix` → `strings.Cut` (tag anywhere) fails TestChatVoiceSegmentsFollowTagPositions with the segment list; re-pulled the same day after the close check lost its whitespace trim

## Test: a segment's leading block markdown survives and voices do not bleed
**Purpose:** per-segment rendering (R3616), the #168 defect
**Input:** `<thinking>- one\n- two</thinking>\nplain **said** text`
**Expected:** a thinking div containing `<ul>` with two items, then a text div containing `<strong>said</strong>`; the text div is not inside the list
**Refs:** crc-ChatVoice.md
**Code:** chatvoice_test.go
**Fire alarm:** render the whole tag-stripped text in one md call and wrap the result once; the list item swallows the plain line and red shows the merged HTML
**Inject:** chatvoice.go:renderChatVoices
**Pulled:** 2026-10-26 — rang: tag-stripped text in one md call fails the bleed test ("plain text bled into the thinking list") and also the icon, preview and content tests

## Test: the icon marks only a change of voice
**Purpose:** R3617 run rule
**Input:** narration, narration, text, thinking, thinking
**Expected:** icon spans on segments 1, 3, 4 only (three spans total)
**Refs:** crc-ChatVoice.md
**Code:** chatvoice_test.go
**Fire alarm:** emit the span on every segment; red reports five spans
**Inject:** chatvoice.go:renderChatVoices
**Pulled:** 2026-10-26 — rang: `seg.voice != prev || true` fails with "icon spans on segments [1 2 3 4 5], want [1 3 4]" (a first `if true` left `prev` unread and did not compile, so it proved nothing)

## Test: chat previews render voices as markdown
**Purpose:** R3620 — RenderPreview no longer escapes chat into `<pre>`
**Input:** RenderPreview(chunk with a thinking list, "chat-jsonl", one token pattern)
**Expected:** output has `ark-voice-thinking`, a `<ul>`, a `<mark>` on the token, no `<pre>`, no `&lt;thinking&gt;`
**Refs:** crc-ChatVoice.md, crc-Searcher.md
**Code:** chatvoice_test.go
**Fire alarm:** drop the chat-jsonl case from RenderPreview so it falls to preEscaped; red shows the `<pre>` output
**Inject:** search.go:RenderPreview
**Pulled:** 2026-10-26 — rang: without the chat-jsonl case the preview lacks "ark-voice-thinking"

## Test: the content view renders chat chunks by voice
**Purpose:** R3619 — chunked and ranged content bodies use the voice renderer
**Input:** renderChatContent on a chunk opening `<thinking>- a list`
**Expected:** `ark-voice-thinking` div containing `<ul>`; no `raw HTML omitted`
**Refs:** crc-ChatVoice.md, crc-Server.md
**Code:** chatvoice_test.go
**Fire alarm:** make renderChatContent call renderMarkdownForContent on the whole text; red shows `raw HTML omitted`
**Inject:** chatvoice.go:renderChatContent
**Pulled:** 2026-10-26 — rang: whole-text renderMarkdownForContent fails "content view should render the thinking list by voice"
