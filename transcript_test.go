package ark

// CRC: crc-TranscriptReader.md | Test: test-TranscriptReader.md

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Lines in the shapes Claude Code writes, measured 2026-10-22.
var (
	narrationSig = base64.StdEncoding.EncodeToString([]byte("\x12\x09narration\x1a\x04xyzw"))
	thinkingSig  = base64.StdEncoding.EncodeToString([]byte("\x12\x08thinking\x1a\x04xyzw"))

	lineAssistant = `{"type":"assistant","timestamp":"2026-10-22T10:00:00.123Z","cwd":"/w/ark","isSidechain":false,"message":{"role":"assistant","content":[` +
		`{"type":"thinking","thinking":"weighing it","signature":"` + thinkingSig + `"},` +
		`{"type":"thinking","thinking":"Both searches are running now","signature":"` + narrationSig + `"},` +
		`{"type":"text","text":"Here is the answer."},` +
		`{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"~/.ark/ark status","description":"x"}}],` +
		`"usage":{"input_tokens":3,"output_tokens":40,"cache_creation_input_tokens":1000,"cache_read_input_tokens":24000}}}`
	lineToolResult = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"ok"}]}]}}`
	lineHuman      = `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"hello there"}}`
	linePeer       = `{"type":"user","isMeta":true,"origin":{"kind":"peer","from":"a1"},"message":{"role":"user","content":"Another Claude session sent a message"}}`
	lineTask       = `{"type":"user","origin":{"kind":"task-notification"},"message":{"role":"user","content":"task done"}}`
	lineCoord      = `{"type":"user","isMeta":true,"origin":{"kind":"coordinator"},"message":{"role":"user","content":"The coordinator sent a message"}}`
	lineNewOrigin  = `{"type":"user","origin":{"kind":"brand-new-origin"},"message":{"role":"user","content":"x"}}`
	lineSkill      = `{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill"}]}}`
	lineUnmarked   = `{"type":"user","message":{"role":"user","content":"an injected note"}}`
	lineTurnEnd    = `{"type":"system","subtype":"turn_duration","timestamp":"2026-10-22T10:00:05Z"}`
	lineSystemInfo = `{"type":"system","subtype":"informational"}`
	lineAttachment = `{"type":"attachment","attachment":{}}`
)

func parse(t *testing.T, line string) (TranscriptRecord, bool, TranscriptStats) {
	t.Helper()
	var stats TranscriptStats
	rec, ok := ParseTranscriptLine([]byte(line), 7, &stats)
	return rec, ok, stats
}

// Refs: R3554, R3555, R3558, R3560
func TestTranscriptRecordsTranslate(t *testing.T) {
	rec, ok, _ := parse(t, lineAssistant)
	if !ok || rec.Kind != TurnAssistant {
		t.Fatalf("assistant line: ok=%v kind=%v", ok, rec.Kind)
	}
	if rec.Line != 7 || rec.Cwd != "/w/ark" || rec.Time.IsZero() {
		t.Errorf("line/cwd/time = %d %q %v", rec.Line, rec.Cwd, rec.Time)
	}
	kinds := []BlockKind{}
	for _, b := range rec.Blocks {
		kinds = append(kinds, b.Kind)
	}
	want := []BlockKind{BlockThinking, BlockNarration, BlockText, BlockToolCall}
	if len(kinds) != len(want) {
		t.Fatalf("block kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("block %d kind = %v, want %v", i, kinds[i], want[i])
		}
	}
	if rec.Text() != "Here is the answer." {
		t.Errorf("Text() = %q", rec.Text())
	}
	calls := rec.ToolCalls()
	if len(calls) != 1 || calls[0].Tool != "Bash" || calls[0].CallID != "toolu_1" || calls[0].Command != "~/.ark/ark status" {
		t.Errorf("tool call = %+v", calls)
	}
	if rec.Usage == nil || rec.Usage.Context != 25000 || rec.Usage.Input != 3 || rec.Usage.Output != 40 {
		t.Errorf("usage = %+v, want context 25000 (cache creation + read)", rec.Usage)
	}

	res, ok, _ := parse(t, lineToolResult)
	if !ok || len(res.Blocks) != 1 || res.Blocks[0].Kind != BlockToolResult || res.Blocks[0].CallID != "toolu_1" || res.Blocks[0].Text != "ok" {
		t.Errorf("tool result = %+v ok=%v", res.Blocks, ok)
	}
	if end, ok, _ := parse(t, lineTurnEnd); !ok || end.Kind != TurnEnd {
		t.Errorf("turn_duration: ok=%v kind=%v", ok, end.Kind)
	}
	for _, l := range []string{lineSystemInfo, lineAttachment} {
		if _, ok, st := parse(t, l); ok || len(st.UnknownRecords) != 0 {
			t.Errorf("%s: used=%v, unknown=%v; want dropped, not unknown", l[:30], ok, st.UnknownRecords)
		}
	}
}

// Refs: R3556
func TestTranscriptSpeakerPrecedence(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Speaker
	}{
		{"human", lineHuman, SpeakerHuman},
		{"peer with isMeta is an agent report", linePeer, SpeakerAgentReport},
		{"coordinator with isMeta is an agent report", lineCoord, SpeakerAgentReport},
		{"unknown origin falls through to the content rules", lineNewOrigin, SpeakerUnmarked},
		{"task notification", lineTask, SpeakerTaskNotification},
		{"tool results", lineToolResult, SpeakerToolResults},
		{"isMeta with no origin is a skill", lineSkill, SpeakerSkill},
		{"no origin, no isMeta", lineUnmarked, SpeakerUnmarked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, ok, _ := parse(t, tc.line)
			if !ok || rec.Speaker != tc.want {
				t.Errorf("speaker = %v (ok=%v), want %v", rec.Speaker, ok, tc.want)
			}
			if got := rec.IsPersonTyped(); got != (tc.want == SpeakerHuman) {
				t.Errorf("IsPersonTyped = %v", got)
			}
		})
	}
}

// Refs: R3565
func TestTranscriptCountsUnknownOrigins(t *testing.T) {
	if _, _, st := parse(t, lineNewOrigin); st.UnknownOrigins["brand-new-origin"] != 1 {
		t.Errorf("unknown origins = %v, want brand-new-origin counted", st.UnknownOrigins)
	}
	for _, l := range []string{lineHuman, linePeer, lineCoord, lineTask, lineUnmarked} {
		if _, _, st := parse(t, l); len(st.UnknownOrigins) != 0 {
			t.Errorf("a known origin was counted as unknown: %v", st.UnknownOrigins)
		}
	}
}

// Refs: R3559
func TestTranscriptNarrationIsItsOwnKind(t *testing.T) {
	rec, _, _ := parse(t, lineAssistant)
	if rec.Blocks[1].Kind != BlockNarration || rec.Blocks[1].Text != "Both searches are running now" {
		t.Errorf("narration block = %+v", rec.Blocks[1])
	}
	if rec.Blocks[0].Kind != BlockThinking {
		t.Errorf("thinking block read as %v", rec.Blocks[0].Kind)
	}
	if strings.Contains(rec.Text(), "Both searches") {
		t.Errorf("narration leaked into Text(): %q", rec.Text())
	}
	// The label is found wherever it falls in the encoding.
	for pad := 0; pad < 3; pad++ {
		sig := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", pad) + "\x12\x09narration"))
		if !isNarration(sig) {
			t.Errorf("label at offset %d not found", pad)
		}
	}
}

// Refs: R3561, R3565
func TestTranscriptReadSurvivesBadLinesAndCounts(t *testing.T) {
	unknownBlock := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_addition","x":1},{"type":"text","text":"after"}]}}`
	body := strings.Join([]string{
		lineHuman,
		`{not json`,
		`{"type":"brand-new-record"}`,
		unknownBlock,
		"",
		lineTurnEnd,
	}, "\n") + "\n"
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func(func(TranscriptRecord) bool) TranscriptStats{
		"file": func(fn func(TranscriptRecord) bool) TranscriptStats {
			st, err := ReadTranscript(path, fn)
			if err != nil {
				t.Fatal(err)
			}
			return st
		},
		"data": func(fn func(TranscriptRecord) bool) TranscriptStats { return ReadTranscriptData([]byte(body), fn) },
	} {
		t.Run(name, func(t *testing.T) {
			var got []TranscriptRecord
			st := read(func(r TranscriptRecord) bool { got = append(got, r); return true })
			if len(got) != 3 || got[0].Kind != TurnUser || got[1].Kind != TurnAssistant || got[2].Kind != TurnEnd {
				t.Fatalf("records = %d %+v", len(got), got)
			}
			if got[1].Line != 4 || got[2].Line != 6 {
				t.Errorf("line numbers = %d, %d; want 4, 6", got[1].Line, got[2].Line)
			}
			if b := got[1].Blocks[0]; b.Kind != BlockUnknown || b.TypeName != "tool_addition" {
				t.Errorf("unknown block = %+v", b)
			}
			if st.Malformed != 1 || st.UnknownRecords["brand-new-record"] != 1 || st.UnknownBlocks["tool_addition"] != 1 {
				t.Errorf("stats = %+v", st)
			}
		})
	}
}

// Run on request against the real transcripts: ARK_TRANSCRIPT_CORPUS=<dir>
// (e.g. ~/.claude/projects). Reads every .jsonl under it and reports what the
// reader could not place. Fails only if a file cannot be read. Stage 4's parity
// test for the chunker's fast path grows from this. Refs: R3561, R3565
func TestTranscriptCorpus(t *testing.T) {
	root := os.Getenv("ARK_TRANSCRIPT_CORPUS")
	if root == "" {
		t.Skip("set ARK_TRANSCRIPT_CORPUS to run against real transcripts")
	}
	var files, records, narration, thinking int
	speakers := map[Speaker]int{}
	total := TranscriptStats{UnknownRecords: map[string]int{}, UnknownBlocks: map[string]int{}, UnknownOrigins: map[string]int{}}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return err
		}
		files++
		st, rerr := ReadTranscript(p, func(r TranscriptRecord) bool {
			records++
			if r.Kind == TurnUser {
				speakers[r.Speaker]++
			}
			for _, b := range r.Blocks {
				switch b.Kind {
				case BlockNarration:
					narration++
				case BlockThinking:
					thinking++
				}
			}
			return true
		})
		if rerr != nil {
			t.Errorf("%s: %v", p, rerr)
		}
		total.Malformed += st.Malformed
		for k, v := range st.UnknownRecords {
			total.UnknownRecords[k] += v
		}
		for k, v := range st.UnknownBlocks {
			total.UnknownBlocks[k] += v
		}
		for k, v := range st.UnknownOrigins {
			total.UnknownOrigins[k] += v
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("files %d, records used %d, malformed lines %d", files, records, total.Malformed)
	t.Logf("speakers %v (1 human, 2 agent report, 3 task, 4 tool results, 5 skill, 6 unmarked)", speakers)
	t.Logf("narration %d, thinking %d", narration, thinking)
	t.Logf("unknown record types %v", total.UnknownRecords)
	t.Logf("unknown block kinds %v", total.UnknownBlocks)
	t.Logf("unknown origin kinds %v", total.UnknownOrigins)
}

// transcriptFieldNames are field names only Claude Code's transcript format
// uses. Generic names such as "message" are left out on purpose: a check that
// fires on unrelated code gets ignored.
var transcriptFieldNames = []string{
	`"isSidechain"`, `"tool_use"`, `"tool_result"`, `"turn_duration"`,
	`cache_read_input_tokens`, `json:"isMeta"`, `json:"origin"`,
}

// codeHits reports the lines of a Go file, comments stripped, that name a
// transcript field.
func codeHits(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for i, line := range strings.Split(string(data), "\n") {
		code := line
		if j := strings.Index(code, "//"); j >= 0 {
			code = code[:j]
		}
		for _, name := range transcriptFieldNames {
			if strings.Contains(code, name) {
				hits = append(hits, path+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				break
			}
		}
	}
	return hits
}

// R3553: nothing but transcript.go decodes a Claude Code transcript. The check
// first proves it can see the field names, in transcript.go itself, so a broken
// scan fails instead of passing on nothing.
// Refs: R3553
func TestNothingElseDecodesTranscripts(t *testing.T) {
	if len(codeHits(t, "transcript.go")) == 0 {
		t.Fatal("the scan found no transcript field names in transcript.go; it cannot see what it is looking for")
	}
	files, _ := filepath.Glob("*.go")
	more, _ := filepath.Glob("cmd/ark/*.go")
	files = append(files, more...)
	if len(files) < 20 {
		t.Fatalf("only %d Go files found; the scan is not looking where the code is", len(files))
	}
	for _, f := range files {
		if f == "transcript.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, h := range codeHits(t, f) {
			t.Errorf("decodes Claude Code's transcript format outside the reader: %s", h)
		}
	}
}
