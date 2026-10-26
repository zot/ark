# ChatVoice
**Requirements:** R3615, R3616, R3617, R3618, R3619, R3620, R3621

Renders a chat chunk's text so each part shows its voice: thinking, narration,
or plain text (specs/chat-transcript.md, "Voices in rendered chat chunks"). The
voice tags themselves are the chunker's (R3608, `thinkingOpen` … in db.go).

## Knows
- voice: thinking | narration | text, with its class (`ark-voice-thinking`,
  `ark-voice-narration`, `ark-voice-text`)
- segment: a voice and the text it covers, tags removed

## Does
- chatVoiceSegments(text): split into segments. An open tag counts only at
  the start of a line, its matching close only at the end of a line; text
  between voiced segments is a text segment; an unclosed open runs to the end;
  a tag anywhere else is text. Empty text segments (the newline between two
  voiced blocks) are dropped. R3615
- renderChatVoices(text, md): render each segment with md (the caller's
  markdown renderer), wrap it in `<div class="ark-voice ark-voice-V">`, and
  start it with an empty `<span class="ark-voice-icon" aria-hidden="true">`
  when it is the first segment or its voice differs from the previous one.
  R3616, R3617
- icon glyphs: not Go's. `install/html/ark-content.css` supplies them as
  `.ark-voice-V > .ark-voice-icon::before { content: … }` (🧠 thinking,
  🗣️ text, 👓 narration). `theme-patches/base-patch.css` is one `@import` of
  it, which the Makefile prepends to the bundled `base.css` that every page
  links; a theme may override the rules in its own sheet. R3618, R3621

## Collaborators
- Server: chunkedBody / unchunkedBody render chat-jsonl through
  renderChatVoices with renderMarkdownForContent per segment. R3619
- Searcher: RenderPreview renders chat-jsonl through renderChatVoices with
  plain goldmark per segment, then highlights tokens. R3620
