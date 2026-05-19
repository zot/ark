package ark

// CRC: crc-TagMatcher.md | R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450, R2451

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// TagNameMode selects how a MatchPredicate compares the tag name.
type TagNameMode int

const (
	NameExact TagNameMode = iota
	NameContains
	NameRegex
)

// TagValueMode selects how a MatchPredicate compares the tag value.
type TagValueMode int

const (
	ValueAny TagValueMode = iota
	ValueExact
	ValueContains
	ValueRegex
)

// MatchPredicate is the parsed form of a `-tag` or `-file-tag` argument.
// CRC: crc-TagMatcher.md | R2442
type MatchPredicate struct {
	NameMode    TagNameMode
	NameStr     string
	NameTokens  []string // lowercased; populated when NameMode == NameContains
	NameRE      *regexp.Regexp
	ValueMode   TagValueMode
	ValueStr    string
	ValueTokens []string // lowercased; populated when ValueMode == ValueContains
	ValueRE     *regexp.Regexp
}

// ParseMatchSyntax parses one `[~]NAME [(=|:|~) VALUE]` argument
// into a MatchPredicate.
// CRC: crc-TagMatcher.md | R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450
func ParseMatchSyntax(arg string) (MatchPredicate, error) {
	var p MatchPredicate
	if arg == "" {
		return p, errors.New("empty tag match argument")
	}

	rest := normalizeTagMatchArg(arg)
	if rest == "" {
		return p, errors.New("empty tag name after normalization")
	}

	switch {
	case strings.HasPrefix(rest, "~"):
		p.NameMode = NameRegex
		rest = rest[1:]
	case strings.HasPrefix(rest, ":"):
		p.NameMode = NameContains
		rest = rest[1:]
	default:
		p.NameMode = NameExact
	}

	name, sep, value := splitNameValue(rest)
	if name == "" {
		return p, errors.New("empty tag name")
	}
	p.NameStr = name
	switch p.NameMode {
	case NameRegex:
		re, err := regexp.Compile("(?i)" + name)
		if err != nil {
			return p, fmt.Errorf("invalid name regex %q: %w", name, err)
		}
		p.NameRE = re
	case NameContains:
		p.NameTokens = strings.Fields(strings.ToLower(name))
		if len(p.NameTokens) == 0 {
			return p, errors.New("empty contains-name tokens")
		}
	}

	switch sep {
	case 0:
		p.ValueMode = ValueAny
	case '=':
		p.ValueMode = ValueExact
		p.ValueStr = value
	case ':':
		if value == "" {
			p.ValueMode = ValueAny // R2448 — degenerate, equivalent to bare name
		} else {
			p.ValueMode = ValueContains
			p.ValueStr = value
			p.ValueTokens = strings.Fields(strings.ToLower(value))
		}
	case '~':
		if value == "" {
			p.ValueMode = ValueAny // R2448 — degenerate, equivalent to bare name
		} else {
			p.ValueMode = ValueRegex
			p.ValueStr = value
			re, err := regexp.Compile(value)
			if err != nil {
				return p, fmt.Errorf("invalid value regex %q: %w", value, err)
			}
			p.ValueRE = re
		}
	}
	return p, nil
}

// normalizeTagMatchArg strips a decorative `@` and trims whitespace.
// The `@` may sit at the very start, immediately after the leading
// regex sigil `~`, or immediately after the leading contains sigil
// `:` — all three forms collapse to their `@`-less equivalents.
// R2449
func normalizeTagMatchArg(arg string) string {
	rest := strings.TrimSpace(arg)
	switch {
	case strings.HasPrefix(rest, "@"):
		rest = rest[1:]
	case strings.HasPrefix(rest, "~@"):
		rest = "~" + rest[2:]
	case strings.HasPrefix(rest, ":@"):
		rest = ":" + rest[2:]
	}
	return rest
}

// splitNameValue finds the first occurrence of `=`, `:`, or `~` in
// rest and splits there. Returns (name, sepByte, value); sepByte is
// 0 when no separator is present.
func splitNameValue(rest string) (string, byte, string) {
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '=', ':', '~':
			return rest[:i], rest[i], rest[i+1:]
		}
	}
	return rest, 0, ""
}

// MatchName reports whether p accepts the given tag name.
// Comparisons are case-insensitive on the assumption that the index
// stores names lowercase.
// CRC: crc-TagMatcher.md | R2443
func (p MatchPredicate) MatchName(name string) bool {
	switch p.NameMode {
	case NameRegex:
		return p.NameRE != nil && p.NameRE.MatchString(name)
	case NameContains:
		lname := strings.ToLower(name)
		for _, tok := range p.NameTokens {
			if !strings.Contains(lname, tok) {
				return false
			}
		}
		return true
	default:
		return strings.EqualFold(p.NameStr, name)
	}
}

// MatchValue reports whether p accepts the given tag value.
// Contains and Regex are case-insensitive. Exact is literal.
// CRC: crc-TagMatcher.md | R2444, R2445, R2446, R2447
func (p MatchPredicate) MatchValue(value string) bool {
	switch p.ValueMode {
	case ValueAny:
		return true
	case ValueExact:
		return p.ValueStr == value
	case ValueContains:
		if len(p.ValueTokens) == 0 {
			return true
		}
		lvalue := strings.ToLower(value)
		for _, tok := range p.ValueTokens {
			if !strings.Contains(lvalue, tok) {
				return false
			}
		}
		return true
	case ValueRegex:
		return p.ValueRE != nil && p.ValueRE.MatchString(value)
	}
	return false
}

// Match reports whether p accepts the (name, value) pair.
// CRC: crc-TagMatcher.md | R2442
func (p MatchPredicate) Match(tv TagValue) bool {
	return p.MatchName(tv.Tag) && p.MatchValue(tv.Value)
}

// Describe renders p as a human-readable string for `-parse` output.
// CRC: crc-TagMatcher.md | R2451
func (p MatchPredicate) Describe() string {
	var nm string
	switch p.NameMode {
	case NameRegex:
		nm = "regex:" + p.NameStr
	case NameContains:
		nm = "contains:" + p.NameStr
	default:
		nm = "exact:" + p.NameStr
	}
	var vm string
	switch p.ValueMode {
	case ValueExact:
		vm = "exact:" + p.ValueStr
	case ValueContains:
		vm = "contains:" + p.ValueStr
	case ValueRegex:
		vm = "regex:" + p.ValueStr
	default:
		vm = "any"
	}
	return nm + " " + vm
}

// Canonical reproduces the predicate as a single sigil-form argument
// suitable for re-parsing. Used to round-trip a predicate through the
// JSON wire shape (servers see the same string the CLI parsed).
// CRC: crc-TagMatcher.md | R2442
func (p MatchPredicate) Canonical() string {
	var b strings.Builder
	switch p.NameMode {
	case NameRegex:
		b.WriteByte('~')
	case NameContains:
		b.WriteByte(':')
	}
	b.WriteString(p.NameStr)
	switch p.ValueMode {
	case ValueExact:
		b.WriteByte('=')
		b.WriteString(p.ValueStr)
	case ValueContains:
		b.WriteByte(':')
		b.WriteString(p.ValueStr)
	case ValueRegex:
		b.WriteByte('~')
		b.WriteString(p.ValueStr)
	}
	return b.String()
}
