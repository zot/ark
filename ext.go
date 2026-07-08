package ark

// CRC: crc-Indexer.md
//
// @ext compound-tag parsing. The storage layer (V/F records, in-memory
// ext map) lives separately; this file is pure parsing.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExtTargetParts is the decomposed shape of an @ext TARGET. See
// specs/at-ext-parsing.md "Target syntax" for the grammar.
//
// CRC: crc-DB.md | R2366, R2367, R2369, R2370, R2371, R2372
type ExtTargetParts struct {
	BaseKind   string // "uuid" or "path"
	BaseValue  string // for "uuid": the UUID value without %; for "path": the absolutized path
	ModifierN  int    // 0 = no modifier; 1+ = [N] or ^ (^ encodes as 1)
	AnchorKind string // "" (bare) | "string" | "regex" | "range"
	AnchorText string // payload (without quotes/slashes); the raw text for range
	Invalid    bool   // true for UUID base + range anchor (resolves to empty)
}

// ParseExtTarget splits an `@ext:` value into (TARGET, []TagValue).
// Format: TARGET `@tag1: v1 @tag2: v2 ...`
// The greedy tagValueRegex captures everything to end of line; this
// function owns the @ext-specific peel of embedded `@tag:` segments
// from that captured value. ExtractTagValues no longer peels — each
// outer tag's owner does its own embedded-tag handling. The name
// ParseExtTarget encodes this @ext-specific semantics; do not
// generalize to a name that suggests a single shared mechanism.
// Returns ok=false when the TARGET is empty or no embedded tag
// follows it — a TARGET-only @ext has nothing to apply.
//
// The TARGET/tag boundary scanner is anchor-aware: if the TARGET
// contains a narrower starting with `"` or `/`, the scanner skips
// over the quoted-string or regex span so an embedded `@tag:`
// inside the anchor is not mistaken for the start of the tag list.
// See specs/at-ext-parsing.md "Target syntax."
// CRC: crc-Indexer.md | R1983, R1984, R2111, R2112, R2365
func ParseExtTarget(value string) (target string, tags []TagValue, ok bool) {
	searchStart := anchorSkip(value)
	first := tagValueRegex.FindStringSubmatchIndex(value[searchStart:])
	if first == nil {
		return "", nil, false
	}
	// Shift indices back into the original value.
	for i := range first {
		first[i] += searchStart
	}
	target = strings.TrimSpace(value[:first[0]])
	if target == "" {
		return "", nil, false
	}
	tags = []TagValue{{Tag: strings.ToLower(value[first[2]:first[3]])}}
	val := value[first[4]:first[5]]
	for {
		sub := tagValueRegex.FindStringSubmatchIndex(val)
		if sub == nil {
			tags[len(tags)-1].Value = strings.TrimSpace(val)
			return target, tags, true
		}
		tags[len(tags)-1].Value = strings.TrimSpace(val[:sub[0]])
		tags = append(tags, TagValue{Tag: strings.ToLower(val[sub[2]:sub[3]])})
		val = val[sub[4]:sub[5]]
	}
}

// ParseExtTargetParts decomposes an @ext TARGET into base /
// modifier / anchor per the grammar in specs/at-ext-parsing.md.
// `sourceDir` is used to absolutize relative path bases. Returns
// (parts, ok). ok=false for empty input.
//
// CRC: crc-DB.md | R2366, R2367, R2368, R2369, R2370, R2371, R2372, R2373, R2374
func ParseExtTargetParts(target, sourceDir string) (ExtTargetParts, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return ExtTargetParts{}, false
	}

	// Locate the narrower colon. The MODIFIER `[N]` / `^` sits between
	// the bare base and the colon, so the first ":" in the target is
	// the narrower start (relative paths shouldn't contain ":" on
	// Linux; Windows paths are out of scope per principles.md).
	var basePart, anchorPart string
	if colonIdx := indexNarrowerColon(target); colonIdx >= 0 {
		basePart = target[:colonIdx]
		anchorPart = target[colonIdx+1:]
	} else {
		basePart = target
	}

	parts := ExtTargetParts{}
	// Peel MODIFIER off the base.
	baseOnly := basePart
	if strings.HasSuffix(baseOnly, "^") {
		baseOnly = baseOnly[:len(baseOnly)-1]
		parts.ModifierN = 1
	} else if open := strings.LastIndexByte(baseOnly, '['); open > 0 && strings.HasSuffix(baseOnly, "]") {
		if n, err := strconv.Atoi(baseOnly[open+1 : len(baseOnly)-1]); err == nil && n >= 1 {
			parts.ModifierN = n
			baseOnly = baseOnly[:open]
		}
	}
	baseOnly = strings.TrimSpace(baseOnly)
	if baseOnly == "" {
		return ExtTargetParts{}, false
	}

	// BASE kind: % sigil → UUID, else → PATH (with \% escape at start
	// disabling the sigil).
	if strings.HasPrefix(baseOnly, `\%`) {
		parts.BaseKind = "path"
		parts.BaseValue = absolutizeExtPath(unescapeExtTarget(baseOnly), sourceDir)
	} else if strings.HasPrefix(baseOnly, "%") {
		parts.BaseKind = "uuid"
		parts.BaseValue = unescapeExtTarget(baseOnly[1:])
	} else {
		parts.BaseKind = "path"
		parts.BaseValue = absolutizeExtPath(unescapeExtTarget(baseOnly), sourceDir)
	}

	// ANCHOR: first non-whitespace char selects the kind.
	anchorPart = strings.TrimSpace(anchorPart)
	if anchorPart == "" {
		return parts, true
	}
	switch anchorPart[0] {
	case '"':
		if end := indexUnescapedByte(anchorPart[1:], '"'); end >= 0 {
			parts.AnchorKind = "string"
			parts.AnchorText = unescapeExtTarget(anchorPart[1 : 1+end])
		}
	case '/':
		if end := indexUnescapedByte(anchorPart[1:], '/'); end >= 0 {
			parts.AnchorKind = "regex"
			parts.AnchorText = unescapeExtTarget(anchorPart[1 : 1+end])
		}
	default:
		if parts.BaseKind == "uuid" {
			// UUIDs reject RANGE_STRING anchors (R2372).
			parts.Invalid = true
		} else {
			parts.AnchorKind = "range"
			parts.AnchorText = anchorPart
		}
	}
	return parts, true
}

// indexNarrowerColon finds the first ":" in target that begins a
// narrower, skipping characters that are part of the MODIFIER. We
// also skip a leading `\` if it precedes a `%` (the `\%` escape).
// Returns -1 if no narrower colon exists.
//
// CRC: crc-DB.md | R2370
func indexNarrowerColon(target string) int {
	return strings.IndexByte(target, ':')
}

// indexUnescapedByte finds the first occurrence of c in s that is
// NOT preceded by an unescaped `\`. Returns -1 if not found.
//
// CRC: crc-DB.md | R2368, R2370
func indexUnescapedByte(s string, c byte) int {
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if s[i] == c {
			return i
		}
		i++
	}
	return -1
}

// unescapeExtTarget strips `\%` escape sequences, leaving the literal
// `%`. Other backslashes pass through verbatim.
//
// CRC: crc-DB.md | R2368
func unescapeExtTarget(s string) string {
	if !strings.Contains(s, `\%`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '%' {
			b.WriteByte('%')
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// absolutizeExtPath turns a PATH base into an absolute filesystem
// path. `~/` expands to $HOME; relative paths join against
// `sourceDir`; absolute paths pass through. Normalization is
// minimal — same rule microfts2 follows for path storage.
//
// CRC: crc-DB.md | R2369, R2374
func absolutizeExtPath(p, sourceDir string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
		return p
	}
	if filepath.IsAbs(p) {
		return p
	}
	if sourceDir == "" {
		return p // best-effort; lookup will likely miss
	}
	return filepath.Join(sourceDir, p)
}

// anchorSkip returns the byte offset to begin the @tag: boundary
// scan from. If the TARGET portion of `value` contains a narrower
// whose anchor is a quoted string (`:"..."`) or regex (`:/.../`),
// the offset points just past the anchor's closing delimiter. For
// targets with no such narrower, the offset is 0.
//
// The rule: the first `:` in the value separates BASE (+ optional
// MODIFIER) from the anchor. If the char immediately after that
// `:` (whitespace skipped) is `"` or `/`, we're in a quoted /
// regex anchor and must skip it; otherwise the anchor is a
// RANGE_STRING or absent and no skip is needed (range strings
// can't contain `@tag:` shape so the regex scanner is safe).
// CRC: crc-Indexer.md | R2365
func anchorSkip(value string) int {
	colon := strings.IndexByte(value, ':')
	if colon < 0 {
		return 0
	}
	i := colon + 1
	n := len(value)
	for i < n && (value[i] == ' ' || value[i] == '\t') {
		i++
	}
	if i >= n {
		return 0
	}
	var closer byte
	switch value[i] {
	case '"':
		closer = '"'
	case '/':
		closer = '/'
	default:
		return 0
	}
	j := i + 1
	for j < n {
		if value[j] == '\\' && j+1 < n {
			j += 2
			continue
		}
		if value[j] == closer {
			return j + 1
		}
		j++
	}
	// Unterminated anchor — fall back to end of value.
	return n
}

// extOp selects the mirror-authoring operation applyExtMirrorEdit
// performs on a mirror file's @ext lines.
type extOp int

const (
	extOpSet    extOp = iota // collapse every (TARGET,tag) value to one new value
	extOpAdd                 // append-if-absent: report an exact (TARGET,tag,value) dup; never mutates
	extOpRemove              // drop (TARGET,tag) spans, optionally filtered by value
)

// mutateExtLine applies op to a single `@ext: TARGET @t1: v1 ...`
// line whose TARGET matches targetSpec byte-for-byte, handling EVERY
// matching `tag` span on the line:
//   - extOpSet: rewrite the first surviving (TARGET,tag) value to
//     value and drop later ones; *setPlaced tracks whether that
//     surviving value has already been written on an earlier line,
//     so the collapse spans the whole file.
//   - extOpRemove: drop matching spans — all of them, or only those
//     whose value equals value when value != "".
//   - extOpAdd: never modifies the line; matched reports an exact
//     (TARGET,tag,value) duplicate.
//
// Non-matching lines pass through unchanged. dropLine is true when
// the line is left with no tags.
// CRC: crc-DB.md | Seq: seq-ext-author.md#1.5 | R2395, R2396, R3047
func mutateExtLine(line, targetSpec, tag, value string, op extOp, setPlaced *bool) (newLine string, dropLine, matched bool) {
	trimmedLeading := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmedLeading, "@ext:") {
		return line, false, false
	}
	leading := line[:len(line)-len(trimmedLeading)]
	valuePart := trimmedLeading[len("@ext:"):]

	target, tags, ok := ParseExtTarget(valuePart)
	if !ok || target != targetSpec {
		return line, false, false
	}
	tagLower := strings.ToLower(tag)
	wantValue := strings.TrimSpace(value)

	if op == extOpAdd {
		// Duplicate scan only — the line is never rewritten.
		for _, tv := range tags {
			if tv.Tag == tagLower && tv.Value == wantValue {
				return line, false, true
			}
		}
		return line, false, false
	}

	kept := tags[:0] // in-place filter of the surviving spans
	for _, tv := range tags {
		if tv.Tag != tagLower {
			kept = append(kept, tv)
			continue
		}
		switch op {
		case extOpSet:
			matched = true
			if !*setPlaced {
				tv.Value = wantValue
				*setPlaced = true
				kept = append(kept, tv)
			}
			// later matches collapse away (dropped)
		case extOpRemove:
			if value == "" || tv.Value == wantValue {
				matched = true // dropped
			} else {
				kept = append(kept, tv) // value filter spares this span
			}
		}
	}
	if !matched {
		return line, false, false
	}
	if len(kept) == 0 {
		return "", true, true
	}

	var sb strings.Builder
	sb.WriteString(leading)
	sb.WriteString("@ext: ")
	sb.WriteString(targetSpec)
	for _, tv := range kept {
		sb.WriteString(" @")
		sb.WriteString(tv.Tag)
		sb.WriteString(": ")
		sb.WriteString(tv.Value)
	}
	return sb.String(), false, true
}

// applyExtMirrorEdit walks every line of a mirror file's bytes,
// applies mutateExtLine under op, and returns the rewritten bytes
// plus a matched flag. For extOpSet/extOpRemove it processes ALL
// matching lines (collapse / remove-all); for extOpAdd it scans for
// an exact duplicate and returns the data unchanged when one is
// found. Callers handle the no-match case (append for set/add, no-op
// for remove).
// CRC: crc-DB.md | Seq: seq-ext-author.md#1.5 | R2395, R2396, R3047
func applyExtMirrorEdit(data []byte, targetSpec, tag, value string, op extOp) (newData []byte, matched bool) {
	if len(data) == 0 {
		return data, false
	}
	setPlaced := false
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		hasNL := j < len(data)
		line := string(data[i:j])

		rewritten, drop, m := mutateExtLine(line, targetSpec, tag, value, op, &setPlaced)
		if m {
			matched = true
			if op == extOpAdd {
				// Exact duplicate found — leave the file untouched.
				return data, true
			}
			if drop {
				i = j
				if hasNL {
					i++
				}
				continue
			}
			line = rewritten
		}
		out = append(out, line...)
		if hasNL {
			out = append(out, '\n')
			i = j + 1
		} else {
			i = j
		}
	}
	return out, matched
}
