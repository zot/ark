package ark

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Chat transcripts: the shared substrate behind `ark chats`, `ark chats list`,
// and the mcp: Lua bindings. It lives in the ark package rather than in the CLI
// so both clients render through the *same* code and their output cannot drift
// (R3291) — which is the whole reason the UI gets a binding instead of a
// reimplementation in Lua.

// chatLabelMax is how wide a session label is allowed to be before it is
// truncated. Long enough to tell two mornings apart, short enough to stay one
// terminal row alongside the date and turn count.
const chatLabelMax = 80

// chatListDefaultLimit caps `ark chats list` rows when the caller names no
// limit — newest first, so the default is "the ones I might still mean".
const chatListDefaultLimit = 20

// ChatStrategy is the chunker strategy chat JSONL files are indexed under.
// Enumeration keys on it, so a chat source is one holding files with this
// strategy rather than one matched by directory name.
const ChatStrategy = "chat-jsonl"

// ChatRenderOptions controls what a transcript shows.
// CRC: crc-CLI.md | R1045, R1047, R1049, R3035
type ChatRenderOptions struct {
	WithTools    bool // ⚙ tool calls
	WithThinking bool // ✻ chain-of-thought (R3035)
	Sidechain    bool // subagent traffic, filtered out by default
	LineLength   int  // word-wrap width; <=0 means DefaultChatLineLength
}

// DefaultChatLineLength is the word-wrap width when none is given.
const DefaultChatLineLength = 100

func (o ChatRenderOptions) lineLength() int {
	if o.LineLength <= 0 {
		return DefaultChatLineLength
	}
	return o.LineLength
}

// RenderChatFile writes a human-readable transcript of one JSONL file.
// CRC: crc-CLI.md | R1045, R1047, R1049, R3035
func RenderChatFile(w io.Writer, path string, opts ChatRenderOptions) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return RenderChatLines(w, f, opts)
}

// RenderChatLines renders a stream of JSONL records as a transcript — the same
// per-record rendering as RenderChatFile, factored out so a non-file source
// (the `ark luhmann send` reply window) renders identically.
// CRC: crc-CLI.md | R1045, R1047, R1049, R3035 — ❯ user / ● assistant markers,
// WithTools shows ⚙ tool calls, WithThinking shows ✻ chain-of-thought,
// sidechain (subagent) records filtered out unless requested
func RenderChatLines(w io.Writer, r io.Reader, opts ChatRenderOptions) error {
	lineLen := opts.lineLength()
	_, err := ReadTranscriptStream(r, func(rec TranscriptRecord) bool { // R3553
		// Skip sidechains (subagent traffic)
		if rec.Sidechain && !opts.Sidechain {
			return true
		}
		switch rec.Kind {
		case TurnUser:
			if text := chatText(rec); text != "" {
				writeWrapped(w, "❯", text, lineLen)
				fmt.Fprintln(w)
			}
		case TurnAssistant:
			for _, b := range rec.Blocks {
				switch b.Kind {
				case BlockText:
					if b.Text != "" {
						writeWrapped(w, "●", b.Text, lineLen)
						fmt.Fprintln(w)
					}
				case BlockThinking, BlockNarration:
					// R3035: chain-of-thought, off by default (verbose). The
					// corpus already indexes it; this restores display parity.
					// Narration renders as it always did, as thinking; whether
					// it should show differently is bloodhound-fix Item 2's.
					if opts.WithThinking && b.Text != "" {
						writeWrapped(w, "✻", b.Text, lineLen)
						fmt.Fprintln(w)
					}
				case BlockToolCall:
					if opts.WithTools {
						fmt.Fprintf(w, "  ⚙ %s %s\n", b.Tool, summarizeToolInput(b.Input))
					}
				}
			}
		}
		return true
	})
	return err
}

// chatText is a user turn's text as a transcript shows it: its text blocks one
// per line, trimmed.
func chatText(rec TranscriptRecord) string {
	var parts []string
	for _, b := range rec.Blocks {
		if b.Kind == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// writeWrapped writes text with a marker on the first line and 2-space indent
// on continuation lines, word-wrapped at lineLen.
// CRC: crc-CLI.md | R1046 — word-wrap at --line-length (default 100)
func writeWrapped(w io.Writer, marker, text string, lineLen int) {
	indent := "  " // continuation indent (matches marker + space width)
	prefix := marker + " "

	paragraphs := strings.Split(text, "\n")
	first := true
	for _, para := range paragraphs {
		words := strings.Fields(para)
		if len(words) == 0 {
			fmt.Fprintln(w)
			continue
		}

		var line string
		if first {
			line = prefix
			first = false
		} else {
			line = indent
		}

		for _, word := range words {
			if len(line)+len(word)+1 > lineLen && line != prefix && line != indent {
				fmt.Fprintln(w, line)
				line = indent + word
			} else if line == prefix || line == indent {
				line += word
			} else {
				line += " " + word
			}
		}
		if line != "" {
			fmt.Fprintln(w, line)
		}
	}
}

// summarizeToolInput returns a brief summary of tool input for display.
func summarizeToolInput(m map[string]any) string {
	// Show the most useful field
	for _, key := range []string{"command", "file_path", "pattern", "path", "prompt", "description", "query", "skill", "subject"} {
		if v, ok := m[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	}
	return ""
}

// ChatSession is one row of `ark chats list` — a conversation identified by
// something a human can read rather than by its UUID.
// CRC: crc-CLI.md | R3284
type ChatSession struct {
	Label    string    `json:"label"`    // first user turn, truncated
	Path     string    `json:"path"`     // what `ark chats` takes
	Modified time.Time `json:"modified"` // newest first
	Turns    int       `json:"turns"`    // user+assistant messages
	Source   string    `json:"source"`   // name of the chat source it came from
}

// ChatSessionOptions narrows and caps a session listing.
// CRC: crc-CLI.md | R3287
type ChatSessionOptions struct {
	Source string // restrict to one chat source (name or dir)
	For    string // restrict to chat sources logging this code source
	Limit  int    // cap rows; <=0 means chatListDefaultLimit
}

func (o ChatSessionOptions) limit() int {
	if o.Limit <= 0 {
		return chatListDefaultLimit
	}
	return o.Limit
}

// ChatSessions lists indexed chat sessions, newest first.
//
// Sessions come from the index, not a directory walk (R3285): chat dirs are
// already ark sources, so this is a files-in-a-source query. A JSONL on disk
// but unindexed does not appear — `ark chats PATH` still renders it (R3242).
//
// The read is ordered deliberately. Enumeration and mtime are cheap (index scan
// + stat), so the listing is sorted and **truncated to the limit first**; only
// the surviving rows are opened for their label and turn count. R3286 makes the
// label a bounded head-read, but a turn count is not bounded — it needs the
// whole file — so capping before reading is what keeps the verb cheap over a
// corpus of thousands of sessions.
// CRC: crc-CLI.md | R3284, R3285, R3286, R3287
func (db *DB) ChatSessions(opts ChatSessionOptions) ([]ChatSession, error) {
	dirs, err := db.chatSourceDirs(opts)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, nil
	}

	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}

	sessions := make([]ChatSession, 0, len(statuses))
	for _, st := range statuses {
		if st.Strategy != ChatStrategy {
			continue
		}
		name, ok := chatSourceOf(st.Path, dirs)
		if !ok {
			continue
		}
		info, err := os.Stat(st.Path)
		if err != nil {
			continue // indexed but gone from disk; `ark files` reports it
		}
		sessions = append(sessions, ChatSession{
			Path:     st.Path,
			Modified: info.ModTime(),
			Source:   name,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Modified.Equal(sessions[j].Modified) {
			return sessions[i].Path < sessions[j].Path
		}
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	if n := opts.limit(); len(sessions) > n {
		sessions = sessions[:n]
	}

	for i := range sessions {
		label, turns := readChatLabel(sessions[i].Path)
		sessions[i].Label, sessions[i].Turns = label, turns
	}
	return sessions, nil
}

// chatSourceDirs resolves the options into the set of source dirs to enumerate,
// keyed by dir with the source's name as the value. An empty result means no
// source qualified, which the caller reports as an empty listing rather than an
// error — except when a named source does not exist, which is a caller mistake.
// CRC: crc-CLI.md | R3287, R3288
func (db *DB) chatSourceDirs(opts ChatSessionOptions) (map[string]string, error) {
	cfg := db.config
	dirs := make(map[string]string)

	switch {
	case opts.Source != "":
		src, err := cfg.ResolveSourceName(opts.Source)
		if err != nil {
			return nil, err
		}
		dirs[src.Dir] = SourceName(src.Dir)
	case opts.For != "":
		// R3288: the pairing is a declared field, resolved by name — no
		// path mangling enters this path (R3289).
		srcs, err := cfg.SourcesLoggingFor(opts.For)
		if err != nil {
			return nil, err
		}
		for _, src := range srcs {
			dirs[src.Dir] = SourceName(src.Dir)
		}
	default:
		for _, src := range cfg.Sources {
			if !IsGlob(src.Dir) {
				dirs[src.Dir] = SourceName(src.Dir)
			}
		}
	}
	return dirs, nil
}

// chatSourceOf returns the name of the source dir containing path. Longest
// match wins, so a source nested inside another attributes its files to the
// nearer one.
func chatSourceOf(path string, dirs map[string]string) (string, bool) {
	best, name := "", ""
	for dir, n := range dirs {
		if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
			continue
		}
		if len(dir) > len(best) {
			best, name = dir, n
		}
	}
	return name, best != ""
}

// readChatLabel reads a session's label — its first user turn, truncated — and
// counts its turns. Both come from one pass: the label is available at the head
// (R3286) but the count needs the whole file, so a single scan serves both
// rather than opening the file twice.
//
// Sidechain records are excluded from both, matching the default transcript
// view: a session's opening line is the human's, not a subagent's.
// CRC: crc-CLI.md | R3284, R3286
func readChatLabel(path string) (label string, turns int) {
	ReadTranscript(path, func(rec TranscriptRecord) bool { // R3553
		if rec.Sidechain || rec.Kind == TurnEnd {
			return true
		}
		turns++
		if label == "" && rec.Kind == TurnUser {
			label = truncateLabel(chatText(rec))
		}
		return true
	})
	return label, turns
}

// chatSessionOptionsFromLua reads the mcp:chatSessions options table. A nil,
// absent, or non-table argument yields the zero options, so `mcp:chatSessions()`
// is the unnarrowed listing rather than an error.
//
// Extracted from the binding closure so the marshalling a test exercises is the
// one production runs. A test that rebuilt this mapping by hand would pass
// while the real binding drifted — the failure shape that hid a dropped `extmap`
// for three months.
// CRC: crc-Server.md | R3287, R3291
func chatSessionOptionsFromLua(L *lua.LState, v lua.LValue) ChatSessionOptions {
	var opts ChatSessionOptions
	t, ok := v.(*lua.LTable)
	if !ok {
		return opts
	}
	opts.Source = lua.LVAsString(L.GetField(t, "source"))
	// `for` is a Lua keyword, so app code writes ["for"]=; the field name on
	// the table is the same either way.
	opts.For = lua.LVAsString(L.GetField(t, "for"))
	if n, ok := L.GetField(t, "limit").(lua.LNumber); ok {
		opts.Limit = int(n)
	}
	return opts
}

// chatRenderOptionsFromLua reads the mcp:chatTranscript options table. A nil,
// absent, or non-table argument yields the default transcript view — text only,
// no tools, no thinking, no sidechain — matching bare `ark chats`.
// CRC: crc-Server.md | R3291
func chatRenderOptionsFromLua(L *lua.LState, v lua.LValue) ChatRenderOptions {
	var opts ChatRenderOptions
	t, ok := v.(*lua.LTable)
	if !ok {
		return opts
	}
	opts.WithTools = lua.LVAsBool(L.GetField(t, "withTools"))
	opts.WithThinking = lua.LVAsBool(L.GetField(t, "withThinking"))
	opts.Sidechain = lua.LVAsBool(L.GetField(t, "sidechain"))
	if n, ok := L.GetField(t, "lineLength").(lua.LNumber); ok {
		opts.LineLength = int(n)
	}
	return opts
}

// truncateLabel flattens a turn to one line and clips it to chatLabelMax runes,
// so a pasted file or a multi-paragraph opening still yields one readable row.
func truncateLabel(text string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	runes := []rune(text)
	if len(runes) <= chatLabelMax {
		return text
	}
	return strings.TrimRight(string(runes[:chatLabelMax]), " ") + "…"
}
