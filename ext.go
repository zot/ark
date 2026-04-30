package ark

// CRC: crc-Indexer.md
//
// @ext compound-tag parsing. The storage layer (V/F records, in-memory
// ext map) lives separately; this file is pure parsing.

import (
	"strings"
)

// ParseExtTarget splits an @ext: value into (TARGET, []TagValue).
// Format: TARGET @tag1: v1 @tag2: v2 ...
// The greedy tagValueRegex captures everything to end of line, so
// embedded @tag: patterns are peeled from the captured value the
// same way ExtractTagValues handles compound tags. Returns ok=false
// when the TARGET is empty or no embedded tag follows it — a
// TARGET-only @ext has nothing to apply.
// CRC: crc-Indexer.md | R1983, R1984
func ParseExtTarget(value string) (target string, tags []TagValue, ok bool) {
	first := tagValueRegex.FindStringSubmatchIndex(value)
	if first == nil {
		return "", nil, false
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
