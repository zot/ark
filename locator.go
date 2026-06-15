package ark

// SuggestExtLocator runs the three-layer locator algorithm for the
// curation workshop's @ext authoring widget. See
// specs/curation-workshop-primitives.md "mcp.suggestExtLocator" and
// design/seq-suggest-locator.md for the full semantics.
// CRC: crc-DB.md | Seq: seq-suggest-locator.md

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"go.etcd.io/bbolt"
)

// LocatorSuggestion is the workshop's recommended (base, locator) for
// authoring an @ext routing targeting a specific chunk. Returned by
// DB.SuggestExtLocator and bridged to Lua via mcp.suggestExtLocator.
// CRC: crc-DB.md | R2397
type LocatorSuggestion struct {
	Base               string         // "uuid" or "path"
	BaseValue          string         // %UUID or absolute path
	LocatorKind        string         // "string" | "regex" | "absolute" | "bare"
	LocatorText        string         // payload (empty for "bare")
	WithinFileDupCount int            // count of OTHER chunks in target's file sharing the same @id
	CrossFileScope     CrossFileScope // count of chunks/files the (base, locator) would route to
}

// CrossFileScope reports how broadly the resolved (base, locator)
// would route. Surfaced in the workshop's "will route to N chunks in
// M files" readout. CRC: crc-DB.md | R2401
type CrossFileScope struct {
	Chunks int
	Files  int
}

// SuggestExtLocator implements the algorithm described in
// specs/curation-workshop-primitives.md "mcp.suggestExtLocator".
// CRC: crc-DB.md | R2397, R2398, R2399, R2400, R2401
func (db *DB) SuggestExtLocator(chunkID uint64) (LocatorSuggestion, error) {
	info, err := db.ChunkInfo(chunkID)
	if err != nil {
		return LocatorSuggestion{}, err
	}
	allChunks := db.AllChunks(info.Path)
	if len(allChunks) == 0 {
		return LocatorSuggestion{}, fmt.Errorf("no chunks for file %s", info.Path)
	}
	var targetContent string
	var otherContents []string
	for _, c := range allChunks {
		if c.Range == info.Range {
			targetContent = c.Content
			continue
		}
		otherContents = append(otherContents, c.Content)
	}
	if targetContent == "" {
		return LocatorSuggestion{}, fmt.Errorf("target chunk content not retrievable for range %q", info.Range)
	}

	idValues := db.chunkIDValuesSync(chunkID)
	withinFileDups := 0
	if len(idValues) > 0 {
		withinFileDups = db.countWithinFileDups(info.FileID, chunkID, idValues)
	}

	suggestion := LocatorSuggestion{
		WithinFileDupCount: withinFileDups,
	}
	if len(idValues) > 0 {
		suggestion.Base = "uuid"
		suggestion.BaseValue = "%" + idValues[0]
	} else {
		suggestion.Base = "path"
		suggestion.BaseValue = info.Path
	}

	// chunk Range starting with " or / violates the soft chunker
	// contract — :RANGE_STRING locator is unavailable in that case
	// but the chunk still indexes and remains searchable via other
	// locator forms.
	// CRC: crc-DB.md | R2378, R2399
	absoluteAvailable := info.Range != "" && !strings.HasPrefix(info.Range, `"`) && !strings.HasPrefix(info.Range, "/")

	db.pickLocator(&suggestion, info, targetContent, otherContents, idValues, withinFileDups, absoluteAvailable)

	suggestion.CrossFileScope = db.computeCrossFileScope(suggestion, info.Path)
	return suggestion, nil
}

// pickLocator applies the R2400 default-locator selection table.
// Read-only chunks prefer absolute; UUID base + no within-file dups
// gets "bare"; otherwise run layers 1 → 2 → 3 with absolute fallback.
// CRC: crc-DB.md | R2398, R2399, R2400
func (db *DB) pickLocator(suggestion *LocatorSuggestion, info ChunkInfo, targetContent string, otherContents, idValues []string, withinFileDups int, absoluteAvailable bool) {
	if !info.Writable {
		if absoluteAvailable {
			suggestion.LocatorKind = "absolute"
			suggestion.LocatorText = info.Range
			return
		}
		db.fillBestStringLayer(suggestion, targetContent, otherContents)
		return
	}
	if len(idValues) > 0 && withinFileDups == 0 {
		suggestion.LocatorKind = "bare"
		return
	}
	if db.tryLayer1(suggestion, targetContent, otherContents) {
		return
	}
	if db.tryLayer2(suggestion, targetContent, otherContents) {
		return
	}
	if absoluteAvailable {
		suggestion.LocatorKind = "absolute"
		suggestion.LocatorText = info.Range
		return
	}
	db.fillBestStringLayer(suggestion, targetContent, otherContents)
}

// chunkIDValuesSync wraps db.chunkIDValues in an env.View so callers
// outside an existing txn (like SuggestExtLocator) can use it.
// CRC: crc-DB.md | R2400
func (db *DB) chunkIDValuesSync(chunkID uint64) []string {
	var out []string
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		out = db.chunkIDValues(txn, chunkID)
		return nil
	})
	return out
}

// countWithinFileDups counts other chunks in fileID that carry any of
// idValues as an @id value. Used for the WithinFileDupCount field.
// CRC: crc-DB.md | R2397, R2400
func (db *DB) countWithinFileDups(fileID, selfChunkID uint64, idValues []string) int {
	idSet := make(map[string]struct{}, len(idValues))
	for _, v := range idValues {
		idSet[v] = struct{}{}
	}
	chunkIDs := db.ChunkIDsForFile(fileID)
	count := 0
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		for _, cid := range chunkIDs {
			if cid == selfChunkID {
				continue
			}
			for _, v := range db.chunkIDValues(txn, cid) {
				if _, ok := idSet[v]; ok {
					count++
					break
				}
			}
		}
		return nil
	})
	return count
}

// ChunkIDsForFile returns the chunk IDs of fileID in F-record order,
// or nil if the file isn't indexed.
// CRC: crc-DB.md | R2400
func (db *DB) ChunkIDsForFile(fileID uint64) []uint64 {
	info, err := db.fts.FileInfoByID(fileID)
	if err != nil || len(info.Chunks) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(info.Chunks))
	for _, c := range info.Chunks {
		out = append(out, c.ChunkID)
	}
	return out
}

// tokenSpansRegex matches a sequence of word characters OR a single
// non-word, non-whitespace character. Used to tokenize lines for
// Layer 1 — punctuation becomes its own token where it would
// otherwise change prefix uniqueness.
// CRC: crc-DB.md | R2398
var tokenSpansRegex = regexp.MustCompile(`\w+|[^\w\s]`)

// tokenSpans returns (start, end) byte spans of tokens in line.
// CRC: crc-DB.md | R2398
func tokenSpans(line string) [][2]int {
	matches := tokenSpansRegex.FindAllStringIndex(line, -1)
	out := make([][2]int, len(matches))
	for i, m := range matches {
		out[i] = [2]int{m[0], m[1]}
	}
	return out
}

// linePrefixesAtLength returns, for each non-blank line in content, the
// line's length-n token-aligned prefix (in lowercase for the uniqueness
// comparison). Lines with fewer than n tokens are skipped. Blank lines
// are skipped. CRC: crc-DB.md | R2398
func linePrefixesAtLength(content string, n int) []string {
	var prefixes []string
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		spans := tokenSpans(line)
		if len(spans) < n {
			continue
		}
		end := spans[n-1][1]
		prefixes = append(prefixes, strings.ToLower(line[:end]))
	}
	return prefixes
}

// tryLayer1 attempts the line-prefix token-minimum strategy. Returns
// true and fills suggestion.LocatorKind / .LocatorText when a unique
// short prefix exists. CRC: crc-DB.md | R2398
func (db *DB) tryLayer1(suggestion *LocatorSuggestion, targetContent string, otherContents []string) bool {
	type candidate struct {
		text    string
		nTokens int
		lineIdx int
	}
	var best candidate
	bestSet := false

	lines := strings.Split(targetContent, "\n")
	for lineIdx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		spans := tokenSpans(line)
		if len(spans) == 0 {
			continue
		}
		for n := 1; n <= len(spans); n++ {
			end := spans[n-1][1]
			prefix := line[:end]
			lower := strings.ToLower(prefix)
			if !uniqueAcrossOtherChunks(lower, n, otherContents) {
				continue
			}
			if !bestSet || n < best.nTokens || (n == best.nTokens && lineIdx < best.lineIdx) {
				best = candidate{text: prefix, nTokens: n, lineIdx: lineIdx}
				bestSet = true
			}
			break
		}
	}
	if !bestSet {
		return false
	}
	if strings.Contains(best.text, `"`) {
		suggestion.LocatorKind = "regex"
		suggestion.LocatorText = regexp.QuoteMeta(best.text)
	} else {
		suggestion.LocatorKind = "string"
		suggestion.LocatorText = best.text
	}
	return true
}

// uniqueAcrossOtherChunks reports whether none of the other chunks
// have a length-n line-prefix equal to lowerPrefix (case-insensitive).
// CRC: crc-DB.md | R2398
func uniqueAcrossOtherChunks(lowerPrefix string, n int, otherContents []string) bool {
	for _, oc := range otherContents {
		for _, p := range linePrefixesAtLength(oc, n) {
			if p == lowerPrefix {
				return false
			}
		}
	}
	return true
}

// tryLayer2 attempts the rare-trigram-anchored substring strategy.
// Find a 3-byte sequence in targetContent that doesn't appear in any
// otherContents; expand it to word boundaries; clamp 12-60 chars.
// Skip trigrams that span newlines. CRC: crc-DB.md | R2398
func (db *DB) tryLayer2(suggestion *LocatorSuggestion, targetContent string, otherContents []string) bool {
	if len(targetContent) < 3 {
		return false
	}
	otherLower := make([]string, len(otherContents))
	for i, oc := range otherContents {
		otherLower[i] = strings.ToLower(oc)
	}
	targetLower := strings.ToLower(targetContent)
	for i := 0; i+3 <= len(targetLower); i++ {
		tri := targetLower[i : i+3]
		if strings.ContainsAny(tri, "\n") {
			continue
		}
		isUnique := true
		for _, oc := range otherLower {
			if strings.Contains(oc, tri) {
				isUnique = false
				break
			}
		}
		if !isUnique {
			continue
		}
		// Expand to word boundaries around [i, i+3).
		start, end := expandToWordBoundaries(targetContent, i, i+3)
		// Clamp to 12–60 chars.
		span := targetContent[start:end]
		if len(span) < 12 {
			// Try to widen, capped at 60.
			span = widenSpan(targetContent, start, end, 12, 60)
			if len(span) < 12 {
				continue
			}
		} else if len(span) > 60 {
			span = span[:60]
		}
		if strings.Contains(span, `"`) {
			suggestion.LocatorKind = "regex"
			suggestion.LocatorText = regexp.QuoteMeta(span)
		} else {
			suggestion.LocatorKind = "string"
			suggestion.LocatorText = span
		}
		return true
	}
	return false
}

// expandToWordBoundaries widens [start, end) to the nearest enclosing
// non-word boundaries (or content bounds).
// CRC: crc-DB.md | R2398
func expandToWordBoundaries(content string, start, end int) (int, int) {
	for start > 0 && isWordByte(content[start-1]) {
		start--
	}
	for end < len(content) && isWordByte(content[end]) {
		end++
	}
	return start, end
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// widenSpan expands [start, end) outward until len reaches min, but
// not past max. Stops at newline boundaries on either side.
// CRC: crc-DB.md | R2398
func widenSpan(content string, start, end, minLen, maxLen int) string {
	for end-start < minLen {
		expanded := false
		if end < len(content) && content[end] != '\n' && (end-start) < maxLen {
			end++
			expanded = true
		}
		if start > 0 && content[start-1] != '\n' && (end-start) < maxLen {
			start--
			expanded = true
		}
		if !expanded {
			break
		}
	}
	return content[start:end]
}

// fillBestStringLayer is the fallback when neither layer 1, layer 2,
// nor absolute can produce a unique result. Tries layer 1 then layer
// 2, accepting the best non-unique candidate. As a last resort, sets
// "bare". CRC: crc-DB.md | R2399
func (db *DB) fillBestStringLayer(suggestion *LocatorSuggestion, targetContent string, otherContents []string) {
	if db.tryLayer1(suggestion, targetContent, otherContents) {
		return
	}
	if db.tryLayer2(suggestion, targetContent, otherContents) {
		return
	}
	suggestion.LocatorKind = "bare"
	suggestion.LocatorText = ""
}

// computeCrossFileScope runs the same resolver path the @ext indexer
// would, scoped per the base. For UUID bases, scans every chunk with
// the matching @id across all files. For path bases, scope is just
// the file the path resolves to.
// CRC: crc-DB.md | R2401
func (db *DB) computeCrossFileScope(suggestion LocatorSuggestion, targetPath string) CrossFileScope {
	if suggestion.LocatorKind == "" || suggestion.Base == "" {
		return CrossFileScope{}
	}
	target := suggestion.BaseValue
	switch suggestion.LocatorKind {
	case "bare":
		// no narrower
	case "absolute":
		target += ":" + suggestion.LocatorText
	case "string":
		target += `:"` + suggestion.LocatorText + `"`
	case "regex":
		target += ":/" + suggestion.LocatorText + "/"
	}
	chunks := db.ResolveExtTarget(target, filepath.Dir(targetPath))
	files := make(map[uint64]struct{})
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		for _, cid := range chunks {
			if fid, ok := db.chunkFileID(txn, cid); ok {
				files[fid] = struct{}{}
			}
		}
		return nil
	})
	return CrossFileScope{Chunks: len(chunks), Files: len(files)}
}
