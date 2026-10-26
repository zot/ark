package ark

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// CRC: crc-ChatVoice.md | Test: test-ChatVoice.md | R3615
func TestChatVoiceSegmentsFollowTagPositions(t *testing.T) {
	text := "<thinking>a\nb</thinking>\n" +
		"said text that quotes `<thinking>` mid-sentence\n" +
		"<narration>para</narration>\n" +
		"<thinking>tail"
	want := []voiceSegment{
		{voiceThinking, "a\nb"},
		{voiceText, "said text that quotes `<thinking>` mid-sentence"},
		{voiceNarration, "para"},
		{voiceThinking, "tail"},
	}
	if got := chatVoiceSegments(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments:\n got  %+v\n want %+v", got, want)
	}
}

// CRC: crc-ChatVoice.md | Test: test-ChatVoice.md | R3616
func TestChatVoicesKeepLeadingBlocksAndDoNotBleed(t *testing.T) {
	html := renderChatVoices("<thinking>- one\n- two</thinking>\nplain **said** text", previewMarkdown)
	thinkEnd := strings.Index(html, "</div>")
	if thinkEnd < 0 || !strings.Contains(html[:thinkEnd], "<ul>") || strings.Count(html[:thinkEnd], "<li>") != 2 {
		t.Fatalf("thinking segment should be a two-item list on its own:\n%s", html)
	}
	if strings.Contains(html[:thinkEnd], "said") {
		t.Fatalf("plain text bled into the thinking list:\n%s", html)
	}
	if !strings.Contains(html[thinkEnd:], `class="ark-voice ark-voice-text"`) || !strings.Contains(html[thinkEnd:], "<strong>said</strong>") {
		t.Fatalf("plain text should be its own text segment:\n%s", html)
	}
	if strings.Contains(html, "&lt;thinking") || strings.Contains(html, "raw HTML omitted") {
		t.Fatalf("a voice tag reached the page:\n%s", html)
	}
}

// CRC: crc-ChatVoice.md | Test: test-ChatVoice.md | R3617
func TestChatVoiceIconMarksOnlyAChangeOfVoice(t *testing.T) {
	text := "<narration>n1</narration>\n<narration>n2</narration>\nsaid\n" +
		"<thinking>t1</thinking>\n<thinking>t2</thinking>"
	html := renderChatVoices(text, func(b []byte) string { return string(b) })
	divs := strings.Split(strings.TrimSpace(html), "\n")
	if len(divs) != 5 {
		t.Fatalf("want 5 segments, got %d:\n%s", len(divs), html)
	}
	var marked []int
	for i, d := range divs {
		if strings.Contains(d, voiceIconSpan) {
			marked = append(marked, i+1)
		}
	}
	if want := []int{1, 3, 4}; !reflect.DeepEqual(marked, want) {
		t.Fatalf("icon spans on segments %v, want %v:\n%s", marked, want, html)
	}
}

// CRC: crc-ChatVoice.md | Test: test-ChatVoice.md | R3620
func TestRenderPreviewRendersChatByVoice(t *testing.T) {
	text := "<thinking>- a list item\n- two</thinking>\nplain said text"
	html := RenderPreview(text, "chat-jsonl", []*regexp.Regexp{regexp.MustCompile(`said`)}, nil, "x.jsonl", 1.5)
	for _, want := range []string{"ark-voice-thinking", "<ul>", "<mark"} {
		if !strings.Contains(html, want) {
			t.Errorf("preview lacks %q:\n%s", want, html)
		}
	}
	for _, bad := range []string{"<pre>", "&lt;thinking&gt;"} {
		if strings.Contains(html, bad) {
			t.Errorf("preview has %q:\n%s", bad, html)
		}
	}
}

// CRC: crc-ChatVoice.md | Test: test-ChatVoice.md | R3619
func TestContentRendersChatByVoice(t *testing.T) {
	html := renderChatContent([]byte("<thinking>- a list\n- more</thinking>"), "/x/chat.jsonl")
	if !strings.Contains(html, "ark-voice-thinking") || !strings.Contains(html, "<ul>") {
		t.Fatalf("content view should render the thinking list by voice:\n%s", html)
	}
	if strings.Contains(html, "raw HTML omitted") {
		t.Fatalf("a voice tag reached goldmark:\n%s", html)
	}
}
