package ark

import "testing"

// R2913 — stripArkTags removes real ark-tag spans (full-line tags take
// their whole line + newline; inline tags take just the span, keeping the
// line) while leaving mentions untouched, so the strip agrees with the
// ExtractTagValues notion of a tag.
func TestStripArkTags(t *testing.T) {
	cases := []struct {
		name     string
		strategy string
		in       string
		want     string
	}{
		{"full-line tag eats line+newline", "markdown",
			"before\n@status: done\nafter\n", "before\nafter\n"},
		{"leading-whitespace full-line tag", "markdown",
			"before\n  @status: done\nafter\n", "before\nafter\n"},
		{"inline tag keeps line, drops span", "markdown",
			"the deadline is @due: friday\nmore\n", "the deadline is \nmore\n"},
		{"mention untouched (no preceding space)", "markdown",
			"email me@example: now\n", "email me@example: now\n"},
		{"def line stripped", "markdown",
			"@tag: priority a measure of urgency\nbody\n", "body\n"},
		{"no tags untouched", "markdown",
			"plain prose here\n", "plain prose here\n"},
		{"consecutive full-line tags", "markdown",
			"@a: 1\n@b: 2\nbody\n", "body\n"},
		{"fenced-code tag is a mention, untouched (markdown)", "markdown",
			"```\n@status: x\n```\n", "```\n@status: x\n```\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(stripArkTags([]byte(tc.in), tc.strategy))
			if got != tc.want {
				t.Errorf("stripArkTags(%q):\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}
