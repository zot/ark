package main

// CRC: crc-NanoCLI.md | Seq: seq-nano-repl-turn.md, seq-nano-session-resume.md | R2490, R2508, R2509, R2510, R2512, R2514, R2519, R2520, R2524, R2525, R2526, R2560, R2561, R2562, R2563

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/zot/ark"
)

// cmdNano is the `ark nano` subcommand entry point. Mirrors the
// nano-go standalone CLI: `[-m model] [-c | -s] [prompt...]`.
// With a prompt, runs one-shot; without one, drops into the REPL.
func cmdNano(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			nanoHelp()
			return
		}
	}
	stderrTTY := nanoIsTerminal(os.Stderr)
	stdoutTTY := nanoIsTerminal(os.Stdout)
	n := &ark.Nano{
		KeepHistory: true,
		TTY:         stderrTTY,
		Spinner:     stderrTTY && stdoutTTY,
	}

	flag, rest := parseNanoFlags(args, n)
	if n.Model == "" {
		nanoDie("model not set: pass -m <model>")
	}

	cwd, _ := os.Getwd()
	sessionsPath := defaultNanoSessionsPath()

	var history []ark.Message
	var label string

	switch flag {
	case "-c":
		sessions, _ := ark.NanoSessionsInCwd(sessionsPath, cwd)
		if len(sessions) == 0 {
			nanoDie("no sessions in this directory")
		}
		last := sessions[len(sessions)-1]
		history, label = last.Messages, last.Label
		fmt.Fprintln(os.Stderr, nanoDim("continuing: "+label, stderrTTY))
	case "-s":
		s, err := pickNanoSession(sessionsPath, cwd, stderrTTY)
		if err != nil {
			nanoDie(err.Error())
		}
		history, label = s.Messages, s.Label
		fmt.Fprintln(os.Stderr, nanoDim("resuming: "+label, stderrTTY))
	}

	prompt := strings.TrimSpace(strings.Join(rest, " "))
	if prompt != "" {
		answer, newHistory, err := n.Run(prompt, history)
		if err != nil {
			nanoDie(err.Error())
		}
		if label == "" {
			label = prompt
		}
		_ = ark.SaveNanoSession(sessionsPath, ark.NanoSession{
			Label: nanoTruncate(label, 80), Cwd: cwd, Ts: time.Now().Unix(), Messages: newHistory,
		})
		if !n.Stream {
			fmt.Println(answer)
		}
		return
	}

	rl, err := readline.New("")
	if err != nil {
		nanoDie(err.Error())
	}
	defer rl.Close()
	readLine := func(p string) (string, error) {
		rl.SetPrompt(p)
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			return "", io.EOF
		}
		return line, err
	}
	if err := n.REPL(history, label, readLine); err != nil {
		nanoDie(err.Error())
	}
}

// CRC: crc-NanoCLI.md | R2508, R2511, R2561, R2562, R2563, R2565
func parseNanoFlags(argv []string, n *ark.Nano) (string, []string) {
	flag := ""
	for len(argv) > 0 {
		switch argv[0] {
		case "-m":
			if len(argv) < 2 {
				nanoDie("-m requires a model name")
			}
			n.Model = argv[1]
			argv = argv[2:]
		case "--base-url":
			if len(argv) < 2 {
				nanoDie("--base-url requires a URL")
			}
			n.BaseURL = argv[1]
			argv = argv[2:]
		case "--max-steps":
			if len(argv) < 2 {
				nanoDie("--max-steps requires a count")
			}
			i, err := strconv.Atoi(argv[1])
			if err != nil {
				nanoDie("--max-steps requires an integer")
			}
			n.MaxSteps = i
			argv = argv[2:]
		case "--approve-all":
			n.ApproveAll = true
			argv = argv[1:]
		case "--stream":
			n.Stream = true
			argv = argv[1:]
		case "-c", "-s":
			flag = argv[0]
			argv = argv[1:]
		default:
			return flag, argv
		}
	}
	return flag, argv
}

// CRC: crc-NanoPicker.md | Seq: seq-nano-session-resume.md#2 | R2513
func pickNanoSession(path, cwd string, tty bool) (ark.NanoSession, error) {
	sessions, err := ark.NanoSessionsInCwd(path, cwd)
	if err != nil {
		return ark.NanoSession{}, err
	}
	if len(sessions) == 0 {
		return ark.NanoSession{}, fmt.Errorf("no sessions in this directory")
	}
	if len(sessions) > 10 {
		sessions = sessions[len(sessions)-10:]
	}
	now := time.Now().Unix()
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		age := now - s.Ts
		var ago string
		switch {
		case age < 3600:
			ago = fmt.Sprintf("%dm", age/60)
		case age < 86400:
			ago = fmt.Sprintf("%dh", age/3600)
		default:
			ago = fmt.Sprintf("%dd", age/86400)
		}
		fmt.Fprintf(os.Stderr, "  %s  %s  %s\n",
			nanoDim(strconv.Itoa(len(sessions)-1-i), tty),
			s.Label, nanoDim(ago+" ago", tty))
	}
	fmt.Fprint(os.Stderr, nanoBold("nano", tty)+nanoDim("#", tty)+" ")
	var line string
	if _, err := fmt.Fscanln(os.Stdin, &line); err != nil {
		return ark.NanoSession{}, fmt.Errorf("invalid session")
	}
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 0 || idx >= len(sessions) {
		return ark.NanoSession{}, fmt.Errorf("invalid session")
	}
	return sessions[len(sessions)-1-idx], nil
}

func defaultNanoSessionsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ark", "nano-sessions.json")
}

func nanoIsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func nanoDie(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func nanoTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func nanoDim(s string, tty bool) string {
	if !tty {
		return s
	}
	return "\033[90m" + s + "\033[0m"
}

func nanoBold(s string, tty bool) string {
	if !tty {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

func nanoHelp() {
	fmt.Println(`Usage: ark nano [-m model] [-c | -s] [prompt...]

Embedded shell-agent loop. Asks an Ollama model what to do, runs
shell commands with your approval, feeds the output back, and
loops until the model produces a final answer.

With a prompt: one-shot. The agent runs the prompt, prints the
final answer to stdout, and exits.
Without a prompt: interactive REPL.

Flags:
  -m <model>        Set the model name (required).
  --base-url <url>  Ollama server base URL
                    (default http://localhost:11434).
  --max-steps <N>   Maximum tool calls per task (default 200).
  --approve-all     Auto-approve every shell command.
  --stream          Print content tokens as Ollama emits them
                    (suppresses the thinking spinner).
  -c                Continue the most recent session whose cwd matches
                    the current working directory.
  -s                Pick from up to ten recent sessions in this
                    directory.
  -h, --help        Show this help.

Sessions:
  Saved to ~/.ark/nano-sessions.json (one entry per turn).
  Use -c to resume the last session in this directory, or -s
  to pick from a numbered list.

REPL:
  :q / quit / exit    End the session.
  :reset / reset      Clear history and start over.
  Ctrl-D / EOF        Exit cleanly.

See readme-nano.md for attribution and license.`)
}
