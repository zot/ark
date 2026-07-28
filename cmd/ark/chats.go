package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	ark "github.com/zot/ark"
)

// cmdChats displays conversation transcripts from JSONL logs.
// CRC: crc-CLI.md | R1044, R1048
func cmdChats(args []string) {
	// R3284: `list` answers "what conversations exist" — the question that
	// precedes rendering one. It is a subcommand rather than a flag because
	// it takes no path and shares none of the render options.
	if len(args) > 0 && args[0] == "list" {
		cmdChatsList(args[1:])
		return
	}

	fs := flag.NewFlagSet("chats", flag.ExitOnError)
	withTools := fs.Bool("with-tools", false, "display tool calls and results")
	thinking := fs.Bool("thinking", false, "display chain-of-thought (thinking) blocks")
	all := fs.Bool("all", false, "display everything: tools + thinking + sidechain")
	sidechain := fs.Bool("sidechain", false, "display sidechain chatter")
	wrap := fs.String("wrap", "", "wrap output with a name tag")
	lineLen := fs.Int("line-length", ark.DefaultChatLineLength, "word-wrap line length")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark chats PATH-OR-GLOB... [options]
       ark chats list [options]

Show conversation transcripts from Claude Code JSONL logs.
An argument naming an existing file is rendered directly; otherwise
it is a glob matched against file basenames under ~/.claude/projects/.

Use 'ark chats list' to see which conversations exist.

Options:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  ark chats list
  ark chats list --for ark
  ark chats ./session.jsonl
  ark chats '*13051f62*.jsonl'
  ark chats '*.jsonl' --with-tools
  ark chats '*.jsonl' --wrap transcript`)
	}
	args = reorderArgs(args)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}
	// R3243: all positionals resolve, in argument order; per-arg no-match
	// warnings come from resolveChatFiles, exit 1 only when nothing matched.
	files := resolveChatFiles(fs.Args())
	if len(files) == 0 {
		os.Exit(1)
	}

	if *wrap != "" {
		fmt.Printf("<%s>\n", *wrap)
	}

	// R3035: --all is a convenience for the complete transcript — it enables
	// tools, thinking, and sidechain together.
	opts := ark.ChatRenderOptions{
		WithTools:    *withTools || *all,
		WithThinking: *thinking || *all,
		Sidechain:    *sidechain || *all,
		LineLength:   *lineLen,
	}
	for _, path := range files {
		if err := renderChat(path, opts); err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", path, err)
		}
	}

	if *wrap != "" {
		fmt.Printf("</%s>\n", *wrap)
	}
}

// cmdChatsList prints the indexed conversations, newest first — the verb that
// turns a directory of UUIDs into a table of contents.
// CRC: crc-CLI.md | R3284, R3285, R3286, R3287
func cmdChatsList(args []string) {
	fs := flag.NewFlagSet("chats list", flag.ExitOnError)
	source := fs.String("source", "", "restrict to one chat source (name or directory)")
	forSrc := fs.String("for", "", "restrict to chat sources logging this source (see: ark sources pair)")
	limit := fs.Int("limit", 0, "maximum rows, newest first (default 20)")
	asJSON := fs.Bool("json", false, "emit one JSON object per line")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark chats list [--source NAME] [--for NAME] [--limit N] [--json]

List indexed conversations, newest first. Each row shows the session's
first user turn as its label, plus modified time, turn count, and the
path 'ark chats' takes.

Options:`)
		fs.PrintDefaults()
	}
	args = reorderArgs(args)
	fs.Parse(args)

	if *source != "" && *forSrc != "" {
		fmt.Fprintln(os.Stderr, "--source and --for are mutually exclusive")
		os.Exit(1)
	}

	opts := ark.ChatSessionOptions{Source: *source, For: *forSrc, Limit: *limit}
	var sessions []ark.ChatSession
	proxyOrLocal(
		func(client *http.Client) error {
			return proxyDecode(client, "POST", "/chats/list", opts, &sessions)
		},
		func(d *ark.DB) error {
			var err error
			sessions, err = d.ChatSessions(opts)
			return err
		},
	)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, s := range sessions {
			if err := enc.Encode(s); err != nil {
				fatal(err)
			}
		}
		return
	}
	printChatSessions(sessions)
}

// printChatSessions renders the human table: date, turn count, label, path.
// The label is the column that makes the verb worth having, so it gets the
// room — the path follows on its own indented line rather than competing.
// CRC: crc-CLI.md | R3284
func printChatSessions(sessions []ark.ChatSession) {
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "no indexed conversations match")
		return
	}
	for _, s := range sessions {
		label := s.Label
		if label == "" {
			label = "(no user turn)"
		}
		fmt.Printf("%s  %4d turns  %s\n", s.Modified.Format("2006-01-02 15:04"), s.Turns, label)
		fmt.Printf("    %s\n", s.Path)
	}
}

// renderChat writes a transcript to stdout.
// CRC: crc-CLI.md | R1045, R1047, R1049, R3035
func renderChat(path string, opts ark.ChatRenderOptions) error {
	return ark.RenderChatFile(os.Stdout, path, opts)
}

// renderChatLines writes a transcript from an already-read stream to stdout —
// the `ark luhmann send` reply window renders through the same code.
// CRC: crc-CLI.md | R1045, R1047, R1049
func renderChatLines(r io.Reader, opts ark.ChatRenderOptions) error {
	return ark.RenderChatLines(os.Stdout, r, opts)
}

// resolveChatFiles resolves positional arguments to JSONL files. An argument
// naming an existing file is taken as-is; anything else is a glob handed to
// findJSONLFiles. Arguments yielding no file warn on stderr.
// CRC: crc-CLI.md | R3242, R3243
func resolveChatFiles(args []string) []string {
	var files []string
	for _, arg := range args {
		// R3242: an existing file renders directly — indexed or not, anywhere on disk
		if st, err := os.Stat(arg); err == nil && st.Mode().IsRegular() {
			files = append(files, arg)
			continue
		}
		matched := findJSONLFiles(arg)
		if len(matched) == 0 {
			fmt.Fprintf(os.Stderr, "no JSONL files matching: %s\n", arg)
		}
		files = append(files, matched...)
	}
	return files
}

// findJSONLFiles walks ~/.claude/projects/ for JSONL files matching a glob pattern.
// CRC: crc-CLI.md | R1050 — GLOB matches file basenames under ~/.claude/projects/
func findJSONLFiles(pattern string) []string {
	// Search common Claude Code conversation log locations
	dirs := []string{}

	// Walk ~/.claude/projects/ for JSONL files
	projectsDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects")
	filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if matched {
			dirs = append(dirs, path)
		}
		return nil
	})

	return dirs
}
