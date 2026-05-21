package ark

// CRC: crc-CLI.md | R2593, R2594, R2595, R2596, R2607

import (
	"strconv"
	"strings"
)

// ConnectionsDoc is the parsed shape of a tmp://connections/<id>.md
// document. Carries every header tag the workshop / CLI care about
// plus every proposal row from the unified ## Proposals body.
// Unknown header tags are kept in Raw for forward compatibility.
type ConnectionsDoc struct {
	Status        string             `json:"status,omitempty"`
	Purpose       string             `json:"purpose,omitempty"`
	Mode          string             `json:"mode,omitempty"`
	RequestID     string             `json:"requestID,omitempty"`
	PinnedChunks  []uint64           `json:"pinnedChunks,omitempty"`
	Started       string             `json:"started,omitempty"`
	Elapsed       int                `json:"elapsed,omitempty"`
	Progress      string             `json:"progress,omitempty"`
	Completed     string             `json:"completed,omitempty"`
	Warning       string             `json:"warning,omitempty"`
	Error         string             `json:"error,omitempty"`
	ProposalCount int                `json:"proposalCount,omitempty"`
	Proposals     []ConnectionsPropo `json:"proposals,omitempty"`
	Raw           map[string]string  `json:"raw,omitempty"`
}

// ConnectionsPropo is one row under ## Proposals. The Kind field drives
// which subset of the typed fields is populated. Untyped fields (Raw)
// preserve unknown @proposal-* lines for forward compatibility.
type ConnectionsPropo struct {
	Kind            string             `json:"kind"`
	Value           string             `json:"value,omitempty"`
	Tag             string             `json:"tag,omitempty"`
	Text            string             `json:"text,omitempty"`
	Score           float64            `json:"score,omitempty"`
	EvidenceChunks  []uint64           `json:"evidenceChunks,omitempty"`
	PerSubstrate    SubstrateScores    `json:"perSubstrate,omitempty"`
	MotivatingFiles []ConnectionsMotiv `json:"motivatingFiles,omitempty"`
	Raw             map[string]string  `json:"raw,omitempty"`
}

// ConnectionsMotiv is one entry in @proposal-motivating-files.
type ConnectionsMotiv struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

// ParseConnectionsDoc decodes a tmp://connections/<id>.md body into
// a ConnectionsDoc. Robust to extra whitespace, missing fields, and
// unknown @proposal-* keys. Lines that don't begin with `@` outside
// the ## Proposals section are ignored.
func ParseConnectionsDoc(body []byte) *ConnectionsDoc {
	doc := &ConnectionsDoc{Raw: map[string]string{}}
	lines := strings.Split(string(body), "\n")

	// First pass: collect header tags until we hit the first `##` heading.
	i := 0
	for ; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.HasPrefix(line, "## ") {
			break
		}
		key, val, ok := parseAtLine(line)
		if !ok {
			continue
		}
		switch key {
		case "connections-status":
			doc.Status = val
		case "purpose":
			doc.Purpose = val
		case "connections-mode":
			doc.Mode = val
		case "connections-request-id":
			doc.RequestID = val
		case "connections-pinned-chunks":
			doc.PinnedChunks = parseUint64List(val)
		case "connections-started":
			doc.Started = val
		case "connections-elapsed":
			n, _ := strconv.Atoi(val)
			doc.Elapsed = n
		case "connections-progress":
			doc.Progress = val
		case "connections-completed":
			doc.Completed = val
		case "connections-warning":
			doc.Warning = val
		case "connections-error":
			doc.Error = val
		case "proposal-count":
			n, _ := strconv.Atoi(val)
			doc.ProposalCount = n
		default:
			doc.Raw[key] = val
		}
	}

	// Skip header section blank lines.
	// Walk forward until we find "## Proposals"; ignore other sections
	// (legacy ## Themes / ## Shared Tag Candidates are tolerated as
	// noise here — `show` and consumers should prefer the unified
	// section for the typed projection).
	inProposals := false
	var cur *ConnectionsPropo
	flush := func() {
		if cur != nil {
			doc.Proposals = append(doc.Proposals, *cur)
		}
		cur = nil
	}
	for ; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			flush()
			inProposals = strings.HasPrefix(line, "## Proposals")
			continue
		}
		if !inProposals {
			continue
		}
		if trimmed == "" {
			continue
		}
		// `- @proposal-kind: foo` starts a new row.
		if strings.HasPrefix(trimmed, "- @") {
			flush()
			cur = &ConnectionsPropo{Raw: map[string]string{}}
			key, val, ok := parseAtLine(strings.TrimPrefix(trimmed, "- "))
			if ok && key == "proposal-kind" {
				cur.Kind = val
			} else if ok {
				cur.Raw[key] = val
			}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := parseAtLine(trimmed)
		if !ok {
			continue
		}
		applyPropField(cur, key, val)
	}
	flush()
	return doc
}

func parseAtLine(line string) (string, string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "@") {
		return "", "", false
	}
	s = strings.TrimPrefix(s, "@")
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(s[:colon])
	val := strings.TrimSpace(s[colon+1:])
	val = strings.Trim(val, `"`)
	return key, val, true
}

func parseUint64List(s string) []uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func applyPropField(p *ConnectionsPropo, key, val string) {
	switch key {
	case "proposal-value":
		p.Value = val
	case "proposal-tag":
		p.Tag = val
	case "proposal-text":
		p.Text = val
	case "proposal-score":
		p.Score, _ = strconv.ParseFloat(val, 64)
	case "proposal-evidence-chunks":
		p.EvidenceChunks = parseUint64List(val)
	case "proposal-evidence-vector-ed":
		p.PerSubstrate.VectorED, _ = strconv.ParseFloat(val, 64)
	case "proposal-evidence-trigram-ed":
		p.PerSubstrate.TrigramED, _ = strconv.ParseFloat(val, 64)
	case "proposal-evidence-vector-ec":
		p.PerSubstrate.VectorEC, _ = strconv.ParseFloat(val, 64)
	case "proposal-evidence-trigram-ec":
		p.PerSubstrate.TrigramEC, _ = strconv.ParseFloat(val, 64)
	case "proposal-motivating-files":
		p.MotivatingFiles = parseMotivList(val)
	default:
		if p.Raw == nil {
			p.Raw = map[string]string{}
		}
		p.Raw[key] = val
	}
}

func parseMotivList(s string) []ConnectionsMotiv {
	parts := strings.Split(s, ",")
	out := make([]ConnectionsMotiv, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		colon := strings.LastIndexByte(p, ':')
		if colon < 0 {
			continue
		}
		path := strings.TrimSpace(p[:colon])
		score, err := strconv.ParseFloat(strings.TrimSpace(p[colon+1:]), 64)
		if err != nil {
			continue
		}
		out = append(out, ConnectionsMotiv{Path: path, Score: score})
	}
	return out
}
