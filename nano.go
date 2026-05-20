// Package nano is a tiny shell-agent loop that talks to Ollama.
//
// The library exposes one type, Nano, that holds configuration and runtime
// state. A caller fills in the fields they care about (Model is required),
// calls Run for a one-shot or REPL for an interactive loop, and gets back
// the updated message history.
//
// Ollama is stateless, so to support session resume we persist the entire
// message log. Set KeepHistory=true to have REPL append to SessionsPath
// after each turn; library users who don't want that can leave it false
// and manage history themselves.
package ark

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	DefaultBaseURL        = "http://localhost:11434"
	DefaultMaxSteps       = 200
	DefaultMaxOutputBytes = 12000
)

var (
	skipDirs = map[string]bool{
		".git": true, ".venv": true, "__pycache__": true,
		"node_modules": true, "venv": true,
	}
	docFileNames   = map[string]bool{"claude.md": true, "agent.md": true, "agents.md": true, "readme.md": true}
	skillFileNames = map[string]bool{"skill.md": true, "skills.md": true}
	skillRoots     = []string{".claude/skills", "~/.claude/skills", "~/.codex/skills", "~/.codex/plugins"}
)

// Message is one entry in the chat history exchanged with Ollama.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ShellArgs is the parsed arguments for the execute_shell tool.
type ShellArgs struct {
	Command     string            `json:"command"`
	Description string            `json:"description"`
	Cwd         string            `json:"cwd,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// NanoSession is one persisted conversation: the full message log plus metadata
// for the picker.
// CRC: crc-NanoSessionStore.md | R2527, R2528, R2529, R2530, R2531, R2532
type NanoSession struct {
	Label    string    `json:"label"`
	Cwd      string    `json:"cwd"`
	Ts       int64     `json:"ts"`
	Messages []Message `json:"messages"`
}

// Nano is the agent's configuration and runtime state.
// CRC: crc-Nano.md | R2486, R2488, R2489, R2491, R2492, R2493, R2494, R2495, R2496, R2497, R2498, R2499, R2534, R2559, R2564, R2566
type Nano struct {
	Model          string // required, e.g. "qwen2.5-coder"
	BaseURL        string // default http://localhost:11434
	MaxSteps       int    // default 200
	MaxOutputBytes int    // default 12000; per-command output clip
	ApproveAll     bool
	KeepHistory    bool   // REPL persists to SessionsPath after each turn
	SessionsPath   string // default ~/.ark/nano-sessions.json
	TTY            bool   // enables ANSI color output on Stderr
	Spinner        bool   // animate a thinking spinner on Stderr during non-streaming chat (R2566)
	Stream         bool   // print content chunks to Stdout as they arrive (R2564)
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Cwd            string
	HTTPClient     *http.Client
}

// CRC: crc-Nano.md | Seq: seq-nano-run-loop.md#1 | R2493, R2494, R2495, R2496, R2535, R2559
func (n *Nano) init() error {
	if n.Model == "" {
		return fmt.Errorf("nano: Model is required")
	}
	if n.BaseURL == "" {
		n.BaseURL = DefaultBaseURL
	}
	if n.MaxSteps == 0 {
		n.MaxSteps = DefaultMaxSteps
	}
	if n.MaxOutputBytes == 0 {
		n.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if n.Stdin == nil {
		n.Stdin = os.Stdin
	}
	if n.Stdout == nil {
		n.Stdout = os.Stdout
	}
	if n.Stderr == nil {
		n.Stderr = os.Stderr
	}
	if n.HTTPClient == nil {
		n.HTTPClient = &http.Client{}
	}
	if n.Cwd == "" {
		d, err := os.Getwd()
		if err != nil {
			return err
		}
		n.Cwd = d
	}
	if n.SessionsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		n.SessionsPath = filepath.Join(home, ".ark", "nano-sessions.json")
	}
	return nil
}

func (n *Nano) color(code int, s string) string {
	if !n.TTY {
		return s
	}
	return fmt.Sprintf("\033[%dm%s\033[0m", code, s)
}

// CRC: crc-NanoSystemPromptBuilder.md | R2556, R2557, R2558
func (n *Nano) findFiles(roots []string, names map[string]bool, limit int) string {
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var found []string
	for _, root := range roots {
		if strings.HasPrefix(root, "~") {
			root = filepath.Join(home, root[1:])
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			if d.IsDir() || !names[strings.ToLower(d.Name())] {
				return nil
			}
			abs, _ := filepath.Abs(path)
			display := abs
			if strings.HasPrefix(abs, home+string(os.PathSeparator)) {
				display = "~" + abs[len(home):]
			} else if rel, err := filepath.Rel(n.Cwd, abs); err == nil {
				display = rel
			}
			if !seen[display] {
				seen[display] = true
				found = append(found, display)
				if len(found) >= limit {
					return fs.SkipAll
				}
			}
			return nil
		})
		if len(found) >= limit {
			break
		}
	}
	sort.Strings(found)
	if len(found) == 0 {
		return "none"
	}
	return strings.Join(found, ", ")
}

// CRC: crc-NanoSystemPromptBuilder.md | R2555, R2556, R2557
func (n *Nano) systemPrompt() string {
	docs := n.findFiles([]string{n.Cwd}, docFileNames, 40)
	skills := n.findFiles(skillRoots, skillFileNames, 40)
	return fmt.Sprintf(`You are Nano, a general-purpose shell agent with one tool: execute_shell.
Use it to inspect, edit, install, test, search, automate, and answer.
Be concise, tenacious, and relentlessly useful. Keep taking shell steps until done or blocked.
Output short plain-text snippets optimized for terminal reading; no markdown rendering or syntax highlighting.
Never run destructive commands unless explicitly requested.
cwd: %s
platform: %s/%s
shell: %s
Important docs (read as needed): %s
Important skill files (read as needed): %s
`, n.Cwd, runtime.GOOS, runtime.GOARCH, os.Getenv("SHELL"), docs, skills)
}

// CRC: crc-NanoShellTool.md | R2536, R2537, R2538
var toolSpec = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "execute_shell",
		"description": "Run a shell command with inherited environment.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":     map[string]any{"type": "string"},
				"description": map[string]any{"type": "string", "description": "Why this command is useful right now, in 5-10 words."},
				"cwd":         map[string]any{"type": "string"},
				"timeout":     map[string]any{"type": "integer"},
				"env":         map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			},
			"required": []string{"command", "description"},
		},
	},
}

type chatRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []map[string]any `json:"tools"`
	Stream   bool             `json:"stream"`
}

type chatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

// CRC: crc-NanoOllamaClient.md | Seq: seq-nano-run-loop.md#2.1 | R2487, R2564
func (n *Nano) chat(messages []Message) (Message, error) {
	if n.Stream {
		return n.chatStream(messages)
	}
	body, err := json.Marshal(chatRequest{
		Model: n.Model, Messages: messages,
		Tools: []map[string]any{toolSpec}, Stream: false,
	})
	if err != nil {
		return Message{}, err
	}
	if n.Spinner {
		done := make(chan struct{})
		go n.spin(done)
		defer close(done)
	}
	req, err := http.NewRequest("POST", n.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.HTTPClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}
	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return Message{}, err
	}
	return cr.Message, nil
}

// chatStream sends a streaming chat request and prints each content
// delta to Stdout as it arrives. Tool calls (typically in the final
// done frame) are accumulated into the returned Message along with the
// concatenated content. The thinking spinner runs from request-sent
// until the first response frame arrives, then yields to the stream —
// the live tokens are the progress signal from that point on.
// CRC: crc-NanoOllamaClient.md | Seq: seq-nano-run-loop.md#2.1 | R2564
func (n *Nano) chatStream(messages []Message) (Message, error) {
	body, err := json.Marshal(chatRequest{
		Model: n.Model, Messages: messages,
		Tools: []map[string]any{toolSpec}, Stream: true,
	})
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequest("POST", n.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var spinDone, spinExited chan struct{}
	if n.Spinner {
		spinDone = make(chan struct{})
		spinExited = make(chan struct{})
		go func() {
			n.spin(spinDone)
			close(spinExited)
		}()
	}
	stopSpinner := func() {
		if spinDone != nil {
			close(spinDone)
			<-spinExited
			spinDone = nil
		}
	}
	defer stopSpinner()
	resp, err := n.HTTPClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}
	accumulated := Message{Role: "assistant"}
	dec := json.NewDecoder(resp.Body)
	for {
		var cr chatResponse
		if err := dec.Decode(&cr); err == io.EOF {
			break
		} else if err != nil {
			return accumulated, err
		}
		if cr.Message.Content != "" {
			stopSpinner()
			fmt.Fprint(n.Stdout, cr.Message.Content)
			accumulated.Content += cr.Message.Content
		}
		if len(cr.Message.ToolCalls) > 0 {
			stopSpinner()
			accumulated.ToolCalls = append(accumulated.ToolCalls, cr.Message.ToolCalls...)
		}
		if cr.Done {
			break
		}
	}
	if accumulated.Content != "" {
		fmt.Fprintln(n.Stdout)
	}
	return accumulated, nil
}

func (n *Nano) spin(done <-chan struct{}) {
	frames := []rune{'-', '\\', '|', '/'}
	i := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			fmt.Fprint(n.Stderr, "\r             \r")
			return
		case <-ticker.C:
			fmt.Fprintf(n.Stderr, "\r  %s", n.color(90, string(frames[i%len(frames)])+" thinking"))
			i++
		}
	}
}

// CRC: crc-NanoApprover.md | Seq: seq-nano-shell-exec.md#2 | R2545, R2546, R2547, R2548, R2549
func (n *Nano) approve(args ShellArgs) bool {
	fmt.Fprintf(n.Stderr, "\n%s\n", n.color(90, "# "+args.Description))
	fmt.Fprintf(n.Stderr, "%s\n", n.color(32, "$ "+args.Command))
	if args.Cwd != "" {
		fmt.Fprintf(n.Stderr, "%s\n", n.color(90, "cwd: "+args.Cwd))
	}
	if args.Timeout != 0 {
		fmt.Fprintf(n.Stderr, "%s\n", n.color(90, fmt.Sprintf("timeout: %d", args.Timeout)))
	}
	if len(args.Env) > 0 {
		fmt.Fprintf(n.Stderr, "%s\n", n.color(90, fmt.Sprintf("env: %v", args.Env)))
	}
	if n.ApproveAll {
		return true
	}
	fmt.Fprintf(n.Stderr, "Approve? %s  %s  %s: ",
		n.color(32, "[y] Approve"),
		n.color(33, "[a] Approve All"),
		n.color(31, "[n] Deny"))
	line, err := bufio.NewReader(n.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	choice := strings.TrimSpace(strings.ToLower(line))
	if choice == "a" || choice == "all" {
		n.ApproveAll = true
		return true
	}
	return choice == "y" || choice == "yes"
}

// CRC: crc-NanoShellTool.md | Seq: seq-nano-shell-exec.md#4 | R2540, R2541, R2542, R2551, R2552, R2554
func (n *Nano) executeShell(args ShellArgs) string {
	timeout := args.Timeout
	if timeout == 0 {
		timeout = 60
	}
	cwd := args.Cwd
	if cwd == "" {
		cwd = n.Cwd
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	cmd := exec.Command("sh", "-c", args.Command)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range args.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	timedOut := false
	if err := cmd.Start(); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	timer := time.AfterFunc(time.Duration(timeout)*time.Second, func() {
		timedOut = true
		_ = cmd.Process.Kill()
	})
	err := cmd.Wait()
	timer.Stop()
	out := buf.String()
	if timedOut {
		return n.clip(fmt.Sprintf("$ %s\ntimeout after %ds\n%s", args.Command, timeout, out))
	}
	exit := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			return fmt.Sprintf("error: %v", err)
		}
		exit = ee.ExitCode()
	}
	return n.clip(fmt.Sprintf("$ %s\nexit %d\n%s", args.Command, exit, out))
}

// CRC: crc-NanoShellTool.md | Seq: seq-nano-shell-exec.md#5.3 | R2553
func (n *Nano) clip(s string) string {
	if len(s) <= n.MaxOutputBytes {
		return s
	}
	return s[len(s)-n.MaxOutputBytes:]
}

// CRC: crc-NanoShellTool.md | Seq: seq-nano-shell-exec.md#1 | R2536, R2539, R2550
func (n *Nano) handleToolCall(tc ToolCall) string {
	if tc.Function.Name != "execute_shell" {
		return "unknown tool"
	}
	var args ShellArgs
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		return fmt.Sprintf("bad arguments: %v", err)
	}
	words := len(strings.Fields(args.Description))
	if words < 5 || words > 10 {
		return "bad arguments: description must be 5-10 words"
	}
	if !n.approve(args) {
		return n.color(31, "denied by user")
	}
	return n.executeShell(args)
}

// Run executes one user prompt against Ollama, looping on tool calls until
// the model produces a plain text answer or MaxSteps is reached. If history
// is nil, a fresh history is started with the system prompt.
// CRC: crc-Nano.md | Seq: seq-nano-run-loop.md | R2500, R2501, R2543, R2544
func (n *Nano) Run(prompt string, history []Message) (string, []Message, error) {
	if err := n.init(); err != nil {
		return "", history, err
	}
	if history == nil {
		history = []Message{{Role: "system", Content: n.systemPrompt()}}
	}
	history = append(history, Message{Role: "user", Content: prompt})
	for step := 0; step < n.MaxSteps; step++ {
		msg, err := n.chat(history)
		if err != nil {
			return "", history, err
		}
		history = append(history, msg)
		if len(msg.ToolCalls) == 0 {
			return msg.Content, history, nil
		}
		for _, tc := range msg.ToolCalls {
			out := n.handleToolCall(tc)
			history = append(history, Message{Role: "tool", Content: out})
		}
	}
	return "stopped: too many tool calls", history, nil
}

// ReadLineFunc reads one prompt line from the user. The CLI plugs in
// chzyer/readline here; library callers can pass nil to get a plain bufio
// reader with no editing or history.
type ReadLineFunc func(prompt string) (string, error)

// REPL runs an interactive multi-turn session.
// CRC: crc-Nano.md | Seq: seq-nano-repl-turn.md | R2502, R2503, R2510, R2521, R2522, R2523
func (n *Nano) REPL(history []Message, label string, readLine ReadLineFunc) error {
	if err := n.init(); err != nil {
		return err
	}
	if readLine == nil {
		r := bufio.NewReader(n.Stdin)
		readLine = func(p string) (string, error) {
			fmt.Fprint(n.Stderr, p)
			line, err := r.ReadString('\n')
			return strings.TrimSpace(line), err
		}
	}
	fmt.Fprintln(n.Stderr, n.color(1, "nano")+" repl "+n.color(90, "(:q quit, :reset reset)"))
	for {
		line, err := readLine(n.color(36, "nano > "))
		if err == io.EOF {
			fmt.Fprintln(n.Stderr)
			return nil
		}
		if err != nil {
			return err
		}
		prompt := strings.TrimSpace(line)
		if prompt == "" {
			continue
		}
		low := strings.ToLower(prompt)
		if low == ":q" || low == "quit" || low == "exit" {
			return nil
		}
		if low == ":reset" || low == "reset" {
			history, label = nil, ""
			fmt.Fprintln(n.Stderr, n.color(90, "reset"))
			continue
		}
		answer, newHistory, err := n.Run(prompt, history)
		if err != nil {
			fmt.Fprintln(n.Stderr, n.color(31, "error: "+err.Error()))
			continue
		}
		history = newHistory
		if label == "" {
			label = prompt
		}
		if n.KeepHistory {
			err := SaveNanoSession(n.SessionsPath, NanoSession{
				Label: truncate(label, 80), Cwd: n.Cwd, Ts: time.Now().Unix(), Messages: history,
			})
			if err != nil {
				fmt.Fprintln(n.Stderr, n.color(31, "session save: "+err.Error()))
			}
		}
		if !n.Stream {
			fmt.Fprintln(n.Stdout, answer)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// LoadNanoSessions reads the sessions file at path. A missing file returns
// (nil, nil) rather than an error.
// CRC: crc-NanoSessionStore.md | R2504, R2527
func LoadNanoSessions(path string) ([]NanoSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []NanoSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// SaveNanoSession appends s to the sessions file at path. If a session with the
// same label and cwd already exists it is replaced. The file is capped at
// the most recent 50 sessions.
// CRC: crc-NanoSessionStore.md | R2505, R2506, R2533
func SaveNanoSession(path string, s NanoSession) error {
	sessions, _ := LoadNanoSessions(path)
	kept := sessions[:0]
	for _, existing := range sessions {
		if existing.Label == s.Label && existing.Cwd == s.Cwd {
			continue
		}
		kept = append(kept, existing)
	}
	kept = append(kept, s)
	if len(kept) > 50 {
		kept = kept[len(kept)-50:]
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// NanoSessionsInCwd returns sessions whose Cwd field matches, oldest first.
// CRC: crc-NanoSessionStore.md | R2507
func NanoSessionsInCwd(path, cwd string) ([]NanoSession, error) {
	sessions, err := LoadNanoSessions(path)
	if err != nil {
		return nil, err
	}
	var out []NanoSession
	for _, s := range sessions {
		if s.Cwd == cwd {
			out = append(out, s)
		}
	}
	return out, nil
}
