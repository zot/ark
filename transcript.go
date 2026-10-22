package ark

// CRC: crc-TranscriptReader.md | R3553

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// The one translator from Claude Code's JSONL transcript format into ark's own
// types. Nothing else in ark decodes a transcript record (R3553): a change to
// Claude Code's format is a change to this file.

// TranscriptKind is what a record is to ark.
type TranscriptKind int

const (
	TurnNone TranscriptKind = iota // the zero value: no record (an unused or malformed line)
	TurnUser
	TurnAssistant
	TurnEnd
)

// String names the kind for diagnostics: user, assistant or turn_end.
func (k TranscriptKind) String() string {
	switch k {
	case TurnUser:
		return recordUser
	case TurnAssistant:
		return recordAssistant
	case TurnEnd:
		return "turn_end"
	}
	return "unknown"
}

// Speaker is who a user turn came from. R3556
type Speaker int

const (
	SpeakerNone Speaker = iota // not a user turn
	SpeakerHuman
	SpeakerAgentReport // another agent's message relayed in: a subagent's hand-back, a coordinator's message
	SpeakerTaskNotification
	SpeakerToolResults
	SpeakerSkill
	SpeakerUnmarked
)

// BlockKind is what a block of a turn is. The reader reports which kind a block
// is, and each consumer decides what to do with thinking and narration.
// CRC: crc-TranscriptReader.md | R3558, R3563
type BlockKind int

const (
	BlockText BlockKind = iota
	BlockThinking
	BlockNarration
	BlockToolCall
	BlockToolResult
	BlockUnknown
)

// TranscriptBlock is one block of a turn, in ark's terms. R3558
type TranscriptBlock struct {
	Kind     BlockKind
	Text     string         // text, thinking, narration and tool-result text
	Tool     string         // tool call: the tool's name
	CallID   string         // tool call: its id; tool result: the call it answers
	Input    map[string]any // tool call: its input
	Command  string         // tool call: Input["command"] when it is a string
	TypeName string         // unknown: the block type the reader did not recognize
}

// TranscriptUsage is an assistant turn's token accounting. R3560
type TranscriptUsage struct {
	Context int // cache-creation + cache-read input tokens: the R2777 value
	Input   int
	Output  int
}

// TranscriptRecord is one transcript line ark uses. Lossy on purpose: it
// carries only what ark uses, and Line leads back to the source.
// CRC: crc-TranscriptReader.md | R3555, R3562
type TranscriptRecord struct {
	Kind      TranscriptKind
	Speaker   Speaker // user turns only
	Sidechain bool
	Time      time.Time // zero when absent
	Cwd       string
	Blocks    []TranscriptBlock
	Usage     *TranscriptUsage // assistant turns only
	Line      int              // 1-based line in its file: the way back to the source (R3562)
}

// Text joins the record's text blocks with a space, skipping blocks that are
// only whitespace: the rule the watermark scan and recall's excerpt were built
// on, kept exactly when they moved onto the reader.
func (r TranscriptRecord) Text() string {
	var parts []string
	for _, b := range r.Blocks {
		if b.Kind == BlockText && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

// Prose is what a person typed in this turn, and whether there was any: a
// person-typed turn with text. A human-origin record that holds only tool
// results has none.
//
// Only the positive marker `origin.kind == "human"` counts. Absence of an origin
// is not a person: tool results and local-command caveats lack one too, and
// injected user records carry other kinds such as "task-notification".
// Anthropic does not publish the format, so this is a conservative allowlist;
// if the marker changes, recall's arming goes quiet, and the -vv
// "turn_duration ignored" log is the tripwire.
// CRC: crc-TranscriptReader.md | R2732, R3009, R3557
func (r TranscriptRecord) Prose() (string, bool) {
	if !r.IsPersonTyped() {
		return "", false
	}
	t := r.Text()
	return t, t != ""
}

// ToolCalls returns the record's tool-call blocks.
func (r TranscriptRecord) ToolCalls() []TranscriptBlock {
	var calls []TranscriptBlock
	for _, b := range r.Blocks {
		if b.Kind == BlockToolCall {
			calls = append(calls, b)
		}
	}
	return calls
}

// IsPersonTyped reports whether the record is something a person typed. R3556
func (r TranscriptRecord) IsPersonTyped() bool {
	return r.Kind == TurnUser && r.Speaker == SpeakerHuman
}

// TranscriptStats counts what a read could not place.
// CRC: crc-TranscriptReader.md | R3565
type TranscriptStats struct {
	UnknownRecords map[string]int // record types ark does not know, by name
	UnknownBlocks  map[string]int // block types ark does not know, by name
	UnknownOrigins map[string]int // user-turn origin kinds ark does not know, by name
	Malformed      int            // lines that are not JSON records
}

func (s *TranscriptStats) unknownRecord(t string) {
	if s.UnknownRecords == nil {
		s.UnknownRecords = map[string]int{}
	}
	s.UnknownRecords[t]++
}

func (s *TranscriptStats) unknownOrigin(k string) {
	if s.UnknownOrigins == nil {
		s.UnknownOrigins = map[string]int{}
	}
	s.UnknownOrigins[k]++
}

func (s *TranscriptStats) unknownBlock(t string) {
	if s.UnknownBlocks == nil {
		s.UnknownBlocks = map[string]int{}
	}
	s.UnknownBlocks[t]++
}

// Record types ark uses, and the ones it knows and drops on purpose (R3554).
// A type in neither set is counted as unknown, so an addition to the format is
// visible (R3565).
const (
	recordUser          = "user"
	recordAssistant     = "assistant"
	recordSystem        = "system"
	subtypeTurnDuration = "turn_duration"
)

// CRC: crc-TranscriptReader.md | R3554
var droppedRecordTypes = map[string]bool{
	"attachment": true, "api-request": true, "api-request-blob": true,
	"api-request-shape": true, "mode": true, "last-prompt": true,
	"atis-latch": true, "ai-title": true, "permission-mode": true,
	"agent-name": true, "bridge-session": true, "queue-operation": true,
	"file-history-snapshot": true, "file-history-delta": true,
	"cost-state": true, "continued-in": true, "progress": true, "summary": true,
	"agent-setting": true, "custom-title": true, "result": true, "started": true,
}

// droppedBlockTypes are block kinds ark knows and does not use (attachments a
// person added). Dropped rather than reported unknown, so the drift counts stay
// at zero until something genuinely new appears (R3565).
var droppedBlockTypes = map[string]bool{"image": true, "document": true}

// origin.kind values. R3556
const (
	originHuman            = "human"
	originPeer             = "peer"        // a subagent's hand-back
	originCoordinator      = "coordinator" // a coordinating session's relayed message
	originTaskNotification = "task-notification"
)

// narrationLabel marks a thinking block as narration inside its signature, a
// base64 blob. Decoded rather than matched as base64 text, so the label is found
// wherever it falls. R3559
var narrationLabel = []byte("narration")

// The wire shapes. Only this file sees them.
type wireRecord struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Cwd         string          `json:"cwd"`
	Origin      *wireOrigin     `json:"origin"`
	Message     json.RawMessage `json:"message"`
}

type wireOrigin struct {
	Kind string `json:"kind"`
}

type wireMessage struct {
	Content json.RawMessage `json:"content"`
	Usage   *wireUsage      `json:"usage"`
}

type wireUsage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// ParseTranscriptLine translates one transcript line. used is false for a
// record ark does not use or a line that is not a record; stats records which.
// stats may be nil. R3554, R3561
func ParseTranscriptLine(line []byte, lineNo int, stats *TranscriptStats) (TranscriptRecord, bool) {
	var w wireRecord
	if err := json.Unmarshal(line, &w); err != nil {
		if stats != nil {
			stats.Malformed++
		}
		return TranscriptRecord{}, false
	}
	rec := TranscriptRecord{Sidechain: w.IsSidechain, Cwd: w.Cwd, Line: lineNo}
	if t, err := time.Parse(time.RFC3339Nano, w.Timestamp); err == nil {
		rec.Time = t
	}
	switch w.Type {
	case recordUser:
		rec.Kind = TurnUser
	case recordAssistant:
		rec.Kind = TurnAssistant
	case recordSystem:
		if w.Subtype != subtypeTurnDuration {
			return TranscriptRecord{}, false
		}
		rec.Kind = TurnEnd
		return rec, true
	default:
		if stats != nil && !droppedRecordTypes[w.Type] {
			stats.unknownRecord(w.Type)
		}
		return TranscriptRecord{}, false
	}
	var msg wireMessage
	if len(w.Message) > 0 {
		if err := json.Unmarshal(w.Message, &msg); err != nil {
			if stats != nil {
				stats.Malformed++
			}
			return TranscriptRecord{}, false
		}
	}
	rec.Blocks = translateContent(msg.Content, stats)
	if rec.Kind == TurnAssistant && msg.Usage != nil {
		u := msg.Usage
		rec.Usage = &TranscriptUsage{Context: u.CacheCreation + u.CacheRead, Input: u.Input, Output: u.Output}
	}
	if rec.Kind == TurnUser {
		rec.Speaker = speakerOf(w, rec.Blocks, stats)
	}
	return rec, true
}

// speakerOf decides a user turn's speaker. Origin first: every agent report
// also carries isMeta, so reading isMeta first would mistake it for a skill
// (measured 2026-10-22). An origin kind the reader does not know is counted and
// falls through to the content rules. R3556, R3565
// CRC: crc-TranscriptReader.md | R3556, R3565
func speakerOf(w wireRecord, blocks []TranscriptBlock, stats *TranscriptStats) Speaker {
	if w.Origin != nil {
		switch w.Origin.Kind {
		case originHuman:
			return SpeakerHuman
		case originPeer, originCoordinator:
			return SpeakerAgentReport
		case originTaskNotification:
			return SpeakerTaskNotification
		case "":
		default:
			if stats != nil {
				stats.unknownOrigin(w.Origin.Kind)
			}
		}
	}
	for _, b := range blocks {
		if b.Kind == BlockToolResult {
			return SpeakerToolResults
		}
	}
	if w.IsMeta {
		return SpeakerSkill
	}
	return SpeakerUnmarked
}

// translateContent turns a message's content into blocks: a string is one text
// block, a list is one block per element. R3558
// CRC: crc-TranscriptReader.md | R3558
func translateContent(raw json.RawMessage, stats *TranscriptStats) []TranscriptBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return []TranscriptBlock{{Kind: BlockText, Text: s}}
	}
	var wbs []wireBlock
	if json.Unmarshal(raw, &wbs) != nil {
		return nil
	}
	blocks := make([]TranscriptBlock, 0, len(wbs))
	for _, wb := range wbs {
		if droppedBlockTypes[wb.Type] {
			continue
		}
		blocks = append(blocks, translateBlock(wb, stats))
	}
	return blocks
}

func translateBlock(wb wireBlock, stats *TranscriptStats) TranscriptBlock {
	switch wb.Type {
	case "text":
		return TranscriptBlock{Kind: BlockText, Text: wb.Text}
	case "thinking":
		if isNarration(wb.Signature) {
			return TranscriptBlock{Kind: BlockNarration, Text: wb.Thinking}
		}
		return TranscriptBlock{Kind: BlockThinking, Text: wb.Thinking}
	case "tool_use":
		cmd, _ := wb.Input["command"].(string)
		return TranscriptBlock{Kind: BlockToolCall, Tool: wb.Name, CallID: wb.ID, Input: wb.Input, Command: cmd}
	case "tool_result":
		return TranscriptBlock{Kind: BlockToolResult, CallID: wb.ToolUseID, Text: toolResultText(wb.Content)}
	default:
		if stats != nil {
			stats.unknownBlock(wb.Type)
		}
		return TranscriptBlock{Kind: BlockUnknown, TypeName: wb.Type}
	}
}

// isNarration reports whether a thinking block's signature carries the
// narration label. R3559
// CRC: crc-TranscriptReader.md | R3559
func isNarration(sig string) bool {
	if sig == "" {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(sig, "="))
		if err != nil {
			return false
		}
	}
	return bytes.Contains(data, narrationLabel)
}

// toolResultText flattens a tool result's content, a string or a list of text
// blocks, to its text.
func toolResultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// transcriptLineMax bounds one transcript line; a large tool result can be
// megabytes.
const transcriptLineMax = 64 << 20

// ReadTranscriptData sends every used record in data to fn, in order; fn returns
// false to stop. Bad and unused lines are skipped and counted. R3561
func ReadTranscriptData(data []byte, fn func(TranscriptRecord) bool) TranscriptStats {
	var stats TranscriptStats
	lineNo := 0
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		lineNo++
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if rec, ok := ParseTranscriptLine(line, lineNo, &stats); ok && !fn(rec) {
			break
		}
	}
	return stats
}

// ReadTranscript sends every used record of the file at path to fn, in order;
// fn returns false to stop. The error is only the file's own. R3561
func ReadTranscript(path string, fn func(TranscriptRecord) bool) (TranscriptStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return TranscriptStats{}, err
	}
	defer f.Close()
	return ReadTranscriptStream(f, fn)
}

// ReadTranscriptStream is ReadTranscript over any reader: a file, or a window
// of a live transcript. The error is only the stream's own. R3561
// CRC: crc-TranscriptReader.md | R3561
func ReadTranscriptStream(r io.Reader, fn func(TranscriptRecord) bool) (TranscriptStats, error) {
	var stats TranscriptStats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), transcriptLineMax)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if rec, ok := ParseTranscriptLine(line, lineNo, &stats); ok && !fn(rec) {
			break
		}
	}
	return stats, sc.Err()
}
