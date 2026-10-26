package ark

import "strings"

// CRC: crc-ChatVoice.md | R3615
// A chat chunk's voices: what was said (plain text), thinking, and narration.
// The tags are the chunker's (R3608, thinkingOpen … in db.go).
type chatVoice int

const (
	voiceText chatVoice = iota
	voiceThinking
	voiceNarration
)

// voiceIconSpan marks the start of a run of one voice; the glyph comes from the
// theme's CSS, never from here, so a skin can change it (R3618).
const voiceIconSpan = `<span class="ark-voice-icon" aria-hidden="true"></span>`

var voiceClasses = [...]string{
	voiceText:      "ark-voice-text",
	voiceThinking:  "ark-voice-thinking",
	voiceNarration: "ark-voice-narration",
}

// voiceDelims pairs each tagged voice with its tags.
var voiceDelims = [...]struct {
	voice       chatVoice
	open, close string
}{
	{voiceThinking, thinkingOpen, thinkingClose},
	{voiceNarration, narrationOpen, narrationClose},
}

type voiceSegment struct {
	voice chatVoice
	text  string
}

// chatVoiceSegments splits chunk text into voice segments. A tag counts only
// where the chunker writes one: the open tag at the start of a line, its close
// at the end of a line. A tag anywhere else — a conversation quoting one
// mid-sentence — is text. An unclosed open runs to the end. Blank segments
// (the newline between two voiced blocks) are dropped.
// CRC: crc-ChatVoice.md | R3615
func chatVoiceSegments(text string) []voiceSegment {
	var segs []voiceSegment
	var lines []string
	cur, closeTag := voiceText, ""
	flush := func() {
		if joined := strings.Join(lines, "\n"); strings.TrimSpace(joined) != "" {
			segs = append(segs, voiceSegment{cur, joined})
		}
		lines = lines[:0]
	}
	for _, line := range strings.Split(text, "\n") {
		if cur == voiceText {
			for _, d := range voiceDelims {
				if rest, ok := strings.CutPrefix(line, d.open); ok {
					flush()
					cur, closeTag, line = d.voice, d.close, rest
					break
				}
			}
		}
		if cur != voiceText {
			if body, ok := strings.CutSuffix(line, closeTag); ok {
				lines = append(lines, body)
				flush()
				cur = voiceText
				continue
			}
		}
		lines = append(lines, line)
	}
	flush()
	return segs
}

// renderChatVoices renders each voice segment with md on its own, so block
// markdown opening a segment stays a block and no segment's markdown absorbs
// the next one's text, and wraps it in an element ark emits. A segment starts
// with the icon span when it opens a run of its voice.
// CRC: crc-ChatVoice.md | R3616, R3617
func renderChatVoices(text string, md func([]byte) string) string {
	var b strings.Builder
	prev := chatVoice(-1)
	for _, seg := range chatVoiceSegments(text) {
		b.WriteString(`<div class="ark-voice `)
		b.WriteString(voiceClasses[seg.voice])
		b.WriteString(`">`)
		if seg.voice != prev {
			b.WriteString(voiceIconSpan)
		}
		b.WriteString(md([]byte(seg.text)))
		b.WriteString("</div>\n")
		prev = seg.voice
	}
	return b.String()
}

// renderChatContent is the content view's chat-jsonl renderer: voice segments,
// each through the content view's markdown renderer.
// CRC: crc-ChatVoice.md | R3619
func renderChatContent(data []byte, filePath string) string {
	return renderChatVoices(string(data), func(seg []byte) string {
		return renderMarkdownForContent(seg, filePath)
	})
}
