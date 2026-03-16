package ark

// CRC: crc-TagBlock.md | Seq: seq-message.md
// R443, R444, R445, R446, R447, R448, R449, R459, R460, R461, R463, R465, R472, R473, R474, R475, R476, R478

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// tagLineRegex matches a well-formed tag line: @name: value
var tagLineRegex = regexp.MustCompile(`^@([a-zA-Z][\w.-]*): (.*)$`)

// strayTagRegex matches tag-like patterns in the body
var strayTagRegex = regexp.MustCompile(`^@([a-zA-Z][\w.-]*):`)

// strayHeadingTagRegex matches markdown headings that look like tags: ## Status: done
var strayHeadingTagRegex = regexp.MustCompile(`^#{1,6}\s+\w+:`)

// Tag is a name-value pair from a tag block.
type Tag struct {
	Name  string
	Value string
}

// TagBlock represents the parsed tag block at the top of a file.
type TagBlock struct {
	tags       []Tag
	bodyOffset int    // byte offset where body begins
	raw        []byte // original file bytes
}

// ParseTagBlock scans lines from the top of data, collecting @tag: value
// lines until the first non-tag line. A blank line after the tag block
// is consumed as the separator.
func ParseTagBlock(data []byte) *TagBlock {
	tb := &TagBlock{raw: data}
	pos := 0

	for pos < len(data) {
		lineEnd := bytes.IndexByte(data[pos:], '\n')
		var line []byte
		var nextPos int
		if lineEnd < 0 {
			line = data[pos:]
			nextPos = len(data)
		} else {
			line = data[pos : pos+lineEnd]
			nextPos = pos + lineEnd + 1
		}

		m := tagLineRegex.FindSubmatch(line)
		if m == nil {
			break
		}
		tb.tags = append(tb.tags, Tag{
			Name:  string(m[1]),
			Value: string(m[2]),
		})
		pos = nextPos
	}

	// Consume blank separator line if present
	if pos < len(data) {
		lineEnd := bytes.IndexByte(data[pos:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = data[pos:]
		} else {
			line = data[pos : pos+lineEnd]
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if lineEnd < 0 {
				pos = len(data)
			} else {
				pos = pos + lineEnd + 1
			}
		}
	}

	tb.bodyOffset = pos
	return tb
}

// Tags returns the ordered tag list.
func (tb *TagBlock) Tags() []Tag {
	return tb.tags
}

// Get returns the value for a tag and whether it was found.
func (tb *TagBlock) Get(name string) (string, bool) {
	for _, t := range tb.tags {
		if t.Name == name {
			return t.Value, true
		}
	}
	return "", false
}

// Set replaces an existing tag's value or appends a new tag.
func (tb *TagBlock) Set(name, value string) {
	for i, t := range tb.tags {
		if t.Name == name {
			tb.tags[i].Value = value
			return
		}
	}
	tb.tags = append(tb.tags, Tag{Name: name, Value: value})
}

// Body returns the body portion of the original data.
func (tb *TagBlock) Body() []byte {
	if tb.bodyOffset >= len(tb.raw) {
		return nil
	}
	return tb.raw[tb.bodyOffset:]
}

// Render emits the tag block followed by a blank separator and the body.
func (tb *TagBlock) Render() []byte {
	var buf bytes.Buffer
	for _, t := range tb.tags {
		fmt.Fprintf(&buf, "@%s: %s\n", t.Name, t.Value)
	}
	if len(tb.tags) > 0 {
		buf.WriteByte('\n')
	}
	buf.Write(tb.Body())
	return buf.Bytes()
}

// Problem describes a format issue in the file.
type Problem struct {
	Line    int
	Message string
}

func (p Problem) String() string {
	return fmt.Sprintf("line %d: %s", p.Line, p.Message)
}

// Validate checks the file for structural problems in the tag block.
func (tb *TagBlock) Validate() []Problem {
	var problems []Problem
	lines := bytes.Split(tb.raw, []byte("\n"))

	// Scan for blank lines within what should be the tag block,
	// and malformed tag lines before the first non-tag content.
	inBlock := true
	tagCount := 0
	for i, line := range lines {
		lineNum := i + 1
		if !inBlock {
			break
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if tagCount > 0 {
				// Check if there are more tags after this blank line
				for j := i + 1; j < len(lines); j++ {
					if tagLineRegex.Match(lines[j]) {
						problems = append(problems, Problem{
							Line:    lineNum,
							Message: "blank line in tag block — splits tags across chunks, breaking AND searches",
						})
						break
					}
					if len(bytes.TrimSpace(lines[j])) > 0 {
						break
					}
				}
			}
			inBlock = false
			continue
		}
		if tagLineRegex.Match(line) {
			tagCount++
			continue
		}
		// Non-blank, non-tag line — check if it looks like a malformed tag
		if bytes.HasPrefix(trimmed, []byte("@")) && !tagLineRegex.Match(line) {
			// Might be missing space after colon
			if bytes.Contains(trimmed, []byte(":")) {
				problems = append(problems, Problem{
					Line:    lineNum,
					Message: fmt.Sprintf("malformed tag: %q — format must be `@name: value` (space after colon)", string(trimmed)),
				})
			}
		}
		inBlock = false
	}

	// Check for missing blank separator
	if len(tb.tags) > 0 && tb.bodyOffset < len(tb.raw) {
		// Count lines in tag block
		tagEndLine := len(tb.tags)
		if tagEndLine < len(lines) {
			separatorLine := lines[tagEndLine]
			if len(bytes.TrimSpace(separatorLine)) > 0 {
				problems = append(problems, Problem{
					Line:    tagEndLine + 1,
					Message: "missing blank line between tag block and body",
				})
			}
		}
	}

	return problems
}

// ScanBody checks the body for stray tag-like patterns that may be misplaced tags.
func (tb *TagBlock) ScanBody() []Problem {
	var problems []Problem
	body := tb.Body()
	if body == nil {
		return nil
	}

	// Count lines before body to get correct line numbers
	bodyStartLine := 1
	for i := 0; i < tb.bodyOffset && i < len(tb.raw); i++ {
		if tb.raw[i] == '\n' {
			bodyStartLine++
		}
	}

	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		lineNum := bodyStartLine + i
		trimmed := bytes.TrimSpace(line)

		if strayTagRegex.Match(trimmed) {
			problems = append(problems, Problem{
				Line:    lineNum,
				Message: fmt.Sprintf("%q looks like a misplaced tag — tags must be in the tag block at the top of the file", string(trimmed)),
			})
		} else if strayHeadingTagRegex.Match(trimmed) {
			// Extract the "word:" part
			parts := strings.SplitN(string(trimmed), " ", 3)
			if len(parts) >= 2 {
				word := strings.TrimSuffix(parts[1], ":")
				problems = append(problems, Problem{
					Line:    lineNum,
					Message: fmt.Sprintf("%q is a markdown heading, not a tag — use `ark tag set FILE %s %s` instead", string(trimmed), strings.ToLower(word), valueFromHeading(string(trimmed))),
				})
			}
		}
	}

	return problems
}

// CheckHeadings scans the body for markdown headings (## ...) and flags
// any whose first word is not in the allowed list. Case-insensitive.
// CRC: crc-TagBlock.md | R611
func (tb *TagBlock) CheckHeadings(allowed []string) []Problem {
	var problems []Problem
	body := tb.Body()
	if body == nil {
		return nil
	}

	allowSet := make(map[string]bool, len(allowed))
	for _, h := range allowed {
		allowSet[strings.ToLower(h)] = true
	}

	bodyStartLine := 1
	for i := 0; i < tb.bodyOffset && i < len(tb.raw); i++ {
		if tb.raw[i] == '\n' {
			bodyStartLine++
		}
	}

	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("## ")) {
			continue
		}
		// Extract the heading word (first word after ##)
		rest := bytes.TrimSpace(trimmed[3:])
		word := string(rest)
		if idx := bytes.IndexAny(rest, " :\t"); idx >= 0 {
			word = string(rest[:idx])
		}
		if !allowSet[strings.ToLower(word)] {
			lineNum := bodyStartLine + i
			problems = append(problems, Problem{
				Line:    lineNum,
				Message: fmt.Sprintf("unexpected heading %q — allowed: %s", string(trimmed), strings.Join(allowed, ", ")),
			})
		}
	}
	return problems
}

// valueFromHeading extracts the value portion after "## Word: value"
func valueFromHeading(heading string) string {
	idx := strings.Index(heading, ":")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(heading[idx+1:])
}
