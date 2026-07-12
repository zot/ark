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
// CRC: crc-Indexer.md | R1983, R1984, R2111, R2112, R2365, R3050, R3090
func ParseExtTarget(value string) (target string, tags []TagValue, ok bool) {
	// Peel the leading first-seen date and optional disposition (R3090),
	// then the reserved `insight: "..."` metadata field (R3050), before the
	// TARGET is parsed. The peel order mirrors the on-disk line shape:
	// <date> <disposition> insight: "..." TARGET @tag: value.
	value = stripLeadingDateDisposition(value)
	value = stripLeadingInsight(value)
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

// extInsightField is the reserved `insight` metadata field name (no @
// sigil) — a quoted rationale carried first on an @ext-candidate,
// before the TARGET, excluded from the routed-tag list. (R3050)
const extInsightField = "insight"

// stripLeadingInsight removes a leading reserved `insight: "..."`
// metadata field (no @ sigil) from an @ext-candidate value, returning
// the remainder that begins at the TARGET. The field is recognized only
// when `insight:` is followed by whitespace and then a quoted value —
// so a relative-path base literally named `insight` with a `:"anchor"`
// narrower (no space before the quote) is not mistaken for it. The
// quoted span is skipped via indexUnescapedByte so the rationale may
// contain `@` or `:`. Absent such a leading field, value is returned
// unchanged, so committed `@ext` and `@ext-judgment` values pass through.
// CRC: crc-Indexer.md | R3050
func stripLeadingInsight(value string) string {
	s := strings.TrimLeft(value, " \t")
	kw := extInsightField + ":"
	if !strings.HasPrefix(s, kw) {
		return value
	}
	rest := s[len(kw):]
	trimmed := strings.TrimLeft(rest, " \t")
	if len(trimmed) == len(rest) || len(trimmed) == 0 || trimmed[0] != '"' {
		return value // no space after the colon, or not quoted — a TARGET, not metadata
	}
	if end := indexUnescapedByte(trimmed[1:], '"'); end >= 0 {
		return trimmed[1+end+1:]
	}
	return value // unterminated quote — leave intact (degrade gracefully)
}

// isExtDate reports whether s is exactly a `YYYY-MM-DD` date shape: ten
// characters, ASCII digits with `-` at positions 4 and 7. Shape-only — it
// does not validate month/day ranges. The narrow, exact-length test is what
// bounds the peel so an ordinary TARGET is not mistaken for a leading date.
// CRC: crc-Indexer.md | R3091
func isExtDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i := 0; i < 10; i++ {
		if i == 4 || i == 7 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// peelDate splits an optional leading `YYYY-MM-DD ` first-seen date off
// body, returning the ten-char date and the remainder past its trailing
// space. Returns ("", body) when body does not begin with a date shape.
// CRC: crc-Indexer.md | R3090, R3091
func peelDate(body string) (date, rest string) {
	if len(body) >= 11 && isExtDate(body[:10]) && body[10] == ' ' {
		return body[:10], body[11:]
	}
	return "", body
}

// stripLeadingDateDisposition peels an optional leading `YYYY-MM-DD `
// first-seen date and — only when a date was present — an optional
// `internal `/`external ` disposition off an @ext-family value, returning
// the remainder that begins at the insight peel / TARGET. Called before
// stripLeadingInsight in ParseExtTarget, mirroring the on-disk line order.
// A committed `@ext` (no leading date) and a bare TARGET pass through
// unchanged; an `@ext-judgment` (date, no disposition) yields just the date
// peel. Gating the disposition peel behind a date peel bounds the ambiguity
// of a TARGET literally named `internal`/`external`.
// CRC: crc-Indexer.md | R3090, R3092, R3093
func stripLeadingDateDisposition(value string) string {
	s := strings.TrimLeft(value, " \t")
	date, rest := peelDate(s)
	if date == "" {
		return value // no leading date → committed @ext or dateless legacy line
	}
	for _, disp := range [...]string{extDispositionInternal, extDispositionExternal} {
		if strings.HasPrefix(rest, disp+" ") {
			return rest[len(disp)+1:]
		}
	}
	return rest
}

// peelExtCandidateDisposition returns the disposition token an
// `@ext-candidate` value carries after its leading date (`internal` /
// `external`), or "" when none is present. Where stripLeadingDateDisposition
// discards the disposition for TARGET parsing, this surfaces it for the
// accept branch, which routes each candidate per its own disposition.
// (R3100)
func peelExtCandidateDisposition(value string) string {
	s := strings.TrimLeft(value, " \t")
	date, rest := peelDate(s)
	if date == "" {
		return "" // no leading date → committed/legacy line, no disposition
	}
	for _, disp := range [...]string{extDispositionInternal, extDispositionExternal} {
		if strings.HasPrefix(rest, disp+" ") {
			return disp
		}
	}
	return ""
}

// acceptedCandidate is one `@ext-candidate` span the accept path matched:
// the disposition that decides its resolution and the routed (tag, value).
type acceptedCandidate struct {
	disposition string
	tag         string
	value       string
}

// collectAcceptedCandidates scans mirror data for every `@ext-candidate`
// line whose TARGET equals targetSpec and that carries the accepted (tag,
// value) — value "" matches any value — returning one acceptedCandidate per
// matching routed tag, tagged with the line's disposition. It matches the
// same spans applyExtMirrorEdit(extOpRemove, candidate) consumes, so the
// accept loop can resolve each per its own disposition while the removal
// drops the lines. The reserved `@count` tag is skipped. (R3100)
func collectAcceptedCandidates(data []byte, targetSpec, tag, value string) []acceptedCandidate {
	marker := extClassCandidate.marker()
	tagLower := strings.ToLower(strings.TrimSpace(tag))
	wantValue := strings.TrimSpace(value)
	var out []acceptedCandidate
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, marker+":") {
			continue
		}
		valuePart := trimmed[len(marker)+1:]
		target, tags, ok := ParseExtTarget(valuePart)
		if !ok || target != targetSpec {
			continue
		}
		disp := peelExtCandidateDisposition(valuePart)
		for _, tv := range tags {
			if tv.Tag == extCountField {
				continue
			}
			if tv.Tag == tagLower && (wantValue == "" || tv.Value == wantValue) {
				out = append(out, acceptedCandidate{disposition: disp, tag: tv.Tag, value: tv.Value})
			}
		}
	}
	return out
}

// extCountField is the reserved `count` metadata field carried as a
// `@count: N` routed-position tag on @ext-candidate / @ext-judgment
// lines. Unlike a peel it stays in the tag's value string (so the V
// record mirrors the file faithfully), but the derivation drops it from
// the routed tags and materializes its signed value into the RC tally /
// signed RJ score. (R3074)
const extCountField = "count"

// extractCountField splits the reserved `@count` field out of a parsed
// routed-tag list. It returns the remaining routed tags, the signed
// count, and whether a count was present. A malformed count value is
// treated as absent (routed tag dropped, count 0). @count is reserved
// for the candidate/judgment classes; a committed @ext carrying one has
// it dropped from routing and ignored (hasCount stays usable but the
// caller does not materialize it). (R3074)
// CRC: crc-Indexer.md | R3074
func extractCountField(tags []TagValue) (routed []TagValue, count int64, hasCount bool) {
	routed = tags[:0]
	for _, tv := range tags {
		if tv.Tag == extCountField {
			if n, err := strconv.ParseInt(strings.TrimSpace(tv.Value), 10, 64); err == nil {
				count, hasCount = n, true
			}
			continue
		}
		routed = append(routed, tv)
	}
	return routed, count, hasCount
}

// onlyReservedCountTags reports whether every tag in tags is the reserved
// `@count` field — i.e. removing the real routed tag(s) left only the tally.
// Such a line is a meaningless husk and is dropped whole. (R3074)
func onlyReservedCountTags(tags []TagValue) bool {
	if len(tags) == 0 {
		return false
	}
	for _, tv := range tags {
		if tv.Tag != extCountField {
			return false
		}
	}
	return true
}

// extClassForTag maps an @ext-family outer tag name to its class.
// Non-family tags map to extClassCommitted by convention; callers gate
// on membership before relying on the result. (R3061)
// CRC: crc-Indexer.md | R3061
func extClassForTag(tag string) extClass {
	switch tag {
	case extCandidateTag:
		return extClassCandidate
	case extJudgmentTag:
		return extClassJudgment
	default:
		return extClassCommitted
	}
}

// isExtFamilyTag reports whether tag is one of the three tag-derived
// source classes (@ext / @ext-candidate / @ext-judgment). (R3061)
func isExtFamilyTag(tag string) bool {
	return tag == tagExt || tag == extCandidateTag || tag == extJudgmentTag
}

// extClass selects which @ext-family tag a mirror line carries. Named
// markers replace the hardcoded "@ext:" literal so one line-mutation
// path serves committed routings, proposals, and judgments alike.
// (R3051, R3052)
type extClass int

const (
	extClassCommitted extClass = iota // @ext           — a live routing edge
	extClassCandidate                 // @ext-candidate — a proposed routing
	extClassJudgment                  // @ext-judgment  — a durable judgment
)

// Mirror-line tag names for the @ext-* family (tagExt = "ext" lives in
// store.go). (R3051)
const (
	extCandidateTag = "ext-candidate"
	extJudgmentTag  = "ext-judgment"
)

// @ext-candidate disposition tokens — a bare word carried after the
// first-seen date that names where an accepted tag is written: external
// routes to the target's mirror file (today's behavior); internal writes
// the tag into the target file's own body (the internal-disposition
// feature). The disposition is part of the candidate line's identity, so
// internal and external are distinct proposals with independent @count
// tallies. (R3092)
const (
	extDispositionExternal = "external"
	extDispositionInternal = "internal"
)

// marker returns the class's @-prefixed line marker: "@ext",
// "@ext-candidate", or "@ext-judgment". (R3052)
func (c extClass) marker() string {
	switch c {
	case extClassCandidate:
		return "@" + extCandidateTag
	case extClassJudgment:
		return "@" + extJudgmentTag
	default:
		return "@" + tagExt
	}
}

// extOp selects the mirror-authoring operation applyExtMirrorEdit
// performs on a mirror file's @ext lines.
type extOp int

const (
	extOpSet    extOp = iota // collapse every (TARGET,tag) value to one new value
	extOpAdd                 // append-if-absent: report an exact (TARGET,tag,value) dup; never mutates
	extOpRemove              // drop (TARGET,tag) spans, optionally filtered by value
)

// mutateExtLine applies op to a single mirror line of the given class
// (`@ext:` / `@ext-candidate:` / `@ext-judgment:`) whose TARGET matches
// targetSpec byte-for-byte, handling EVERY matching `tag` span:
//   - extOpSet: rewrite the first surviving (TARGET,tag) value to
//     value and drop later ones; *setPlaced tracks whether that
//     surviving value has already been written on an earlier line,
//     so the collapse spans the whole file.
//   - extOpRemove: drop matching spans — all of them, or only those
//     whose value equals value when value != ""; each dropped span's
//     (tag, value) is appended to *removed when non-nil (the
//     accept/reject transitions read this to re-emit under a new class).
//   - extOpAdd: never modifies the line; matched reports an exact
//     (TARGET,tag,value) duplicate.
//
// Only lines carrying `class`'s marker are considered; other classes and
// non-@ext lines pass through unchanged. dropLine is true when the line
// is left with no tags.
// CRC: crc-DB.md | Seq: seq-ext-author.md#1.5 | R2395, R2396, R3047, R3052, R3054, R3055
func mutateExtLine(line, targetSpec, tag, value string, op extOp, class extClass, setPlaced *bool, removed *[]TagValue) (newLine string, dropLine, matched bool) {
	marker := class.marker()
	trimmedLeading := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmedLeading, marker+":") {
		return line, false, false
	}
	leading := line[:len(line)-len(trimmedLeading)]
	valuePart := trimmedLeading[len(marker)+1:]

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
				if removed != nil {
					*removed = append(*removed, tv)
				}
			} else {
				kept = append(kept, tv) // value filter spares this span
			}
		}
	}
	if !matched {
		return line, false, false
	}
	// When the removal leaves only the reserved `@count` tally (no real routed
	// tag survives), the candidate/judgment line is meaningless — drop it whole
	// rather than rebuild a `@marker: TARGET @count: N` husk, which would also
	// shed the leading date / disposition / insight that ParseExtTarget strips.
	// (R3074: @count rides with its line; consuming the tag consumes the tally.)
	if len(kept) == 0 || onlyReservedCountTags(kept) {
		return "", true, true
	}

	var sb strings.Builder
	sb.WriteString(leading)
	sb.WriteString(marker)
	sb.WriteString(": ")
	sb.WriteString(targetSpec)
	for _, tv := range kept {
		sb.WriteString(" @")
		sb.WriteString(tv.Tag)
		sb.WriteString(": ")
		sb.WriteString(tv.Value)
	}
	return sb.String(), false, true
}

// applyExtMirrorEdit walks every line of a mirror file's bytes, applies
// mutateExtLine under op for the given class, and returns the rewritten
// bytes, a matched flag, and (for extOpRemove) the (tag, value) pairs it
// removed. For extOpSet/extOpRemove it processes ALL matching lines
// (collapse / remove-all); for extOpAdd it scans for an exact duplicate
// and returns the data unchanged when one is found. Callers handle the
// no-match case (append for set/add, no-op for remove).
// CRC: crc-DB.md | Seq: seq-ext-author.md#1.5 | R2395, R2396, R3047, R3052
func applyExtMirrorEdit(data []byte, targetSpec, tag, value string, op extOp, class extClass) (newData []byte, matched bool, removed []TagValue) {
	if len(data) == 0 {
		return data, false, nil
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

		rewritten, drop, m := mutateExtLine(line, targetSpec, tag, value, op, class, &setPlaced, &removed)
		if m {
			matched = true
			if op == extOpAdd {
				// Exact duplicate found — leave the file untouched.
				return data, true, nil
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
	return out, matched, removed
}

// upsertExtLine applies op for (targetSpec, tag, value) on class to
// data, appending a fresh single-tag line of that class when nothing
// matched (set/add). Pure — no file I/O; shared by the file-authoring
// DB methods and the accept/reject transitions. (R3052)
func upsertExtLine(data []byte, targetSpec, tag, value string, op extOp, class extClass) []byte {
	newData, matched, _ := applyExtMirrorEdit(data, targetSpec, tag, value, op, class)
	if !matched && (op == extOpSet || op == extOpAdd) {
		newData = appendExtLine(newData, targetSpec, tag, value, class)
	}
	return newData
}

// appendExtLine appends a single-tag line of the given class. An empty
// value emits a tag-name-only routed tag (`@tag:`), the form
// @ext-judgment uses. (R3051, R3055)
func appendExtLine(data []byte, targetSpec, tag, value string, class extClass) []byte {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	var sb strings.Builder
	sb.WriteString(class.marker())
	sb.WriteString(": ")
	sb.WriteString(targetSpec)
	sb.WriteString(" @")
	sb.WriteString(strings.ToLower(tag))
	sb.WriteString(":")
	if v := strings.TrimSpace(value); v != "" {
		sb.WriteString(" ")
		sb.WriteString(v)
	}
	sb.WriteString("\n")
	return append(data, sb.String()...)
}

// candidateLine builds the DATELESS identity of an @ext-candidate mirror
// line: `@ext-candidate: <disposition> insight: "…" TARGET @tag: value`.
// The disposition (default external) sits right after the marker and is
// part of the identity — internal and external are distinct proposals with
// independent tallies. A non-empty insight is emitted quoted next, before
// the TARGET — with no @ sigil, because it is metadata, not a routed tag —
// so it never collides with an undelimited TARGET, and distinct insights
// make distinct lines (preserved, not collapsed). The first-seen date is
// NOT part of the identity; upsertCountLine reinserts it after the marker
// at append time. (R3051, R3053, R3092)
func candidateLine(targetSpec, tag, value, insight, disposition string) string {
	var sb strings.Builder
	sb.WriteString(extClassCandidate.marker())
	sb.WriteString(": ")
	if d := strings.TrimSpace(disposition); d != "" {
		sb.WriteString(d)
		sb.WriteString(" ")
	}
	if s := strings.TrimSpace(insight); s != "" {
		sb.WriteString(extInsightField)
		sb.WriteString(`: "`)
		sb.WriteString(strings.ReplaceAll(s, `"`, `\"`))
		sb.WriteString(`" `)
	}
	sb.WriteString(targetSpec)
	sb.WriteString(" @")
	sb.WriteString(strings.ToLower(tag))
	sb.WriteString(":")
	if v := strings.TrimSpace(value); v != "" {
		sb.WriteString(" ")
		sb.WriteString(v)
	}
	return sb.String()
}

// mirrorHasLine reports whether data contains line as an exact whole
// line. Used for @ext-candidate duplicate detection (whole-line match,
// so a differing insight is a distinct proposal). (R3053)
func mirrorHasLine(data []byte, line string) bool {
	for _, existing := range strings.Split(string(data), "\n") {
		if existing == line {
			return true
		}
	}
	return false
}

// judgmentIdentity builds the tag-name-only `@ext-judgment: TARGET @tag:`
// line (no value, no `@count`) — the identity a signed `@count` field
// rides on. (R3055, R3075)
func judgmentIdentity(targetSpec, tag string) string {
	return extClassJudgment.marker() + ": " + targetSpec + " @" + strings.ToLower(tag) + ":"
}

// splitMarkerPrefix splits a mirror line at the first ": " — the marker
// boundary, since every @ext-family marker (`@ext:`, `@ext-candidate:`,
// `@ext-judgment:`) is written with a trailing space. Returns the prefix
// through and including that ": ", the remaining body, and ok=false when
// no ": " is present.
// CRC: crc-Indexer.md | R3090
func splitMarkerPrefix(line string) (prefix, body string, ok bool) {
	if idx := strings.Index(line, ": "); idx >= 0 {
		return line[:idx+2], line[idx+2:], true
	}
	return line, "", false
}

// withDate reinserts a first-seen date immediately after identity's marker
// prefix, producing `<marker>: <date> <identity-body>`. An empty date (or a
// prefixless identity) returns identity unchanged, so a dateless legacy
// line round-trips.
// CRC: crc-Indexer.md | R3090
func withDate(identity, date string) string {
	if date == "" {
		return identity
	}
	prefix, body, ok := splitMarkerPrefix(identity)
	if !ok {
		return identity
	}
	return prefix + date + " " + body
}

// bumpCountLine applies a signed delta to the reserved `@count` field of
// the first mirror line whose DATELESS text equals `identity` (a candidate
// or judgment line built without its first-seen date or `@count` suffix).
// Each line's own leading date is peeled before the identity match and
// reinserted on rewrite — the first-seen freeze, so a repeat never restamps
// the date. A bare identity line counts as `@count: 0`, so the first bump
// materializes `delta`. A resulting count of 0 removes the line entirely
// (absent ≡ neutral, R2881). Returns the rewritten bytes and whether a line
// matched; the caller appends a fresh line when nothing matched.
// CRC: crc-Indexer.md | R3074, R3075, R3090, R3091
func bumpCountLine(data []byte, identity string, delta int64) (newData []byte, bumped bool) {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		prefix, body, ok := splitMarkerPrefix(line)
		if !ok {
			continue
		}
		ownDate, datelessBody := peelDate(body)
		dateless := prefix + datelessBody
		var cur int64
		switch {
		case dateless == identity:
			cur = 0
		case strings.HasPrefix(dateless, identity+" @count:"):
			n, err := strconv.ParseInt(strings.TrimSpace(dateless[len(identity)+len(" @count:"):]), 10, 64)
			if err != nil {
				continue
			}
			cur = n
		default:
			continue
		}
		nc := cur + delta
		if nc == 0 {
			lines = append(lines[:i], lines[i+1:]...)
		} else {
			lines[i] = withDate(identity, ownDate) + " @count: " + strconv.FormatInt(nc, 10)
		}
		return []byte(strings.Join(lines, "\n")), true
	}
	return data, false
}

// upsertCountLine bumps `identity`'s signed `@count` by delta when a
// matching line exists, else appends `<marker>: <date> <identity-body>
// @count: <delta>` — the first-seen date is stamped only on this append
// (a later repeat preserves it via bumpCountLine's freeze). An absent line
// starts at 0, so the append materializes exactly delta. The single
// read-modify-write path shared by the candidate tally (`+1` per repeat)
// and the judgment score (`-1` per reject); one closure-actor call keeps
// it lost-update-free (R986). (R3074, R3075, R3090)
func upsertCountLine(data []byte, identity, date string, delta int64) []byte {
	if nd, bumped := bumpCountLine(data, identity, delta); bumped {
		return nd
	}
	line := withDate(identity, date) + " @count: " + strconv.FormatInt(delta, 10)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, line...)
	return append(data, '\n')
}
