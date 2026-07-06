package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdChats displays conversation transcripts from JSONL logs.
// CRC: crc-CLI.md | R1044, R1048
func cmdChats(args []string) {
	fs := flag.NewFlagSet("chats", flag.ExitOnError)
	withTools := fs.Bool("with-tools", false, "display tool calls and results")
	thinking := fs.Bool("thinking", false, "display chain-of-thought (thinking) blocks")
	all := fs.Bool("all", false, "display everything: tools + thinking + sidechain")
	sidechain := fs.Bool("sidechain", false, "display sidechain chatter")
	wrap := fs.String("wrap", "", "wrap output with a name tag")
	lineLen := fs.Int("line-length", 100, "word-wrap line length")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark chats GLOB [options]

Show conversation transcripts from Claude Code JSONL logs.
GLOB matches against indexed JSONL file paths.

Options:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Examples:
  ark chats '*.jsonl'
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
	pattern := fs.Arg(0)

	// Find matching JSONL files in indexed sources
	files := findJSONLFiles(pattern)
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no JSONL files matching: %s\n", pattern)
		os.Exit(1)
	}

	if *wrap != "" {
		fmt.Printf("<%s>\n", *wrap)
	}

	// R3035: --all is a convenience for the complete transcript — it enables
	// tools, thinking, and sidechain together.
	showTools := *withTools || *all
	showThinking := *thinking || *all
	showSidechain := *sidechain || *all

	for _, path := range files {
		if err := renderChat(path, showTools, showThinking, *lineLen, showSidechain); err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", path, err)
		}
	}

	if *wrap != "" {
		fmt.Printf("</%s>\n", *wrap)
	}
}

// findJSONLFiles searches indexed sources for JSONL files matching a glob pattern.
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

// jsonlRecord is a minimal parse of a JSONL conversation record.
type jsonlRecord struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

type messageContent struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"` // R3035: chain-of-thought block
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// renderChat reads a JSONL file and prints a human-readable transcript.
// CRC: crc-CLI.md | R1045, R1047, R1049, R3035 — ❯ user / ● assistant markers,
// --with-tools shows ⚙ tool calls, --thinking shows ✻ chain-of-thought,
// sidechain (subagent) records filtered out unless requested
func renderChat(path string, withTools, withThinking bool, lineLen int, sidechain bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		var rec jsonlRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}

		// Skip sidechains (subagent traffic)
		if rec.IsSidechain && !sidechain {
			continue
		}

		if rec.Type != "user" && rec.Type != "assistant" {
			continue
		}

		var msg messageContent
		if err := json.Unmarshal(rec.Message, &msg); err != nil {
			continue
		}

		if rec.Type == "user" {
			text := extractText(msg.Content)
			if text != "" {
				printWrapped("❯", text, lineLen)
				fmt.Println()
			}
		} else if rec.Type == "assistant" {
			blocks := extractBlocks(msg.Content)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						printWrapped("●", b.Text, lineLen)
						fmt.Println()
					}
				case "thinking":
					// R3035: chain-of-thought, off by default (verbose). The
					// corpus already indexes it; this restores display parity.
					if withThinking && b.Thinking != "" {
						printWrapped("✻", b.Thinking, lineLen)
						fmt.Println()
					}
				case "tool_use":
					if withTools {
						input := summarizeToolInput(b.Input)
						fmt.Printf("  ⚙ %s %s\n", b.Name, input)
					}
				}
			}
		}
	}
	return scanner.Err()
}

// extractText gets the text content from a user message.
func extractText(content json.RawMessage) string {
	// Try as string first
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// Try as array of content blocks
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// extractBlocks parses content as an array of content blocks.
func extractBlocks(content json.RawMessage) []contentBlock {
	var blocks []contentBlock
	json.Unmarshal(content, &blocks)
	return blocks
}

// printWrapped prints text with a marker on the first line and 2-space indent
// on continuation lines, word-wrapped at lineLen.
// CRC: crc-CLI.md | R1046 — word-wrap at --line-length (default 100)
func printWrapped(marker, text string, lineLen int) {
	indent := "  " // continuation indent (matches marker + space width)
	prefix := marker + " "
	contentWidth := lineLen - len(indent)
	if contentWidth < 20 {
		contentWidth = 20
	}

	paragraphs := strings.Split(text, "\n")
	first := true
	for _, para := range paragraphs {
		if para == "" {
			fmt.Println()
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			fmt.Println()
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
				fmt.Println(line)
				line = indent + word
			} else {
				if line == prefix || line == indent {
					line += word
				} else {
					line += " " + word
				}
			}
		}
		if line != "" {
			fmt.Println(line)
		}
	}
}

// summarizeToolInput returns a brief summary of tool input for display.
func summarizeToolInput(input json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
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
