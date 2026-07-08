package main

// CRC: crc-CLI.md | Seq: seq-cli-dispatch.md

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zot/ark"
	bolterrors "go.etcd.io/bbolt/errors"

	ucli "github.com/urfave/cli/v3"
	cli "github.com/zot/ui-engine/cli"
)

// arkDir is the ark directory, set from --dir flag (global, parsed before subcommand).
var arkDir string

// R1251, R1252: Default system prompt for spectral search expansion.
const defaultSearchPrompt = `You are a search expansion oracle for a tag-based knowledge system.

When given a tag name and value, suggest alternative tag names and values
that a human might search for when looking for related content.

Rules:
- Return ONLY a JSON array of objects with "tag" and "value" fields
- Suggest 3-8 alternatives
- Include synonyms, related concepts, broader/narrower terms
- Tag names are lowercase, hyphenated (e.g., "design", "architecture", "decision")
- Values can be any text

When given a numbered list of matches and asked which are relevant,
return ONLY a JSON array of numbers (e.g., [1, 3, 5]).
`

// stringSlice is a flag.Value that accumulates repeated flag values.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	// R725, R726: ExpandVerbosityFlags pre-tokenizes a bundled -vvvv into
	// -v -v -v -v (urfave does not bundle short flags); the root's -v count
	// flag accumulates them. The root then parses --dir/-v as global flags
	// before the subcommand and routes to the matching node's Action.
	// CRC: crc-CLITree.md | Seq: seq-cli-urfave.md#1.4 | R2916, R2923
	args := cli.ExpandVerbosityFlags(os.Args[1:])
	root := buildArkCommand()
	// R2953: order every subcommand list alphabetically so help is easy to
	// scan. urfave renders commands in slice order; dispatch matches by name,
	// so this affects only the help display, not routing.
	// CRC: crc-CLITree.md | R2953
	sortCommandTree(root.Commands)
	// Errors are handled by the root's ExitErrHandler (which exits); a
	// returned error here would already have been rendered.
	_ = root.Run(context.Background(), append([]string{"ark"}, args...))
}

// buildArkCommand constructs the ark urfave command tree: the command
// nodes, the global flags (--dir/-v) with a Before hook that copies them
// into the arkDir/verbosity package globals handler bodies already read,
// and the cross-cutting CLI conventions (error format, exit codes). The
// root help (NAME/USAGE/COMMANDS/OPTIONS) replaces the retired usage().
// CRC: crc-CLITree.md | Seq: seq-cli-urfave.md#1.5, seq-cli-urfave.md#4.2 | R2916, R2923, R2926, R2927
func buildArkCommand() *ucli.Command {
	// -v count, accumulated by urfave across repeated/expanded -v flags and
	// read by the Before hook. (R724, R727)
	var verbosity int
	return &ucli.Command{
		Name:  "ark",
		Usage: "digital zettelkasten — hybrid trigram + vector search",
		// Global flags recognized before the subcommand (R2923). --dir
		// defaults to ~/.ark/; -v is a repeatable count flag.
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "dir", Value: defaultDB(), Usage: "database directory (default ~/.ark/)"}, // R71
			&ucli.BoolFlag{Name: "v", Usage: "increase verbosity (repeatable, up to -vvvv)", Config: ucli.BoolConfig{Count: &verbosity}},
		},
		// Before runs ahead of any subcommand Action, so the bodies that
		// read arkDir / the verbosity level see the parsed globals. (R724,
		// R727, R2923)
		Before: func(ctx context.Context, c *ucli.Command) (context.Context, error) {
			arkDir = c.String("dir")
			ark.SetVerbosity(verbosity)
			return ctx, nil
		},
		// Action runs only when no subcommand matched: bare `ark` shows the
		// root help (exit 0); an unknown command name reports cleanly and
		// exits 1 — the state-A "unknown command" UX, since urfave's default
		// would otherwise emit a cryptic "No help topic for '<name>'".
		// CRC: crc-CLITree.md | R2916
		Action: func(_ context.Context, c *ucli.Command) error {
			if c.Args().Present() {
				fmt.Fprintf(os.Stderr, "unknown command: %s\n", c.Args().First())
				os.Exit(1)
			}
			return ucli.ShowRootCommandHelp(c)
		},
		// ExitErrHandler renders errors urfave itself raises (flag-parse
		// failures, unknown flags) as `error: <msg>` on stderr — the
		// fatal() shape — and exits with the error's ExitCoder code (else
		// 1). It must exit here, or the code is lost and the message
		// double-prints. (R2926, R2927)
		ExitErrHandler: func(_ context.Context, _ *ucli.Command, err error) {
			if err == nil {
				return
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			code := 1
			var ec ucli.ExitCoder
			if errors.As(err, &ec) {
				code = ec.ExitCode()
			}
			os.Exit(code)
		},
		Commands: arkCommands(),
	}
}

// sortCommandTree orders every command node's subcommands alphabetically by
// Name, recursively, so help lists are easy to scan at every depth. urfave/cli
// v3 renders commands in slice order; dispatch matches by name, so this affects
// only the help display, not routing.
// CRC: crc-CLITree.md | R2953
func sortCommandTree(cmds []*ucli.Command) {
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	for _, c := range cmds {
		sortCommandTree(c.Commands)
	}
}

// arkCommands returns every ark command node — the full CLI surface, now
// that the urfave migration is complete (no legacy dispatch remains).
// CRC: crc-CLITree.md | R2916
func arkCommands() []*ucli.Command {
	cmds := []*ucli.Command{
		connectionsCommand(),
		monitorCommand(),
		luhmannCommand(),
		bloodhoundCommand(),
		embedCommand(),
		discussedCommand(),
		tagCommand(),
		configCommand(),
		extCommand(),
		scheduleCommand(),
		messageCommand(),
		uiCommand(),
		subscribeCommand(),
		subscribersCommand(),
		listenCommand(),
		searchCommand(),
	}
	return append(cmds, flatCommands()...)
}

// cmdVersion prints the build version. ark.Version is injected at build time
// from README.md's "Version:" line by the Makefile (-X ark.Version=...), and
// falls back to "dev" for plain `go build`.
// CRC: crc-CLI.md | R2960
func cmdVersion(_ []string) {
	fmt.Printf("ark %s\n", ark.Version)
}

// CRC: crc-CLI.md | R29 — default database directory is ~/.ark/ (--dir overrides; R71)
func defaultDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ark"
	}
	return filepath.Join(home, ".ark")
}

// withDB opens the database, runs fn, and closes it. Fatals on error.
func withDB(fn func(db *ark.DB)) {
	d, err := ark.Open(arkDir)
	if err != nil {
		fatal(err)
	}
	defer d.Close()
	fn(d)
}

// Stubborn dispatch tunables.
// CRC: crc-CLI.md | R2996, R2997
const (
	dispatchStubbornWindow = 30 * time.Second       // wait through a server bounce this long
	dispatchRetryInterval  = 200 * time.Millisecond // poll cadence while stubborn
	dispatchLockTimeout    = 2 * time.Second        // bounded local open (microfts2 R672)
)

// serverUnreachable reports whether err is a transport-level failure (the
// server is down or bouncing) rather than an application error from a live
// server. proxyRaw returns *url.Error for client.Do failures and a plain
// formatted error for non-200 responses, so a *url.Error means "couldn't
// reach the server" — the signal to be stubborn.
// CRC: crc-CLI.md | R2996
func serverUnreachable(err error) bool {
	var ue *url.Error
	return errors.As(err, &ue)
}

// proxyOrLocal is the single dispatch point for every DB-touching CLI
// command. The index is single-process (bbolt file lock), so a direct
// local open blocks while the server holds it; proxyOrLocal prefers a
// running server and opens locally only when none is reachable.
//
//   - proxy talks to the server. It may be nil for maintenance/diagnostic
//     commands needing exclusive local access; with a server running those
//     fail fast ("stop the server") instead of hanging.
//   - local opens the index directly (bounded lock wait) and runs the op.
//
// Stubborn Plumbing: a transport error is a bounce, not a failure —
// proxyOrLocal waits for the server to return and retries until the
// stubborn window elapses. On a local-open lock timeout it loops
// to recheck server liveness (the lock may now be held by a server that
// just came up) and re-dispatches. A real error surfaces only
// after the window closes.
// CRC: crc-CLI.md | R2995, R2996, R2997
// CRC: crc-CLI.md | Seq: seq-cli-dispatch.md | R88
func proxyOrLocal(proxy func(*http.Client) error, local func(*ark.DB) error) {
	start := time.Now()
	for {
		if client := serverClient(arkDir); client != nil {
			if proxy == nil {
				fatal(fmt.Errorf("a server is running and holds the index; stop it with `ark stop` to run this command"))
			}
			err := proxy(client)
			if err == nil {
				return
			}
			if serverUnreachable(err) && time.Since(start) < dispatchStubbornWindow {
				time.Sleep(dispatchRetryInterval) // bounce — wait and retry
				continue
			}
			fatal(err)
		}

		// No server answered the dial: open locally with a bounded lock wait.
		d, err := ark.OpenWithTimeout(arkDir, dispatchLockTimeout)
		if err != nil {
			// Only a lock timeout is worth retrying — something holds the
			// index though no server answered (a race with a server coming
			// up, or a bounce in progress). Other open failures (config,
			// schema) are real; fatal at once.
			// CRC: crc-CLI.md | R2997
			if errors.Is(err, bolterrors.ErrTimeout) && time.Since(start) < dispatchStubbornWindow {
				time.Sleep(dispatchRetryInterval)
				continue
			}
			fatal(err)
		}
		runErr := local(d)
		d.Close()
		if runErr != nil {
			fatal(runErr)
		}
		return
	}
}

// withExclusiveDB runs fn against a locally-opened index but refuses (fails
// fast) when a server holds it — for maintenance/diagnostic commands that
// need exclusive access and have no server proxy. It is proxyOrLocal with a
// nil proxy; fn keeps its existing fatal-internally style.
// CRC: crc-CLI.md | R3002
func withExclusiveDB(fn func(*ark.DB)) {
	proxyOrLocal(nil, func(d *ark.DB) error { fn(d); return nil })
}

// serverClient returns an http.Client that connects over Unix socket,
// or nil if no server is running.
// CRC: crc-CLI.md | Seq: seq-cli-dispatch.md | R4
func serverClient(dbPath string) *http.Client {
	socketPath := filepath.Join(dbPath, "ark.sock")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil
	}
	conn.Close()

	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
}

// proxyRaw sends a request to the running server and returns the response body.
func proxyRaw(client *http.Client, method, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://ark"+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

// proxyDecode sends a request and decodes the JSON response into v.
func proxyDecode(client *http.Client, method, path string, body any, v any) error {
	data, err := proxyRaw(client, method, path, body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// proxyOK sends a request and checks for success (ignores response body).
func proxyOK(client *http.Client, method, path string, body any) error {
	_, err := proxyRaw(client, method, path, body)
	return err
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// requireServer returns a serverClient for arkDir or fatals with a
// consistent "server not running (op requires server)" message.
func requireServer(op string) *http.Client {
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (%s requires server)", op))
	}
	return client
}

// Command implementations

// CRC: crc-CLI.md | Seq: seq-install.md | R278, R279, R323, R325
func cmdSetup(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stderr, "Usage: ark setup\n\nBootstrap ~/.ark/: extract bundled assets, install global skills\nand agent, run linkapp. Idempotent.")
		os.Exit(0)
	}
	if err := runSetup(); err != nil {
		fatal(err)
	}
}

// runSetup is the idempotent global bootstrap. Extracts bundled assets to
// ~/.ark/, installs global skills and agent, runs linkapp.
func runSetup() error {
	bundled, err := cli.IsBundled()
	if err != nil || !bundled {
		return fmt.Errorf("binary is not bundled — cannot extract assets")
	}

	// Create ~/.ark/ if needed
	if err := os.MkdirAll(arkDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", arkDir, err)
	}

	// Extract bundled assets (html/, lua/, viewdefs/, apps/, skills/, agents/)
	if err := cli.BundleExtractBundle(arkDir); err != nil {
		return fmt.Errorf("extract bundle: %w", err)
	}
	fmt.Println("Extracted bundled assets to", arkDir)

	// Run linkapp for the ark app
	appsDir := filepath.Join(arkDir, "apps", "ark")
	if _, err := os.Stat(appsDir); err == nil {
		runLinkapp(arkDir, "ark")
	}

	// Install global skills and agent to ~/.claude/
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	claudeDir := filepath.Join(home, ".claude")
	installed := installBundledSkillsAndAgent(arkDir, claudeDir)
	for _, item := range installed {
		fmt.Println("Installed:", item)
	}

	// R2969: provision the llama.cpp libs if a model is configured and the
	// libs are absent. Best-effort — a download failure leaves the rest of
	// setup intact; `ark embed install` retries.
	if cfg, err := ark.LoadConfig(filepath.Join(arkDir, "ark.toml")); err == nil && cfg.Embedding.Model != "" {
		libs := ark.NewLlamaLibs(cfg.Embedding, arkDir)
		if err := libs.Provision(false); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not provision llama.cpp libs: %v\n  run `ark embed install` to retry\n", err)
		} else {
			fmt.Println("llama.cpp libraries ready in", libs.LibDir())
		}
	}

	return nil
}

// installBundledSkillsAndAgent copies skills and agent from ~/.ark/ to ~/.claude/.
func installBundledSkillsAndAgent(arkDir, claudeDir string) []string {
	var installed []string

	// Skills: ~/.ark/skills/ark/ → ~/.claude/skills/ark/
	//         ~/.ark/skills/ui/  → ~/.claude/skills/ui/
	for _, skill := range []string{"ark", "ui"} {
		src := filepath.Join(arkDir, "skills", skill, "SKILL.md")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dstDir := filepath.Join(claudeDir, "skills", skill)
		os.MkdirAll(dstDir, 0755)
		dst := filepath.Join(dstDir, "SKILL.md")
		if data, err := os.ReadFile(src); err == nil {
			if err := os.WriteFile(dst, data, 0644); err == nil {
				installed = append(installed, dst)
			}
		}
	}

	// Agent: ~/.ark/agents/ark.md → ~/.claude/agents/ark.md
	agentSrc := filepath.Join(arkDir, "agents", "ark.md")
	if _, err := os.Stat(agentSrc); err == nil {
		agentDstDir := filepath.Join(claudeDir, "agents")
		os.MkdirAll(agentDstDir, 0755)
		agentDst := filepath.Join(agentDstDir, "ark.md")
		if data, err := os.ReadFile(agentSrc); err == nil {
			if err := os.WriteFile(agentDst, data, 0644); err == nil {
				installed = append(installed, agentDst)
			}
		}
	}

	return installed
}

// runLinkapp creates lua/ and viewdefs/ symlinks for an app.
func runLinkapp(baseDir, app string) {
	appsDir := filepath.Join(baseDir, "apps")
	luaDir := filepath.Join(baseDir, "lua")
	viewdefsDir := filepath.Join(baseDir, "viewdefs")
	appDir := filepath.Join(appsDir, app)

	os.MkdirAll(luaDir, 0755)
	os.MkdirAll(viewdefsDir, 0755)

	// Link lua file: lua/app.lua -> ../apps/app/app.lua
	appLua := filepath.Join(appDir, "app.lua")
	if _, err := os.Stat(appLua); err == nil {
		target := filepath.Join(luaDir, app+".lua")
		os.Remove(target)
		os.Symlink(filepath.Join("../apps", app, "app.lua"), target)
	}

	// Link app directory: lua/app -> ../apps/app
	appLink := filepath.Join(luaDir, app)
	os.Remove(appLink)
	os.Symlink(filepath.Join("../apps", app), appLink)

	// Link viewdefs
	vdDir := filepath.Join(appDir, "viewdefs")
	if entries, err := os.ReadDir(vdDir); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".html" {
				target := filepath.Join(viewdefsDir, e.Name())
				os.Remove(target)
				os.Symlink(filepath.Join("../apps", app, "viewdefs", e.Name()), target)
			}
		}
	}
}

// isBootstrapped checks if ~/.ark/ has been set up (html/ directory exists).
func isBootstrapped() bool {
	_, err := os.Stat(filepath.Join(arkDir, "html"))
	return err == nil
}

// CRC: crc-CLI.md | R72
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	embedCmd := fs.String("embed-cmd", "", "embedding command (optional, enables vector search)")
	queryCmd := fs.String("query-cmd", "", "query embedding command (optional)")
	caseInsensitive := fs.Bool("case-insensitive", true, "case-insensitive indexing")
	aliasStr := fs.String("aliases", "", "byte aliases (from=to,...)")
	noSetup := fs.Bool("no-setup", false, "skip automatic setup")
	ifNeeded := fs.Bool("if-needed", false, "skip if database already exists")
	fs.Parse(args)

	// --if-needed: exit silently if DB exists
	if *ifNeeded {
		if _, err := os.Stat(filepath.Join(arkDir, ark.IndexFileName)); err == nil {
			return
		}
	}

	// Remove existing database file before creating fresh
	os.Remove(filepath.Join(arkDir, ark.IndexFileName))

	// Auto-setup if not bootstrapped (unless --no-setup)
	if !*noSetup && !isBootstrapped() {
		if err := runSetup(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup failed: %v\n", err)
		}
	}

	aliases := parseAliases(*aliasStr)

	// Try reading seed files from bundle
	var tagsSeed []byte
	if data, err := cli.BundleReadFile("install/tags.md"); err == nil {
		tagsSeed = data
	}
	var configSeed []byte
	if data, err := cli.BundleReadFile("install/ark.toml"); err == nil {
		configSeed = data
	}

	opts := ark.InitOpts{
		EmbedCmd:        *embedCmd,
		QueryCmd:        *queryCmd,
		CaseInsensitive: *caseInsensitive,
		Aliases:         aliases,
		TagsSeed:        tagsSeed,
		ConfigSeed:      configSeed,
	}
	if err := ark.Init(arkDir, opts); err != nil {
		fatal(err)
	}

	// R1252: Create ~/.ark/searching/ with default CLAUDE.md
	searchDir := filepath.Join(arkDir, "searching")
	claudeFile := filepath.Join(searchDir, "CLAUDE.md")
	if _, err := os.Stat(claudeFile); os.IsNotExist(err) {
		os.MkdirAll(searchDir, 0755)
		os.WriteFile(claudeFile, []byte(defaultSearchPrompt), 0644)
	}

	fmt.Printf("initialized ark database at %s\n", arkDir)
}

// CRC: crc-CLI.md
func cmdRebuild(args []string) {
	// Refuse if server is running
	if client := serverClient(arkDir); client != nil {
		fmt.Fprintln(os.Stderr, "error: server is running — stop it first with 'ark stop'")
		os.Exit(1)
	}
	// init --no-setup handles db removal and recreation, reading settings from ark.toml
	cmdInit([]string{"--no-setup"})
	// CRC: crc-CLI.md | Seq: seq-rebuild-read-serve.md#1.3 | R2984, R2985, R2990
	// Run the scan under a read-only server so `ark status` / `ark search`
	// from another terminal return live progress instead of blocking on
	// bbolt's single-process file lock. Serve returns when indexing drains.
	// The brief init→socket-bind drop window above is uncovered (R2990).
	if err := ark.Serve(arkDir, ark.ServeOpts{Rebuild: true}); err != nil {
		fatal(err)
	}
	fmt.Println("rebuild complete")
	// R1294: embeddings regenerate on next server start (batch embed post-reconcile)
	cfg, _ := ark.LoadConfig(filepath.Join(arkDir, "ark.toml"))
	if cfg != nil && cfg.Embedding.Model != "" {
		fmt.Println("embeddings (tags + chunks) will regenerate on next 'ark serve'")
	}
}

// CRC: crc-CLI.md | R73
func cmdAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark add [options] PATH...

Add files to the index. For tmp:// paths, content comes from
--content, --from-file, or stdin.

Options:`)
		fs.PrintDefaults()
	}
	strategy := fs.String("strategy", "", "chunking strategy")
	contentFlag := fs.String("content", "", "inline content for tmp:// documents")
	fromFile := fs.String("from-file", "", "read content from file for tmp:// documents")
	appendFlag := fs.Bool("append", false, "append to existing tmp:// document instead of replacing") // R909, R910
	fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or directories specified")
		os.Exit(1)
	}

	// R669, R694, R695, R696: tmp:// paths are added via server overlay
	if len(paths) == 1 && strings.HasPrefix(paths[0], "tmp://") {
		client := serverClient(arkDir)
		if client == nil {
			fmt.Fprintln(os.Stderr, "error: tmp:// requires a running server")
			os.Exit(1)
		}
		var content []byte
		var err error
		switch {
		case *contentFlag != "":
			content = []byte(*contentFlag)
		case *fromFile != "":
			content, err = os.ReadFile(*fromFile)
			if err != nil {
				fatal(err)
			}
		default:
			content, err = io.ReadAll(os.Stdin)
			if err != nil {
				fatal(err)
			}
		}
		strat := *strategy
		if strat == "" {
			strat = "lines"
		}
		endpoint := "/tmp/add" // R910: --append routes to /tmp/append
		if *appendFlag {
			endpoint = "/tmp/append"
		}
		if err := proxyOK(client, "POST", endpoint, map[string]any{
			"path": paths[0], "strategy": strat,
			"content":  base64.StdEncoding.EncodeToString(content),
			"encoding": "base64",
		}); err != nil {
			fatal(err)
		}
		return
	}

	proxyOrLocal(
		func(client *http.Client) error {
			if err := proxyOK(client, "POST", "/add", map[string]any{
				"paths": paths, "strategy": *strategy,
			}); err != nil {
				return err
			}
			return nil
		},
		func(d *ark.DB) error {
			if err := d.Add(paths, *strategy); err != nil {
				return err
			}
			return nil
		},
	)
}

// CRC: crc-CLI.md | R74
func cmdRemove(args []string) {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark remove <path|pattern>...\n\nRemove files from the index. Accepts paths or glob patterns.")
	}
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or patterns specified")
		os.Exit(1)
	}

	// R670: tmp:// paths are removed via server overlay
	if len(patterns) == 1 && strings.HasPrefix(patterns[0], "tmp://") {
		client := serverClient(arkDir)
		if client == nil {
			fmt.Fprintln(os.Stderr, "error: tmp:// requires a running server")
			os.Exit(1)
		}
		if err := proxyOK(client, "POST", "/tmp/remove", map[string]any{
			"path": patterns[0],
		}); err != nil {
			fatal(err)
		}
		return
	}

	proxyOrLocal(
		func(client *http.Client) error {
			if err := proxyOK(client, "POST", "/remove", map[string]any{"patterns": patterns}); err != nil {
				return err
			}
			return nil
		},
		func(d *ark.DB) error {
			if err := d.Remove(patterns); err != nil {
				return err
			}
			return nil
		},
	)
}

// CRC: crc-CLI.md | R75
func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark scan\n\nWalk configured source directories, index new files, flag unresolved.\nDoes not re-check existing files (use refresh for that).")
	}
	fs.Parse(args)

	proxyOrLocal(
		func(client *http.Client) error {
			var result struct {
				NewFiles      int `json:"newFiles"`
				NewUnresolved int `json:"newUnresolved"`
			}
			if err := proxyDecode(client, "POST", "/scan", nil, &result); err != nil {
				return err
			}
			fmt.Printf("new files: %d, new unresolved: %d\n", result.NewFiles, result.NewUnresolved)
			return nil
		},
		func(d *ark.DB) error {
			results, err := d.Scan()
			if err != nil {
				return err
			}
			fmt.Printf("new files: %d, new unresolved: %d\n",
				len(results.NewFiles), len(results.NewUnresolved))
			return nil
		},
	)
}

// CRC: crc-CLI.md | R76
func cmdRefresh(args []string) {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark refresh [PATTERN...]\n\nRe-index stale files. Optional glob patterns to scope the refresh.")
	}
	fs.Parse(args)

	patterns := fs.Args()

	proxyOrLocal(
		func(client *http.Client) error {
			if err := proxyOK(client, "POST", "/refresh", map[string]any{"patterns": patterns}); err != nil {
				return err
			}
			fmt.Println("refresh complete")
			return nil
		},
		func(d *ark.DB) error {
			if err := d.Refresh(patterns); err != nil {
				return err
			}
			fmt.Println("refresh complete")
			return nil
		},
	)
}

// CRC: crc-CLI.md | Seq: seq-filter-stack.md | R1770, R1771, R1772, R1773, R1774, R1775, R1776, R1940

type filterEntry struct {
	polarity string // "with" or "without"
	mode     string // "contains", "fuzzy", "regex", "tag", "about", "files"
	query    string
	k        int // R1940: about-mode filter top-K override (0 = use config default)
}

// parseFilterStack extracts filter entries from raw args before flag.Parse.
// Returns filter entries, remaining args for flag.Parse, and whether -parse was requested.
func parseFilterStack(args []string) (entries []filterEntry, remaining []string, parseOnly bool) {
	polarity := "with"
	var accum []string // accumulator for bare terms / contains coalescing

	flush := func() {
		if len(accum) > 0 {
			entries = append(entries, filterEntry{polarity, "contains", strings.Join(accum, " "), 0})
			accum = nil
		}
	}

	// flags that take a value argument — pass both flag and value through
	valuedFlags := map[string]bool{
		"-k": true, "-after": true, "-before": true, "-score": true,
		"-session": true, "-wrap": true, "-like-file": true,
		"-cpuprofile": true, "-memprofile": true, "-trace": true,
		"-preview": true,
	}

	i := 0
	for i < len(args) {
		arg := args[i]
		// normalize double-dash to single for our switch
		norm := arg
		if strings.HasPrefix(arg, "--") {
			norm = arg[1:]
		}

		switch norm {
		case "-with":
			flush()
			polarity = "with"
		case "-without":
			flush()
			polarity = "without"
		case "-contains":
			flush()
			i++
			if i < len(args) {
				accum = []string{args[i]}
			}
		case "-fuzzy":
			flush()
			i++
			if i < len(args) {
				entries = append(entries, filterEntry{polarity, "fuzzy", args[i], 0})
			}
		case "-regex":
			flush()
			i++
			if i < len(args) {
				entries = append(entries, filterEntry{polarity, "regex", args[i], 0})
			}
		case "-tag":
			// R2442, R2451: store the raw sigil-form arg; the
			// shared parser runs at point of use (server, local
			// fallback, and -parse rendering all parse the same
			// string so behavior is identical end-to-end).
			flush()
			i++
			if i < len(args) {
				entries = append(entries, filterEntry{polarity, "tag", args[i], 0})
			}
		case "-file-tag":
			// R2453: file-level tag predicate. Same parser, different
			// chunk filter at execution time.
			flush()
			i++
			if i < len(args) {
				entries = append(entries, filterEntry{polarity, "file-tag", args[i], 0})
			}
		case "-about":
			flush()
			i++
			if i < len(args) {
				entries = append(entries, filterEntry{polarity, "about", args[i], 0})
			}
		case "-files":
			flush()
			i++
			if i < len(args) {
				entries = append(entries, filterEntry{polarity, "files", args[i], 0})
			}
		case "-filter-k":
			flush()
			i++
			if i < len(args) {
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 1 {
					fmt.Fprintf(os.Stderr, "warning: invalid --filter-k value %q -- ignored\n", args[i])
				} else if len(entries) == 0 {
					fmt.Fprintln(os.Stderr, "warning: --filter-k has no prior filter entry to apply to -- ignored")
				} else {
					last := &entries[len(entries)-1]
					if last.mode != "about" {
						fmt.Fprintf(os.Stderr, "warning: --filter-k only applies to -about filters, not %q -- ignored\n", last.mode)
					} else {
						last.k = n
					}
				}
			}
		case "-parse":
			parseOnly = true
		default:
			if strings.HasPrefix(arg, "-") {
				remaining = append(remaining, arg)
				if valuedFlags[norm] {
					i++
					if i < len(args) {
						remaining = append(remaining, args[i])
					}
				}
			} else {
				accum = append(accum, arg)
			}
		}
		i++
	}
	flush()
	return
}

// formatFilterStack prints the disambiguated command for -parse.
// Tag and file-tag rows are decoded via TagMatcher.Describe so the
// user sees the resolved name-mode and value-mode rather than the
// raw sigil string. R2451
// CRC: crc-CLI.md | R1781, R1782
func formatFilterStack(entries []filterEntry) string {
	var parts []string
	parts = append(parts, "ark search")
	pol := "with"
	for _, e := range entries {
		if e.polarity != pol {
			parts = append(parts, "-"+e.polarity)
			pol = e.polarity
		}
		if e.mode == "tag" || e.mode == "file-tag" {
			if p, err := ark.ParseMatchSyntax(e.query); err == nil {
				parts = append(parts, "-"+e.mode+" "+p.Describe())
			} else {
				parts = append(parts, fmt.Sprintf("-%s %q (parse error: %v)", e.mode, e.query, err))
			}
		} else if strings.ContainsAny(e.query, " \t\"") || strings.HasPrefix(e.query, "-") {
			parts = append(parts, fmt.Sprintf("-%s %q", e.mode, e.query))
		} else {
			parts = append(parts, fmt.Sprintf("-%s %s", e.mode, e.query))
		}
		if e.k > 0 {
			parts = append(parts, fmt.Sprintf("--filter-k %d", e.k))
		}
	}
	return strings.Join(parts, " ")
}

// anchorFilterToCwd resolves a -files/-exclude-files glob to an absolute
// pattern relative to cwd, the way UNIX tools treat relative paths. Patterns
// that are already absolute (`/`), home-relative (`~`), or virtual tmp://
// overlay docs are left untouched. CLI-side only — the server cannot know the
// client's cwd. A trailing slash (directory marker) is preserved across the
// join, which filepath.Join would otherwise strip.
// CRC: crc-CLI.md | R2958
func anchorFilterToCwd(glob, cwd string) string {
	if glob == "" ||
		strings.HasPrefix(glob, "/") ||
		strings.HasPrefix(glob, "~") ||
		strings.HasPrefix(glob, "tmp://") {
		return glob
	}
	joined := filepath.Join(cwd, glob)
	if strings.HasSuffix(glob, "/") {
		joined += "/"
	}
	return joined
}

// CRC: crc-CLI.md | R77, R78
func cmdSearch(args []string) {
	// Subcommand dispatch
	if len(args) > 0 && args[0] == "expand" {
		cmdSearchExpand(args[1:])
		return
	}

	// R1770-R1778: parse filter stack before flag.Parse
	filterEntries, remaining, parseOnly := parseFilterStack(args)

	if parseOnly {
		fmt.Println(formatFilterStack(filterEntries))
		return
	}

	// R2958: resolve -files/-exclude-files globs cwd-relative, CLI-side, so
	// both cold-start and server-proxied searches receive absolute patterns
	// (only the CLI knows the client's working directory).
	if cwd, err := os.Getwd(); err == nil {
		for i := range filterEntries {
			if filterEntries[i].mode == "files" {
				filterEntries[i].query = anchorFilterToCwd(filterEntries[i].query, cwd)
			}
		}
	}

	fs := flag.NewFlagSet("search", flag.ExitOnError)
	// CRC: crc-CLI.md | R1788, R1789
	fs.Usage = func() {
		// DSL help is the shared searchHelp const (also the search node's
		// Description), so the authored blurb has one source (R2921); the
		// flag-by-flag list follows under "Output:".
		fmt.Fprintln(os.Stderr, searchHelp+"\n\nOutput:")
		fs.PrintDefaults()
	}
	k := fs.Int("k", 20, "max results")                        // R58
	scores := fs.Bool("scores", false, "show scores")          // R59
	after := fs.String("after", "", "only results after date") // R60
	before := fs.String("before", "", "only results before date")
	likeFile := fs.String("like-file", "", "find similar files using FTS density scoring")
	score := fs.String("score", "", "scoring strategy: auto (default), coverage, density")
	multi := fs.Bool("multi", false, "run all strategies (coverage, density, overlap, bm25)")
	proximity := fs.Bool("proximity", false, "rerank top results by query term proximity")
	// CRC: crc-CLI.md | R654, R655, R656, R681 — --session NAME routes via the server proxy (req.Session, requires a running server; R681: --session always proxies); without it search runs the normal direct/proxy path
	session := fs.String("session", "", "named session for cross-query cache (requires server)")
	noTmp := fs.Bool("no-tmp", false, "exclude tmp:// documents from results")
	tags := fs.Bool("tags", false, "output extracted @tag activity as markdown bullets (see -no-values/-no-chunks/-no-files)")
	noValues := fs.Bool("no-values", false, "with -tags: collapse the value layer (tag → files/chunks)")
	noChunks := fs.Bool("no-chunks", false, "with -tags: drop :range from locations (file paths only)")
	noFiles := fs.Bool("no-files", false, "with -tags: drop file/chunk locations entirely (tag/value counts only)")
	tagsJSON := fs.Bool("json", false, "with -tags: emit TagResult JSONL instead of markdown bullets")
	chunks := fs.Bool("chunks", false, "emit chunk text as JSONL")
	files := fs.Bool("file-content", false, "emit full file content as JSONL")
	preview := fs.Int("preview", 0, "with --chunks: extract N-char preview window around match")
	wrap := fs.String("wrap", "", "wrap output in XML tags (e.g. memory, knowledge)")
	cpuProfile := fs.String("cpuprofile", "", "write CPU profile to file")
	memProfile := fs.String("memprofile", "", "write memory profile to file")
	traceFile := fs.String("trace", "", "write execution trace to file (view with go tool trace or Chrome DevTools)")
	fs.Parse(remaining)

	// CRC: crc-CLI.md | R981, R982, R985
	if *traceFile != "" {
		f, err := os.Create(*traceFile)
		if err != nil {
			fatal(err)
		}
		trace.Start(f)
		defer func() {
			trace.Stop()
			f.Close()
		}()
	}
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer func() {
			pprof.StopCPUProfile()
			f.Close()
		}()
	}
	if *memProfile != "" {
		defer func() {
			f, err := os.Create(*memProfile)
			if err != nil {
				fatal(err)
			}
			runtime.GC()
			pprof.WriteHeapProfile(f)
			f.Close()
		}()
	}

	// CRC: crc-CLI.md | R110
	if *chunks && *files {
		fmt.Fprintln(os.Stderr, "error: --chunks and --file-content are mutually exclusive")
		os.Exit(1)
	}

	var afterTime, beforeTime time.Time
	if *after != "" {
		t, err := ark.ParseDate(*after)
		if err != nil {
			fatal(fmt.Errorf("parse --after: %w", err))
		}
		afterTime = t
	}
	if *before != "" {
		t, err := ark.ParseDate(*before)
		if err != nil {
			fatal(fmt.Errorf("parse --before: %w", err))
		}
		beforeTime = t
	}

	// CRC: crc-CLI.md | R573, R579 — three valid --score modes (auto = default
	// when omitted, coverage, density); any unknown mode errors and exits.
	switch *score {
	case "", "auto", "coverage", "density":
		// valid
	default:
		fmt.Fprintf(os.Stderr, "error: unknown --score mode: %s\n", *score)
		os.Exit(1)
	}

	// R1776-R1778: split filter entries into primary search + chunk filters
	var primaryQuery, primaryAbout, primaryContains string
	var primaryRegex []string
	var primaryFuzzy bool
	// R2442, R2453: primary tag/file-tag predicate in sigil form.
	// The CLI keeps the raw string and lets server / local fallback
	// share one parser instead of marshalling tokens twice.
	var primaryTagQuery string
	var primaryFileTag bool
	var primaryTagPredicate *ark.MatchPredicate
	var chunkFilters []ark.ChunkFilterRow

	if len(filterEntries) > 0 {
		primary := filterEntries[0]
		switch primary.mode {
		case "contains":
			primaryContains = primary.query
		case "fuzzy":
			primaryFuzzy = true
			primaryQuery = primary.query
		case "regex":
			primaryRegex = []string{primary.query}
		case "about":
			primaryAbout = primary.query
		case "tag":
			// R2442, R2451: parse once; the server and the local
			// fallback share the same predicate resolution path.
			if pp, perr := ark.ParseMatchSyntax(primary.query); perr == nil {
				primaryTagQuery = primary.query
				primaryFileTag = false
				primaryTagPredicate = &pp
			}
		case "file-tag":
			// R2453: file-scoped predicate. Same parse, different
			// resolution shape (every chunk on a matching file).
			if pp, perr := ark.ParseMatchSyntax(primary.query); perr == nil {
				primaryTagQuery = primary.query
				primaryFileTag = true
				primaryTagPredicate = &pp
			}
		case "files":
			// files-only primary: becomes a chunk filter, promote next entry to primary
			chunkFilters = append(chunkFilters, ark.ChunkFilterRow{Polarity: primary.polarity, Mode: "files", Query: primary.query, K: primary.k})
			if len(filterEntries) > 1 {
				second := filterEntries[1]
				switch second.mode {
				case "contains":
					primaryContains = second.query
				case "fuzzy":
					primaryFuzzy = true
					primaryQuery = second.query
				case "regex":
					primaryRegex = []string{second.query}
				case "about":
					primaryAbout = second.query
				}
				filterEntries = filterEntries[1:] // shift so [1:] below skips the promoted entry
			}
		}
		for _, e := range filterEntries[1:] {
			chunkFilters = append(chunkFilters, ark.ChunkFilterRow{
				Polarity: e.polarity,
				Mode:     e.mode,
				Query:    e.query,
				K:        e.k,
			})
		}
	}

	if *likeFile == "" && primaryContains == "" && primaryAbout == "" && len(primaryRegex) == 0 && !primaryFuzzy && primaryQuery == "" && primaryTagPredicate == nil {
		fmt.Fprintln(os.Stderr, "error: no search query")
		os.Exit(1)
	}

	isSplit := primaryAbout != "" || primaryContains != "" || len(primaryRegex) > 0 || *likeFile != ""

	// R590: --multi is mutually exclusive with --score
	if *multi && *score != "" {
		fmt.Fprintln(os.Stderr, "error: --multi and --score are mutually exclusive")
		os.Exit(1)
	}
	// CRC: crc-CLI.md | R592 — --multi is an error with --about, --regex, or --like-file
	if *multi && (primaryAbout != "" || len(primaryRegex) > 0 || *likeFile != "") {
		fmt.Fprintln(os.Stderr, "error: --multi cannot be used with --about, --regex, or --like-file")
		os.Exit(1)
	}

	// Determine query string for highlighting/preview
	var queryStr string
	if primaryContains != "" {
		queryStr = primaryContains
	} else if primaryAbout != "" {
		queryStr = primaryAbout
	} else if len(primaryRegex) > 0 {
		queryStr = primaryRegex[0]
	} else {
		queryStr = primaryQuery
	}

	// Server-first: proxy to server if available, fall back to local.
	// Server keeps caches warm (file name map, LMDB pages).
	// CRC: crc-CLI.md | R1783, R1784
	if client := serverClient(arkDir); client != nil {
		// R1786: old filter flags removed — filter stack subsumes them
		req := struct {
			Query           string               `json:"query"`
			About           string               `json:"about,omitempty"`
			Contains        string               `json:"contains,omitempty"`
			Regex           []string             `json:"regex,omitempty"`
			LikeFile        string               `json:"likeFile,omitempty"`
			Fuzzy           bool                 `json:"fuzzy,omitempty"`
			K               int                  `json:"k"`
			Scores          bool                 `json:"scores,omitempty"`
			After           string               `json:"after,omitempty"`
			Before          string               `json:"before,omitempty"`
			Chunks          bool                 `json:"chunks,omitempty"`
			Files           bool                 `json:"files,omitempty"`
			Tags            bool                 `json:"tags,omitempty"`
			ChunkFilters    []ark.ChunkFilterRow `json:"chunkFilters,omitempty"`
			Session         string               `json:"session,omitempty"`
			NoTmp           bool                 `json:"noTmp,omitempty"`
			PrimaryTagQuery string               `json:"primaryTagQuery,omitempty"`
			PrimaryFileTag  bool                 `json:"primaryFileTag,omitempty"`
		}{
			Query:           primaryQuery,
			About:           primaryAbout,
			Contains:        primaryContains,
			Regex:           primaryRegex,
			LikeFile:        *likeFile,
			Fuzzy:           primaryFuzzy,
			K:               *k,
			Scores:          *scores,
			After:           *after,
			Before:          *before,
			Chunks:          *chunks,
			Files:           *files,
			Tags:            *tags,
			ChunkFilters:    chunkFilters,
			Session:         *session,
			NoTmp:           *noTmp,
			PrimaryTagQuery: primaryTagQuery,
			PrimaryFileTag:  primaryFileTag,
		}
		if *tags {
			// Server returns []ark.TagResult for Tags:true; decode into that
			// shape (previously decoded into []SearchResultEntry, silently
			// producing empty output).
			// CRC: crc-CLI.md | R2432, R2441
			var tagResults []ark.TagResult
			if err := proxyDecode(client, "POST", "/search", req, &tagResults); err == nil {
				if *tagsJSON {
					emitTagsJSONL(tagResults)
				} else {
					printTagsBabyFood(tagResults, buildTagsCfg(filterEntries, *noValues, *noChunks, *noFiles), *scores)
				}
				return
			}
		} else {
			var results []ark.SearchResultEntry
			if err := proxyDecode(client, "POST", "/search", req, &results); err == nil {
				printSearchResults(results, *scores, *chunks, *files, *wrap, *preview, queryStr)
				return
			}
		}
		// Server proxy failed — fall through to local search
	}

	// Local LMDB path (fallback when server not running)
	withDB(func(d *ark.DB) {
		done := d.NewSearchCache()
		defer done()
		opts := ark.SearchOpts{
			K:         *k,
			Scores:    *scores,
			After:     afterTime,
			Before:    beforeTime,
			About:     primaryAbout,
			Contains:  primaryContains,
			Regex:     primaryRegex,
			LikeFile:  *likeFile,
			Tags:      *tags,
			Score:     *score,
			Multi:     *multi,
			Fuzzy:     primaryFuzzy,
			Proximity: *proximity,
			NoTmp:     *noTmp,
			// R2951: index-lookup primaries (tag/file-tag/about) apply the
			// post-filter stack via opts.ChunkFilters in SearchTagChunks /
			// the vec-only path — the server path sets this in buildSearchOpts;
			// the local path must too, or the funnel sees an empty stack.
			ChunkFilters: chunkFilters,
		}

		// CRC: crc-CLI.md | R1936 — about queries (primary or filter)
		// require the warm embedding model owned by `ark serve`.
		// Cold-path warm-up per CLI invocation isn't worth the cost.
		hasAboutFilter := false
		for _, row := range chunkFilters {
			if row.Mode == "about" && row.Query != "" {
				hasAboutFilter = true
				break
			}
		}
		if primaryAbout != "" || hasAboutFilter {
			fatal(fmt.Errorf("about queries require a running server — start `ark serve` first"))
		}

		// R1784, R1787: wire non-about chunk filter rows.
		if len(chunkFilters) > 0 {
			ar, err := ark.ResolveAboutFilters(chunkFilters, "", *k*2, nil, d.Store(), d.Config())
			if err != nil {
				fatal(err)
			}
			opts.SetExtraOpts(ar.Early...)
			if paths, pathErr := d.FTS().FileIDPaths(); pathErr == nil {
				cache := d.FTS().NewChunkCache()
				opts.SetExtraOpts(ark.BuildChunkFilters(ar.Remaining, cache, paths, d.Store())...)
			}
			opts.SetExtraOpts(ar.Late...)
		}

		var results []ark.SearchResultEntry
		var err error
		query := primaryQuery
		switch {
		case primaryTagPredicate != nil && !isSplit && !primaryFuzzy && !*multi:
			// R2442, R2453: predicate-driven bypass (local fallback).
			chunkIDs := d.ResolveTagPredicateChunks(*primaryTagPredicate, primaryFileTag)
			results, err = d.SearchTagChunks(chunkIDs, opts)
		// CRC: crc-CLI.md | R585, R591 — --multi runs all strategies; works with a combined query or --contains
		case *multi:
			if query == "" && primaryContains != "" {
				query = primaryContains
			}
			results, err = d.SearchMulti(query, opts)
		case primaryFuzzy:
			// CRC: crc-CLI.md | R739, R741, R742, R743 — --fuzzy is a primary mode taking a positional query; filters/proximity/-k/--chunks/--file-content/--tags/--scores/--no-tmp compose via the shared opts
			results, err = d.SearchFuzzy(query, opts)
		case isSplit:
			results, err = d.SearchSplit(opts)
		default:
			results, err = d.SearchCombined(query, opts)
		}
		if err != nil {
			fatal(err)
		}

		// CRC: crc-Searcher.md | R114 — fills apply after any search mode (combined/split/fuzzy/multi/tag)
		if *tags || *chunks {
			results, err = d.FillChunks(results)
			if err != nil {
				fatal(err)
			}
		} else if *files {
			results, err = d.FillFiles(results)
			if err != nil {
				fatal(err)
			}
		}

		// CRC: crc-CLI.md | R189 — -tags switches output to extracted @tag vocabulary
		if *tags {
			tagResults := ark.ExtractResultTags(results)
			if *tagsJSON {
				emitTagsJSONL(tagResults)
			} else {
				printTagsBabyFood(tagResults, buildTagsCfg(filterEntries, *noValues, *noChunks, *noFiles), *scores)
			}
		} else {
			printSearchResults(results, *scores, *chunks, *files, *wrap, *preview, queryStr)
		}
	})
}

// CRC: crc-CLI.md | R178, R181 — ark search --wrap <name> emits results in XML
// tags named by the arg; works with both --chunks and --files output modes.
func printSearchResults(results []ark.SearchResultEntry, scores, chunks, files bool, wrap string, previewN int, query string) {
	if wrap != "" {
		for _, r := range results {
			if chunks {
				fmt.Printf("<%s source=%q range=%q>\n", wrap, r.Path, r.Range)
				writeEscaped(os.Stdout, r.Text, wrap)
				fmt.Printf("</%s>\n", wrap)
			} else if files {
				fmt.Printf("<%s source=%q>\n", wrap, r.Path)
				writeEscaped(os.Stdout, r.Text, wrap)
				fmt.Printf("</%s>\n", wrap)
			} else {
				fmt.Printf("<%s source=%q range=%q />\n", wrap, r.Path, r.Range)
			}
		}
		return
	}
	enc := json.NewEncoder(os.Stdout)
	for _, r := range results {
		if chunks {
			cr := ark.ChunkResult{
				Path:  r.Path,
				Range: r.Range,
				Score: r.Score,
				Text:  r.Text,
			}
			if previewN > 0 {
				cr.Preview = ark.ExtractPreview(r.Text, query, previewN)
				cr.Text = "" // omit full text when preview is requested
			}
			enc.Encode(cr)
		} else if files {
			enc.Encode(ark.FileResult{
				Path:  r.Path,
				Score: r.Score,
				Text:  r.Text,
			})
		} else if scores {
			// R599, R600: show strategy when multi-search produced the result
			if r.Strategy != "" {
				fmt.Printf("%s:%s\t%.4f\t%s\n", r.Path, r.Range, r.Score, r.Strategy)
			} else {
				fmt.Printf("%s:%s\t%.4f\n", r.Path, r.Range, r.Score)
			}
		} else {
			// R51: output format is filepath:range (startline-endline), score added under --scores above
			fmt.Printf("%s:%s\n", r.Path, r.Range)
		}
	}
}

// tagsCfg drives -tags output formatting: which subtrees the agent
// already knows about (and so can be suppressed), and how deep to
// descend before stopping.
// CRC: crc-CLI.md | R2433, R2435, R2436, R2437, R2438, R2439, R2440
type tagsCfg struct {
	noValues  bool                       // collapse value layer
	noChunks  bool                       // file paths only, no :range
	noFiles   bool                       // no locations at all (implies noChunks)
	hideName  map[string]bool            // -with -tag NAME → hide @NAME header
	hideValue map[string]map[string]bool // -with -tag NAME:VALUE → hide that value's header
}

// buildTagsCfg scans the parsed filter stack for `-with -tag NAME[:VALUE]`
// entries and records which subtrees to suppress in the -tags output. The
// agent supplied the filter, so it already knows the prefix — echoing it
// is pure token cost. `-without -tag …` never triggers suppression: the
// agent asked to exclude that tag and showing what else is there is the
// useful answer.
// CRC: crc-CLI.md | R2435, R2436, R2440
func buildTagsCfg(filters []filterEntry, noValues, noChunks, noFiles bool) tagsCfg {
	cfg := tagsCfg{
		noValues:  noValues,
		noChunks:  noChunks,
		noFiles:   noFiles,
		hideName:  make(map[string]bool),
		hideValue: make(map[string]map[string]bool),
	}
	for _, f := range filters {
		if f.polarity != "with" || f.mode != "tag" {
			continue
		}
		// R2442: parse via the shared sigil parser. Only exact-name
		// rows feed adaptive suppression — regex / contains rows can
		// hide an unbounded set of tags, which would over-suppress.
		p, err := ark.ParseMatchSyntax(f.query)
		if err != nil || p.NameMode != ark.NameExact {
			continue
		}
		nameStr := strings.ToLower(p.NameStr)
		cfg.hideName[nameStr] = true
		if p.ValueMode != ark.ValueContains || len(p.ValueTokens) == 0 {
			continue
		}
		if cfg.hideValue[nameStr] == nil {
			cfg.hideValue[nameStr] = make(map[string]bool)
		}
		for _, tok := range p.ValueTokens {
			cfg.hideValue[nameStr][tok] = true
		}
	}
	return cfg
}

// emitTagsJSONL writes one TagResult per line as JSON for programmatic
// consumers. Suppression flags and adaptive defaults are intentionally
// not applied — JSON is the raw structured view; consumers filter
// themselves (jq, etc.).
// CRC: crc-CLI.md | R2441
func emitTagsJSONL(tags []ark.TagResult) {
	enc := json.NewEncoder(os.Stdout)
	for _, t := range tags {
		enc.Encode(t)
	}
}

// printTagsBabyFood renders extracted tag activity as a markdown bullet
// tree: @tag → value → file → :range. Layers are dropped according to
// cfg — both the adaptive suppression from -with -tag filters and the
// explicit -no-values / -no-chunks / -no-files flags. The output is
// stable across invocations: tags sorted by count desc (ties by name),
// values by count desc (ties by value), locations in extraction order.
// CRC: crc-CLI.md | R192, R2433, R2435, R2436, R2437, R2438, R2439, R2440 —
// R192: with -scores, the tag header carries the best chunk score [%.4f].
func printTagsBabyFood(tags []ark.TagResult, cfg tagsCfg, scores bool) {
	for _, t := range tags {
		emitTag(t, cfg, scores)
	}
}

func emitTag(t ark.TagResult, cfg tagsCfg, scores bool) {
	baseIndent := 0
	showName := !cfg.hideName[t.Tag]
	if showName {
		header := fmt.Sprintf("- @%s (%d", t.Tag, t.Count)
		if t.FileCount > 1 {
			header += fmt.Sprintf(" in %d files", t.FileCount)
		}
		header += ")"
		if scores {
			header += fmt.Sprintf(" [%.4f]", t.BestScore)
		}
		fmt.Println(header)
		baseIndent = 2
	}
	if cfg.noValues {
		emitLocations(flattenValueLocations(t.Values), cfg, baseIndent)
		return
	}
	for _, v := range t.Values {
		valIndent := baseIndent
		hidden := cfg.hideValue[t.Tag][v.Value]
		if !hidden {
			valLabel := v.Value
			if valLabel == "" {
				valLabel = "(no value)"
			}
			fmt.Printf("%s- %s (%d)\n", spaces(baseIndent), valLabel, v.Count)
			valIndent = baseIndent + 2
		}
		emitLocations(v.Locations, cfg, valIndent)
	}
}

// flattenValueLocations merges every value's locations into a single
// list, preserving extraction order across values.
// CRC: crc-CLI.md | R2437
func flattenValueLocations(values []ark.TagValueGroup) []ark.TagLocation {
	var out []ark.TagLocation
	for _, v := range values {
		out = append(out, v.Locations...)
	}
	return out
}

// emitLocations prints the location layer per cfg: file:range entries,
// file-only entries (deduped), or nothing.
// CRC: crc-CLI.md | R2438, R2439
func emitLocations(locs []ark.TagLocation, cfg tagsCfg, indent int) {
	if cfg.noFiles {
		return
	}
	if cfg.noChunks {
		seen := make(map[string]bool)
		for _, loc := range locs {
			if seen[loc.Path] {
				continue
			}
			seen[loc.Path] = true
			fmt.Printf("%s- %s\n", spaces(indent), loc.Path)
		}
		return
	}
	for _, loc := range locs {
		fmt.Printf("%s- %s:%s\n", spaces(indent), loc.Path, loc.Range)
	}
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// CRC: crc-CLI.md | Seq: seq-spectral-expand.md | R1379
func cmdSearchExpand(args []string) {
	fs := flag.NewFlagSet("search expand", flag.ExitOnError)
	wait := fs.Bool("wait", false, "lotto tube: block until expansion requests arrive, print as JSON")
	fuzzy := fs.String("fuzzy", "", "fuzzy match: JSON array of {tag,value} alternatives")
	search := fs.String("search", "", "search: JSON array of {tag,value} pairs, return chunk results")
	resultFlag := fs.String("result", "", "post result: REQUEST_ID (result JSON as second arg)")
	errorFlag := fs.String("error", "", "post error: REQUEST_ID=ERROR_MESSAGE")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark search expand [options] <tag> [value]

Expand a tag search via spectral search (Haiku-powered query expansion).
Requires a running server with claude on PATH.

Subcommands (for sidecar agent use):
  --wait              Lotto tube: block until expansion requests arrive
  --fuzzy JSON        Fuzzy match: JSON array of {tag,value} alternatives
  --search JSON       Search: JSON array of curated {tag,value} pairs, return chunks
  --result ID JSON    Post result JSON for request ID (JSON as trailing arg)
  --error ID=MESSAGE  Post error for request ID

Options:`)
		fs.PrintDefaults()
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fs.Usage()
		os.Exit(0)
	}
	fs.Parse(args)

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}

	if *wait {
		// Lotto tube: block until requests arrive
		data, err := proxyRaw(client, "GET", "/search/curate/wait", nil)
		if err != nil {
			fatal(err)
		}
		fmt.Print(string(data))
		return
	}

	if *fuzzy != "" {
		// Fuzzy match alternatives against V records
		var alts []struct {
			Tag   string `json:"tag"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(*fuzzy), &alts); err != nil {
			fatal(fmt.Errorf("parsing fuzzy JSON: %w", err))
		}
		data, err := proxyRaw(client, "POST", "/search/expand/fuzzy", alts)
		if err != nil {
			fatal(err)
		}
		fmt.Print(string(data))
		return
	}

	if *search != "" {
		// Search curated tag/value pairs, return chunk-level results
		var alts []struct {
			Tag   string `json:"tag"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(*search), &alts); err != nil {
			fatal(fmt.Errorf("parsing search JSON: %w", err))
		}
		data, err := proxyRaw(client, "POST", "/search/expand/search", alts)
		if err != nil {
			fatal(err)
		}
		fmt.Print(string(data))
		return
	}

	if *resultFlag != "" {
		// Search curated pairs and post result for request ID in one step
		rest := fs.Args()
		if len(rest) == 0 {
			fatal(fmt.Errorf("--result requires curated JSON as trailing argument"))
		}
		// Search the curated pairs
		var alts []struct {
			Tag   string `json:"tag"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(rest[0]), &alts); err != nil {
			fatal(fmt.Errorf("parsing curated JSON: %w", err))
		}
		searchData, err := proxyRaw(client, "POST", "/search/expand/search", alts)
		if err != nil {
			// Post error if search fails
			proxyOK(client, "POST", "/search/curate/result", map[string]any{
				"id":    *resultFlag,
				"error": err.Error(),
			})
			fatal(err)
		}
		// Post search results
		err = proxyOK(client, "POST", "/search/curate/result", map[string]any{
			"id":      *resultFlag,
			"results": json.RawMessage(searchData),
		})
		if err != nil {
			fatal(err)
		}
		return
	}

	if *errorFlag != "" {
		// Post error for a request ID: --error ID=MESSAGE
		parts := strings.SplitN(*errorFlag, "=", 2)
		id := parts[0]
		msg := "unknown error"
		if len(parts) > 1 {
			msg = parts[1]
		}
		err := proxyOK(client, "POST", "/search/curate/result", map[string]any{
			"id":    id,
			"error": msg,
		})
		if err != nil {
			fatal(err)
		}
		return
	}

	// Interactive: queue expansion and wait for result
	rest := fs.Args()
	if len(rest) < 1 {
		fs.Usage()
		os.Exit(1)
	}
	tag := rest[0]
	value := ""
	if len(rest) > 1 {
		value = strings.Join(rest[1:], " ")
	}

	// Queue the request
	var queued struct {
		RequestID string `json:"requestId"`
	}
	err := proxyDecode(client, "POST", "/search/curate", map[string]string{
		"tag":   tag,
		"value": value,
	}, &queued)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "queued expansion %s — waiting for sidecar agent...\n", queued.RequestID)

	// Stubbornly poll for result — retry until done, like the lotto tube
	var result struct {
		ID      string          `json:"id"`
		Results json.RawMessage `json:"results"`
		Error   string          `json:"error,omitempty"`
		Done    bool            `json:"done"`
	}
	for {
		err = proxyDecode(client, "GET", "/search/curate/result/"+queued.RequestID, nil, &result)
		if err != nil {
			// Server may have restarted — sleep and retry
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if result.Done {
			break
		}
		// Not done yet — the server-side WaitForResult timed out.
		// Sleep briefly and retry.
		time.Sleep(250 * time.Millisecond)
	}
	if result.Error != "" {
		fatal(fmt.Errorf("expansion failed: %s", result.Error))
	}
	// Pretty-print the result
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result.Results, "", "  "); err != nil {
		fmt.Print(string(result.Results))
	} else {
		fmt.Print(pretty.String())
	}
	fmt.Println()
}

// parseConnectionsInputs converts positional tokens to ConnectionsInput
// entries. typeHint = "" auto-detects each token (decimal → chunkID,
// `path:locator` → range, anything else → text). typeHint = "chunk"
// forces every token to a chunk reference (still chunkID-or-path:locator
// by shape; non-chunk-shaped tokens error). typeHint = "text" treats
// every token literally. R2616
func parseConnectionsInputs(tokens []string, typeHint string) ([]ark.ConnectionsInput, error) {
	out := make([]ark.ConnectionsInput, 0, len(tokens))
	for _, tok := range tokens {
		switch typeHint {
		case "chunk":
			if n, err := strconv.ParseUint(tok, 10, 64); err == nil {
				out = append(out, ark.ConnectionsInput{ChunkID: n})
				continue
			}
			path, rng, ok := splitPathLocator(tok)
			if !ok {
				return nil, fmt.Errorf("--type chunk: %q must be a decimal chunkID, PATH:N, or PATH:N-M", tok)
			}
			out = append(out, ark.ConnectionsInput{Path: path, Range: rng})
		case "text":
			out = append(out, ark.ConnectionsInput{Text: tok})
		case "":
			// Auto-detect.
			if n, err := strconv.ParseUint(tok, 10, 64); err == nil {
				out = append(out, ark.ConnectionsInput{ChunkID: n})
				continue
			}
			if path, rng, ok := splitPathLocator(tok); ok {
				out = append(out, ark.ConnectionsInput{Path: path, Range: rng})
				continue
			}
			out = append(out, ark.ConnectionsInput{Text: tok})
		default:
			return nil, fmt.Errorf("--type: unknown value %q (want chunk | text)", typeHint)
		}
	}
	return out, nil
}

func splitPathLocator(tok string) (path, rng string, ok bool) {
	colon := strings.LastIndexByte(tok, ':')
	if colon <= 0 || colon >= len(tok)-1 {
		return "", "", false
	}
	return tok[:colon], tok[colon+1:], true
}

// waitConnectionsDoc polls /fetch?path=PATH until @connections-status is
// terminal or the timeout elapses. Returns the markdown body or, with
// asJSON, the JSON projection of the parsed doc.
func waitConnectionsDoc(client *http.Client, path string, timeoutSec int, asJSON bool) (string, error) {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		body, err := proxyRaw(client, "GET", "/fetch?path="+url.QueryEscape(path), nil)
		if err == nil {
			doc := ark.ParseConnectionsDoc(body)
			if doc.Status == "completed" || doc.Status == "errored" {
				if asJSON {
					out, _ := json.MarshalIndent(doc, "", "  ")
					return string(out) + "\n", nil
				}
				return string(body), nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Best-effort last-status report on timeout.
	body, _ := proxyRaw(client, "GET", "/fetch?path="+url.QueryEscape(path), nil)
	doc := ark.ParseConnectionsDoc(body)
	fmt.Fprintf(os.Stderr, "timeout after %ds; last-seen status: %s\n", timeoutSec, doc.Status)
	os.Exit(1)
	return "", nil
}

func renderConnectionsShow(doc *ark.ConnectionsDoc) {
	fmt.Printf("status:        %s\n", doc.Status)
	fmt.Printf("mode:          %s\n", doc.Mode)
	fmt.Printf("purpose:       %s\n", doc.Purpose)
	if doc.Warning != "" {
		fmt.Printf("warning:       %s\n", doc.Warning)
	}
	if doc.Error != "" {
		fmt.Printf("error:         %s\n", doc.Error)
	}
	fmt.Printf("requestID:     %s\n", doc.RequestID)
	fmt.Printf("proposals:     %d\n", len(doc.Proposals))
	if len(doc.Proposals) == 0 {
		return
	}
	fmt.Println()
	for i, p := range doc.Proposals {
		switch p.Kind {
		case "tag-name":
			fmt.Printf("[%d] %s (score=%.4f)\n", i+1, p.Value, p.Score)
		case "theme":
			fmt.Printf("[%d] theme: %s\n", i+1, p.Text)
		case "shared-tag":
			fmt.Printf("[%d] @%s: %s\n", i+1, p.Tag, p.Value)
		default:
			fmt.Printf("[%d] %s\n", i+1, p.Kind)
		}
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// projectSessionIDs enumerates `<session-uuid>.jsonl` files under
// ~/.claude/projects/<encoded-cwd>/ where encoded-cwd replaces every
// "/" with "-" (Claude Code's convention). Returns the basenames
// without extension. R2744
func projectSessionIDs() ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	encoded := strings.ReplaceAll(cwd, "/", "-")
	dir := filepath.Join(home, ".claude", "projects", encoded)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".jsonl"))
	}
	return ids, nil
}

func cmdConnectionsSidecarWait(_ []string) {
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	data, err := proxyRaw(client, "GET", "/connections/wait", nil)
	if err != nil {
		fatal(err)
	}
	fmt.Print(string(data))
}

func cmdConnectionsSidecarFetch(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: ark connections sidecar-fetch ID")
		os.Exit(1)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	data, err := proxyRaw(client, "GET", "/connections/fetch?id="+url.QueryEscape(args[0]), nil)
	if err != nil {
		fatal(err)
	}
	fmt.Print(string(data))
}

func cmdConnectionsSidecarResult(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: ark connections sidecar-result ID")
		os.Exit(1)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	var payload ark.ConnectionsResult
	dec := json.NewDecoder(os.Stdin)
	if err := dec.Decode(&payload); err != nil {
		fatal(fmt.Errorf("parsing result JSON from stdin: %w", err))
	}
	if err := proxyOK(client, "POST", "/connections/result", map[string]any{
		"id":     args[0],
		"result": payload,
	}); err != nil {
		fatal(err)
	}
}

func cmdConnectionsSidecarError(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ark connections sidecar-error ID MESSAGE")
		os.Exit(1)
	}
	id := args[0]
	msg := ""
	if len(args) > 1 {
		msg = strings.Join(args[1:], " ")
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	if err := proxyOK(client, "POST", "/connections/error", map[string]any{
		"id":      id,
		"message": msg,
	}); err != nil {
		fatal(err)
	}
}

func cmdEmbedBenchTags(db *ark.DB, lib *ark.Librarian) {
	tags, err := db.TagList()
	if err != nil {
		fatal(err)
	}
	var texts []string
	for _, tc := range tags {
		values, err := db.Store().QueryTagValues(tc.Tag, "")
		if err != nil {
			continue
		}
		for _, v := range values {
			texts = append(texts, strings.ReplaceAll(tc.Tag, "-", " ")+": "+v.Value)
		}
	}
	fmt.Printf("collected %d tag values\n", len(texts))

	batchSize := 50
	start := time.Now()
	var batchTotal int
	for i := 0; i < len(texts); i += batchSize {
		end := min(i+batchSize, len(texts))
		vecs, err := lib.EmbedBatch(texts[i:end])
		if err != nil {
			fmt.Fprintf(os.Stderr, "batch error at %d: %v\n", i, err)
			continue
		}
		batchTotal += len(vecs)
	}
	batchElapsed := time.Since(start)
	fmt.Printf("batch(%d): embedded %d values in %v (%.1f ms/value)\n",
		batchSize, batchTotal, batchElapsed,
		float64(batchElapsed.Milliseconds())/float64(max(batchTotal, 1)))

	start = time.Now()
	var singleTotal int
	for _, text := range texts {
		if _, err := lib.EmbedQuery(text); err != nil {
			continue
		}
		singleTotal++
	}
	singleElapsed := time.Since(start)
	fmt.Printf("single:    embedded %d values in %v (%.1f ms/value)\n",
		singleTotal, singleElapsed,
		float64(singleElapsed.Milliseconds())/float64(max(singleTotal, 1)))

	fmt.Printf("speedup: %.1fx\n", float64(singleElapsed)/float64(batchElapsed))
}

func cmdEmbedBenchChunks(db *ark.DB, lib *ark.Librarian, ctxSize, parallel int) {
	files, err := db.Files()
	if err != nil {
		fatal(err)
	}
	if len(files) == 0 {
		fmt.Println("no indexed files")
		return
	}

	const sampleSize = 200
	fileChunkCache := make(map[string][]string)
	var chunks []string
	var minBytes, maxBytes int

	for len(chunks) < sampleSize {
		fpath := files[rand.Intn(len(files))]
		cached, ok := fileChunkCache[fpath]
		if !ok {
			results := db.AllChunks(fpath)
			for _, cr := range results {
				if cr.Content != "" {
					cached = append(cached, cr.Content)
				}
			}
			fileChunkCache[fpath] = cached
		}
		if len(cached) == 0 {
			continue
		}
		c := cached[rand.Intn(len(cached))]
		chunks = append(chunks, c)
		n := len(c)
		if minBytes == 0 || n < minBytes {
			minBytes = n
		}
		if n > maxBytes {
			maxBytes = n
		}
	}

	var totalBytes int
	for _, c := range chunks {
		totalBytes += len(c)
	}
	fmt.Printf("sampled %d chunks from %d files (avg %d bytes, min %d, max %d)\n",
		len(chunks), len(fileChunkCache), totalBytes/max(len(chunks), 1), minBytes, maxBytes)
	fmt.Printf("context: %d tokens, parallel: %d, tokens/seq: %d\n",
		ctxSize, parallel, ctxSize/parallel)

	seqMax := parallel
	seqTokens := ctxSize / seqMax
	seqBytes := seqTokens * 3
	start := time.Now()
	var batchTotal, batchCount, skipped int
	var batch []string
	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		vecs, err := lib.EmbedBatch(batch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "batch error (%d items): %v\n", len(batch), err)
		} else {
			batchTotal += len(vecs)
		}
		batchCount++
		batch = batch[:0]
	}
	for _, c := range chunks {
		if len(c) > seqBytes {
			skipped++
			continue
		}
		batch = append(batch, c)
		if len(batch) >= seqMax {
			flushBatch()
		}
	}
	flushBatch()
	batchElapsed := time.Since(start)
	fmt.Printf("batch(%d): embedded %d chunks in %d dispatches, %v (%.1f ms/chunk)\n",
		seqMax, batchTotal, batchCount, batchElapsed,
		float64(batchElapsed.Milliseconds())/float64(max(batchTotal, 1)))
	if skipped > 0 {
		fmt.Printf("           skipped %d chunks exceeding %d-byte seq limit\n", skipped, seqBytes)
	}

	start = time.Now()
	var embedded int
	for _, c := range chunks {
		if len(c) > seqBytes {
			continue
		}
		if _, err := lib.EmbedQuery(c); err != nil {
			continue
		}
		embedded++
	}
	singleElapsed := time.Since(start)
	fmt.Printf("single:    embedded %d chunks in %v (%.1f ms/chunk)\n",
		embedded, singleElapsed,
		float64(singleElapsed.Milliseconds())/float64(max(embedded, 1)))
	fmt.Printf("speedup: %.1fx\n", float64(singleElapsed)/float64(batchElapsed))
}

// CRC: crc-CLI.md | R2085
// CRC: crc-CLI.md, crc-Server.md | R79, R170, R171
func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	noScan := fs.Bool("no-scan", false, "skip startup reconciliation")
	force := fs.Bool("force", false, "accept config changes, clear error conditions")
	compact := fs.Bool("compact", false, "compact LMDB via mdb_env_copy2 before opening (overrides ark.toml auto_compact)")
	fs.Parse(args)

	// CRC: crc-CLI.md | R2126, R2127
	// -compact (when supplied) overrides ark.toml auto_compact;
	// when absent, fall back to the toml setting (default false).
	compactSupplied := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "compact" {
			compactSupplied = true
		}
	})
	resolvedCompact := *compact
	if !compactSupplied {
		if cfg, err := ark.LoadConfig(filepath.Join(arkDir, "ark.toml")); err == nil {
			resolvedCompact = cfg.AutoCompact
		}
	}

	err := ark.Serve(arkDir, ark.ServeOpts{NoScan: *noScan, Force: *force, Compact: resolvedCompact})
	if errors.Is(err, ark.ServerAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "ark server already running")
		os.Exit(0)
	}
	if err != nil {
		fatal(err)
	}
}

// CRC: crc-CLI.md, crc-DB.md | R250, R255, R256 — output order (files, stale, missing, unresolved, chunks, sources, strategies, map, server); new fields after existing; map in human-readable MB/GB via formatBytes
func printStatus(status *ark.StatusInfo, serverRunning bool) {
	server := "not running"
	if serverRunning {
		server = "running (v" + status.Version + ")"
	}
	if status.DBFormat != "" {
		fmt.Printf("db format: %s\n", status.DBFormat)
	}
	// R605, R606: total size parenthesized after file count
	fmt.Printf("files: %d (%s)\nstale: %d\nmissing: %d\nunresolved: %d\n",
		status.Files, formatBytes(status.TotalSize), status.Stale, status.Missing, status.Unresolved)
	fmt.Printf("chunks: %d\nsources: %d\n", status.Chunks, status.Sources)
	if status.TmpFiles > 0 {
		fmt.Printf("tmp files: %d\n", status.TmpFiles)
	}
	if len(status.Strategies) > 0 {
		fmt.Print("strategies:")
		// R2953: sort by name — Go map iteration is otherwise random, making the
		// strategies line non-deterministic and hard to scan.
		names := make([]string, 0, len(status.Strategies))
		for name := range status.Strategies {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf(" %s=%d", name, status.Strategies[name])
		}
		fmt.Println()
	}
	if status.MapTotal > 0 {
		pct := float64(status.MapUsed) / float64(status.MapTotal) * 100
		fmt.Printf("map: %s / %s (%.0f%%)\n",
			formatBytes(status.MapUsed), formatBytes(status.MapTotal), pct)
	}
	fmt.Printf("server: %s\n", server)
	if status.UIRunning {
		fmt.Printf("ui: running (port %d)\n", status.UIPort)
	} else if serverRunning {
		fmt.Println("ui: not available")
	}
}

// CRC: crc-CLI.md | R2473, R2474, R2475, R2476
func printDBCounts(counts *ark.DBRecordCounts, mapUsed, mapTotal int64) {
	var totalRecs int64
	var totalKeys, totalVals int64
	printRecordSection("microfts2", counts.Microfts2, &totalRecs, &totalKeys, &totalVals)
	printRecordSection("ark", counts.Ark, &totalRecs, &totalKeys, &totalVals)
	data := totalKeys + totalVals
	fmt.Printf("\ndb total: %d records, %s keys, %s vals (%s data",
		totalRecs, formatBytes(totalKeys), formatBytes(totalVals), formatBytes(data))
	if mapUsed > 0 {
		fmt.Printf(" in %s map", formatBytes(mapUsed))
	}
	fmt.Println(")")
}

func printRecordSection(name string, recs []ark.RecordCount, totalRecs *int64, totalKeys, totalVals *int64) {
	fmt.Printf("\ndb: %s\n", name)
	for _, r := range recs {
		fmt.Printf("  %-2s %-16s %7d  keys %-10s  vals %s\n",
			r.Prefix, r.Purpose, r.Count,
			formatBytes(r.KeyBytes), formatBytes(r.ValueBytes))
		*totalRecs += r.Count
		*totalKeys += r.KeyBytes
		*totalVals += r.ValueBytes
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// writeEscaped writes s to w, escaping the closing tag to prevent premature tag closure.
// CRC: crc-CLI.md | R180 — </tag> occurrences in content become &lt;/tag> so the wrap XML stays valid.
func writeEscaped(w io.Writer, s string, tag string) {
	closing := "</" + tag + ">"
	for {
		idx := strings.Index(s, closing)
		if idx < 0 {
			io.WriteString(w, s)
			return
		}
		io.WriteString(w, s[:idx])
		io.WriteString(w, "&lt;/"+tag+">")
		s = s[idx+len(closing):]
	}
}

// filterPaths returns only paths matching at least one pattern.
// If patterns is empty, returns all paths unchanged.
func filterPaths(paths []string, patterns []string) []string {
	if len(patterns) == 0 {
		return paths
	}
	m := &ark.Matcher{Dotfiles: true}
	var out []string
	for _, p := range paths {
		for _, pat := range patterns {
			if m.Match(pat, p, "", false) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

// CRC: crc-CLI.md | R80, R1514, R1515, R1516, R1523, R1524, R1525, R1526, R1527, R1528, R1531
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	showDB := fs.Bool("db", false, "show LMDB record counts by type")
	showChunks := fs.Bool("chunks", false, "show chunk size statistics")
	tokenize := fs.Bool("tokenize", false, "measure in tokens (requires tag_model)")
	var filterFiles, excludeFiles stringSlice
	fs.Var(&filterFiles, "filter-files", "path-based positive filter (repeatable, glob pattern)")
	fs.Var(&excludeFiles, "exclude-files", "path-based negative filter (repeatable, glob pattern)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark status [options]")
		fmt.Fprintln(os.Stderr, "\nShow database status. With --chunks, show chunk size statistics.")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	proxied := false
	if client := serverClient(arkDir); client != nil {
		path := "/status"
		if *showDB {
			path = "/status?db=true"
		}
		var resp struct {
			ark.StatusInfo
			DB *ark.DBRecordCounts `json:"db"`
		}
		if err := proxyDecode(client, "GET", path, nil, &resp); err != nil {
			fatal(err)
		}
		printStatus(&resp.StatusInfo, true)
		if resp.DB != nil {
			printDBCounts(resp.DB, resp.MapUsed, resp.MapTotal)
		}
		if resp.Version != ark.Version {
			fmt.Fprintf(os.Stderr, "WARNING: server is v%s but CLI is v%s — restart server to match\n",
				resp.Version, ark.Version)
		}
		if !*showChunks {
			return
		}
		proxied = true
	}

	withDB(func(d *ark.DB) {
		if !proxied {
			status, err := d.Status()
			if err != nil {
				fatal(err)
			}
			printStatus(status, false)
			if *showDB {
				dbCounts, err := d.StatusDB()
				if err != nil {
					fatal(err)
				}
				printDBCounts(dbCounts, status.MapUsed, status.MapTotal)
			}
		}

		// CRC: crc-CLI.md | R1565, R1566, R1567
		eRecords, _ := d.Store().ReadERecords()
		if len(eRecords) > 0 {
			fmt.Println("warnings:")
			// R2953: sort by name for deterministic, scannable output.
			warnNames := make([]string, 0, len(eRecords))
			for name := range eRecords {
				warnNames = append(warnNames, name)
			}
			sort.Strings(warnNames)
			for _, name := range warnNames {
				fmt.Printf("  %s: %s\n", name, string(eRecords[name]))
			}
		}

		if !*showChunks {
			return
		}

		// Chunk stats
		ff := ark.ExpandTildeSlice([]string(filterFiles))
		ef := ark.ExpandTildeSlice([]string(excludeFiles))

		var sizeFn func(string) int // nil = use CRecord ContentLen (fast path)
		unit := "bytes"
		modelName := ""

		if *tokenize {
			emb := d.Config().Embedding
			if emb.Model == "" {
				fatal(fmt.Errorf("--tokenize requires [embedding] model in ark.toml"))
			}
			modelPath := filepath.Join(arkDir, emb.Model)
			tok, err := ark.NewTokenizer(emb.ResolveLibDir(arkDir), modelPath)
			if err != nil {
				fatal(err)
			}
			defer tok.Close()
			sizeFn = tok.CountTokens
			unit = "tokens"
			modelName = tok.ModelName()
		}

		result, err := d.ChunkStats(ff, ef, sizeFn)
		if err != nil {
			fatal(err)
		}
		if len(result.Rows) == 0 {
			fmt.Println("no chunks found")
			return
		}

		printChunkStats(result, unit, modelName)
	})
}

func printChunkStats(result *ark.ChunkStatsResult, unit, modelName string) {
	if modelName != "" {
		fmt.Printf("chunk sizes (%s, %s):\n", unit, modelName)
	} else {
		fmt.Printf("chunk sizes (%s):\n", unit)
	}

	// Compute column widths
	headers := []string{"strategy", "count", "min", "max", "mean", "median", "p90", "p95", "p99"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}

	type rowStrings [9]string
	rows := make([]rowStrings, len(result.Rows))
	for i, r := range result.Rows {
		rows[i] = rowStrings{
			r.Strategy,
			fmt.Sprintf("%d", r.Count),
			fmt.Sprintf("%d", r.Min),
			fmt.Sprintf("%d", r.Max),
			fmt.Sprintf("%d", r.Mean),
			fmt.Sprintf("%d", r.Median),
			fmt.Sprintf("%d", r.P90),
			fmt.Sprintf("%d", r.P95),
			fmt.Sprintf("%d", r.P99),
		}
		for j, s := range rows[i] {
			if len(s) > widths[j] {
				widths[j] = len(s)
			}
		}
	}

	// Print header (strategy left-aligned, rest right-aligned)
	fmt.Printf("%-*s", widths[0], headers[0])
	for j := 1; j < len(headers); j++ {
		fmt.Printf("  %*s", widths[j], headers[j])
	}
	fmt.Println()

	// Print rows
	for _, r := range rows {
		fmt.Printf("%-*s", widths[0], r[0])
		for j := 1; j < len(r); j++ {
			fmt.Printf("  %*s", widths[j], r[j])
		}
		fmt.Println()
	}
}

// CRC: crc-CLI.md | R81, R1573, R1574, R1575, R1576, R1577, R1578, R1579, R1580, R1581, R1582, R1583, R1584, R1585, R1586
func cmdFiles(args []string) {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	showStatus := fs.Bool("status", false, "show file status, bytes, and chunk count")
	verbose := fs.Bool("detail", false, "show per-file chunk size stats (with --status)")
	var filterFiles, excludeFiles stringSlice
	fs.Var(&filterFiles, "filter-files", "path-based positive filter (repeatable, glob pattern)")
	fs.Var(&excludeFiles, "exclude-files", "path-based negative filter (repeatable, glob pattern)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark files [options] [pattern...]")
		fmt.Fprintln(os.Stderr, "\nList indexed files. Positional patterns narrow the result.")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	patterns := fs.Args()

	ff := ark.ExpandTildeSlice([]string(filterFiles))
	ef := ark.ExpandTildeSlice([]string(excludeFiles))

	if !*showStatus {
		// Simple path: just list files
		if client := serverClient(arkDir); client != nil {
			var files []string
			if err := proxyDecode(client, "GET", "/files", nil, &files); err != nil {
				fatal(err)
			}
			printLines(filterPaths(matchBaseSet(files, ff, ef), patterns))
			return
		}
		withDB(func(d *ark.DB) {
			files, err := d.Files()
			if err != nil {
				fatal(err)
			}
			printLines(filterPaths(matchBaseSet(files, ff, ef), patterns))
		})
		return
	}

	// --status: proxy to server (includes tmp files) or fall back to local DB
	type fileStatusEntry struct {
		Path       string `json:"path"`
		Status     string `json:"status"`
		Bytes      int64  `json:"bytes"`
		ChunkCount int    `json:"chunk_count"`
		ChunkSizes []int  `json:"chunk_sizes,omitempty"`
	}

	var entries []fileStatusEntry
	if client := serverClient(arkDir); client != nil {
		if err := proxyDecode(client, "POST", "/files/status", map[string]any{
			"patterns": patterns,
		}, &entries); err != nil {
			fatal(err)
		}
		// Apply filter-files/exclude-files
		filtered := make([]fileStatusEntry, 0, len(entries))
		for _, e := range entries {
			paths := matchBaseSet([]string{e.Path}, ff, ef)
			if len(paths) > 0 {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	} else {
		withDB(func(d *ark.DB) {
			files, err := d.Files()
			if err != nil {
				fatal(err)
			}
			staleList, _ := d.Stale()
			missingList, _ := d.Missing()
			staleSet := make(map[string]bool, len(staleList))
			for _, s := range staleList {
				staleSet[s] = true
			}
			missingSet := make(map[string]bool, len(missingList))
			for _, m := range missingList {
				missingSet[m.Path] = true
			}
			files = filterPaths(matchBaseSet(files, ff, ef), patterns)
			for _, f := range files {
				status := "G"
				if missingSet[f] {
					status = "M"
				} else if staleSet[f] {
					status = "S"
				}
				var fileBytes int64
				if fi, err := os.Stat(f); err == nil {
					fileBytes = fi.Size()
				}
				sizes := d.ChunkSizes(f)
				entries = append(entries, fileStatusEntry{
					Path: f, Status: status, Bytes: fileBytes,
					ChunkCount: len(sizes), ChunkSizes: sizes,
				})
			}
		})
	}

	// Compute column widths and print
	type fileRow struct {
		status string
		bytes  string
		chunks string
		path   string
		sizes  []int
	}
	rows := make([]fileRow, 0, len(entries))
	maxBytes, maxChunks := len("bytes"), len("chunks")
	for _, e := range entries {
		bStr := fmt.Sprintf("%d", e.Bytes)
		cStr := fmt.Sprintf("%d", e.ChunkCount)
		if len(bStr) > maxBytes {
			maxBytes = len(bStr)
		}
		if len(cStr) > maxChunks {
			maxChunks = len(cStr)
		}
		rows = append(rows, fileRow{e.Status, bStr, cStr, e.Path, e.ChunkSizes})
	}
	for _, r := range rows {
		fmt.Printf("%s  %*s  %*s  %s\n", r.status, maxBytes, r.bytes, maxChunks, r.chunks, r.path)
		if *verbose && len(r.sizes) > 0 {
			sort.Ints(r.sizes)
			n := len(r.sizes)
			sum := 0
			for _, s := range r.sizes {
				sum += s
			}
			fmt.Printf("  min: %d  max: %d  mean: %d  median: %d  p90: %d  p95: %d\n",
				r.sizes[0], r.sizes[n-1], sum/n,
				percentileInts(r.sizes, 50),
				percentileInts(r.sizes, 90),
				percentileInts(r.sizes, 95))
		}
	}
}

// matchBaseSet applies --filter-files/--exclude-files to get the base set.
func matchBaseSet(paths []string, include, exclude []string) []string {
	if len(include) == 0 && len(exclude) == 0 {
		return paths
	}
	m := &ark.Matcher{Dotfiles: true}
	var out []string
	for _, p := range paths {
		if len(include) > 0 {
			matched := false
			for _, pat := range include {
				if m.Match(pat, p, "", false) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		excluded := false
		for _, pat := range exclude {
			if m.Match(pat, p, "", false) {
				excluded = true
				break
			}
		}
		if !excluded {
			out = append(out, p)
		}
	}
	return out
}

func percentileInts(sorted []int, p int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (p * (n - 1)) / 100
	return sorted[idx]
}

// CRC: crc-CLI.md | R82
func cmdStale(args []string) {
	fs := flag.NewFlagSet("stale", flag.ExitOnError)
	fs.Parse(args)
	patterns := fs.Args()

	proxyOrLocal(
		func(client *http.Client) error {
			var stale []string
			if err := proxyDecode(client, "GET", "/stale", nil, &stale); err != nil {
				return err
			}
			printLines(filterPaths(stale, patterns))
			return nil
		},
		func(d *ark.DB) error {
			stale, err := d.Stale()
			if err != nil {
				return err
			}
			printLines(filterPaths(stale, patterns))
			return nil
		},
	)
}

// CRC: crc-CLI.md | R83
func cmdMissing(args []string) {
	fs := flag.NewFlagSet("missing", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark missing [PATTERN...]\n\nList files that are indexed but no longer exist on disk.\nOptional patterns to filter the list.")
	}
	fs.Parse(args)
	patterns := fs.Args()

	proxyOrLocal(
		func(client *http.Client) error {
			var missing []ark.MissingRecord
			if err := proxyDecode(client, "GET", "/missing", nil, &missing); err != nil {
				return err
			}
			var paths []string
			for _, m := range missing {
				paths = append(paths, m.Path)
			}
			printLines(filterPaths(paths, patterns))
			return nil
		},
		func(d *ark.DB) error {
			missing, err := d.Missing()
			if err != nil {
				return err
			}
			var paths []string
			for _, m := range missing {
				paths = append(paths, m.Path)
			}
			printLines(filterPaths(paths, patterns))
			return nil
		},
	)
}

// parseDiscussedTagArg parses one `@name[:value]` token and returns
// the (tag, value) pair. The leading `@` is required. Names and
// values may not contain `\x00`.
// CRC: crc-CLI.md | R2654
func parseDiscussedTagArg(arg string) (tag, value string, err error) {
	if arg == "" || arg[0] != '@' {
		return "", "", fmt.Errorf("tag must start with '@': %q", arg)
	}
	rest := arg[1:]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		tag = rest
	} else {
		tag = rest[:colon]
		value = rest[colon+1:]
	}
	if tag == "" {
		return "", "", fmt.Errorf("empty tag name in %q", arg)
	}
	if strings.ContainsRune(tag, 0) || strings.ContainsRune(value, 0) {
		return "", "", fmt.Errorf("tag arguments may not contain NUL: %q", arg)
	}
	return tag, value, nil
}

// parseDiscussedList parses a comma-separated `@t1[:v1],@t2[:v2],...`
// string for the --discussed flag on `ark connections recall`. An
// empty input yields an empty slice. R2654, R2655
func parseDiscussedList(s string) ([]ark.Discussed, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]ark.Discussed, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		tag, value, err := parseDiscussedTagArg(p)
		if err != nil {
			return nil, err
		}
		out = append(out, ark.Discussed{Tag: tag, Value: value})
	}
	return out, nil
}

// CRC: crc-CLI.md | R84
func cmdDismiss(args []string) {
	fs := flag.NewFlagSet("dismiss", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark dismiss <path|pattern>...\n\nRemove missing files from the index. Accepts paths or glob patterns.")
	}
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or patterns specified")
		os.Exit(1)
	}

	proxyOrLocal(
		func(client *http.Client) error {
			if err := proxyOK(client, "POST", "/dismiss", map[string]any{"patterns": patterns}); err != nil {
				return err
			}
			return nil
		},
		func(d *ark.DB) error {
			if err := d.Dismiss(patterns); err != nil {
				return err
			}
			return nil
		},
	)
}

func cmdGrams(args []string) {
	fs := flag.NewFlagSet("grams", flag.ExitOnError)
	fs.Parse(args)

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: query required")
		os.Exit(1)
	}

	// R2999, R3003: /grams proxies; GramCounts runs locally. Both yield
	// decoded GramCounts so output matches.
	emitGrams := func(grams []ark.GramCount) {
		for _, g := range grams {
			fmt.Printf("%q\t%d\n", g.Gram, g.Count)
		}
	}
	proxyOrLocal(
		func(client *http.Client) error {
			var grams []ark.GramCount
			if err := proxyDecode(client, "POST", "/grams", map[string]any{"query": query}, &grams); err != nil {
				return err
			}
			emitGrams(grams)
			return nil
		},
		func(d *ark.DB) error {
			grams, err := d.GramCounts(query)
			if err != nil {
				return err
			}
			emitGrams(grams)
			return nil
		},
	)
}

func cmdSources(args []string) {
	fs := flag.NewFlagSet("sources", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark sources [check]\n\nManage source directories. Default subcommand is 'check':\nexpand globs from config, add new directories, report orphans.")
	}
	fs.Parse(args)

	sub := "check"
	if fs.NArg() > 0 {
		sub = fs.Arg(0)
	}
	if sub == "--help" || sub == "-h" {
		fs.Usage()
		os.Exit(0)
	}

	switch sub {
	case "check":
		proxyOrLocal(
			func(client *http.Client) error {
				var result ark.SourcesCheckResult
				if err := proxyDecode(client, "POST", "/config/sources-check", nil, &result); err != nil {
					return err
				}
				printSourcesCheck(&result)
				return nil
			},
			func(d *ark.DB) error {
				result, err := d.SourcesCheck()
				if err != nil {
					return err
				}
				printSourcesCheck(result)
				return nil
			},
		)
	default:
		fmt.Fprintf(os.Stderr, "unknown sources subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func printSourcesCheck(result *ark.SourcesCheckResult) {
	for _, d := range result.Added {
		fmt.Printf("+ %s\n", d)
	}
	for _, d := range result.MIA {
		fmt.Printf("- %s\n", d)
	}
	for _, d := range result.Orphaned {
		fmt.Printf("? %s\n", d)
	}
	if len(result.Added) == 0 && len(result.MIA) == 0 && len(result.Orphaned) == 0 {
		fmt.Println("no changes")
	}
}

// CRC: crc-CLI.md | R86
func cmdUnresolved(args []string) {
	fs := flag.NewFlagSet("unresolved", flag.ExitOnError)
	fs.Parse(args)

	proxyOrLocal(
		func(client *http.Client) error {
			var unresolved []ark.UnresolvedRecord
			if err := proxyDecode(client, "GET", "/unresolved", nil, &unresolved); err != nil {
				return err
			}
			for _, u := range unresolved {
				fmt.Println(u.Path)
			}
			return nil
		},
		func(d *ark.DB) error {
			unresolved, err := d.Unresolved()
			if err != nil {
				return err
			}
			for _, u := range unresolved {
				fmt.Println(u.Path)
			}
			return nil
		},
	)
}

// CRC: crc-CLI.md | R87
func cmdResolve(args []string) {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark resolve <pattern>...\n\nDismiss unresolved files matching the given glob patterns.")
	}
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no patterns specified")
		os.Exit(1)
	}

	proxyOrLocal(
		func(client *http.Client) error {
			if err := proxyOK(client, "POST", "/resolve", map[string]any{"patterns": patterns}); err != nil {
				return err
			}
			return nil
		},
		func(d *ark.DB) error {
			if err := d.Resolve(patterns); err != nil {
				return err
			}
			return nil
		},
	)
}

// CRC: crc-CLI.md | R165
func cmdFetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	wrap := fs.String("wrap", "", "wrap output in XML tags (e.g. memory, knowledge)")
	fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "error: file path(s) required")
		os.Exit(1)
	}

	// R510, R692, R2993: prefer the server when one is reachable. The index is
	// single-process (bbolt file lock), so opening it locally while the
	// server holds the DB would block indefinitely, and tmp:// content
	// lives only in server memory. With no server, ordinary paths read
	// locally and tmp:// paths error. Mirrors R510 (tag defs).
	hasTmp := false
	for _, p := range paths {
		if strings.HasPrefix(p, "tmp://") {
			hasTmp = true
			break
		}
	}

	emit := func(source, content string) {
		if !strings.HasPrefix(source, "tmp://") {
			if abs, err := filepath.Abs(source); err == nil {
				source = abs
			}
		}
		// CRC: crc-CLI.md | R178 — ark fetch --wrap <name> (files/fetch form: source attr, no range).
		if *wrap != "" {
			fmt.Printf("<%s source=%q>\n", *wrap, source)
			writeEscaped(os.Stdout, content, *wrap)
			fmt.Printf("</%s>\n", *wrap)
		} else {
			os.Stdout.WriteString(content)
		}
	}

	if client := serverClient(arkDir); client != nil {
		for _, filePath := range paths {
			data, err := proxyRaw(client, "POST", "/fetch", map[string]string{"path": filePath})
			if err != nil {
				fatal(err)
			}
			var resp struct{ Content string }
			if err := json.Unmarshal(data, &resp); err != nil {
				fatal(fmt.Errorf("decode fetch response: %w", err))
			}
			emit(filePath, resp.Content)
		}
		return
	}

	// No server reachable — tmp:// content lives only in server memory.
	if hasTmp {
		fmt.Fprintln(os.Stderr, "error: tmp:// requires a running server")
		os.Exit(1)
	}

	// Local fallback: opening the index directly is safe only because no
	// server holds the bbolt lock (R2993).
	withDB(func(d *ark.DB) {
		for _, filePath := range paths {
			data, err := d.Fetch(filePath)
			if err != nil {
				fatal(err)
			}
			emit(filePath, string(data))
		}
	})
}

// emitChunkResult formats a ChunkFetchResult for `ark chunks`, shared by
// the proxy and local arms so their output is identical. R2998
// R481: default output is JSONL — one JSON object per chunk (same format as
// `ark search --chunks`). R482: each object is a microfts2.ChunkResult with
// path, range, content, and 0-based index.
// CRC: crc-CLI.md | R481, R482, R2998
func emitChunkResult(res ark.ChunkFetchResult, filePath, chunkRange, anchor, wrap string) {
	if res.HasSub {
		if wrap != "" {
			fmt.Printf("<%s source=%q range=%q>\n", wrap, filePath, fmt.Sprintf("%s:%q", chunkRange, anchor))
			writeEscaped(os.Stdout, res.Subchunk, wrap)
			fmt.Printf("</%s>\n", wrap)
		} else {
			fmt.Println(res.Subchunk)
		}
		return
	}
	if wrap != "" {
		for _, c := range res.Chunks {
			fmt.Printf("<%s source=%q range=%q>\n", wrap, c.Path, c.Range)
			writeEscaped(os.Stdout, c.Content, wrap)
			fmt.Printf("</%s>\n", wrap)
		}
		return
	}
	enc := json.NewEncoder(os.Stdout)
	for _, c := range res.Chunks {
		enc.Encode(c)
	}
}

// CRC: crc-CLI.md | R479, R480, R487
func cmdChunks(args []string) {
	fs := flag.NewFlagSet("chunks", flag.ExitOnError)
	before := fs.Int("before", 0, "number of chunks before target")
	after := fs.Int("after", 0, "number of chunks after target")
	wrap := fs.String("wrap", "", "wrap output in XML tags")
	showStatus := fs.Bool("status", false, "show SIZE FILE:LOCATION for all chunks matching patterns")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark chunks [options] <chunkid>
       ark chunks [options] <path>:<range>
       ark chunks [options] <path>:<range>:"<snippet>"
       ark chunks [options] <path> <range>
       ark chunks -status [pattern...]

Show chunk content, or list chunks with sizes. The single-argument
forms (decimal chunkID or path:range) make it easy to paste a line
straight from search/recall output.

A recall chat sub-chunk locator path:range:"<snippet>" returns just that
one markdown sub-chunk (the matched paragraph) of the conversation turn.
Drop the :"<snippet>" — fetch path:range — to get the whole turn instead,
the zoom-out for fuller context.

Options:`)
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	if *showStatus {
		cmdChunksStatus(fs.Args())
		return
	}

	posArgs := fs.Args()
	filePath, chunkRange, anchor, err := resolveChunksTarget(posArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, `usage: ark chunks <chunkid> | <path>:<range> | <path>:<range>:"<snippet>" | <path> <range>  [-before N] [-after N]`)
		os.Exit(1)
	}

	// R485, R2998, R3003: dispatch through the central helper. /chunks proxies
	// to the server when one holds the single-process index; otherwise
	// FetchChunkContent runs the identical resolution locally (cold-start) —
	// read-only and fast either way. emitChunkResult formats both so output
	// cannot drift.
	proxyOrLocal(
		func(client *http.Client) error {
			var res ark.ChunkFetchResult
			if err := proxyDecode(client, "POST", "/chunks", map[string]any{
				"path": filePath, "range": chunkRange, "anchor": anchor,
				"before": *before, "after": *after,
			}, &res); err != nil {
				return err
			}
			emitChunkResult(res, filePath, chunkRange, anchor, *wrap)
			return nil
		},
		func(d *ark.DB) error {
			res, err := d.FetchChunkContent(filePath, chunkRange, anchor, *before, *after)
			if err != nil {
				return err
			}
			emitChunkResult(res, filePath, chunkRange, anchor, *wrap)
			return nil
		},
	)
}

// resolveChunksTarget parses the positional arguments of `ark chunks`.
// Returns (path, range, anchor) on success. When the input is a bare
// chunkID, path is empty and range carries the decimal chunkID string —
// the caller resolves chunkID → (path, range) via db.ChunkInfo.
//
// Accepted shapes:
//   - `<chunkid>`               single all-digits arg
//   - `<path>:<range>`          single arg with `:NN[-MM]` suffix
//   - `<path>:<range>:"snip"`   recall chat sub-chunk locator (R2914)
//   - `<path> <range>`          classic two-arg form
//
// anchor is the chat sub-chunk snippet from a path:range:"<snippet>" locator,
// or "" when absent; it selects the matched markdown sub-chunk within the
// turn. Dropping it (path:range) fetches the whole turn.
func resolveChunksTarget(posArgs []string) (path, rangeLabel, anchor string, err error) {
	switch len(posArgs) {
	case 0:
		return "", "", "", fmt.Errorf("missing target")
	case 1:
		arg := posArgs[0]
		if isAllDigits(arg) {
			return "", arg, "", nil
		}
		// path:range:"snippet" — a quoted string anchor after a path:range
		// is the recall chat sub-chunk locator (R2914).
		if i := strings.Index(arg, `:"`); i > 0 && strings.HasSuffix(arg, `"`) && len(arg) > i+2 {
			base, snip := arg[:i], arg[i+2:len(arg)-1]
			if j := strings.LastIndexByte(base, ':'); j > 0 && j < len(base)-1 {
				if looksLikeRange(base[j+1:]) {
					return base[:j], base[j+1:], snip, nil
				}
			}
		}
		if idx := strings.LastIndexByte(arg, ':'); idx > 0 && idx < len(arg)-1 {
			if cand := arg[idx+1:]; looksLikeRange(cand) {
				return arg[:idx], cand, "", nil
			}
		}
		return "", "", "", fmt.Errorf(`single argument must be a decimal chunkID, path:range, or path:range:"snippet"`)
	case 2:
		return posArgs[0], posArgs[1], "", nil
	default:
		return "", "", "", fmt.Errorf("too many arguments")
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// looksLikeRange recognizes `NN` and `NN-MM` shapes (the two range
// labels every chunker uses today). Stays narrow on purpose — exotic
// label formats fall back to the two-arg form.
func looksLikeRange(s string) bool {
	if s == "" {
		return false
	}
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		return idx > 0 && idx < len(s)-1 && isAllDigits(s[:idx]) && isAllDigits(s[idx+1:])
	}
	return isAllDigits(s)
}

func cmdChunksStatus(patterns []string) {
	type chunkEntry struct {
		Path     string `json:"path"`
		Location string `json:"location"`
		Size     int    `json:"size"`
	}

	var entries []chunkEntry
	proxyOrLocal(
		func(client *http.Client) error {
			if err := proxyDecode(client, "POST", "/files/status", map[string]any{
				"patterns": patterns,
				"chunks":   true,
			}, &entries); err != nil {
				return err
			}
			return nil
		},
		func(d *ark.DB) error {
			files, err := d.Files()
			if err != nil {
				return err
			}
			files = filterPaths(files, patterns)
			for _, f := range files {
				chunks := d.AllChunks(f)
				for _, c := range chunks {
					entries = append(entries, chunkEntry{Path: f, Location: c.Range, Size: len(c.Content)})
				}
			}
			return nil
		},
	)

	// Print SIZE FILE:LOCATION
	maxSize := len("size")
	for _, e := range entries {
		s := fmt.Sprintf("%d", e.Size)
		if len(s) > maxSize {
			maxSize = len(s)
		}
	}
	for _, e := range entries {
		fmt.Printf("%*d  %s:%s\n", maxSize, e.Size, e.Path, e.Location)
	}
}

// CRC: crc-CLI.md, crc-Server.md | R172, R173, R174, R176
func cmdStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	force := fs.Bool("f", false, "send SIGKILL instead of SIGTERM")
	fs.Parse(args)

	pidPath := ark.PidFilePath(arkDir)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no PID file found — server may not be running")
		os.Exit(1)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid PID file: %v\n", err)
		os.Exit(1)
	}

	// Verify process exists and is ark (handles PID rollover)
	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "process %d not found\n", pid)
		os.Exit(1)
	}
	// Check if process is alive by sending signal 0
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Fprintf(os.Stderr, "process %d not running: %v\n", pid, err)
		os.Exit(1)
	}

	// Send signal
	sig := syscall.SIGTERM
	if *force {
		sig = syscall.SIGKILL
	}
	if err := proc.Signal(sig); err != nil {
		fmt.Fprintf(os.Stderr, "failed to send signal: %v\n", err)
		os.Exit(1)
	}

	// Poll until process exits (timeout 10s)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			fmt.Fprintf(os.Stderr, "ark server stopped (pid %d)\n", pid)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "server did not stop within timeout (pid %d)\n", pid)
	os.Exit(1)
}

// uiClient returns an http.Client connected to the ark unix socket.
// Exits if the server isn't running.
func uiClient() *http.Client {
	client := serverClient(arkDir)
	if client == nil {
		fmt.Fprintln(os.Stderr, "UI not available — server may not be running")
		os.Exit(1)
	}
	return client
}

// uiRequest sends an HTTP request to the Frictionless API via the unix socket.
func uiRequest(method, path string, jsonBody string) []byte {
	client := uiClient()

	var body io.Reader
	if jsonBody != "" {
		body = strings.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, "http://ark"+path, body)
	if err != nil {
		fatal(err)
	}
	if jsonBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "UI not available — server may not be running")
		os.Exit(1)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fatal(err)
	}
	return data
}

// CRC: crc-CLI.md | Seq: seq-install.md | R332
func cmdUIInstall(args []string) {
	// R429: Bootstrap — ensure setup and database exist
	cmdInit([]string{"--if-needed"})

	// R430: Start server if not running
	if serverClient(arkDir) == nil {
		self, err := os.Executable()
		if err != nil {
			fatal(fmt.Errorf("find executable: %w", err))
		}
		cmd := exec.Command(self, "--dir", arkDir, "serve")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start server: %v\n", err)
		} else {
			// Detach — let server run independently
			cmd.Process.Release()
			fmt.Println("Started ark server")
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal(fmt.Errorf("get working directory: %w", err))
	}

	// R431, R435: Create project .claude/skills/ and .claude/agents/ if needed
	skillsDir := filepath.Join(cwd, ".claude", "skills")
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	os.MkdirAll(skillsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	// R431: Symlink skills from ~/.ark/skills/ into project
	for _, skill := range []string{"ark", "ui"} {
		src := filepath.Join(arkDir, "skills", skill)
		if _, err := os.Stat(src); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s not found in %s\n", skill, filepath.Join(arkDir, "skills"))
			continue
		}
		dst := filepath.Join(skillsDir, skill)
		// R434: Remove existing (file, symlink, or directory) — idempotent
		os.RemoveAll(dst)
		if err := os.Symlink(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "warning: symlink %s: %v\n", skill, err)
			continue
		}
		fmt.Printf("Linked: %s → %s\n", dst, src)
	}

	// R432: Symlink agent from ~/.ark/agents/ark.md into project
	agentSrc := filepath.Join(arkDir, "agents", "ark.md")
	if _, err := os.Stat(agentSrc); err == nil {
		agentDst := filepath.Join(agentsDir, "ark.md")
		os.Remove(agentDst)
		if err := os.Symlink(agentSrc, agentDst); err != nil {
			fmt.Fprintf(os.Stderr, "warning: symlink agent: %v\n", err)
		} else {
			fmt.Printf("Linked: %s → %s\n", agentDst, agentSrc)
		}
	} else {
		fmt.Fprintf(os.Stderr, "warning: agent not found at %s\n", agentSrc)
	}

	// R433: Crank-handle — tell Claude what to do next
	fmt.Println()
	fmt.Println("---")
	fmt.Println("Add the following line near the top of this project's CLAUDE.md:")
	fmt.Println()
	fmt.Println("  load /ark first")
	fmt.Println("---")
}

func cmdUIRun(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ark ui run '<lua code>'")
		os.Exit(1)
	}
	code := args[0]
	body, _ := json.Marshal(map[string]string{"code": code})
	result := uiRequest("POST", "/api/ui_run", string(body))
	os.Stdout.Write(result)
	fmt.Println()
}

func cmdUIDisplay(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ark ui display <app-name>")
		os.Exit(1)
	}
	body, _ := json.Marshal(map[string]string{"name": args[0]})
	result := uiRequest("POST", "/api/ui_display", string(body))
	os.Stdout.Write(result)
	fmt.Println()
}

func cmdUIEvent(args []string) {
	client := uiClient()
	for {
		req, err := http.NewRequest("GET", "http://ark/wait?timeout=120", nil)
		if err != nil {
			fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			// Connection failed — server may have restarted
			time.Sleep(1 * time.Second)
			client = uiClient()
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		out := strings.TrimSpace(string(data))

		if out == "" {
			continue
		}
		// On server_reconfigured or transient responses, retry
		if strings.Contains(out, "server_reconfigured") || strings.Contains(out, "No active session") {
			time.Sleep(1 * time.Second)
			continue
		}
		fmt.Println(out)
		return
	}
}

func cmdUICheckpoint(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: ark ui checkpoint <cmd> <app> [msg]

Commands:
  save <app> [msg]       save a checkpoint
  list <app>             list checkpoints
  rollback <app> [n]     rollback to nth checkpoint (default: undo last)
  diff <app> [n]         diff against nth checkpoint (default: 1)
  clear <app>            reset to baseline
  baseline <app>         set current state as baseline
  count <app>            count checkpoints
  update <app> [msg]     save to updates branch
  local <app> [msg]      save to local branch`)
		os.Exit(1)
	}

	cmd := args[0]
	app := args[1]
	var msg string
	if len(args) > 2 {
		msg = args[2]
	}

	fossil := fossilBin()
	if fossil == "" {
		// Crank-handle: output a prompt for the agent to install fossil
		fmt.Printf(`Fossil is not installed. To set up checkpoints, run these commands:

mkdir -p ~/.claude/bin
# Download fossil for your platform:
#   Linux x86_64:
curl -sL "https://fossil-scm.org/home/uv/fossil-linux-x64-2.27.tar.gz" | tar -xzf - -C ~/.claude/bin fossil
#   macOS ARM:
# curl -sL "https://fossil-scm.org/home/uv/fossil-mac-arm-2.27.tar.gz" | tar -xzf - -C ~/.claude/bin fossil
#   macOS x86_64:
# curl -sL "https://fossil-scm.org/home/uv/fossil-mac-x64-2.27.tar.gz" | tar -xzf - -C ~/.claude/bin fossil
chmod +x ~/.claude/bin/fossil

Then re-run: ark ui checkpoint %s %s
`, cmd, app)
		os.Exit(1)
	}

	appDir := filepath.Join(arkDir, "apps", app)
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "App not found: %s\n", app)
		os.Exit(1)
	}

	repo := filepath.Join(appDir, "checkpoint.fossil")

	switch cmd {
	case "save":
		checkpointSave(fossil, appDir, repo, msg)
	case "list":
		checkpointList(fossil, appDir, repo)
	case "rollback":
		checkpointRollback(fossil, appDir, repo, msg) // msg is used as "n"
	case "diff":
		checkpointDiff(fossil, appDir, repo, msg) // msg is used as "n"
	case "clear":
		checkpointBaseline(fossil, appDir, repo)
	case "baseline":
		checkpointBaseline(fossil, appDir, repo)
	case "count":
		checkpointCount(fossil, appDir, repo)
	case "update":
		checkpointUpdate(fossil, appDir, repo, msg)
	case "local":
		checkpointLocal(fossil, appDir, repo, msg)
	default:
		fmt.Fprintf(os.Stderr, "unknown checkpoint command: %s\n", cmd)
		os.Exit(1)
	}
}

// fossilBin returns the path to the fossil binary, or "" if not found.
func fossilBin() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	bin := filepath.Join(home, ".claude", "bin", "fossil")
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	return bin
}

// fossilRun executes a fossil command in the given directory, returning combined output.
func fossilRun(fossil, dir string, args ...string) (string, error) {
	cmd := exec.Command(fossil, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fossilInit initializes a new checkpoint repo for an app.
func fossilInit(fossil, appDir, repo string) {
	fossilRun(fossil, appDir, "init", repo, "--project-name", filepath.Base(appDir))
	fossilRun(fossil, appDir, "open", repo, "--force")
	fossilRun(fossil, appDir, "settings", "ignore-glob", ".#*,.*~,*~,checkpoint.fossil,.fslckout")
	fossilRun(fossil, appDir, "add", ".")
}

func checkpointSave(fossil, appDir, repo, msg string) {
	if msg == "" {
		msg = "checkpoint"
	}

	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fossilInit(fossil, appDir, repo)
		if msg == "checkpoint" {
			msg = "initial state"
		}
		fossilRun(fossil, appDir, "commit", "-m", msg, "--no-warnings")
		fmt.Printf("Created checkpoint: %s\n", msg)
	} else {
		fossilRun(fossil, appDir, "addremove")
		changes, _ := fossilRun(fossil, appDir, "changes", "--quiet")
		if strings.TrimSpace(changes) == "" {
			fmt.Println("No changes to checkpoint")
			return
		}
		fossilRun(fossil, appDir, "commit", "-m", msg, "--no-warnings")
		fmt.Printf("Saved checkpoint: %s\n", msg)
	}
	// Notify UI of checkpoint change
	notifyCheckpointChange()
}

func checkpointList(fossil, appDir, repo string) {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fmt.Printf("No checkpoints for %s\n", filepath.Base(appDir))
		return
	}
	out, _ := fossilRun(fossil, appDir, "timeline", "-t", "ci", "--oneline")
	// Remove last 3 lines (fossil footer)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 3 {
		lines = lines[:len(lines)-3]
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

func checkpointRollback(fossil, appDir, repo, n string) {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "No checkpoints for %s\n", filepath.Base(appDir))
		os.Exit(1)
	}
	if n == "" {
		out, _ := fossilRun(fossil, appDir, "undo")
		fmt.Print(out)
	} else {
		out, _ := fossilRun(fossil, appDir, "timeline", "-n", "100", "-t", "ci", "--oneline")
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		// Filter out footer/baseline lines
		var commits []string
		for _, l := range lines {
			if strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") || strings.Contains(l, "=== BASELINE ===") {
				continue
			}
			commits = append(commits, l)
		}
		idx, err := strconv.Atoi(n)
		if err != nil || idx < 1 || idx > len(commits) {
			fmt.Fprintf(os.Stderr, "Checkpoint %s not found\n", n)
			os.Exit(1)
		}
		hash := strings.Fields(commits[idx-1])[0]
		fossilRun(fossil, appDir, "checkout", hash, "--force")
	}
	fmt.Println("Rolled back to checkpoint")
}

func checkpointDiff(fossil, appDir, repo, n string) {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "No checkpoints for %s\n", filepath.Base(appDir))
		os.Exit(1)
	}
	if n == "" {
		n = "1"
	}
	out, _ := fossilRun(fossil, appDir, "timeline", "-t", "ci", "--oneline")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Remove last 3 lines (footer)
	if len(lines) > 3 {
		lines = lines[:len(lines)-3]
	}
	idx, err := strconv.Atoi(n)
	if err != nil || idx < 1 || idx > len(lines) {
		fmt.Fprintf(os.Stderr, "Checkpoint %s not found\n", n)
		os.Exit(1)
	}
	hash := strings.Fields(lines[idx-1])[0]
	out, _ = fossilRun(fossil, appDir, "diff", "--from", hash)
	fmt.Print(out)
}

func checkpointBaseline(fossil, appDir, repo string) {
	app := filepath.Base(appDir)
	bundle := filepath.Join(appDir, ".preserved-bundle")
	hasBundle := false

	if _, err := os.Stat(repo); err == nil {
		// Export updates and local branches into a bundle before destroying
		for _, branch := range []string{"updates", "local"} {
			branchList, _ := fossilRun(fossil, appDir, "branch", "list")
			if strings.Contains(branchList, branch) {
				_, err := fossilRun(fossil, appDir, "bundle", "export", bundle, "--branch", branch, "--standalone")
				if err == nil {
					hasBundle = true
				}
			}
		}
		fossilRun(fossil, appDir, "close", "--force")
		os.Remove(repo)
		os.Remove(filepath.Join(appDir, ".fslckout"))
	}

	// Create fresh repo with current state as baseline
	fossilInit(fossil, appDir, repo)
	fossilRun(fossil, appDir, "commit", "-m", "=== BASELINE ===", "--no-warnings")

	// Restore preserved branches
	if hasBundle {
		if _, err := os.Stat(bundle); err == nil {
			fossilRun(fossil, appDir, "bundle", "import", bundle, "--force", "--publish")
			os.Remove(bundle)
			fmt.Printf("Baseline set for %s (preserved branches restored)\n", app)
			notifyCheckpointChange()
			return
		}
	}
	fmt.Printf("Baseline set for %s\n", app)
	notifyCheckpointChange()
}

func checkpointCount(fossil, appDir, repo string) {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fmt.Println("0")
		return
	}
	out, _ := fossilRun(fossil, appDir, "timeline", "-t", "ci", "--oneline", "-b", "trunk")
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.Contains(line, "=== BASELINE ===") ||
			strings.Contains(line, "initial empty check-in") ||
			strings.HasPrefix(line, "+++ ") {
			continue
		}
		count++
	}
	fmt.Println(count)
}

func checkpointUpdate(fossil, appDir, repo, msg string) {
	if msg == "" {
		msg = "update"
	}

	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fossilInit(fossil, appDir, repo)
		fossilRun(fossil, appDir, "commit", "-m", "=== BASELINE ===", "--no-warnings")
		fossilRun(fossil, appDir, "commit", "--branch", "updates", "-m", msg, "--allow-empty", "--no-warnings")
		fossilRun(fossil, appDir, "update", "trunk")
	} else {
		// Check for uncommitted checkpoints
		countOut, _ := fossilRun(fossil, appDir, "timeline", "-t", "ci", "--oneline", "-b", "trunk")
		count := 0
		for _, line := range strings.Split(countOut, "\n") {
			if line == "" || strings.Contains(line, "=== BASELINE ===") ||
				strings.Contains(line, "initial empty check-in") ||
				strings.HasPrefix(line, "+++ ") {
				continue
			}
			count++
		}
		if count > 0 {
			fmt.Fprintf(os.Stderr, "Error: %d checkpoint(s) exist. Consolidate before updating.\n", count)
			os.Exit(1)
		}
		// Switch to updates branch (create if needed), commit, switch back
		_, err := fossilRun(fossil, appDir, "update", "updates")
		if err != nil {
			fossilRun(fossil, appDir, "addremove")
			fossilRun(fossil, appDir, "commit", "--branch", "updates", "-m", msg, "--allow-empty", "--no-warnings")
		} else {
			fossilRun(fossil, appDir, "addremove")
			fossilRun(fossil, appDir, "commit", "-m", msg, "--allow-empty", "--no-warnings")
		}
		fossilRun(fossil, appDir, "update", "trunk")
	}
	fmt.Printf("Update checkpoint: %s\n", msg)
}

func checkpointLocal(fossil, appDir, repo, msg string) {
	if msg == "" {
		msg = "local"
	}

	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fossilInit(fossil, appDir, repo)
		fossilRun(fossil, appDir, "commit", "-m", "=== BASELINE ===", "--no-warnings")
		fossilRun(fossil, appDir, "commit", "--branch", "local", "-m", msg, "--allow-empty", "--no-warnings")
		fossilRun(fossil, appDir, "update", "trunk")
	} else {
		_, err := fossilRun(fossil, appDir, "update", "local")
		if err != nil {
			fossilRun(fossil, appDir, "addremove")
			fossilRun(fossil, appDir, "commit", "--branch", "local", "-m", msg, "--allow-empty", "--no-warnings")
		} else {
			fossilRun(fossil, appDir, "addremove")
			fossilRun(fossil, appDir, "commit", "-m", msg, "--allow-empty", "--no-warnings")
		}
		fossilRun(fossil, appDir, "update", "trunk")
	}
	fmt.Printf("Local checkpoint: %s\n", msg)
}

// notifyCheckpointChange tells the UI to refresh checkpoint state.
func notifyCheckpointChange() {
	// Best-effort via unix socket — ignore errors
	client := serverClient(arkDir)
	if client == nil {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"code": "if appConsole then appConsole._checkpointsTime = 0 end",
	})
	req, err := http.NewRequest("POST", "http://ark/api/ui_run", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func cmdUIAudit(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ark ui audit <app-name>")
		os.Exit(1)
	}
	body, _ := json.Marshal(map[string]string{"name": args[0]})
	result := uiRequest("POST", "/api/ui_audit", string(body))
	os.Stdout.Write(result)
	fmt.Println()
}

// CRC: crc-CLI.md | Seq: seq-server-startup.md
func cmdUIReload(args []string) {
	client := uiClient()
	var result struct {
		Status string `json:"status"`
		Port   int    `json:"port"`
	}
	if err := proxyDecode(client, "POST", "/ui/reload", nil, &result); err != nil {
		fatal(err)
	}
	fmt.Printf("ui: reloaded (port %d)\n", result.Port)
}

// CRC: crc-CLI.md | R289
func cmdUIStatus(args []string) {
	client := serverClient(arkDir)
	if client == nil {
		fmt.Println("ui: not available")
		return
	}
	var status ark.StatusInfo
	if err := proxyDecode(client, "GET", "/status", nil, &status); err != nil {
		fatal(err)
	}
	if !status.UIRunning {
		fmt.Println("ui: not available")
		return
	}
	fmt.Printf("ui: running (port %d)\n", status.UIPort)
	if status.UIIndexing {
		fmt.Println("indexing: yes")
	} else {
		fmt.Println("indexing: no")
	}
}

func cmdUIOpen(args []string) {
	portPath := filepath.Join(arkDir, "ui-port")
	data, err := os.ReadFile(portPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "UI not available — server may not be running or UI not installed")
		os.Exit(1)
	}
	port := strings.TrimSpace(string(data))
	url := "http://127.0.0.1:" + port
	// Try xdg-open (Linux), then open (macOS)
	for _, cmd := range []string{"xdg-open", "open"} {
		if err := exec.Command(cmd, url).Start(); err == nil {
			fmt.Fprintf(os.Stderr, "opened %s\n", url)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "could not open browser — visit %s\n", url)
}

// CRC: crc-CLI.md
func cmdUILinkapp(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: ark ui linkapp add|remove <app>")
		os.Exit(1)
	}
	action := args[0]
	app := args[1]
	appsDir := filepath.Join(arkDir, "apps")
	luaDir := filepath.Join(arkDir, "lua")
	viewdefsDir := filepath.Join(arkDir, "viewdefs")

	switch action {
	case "add":
		appDir := filepath.Join(appsDir, app)
		if _, err := os.Stat(appDir); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: app '%s' not found in %s/\n", app, appsDir)
			os.Exit(1)
		}
		os.MkdirAll(luaDir, 0755)
		os.MkdirAll(viewdefsDir, 0755)

		// Link lua file: lua/app.lua -> ../apps/app/app.lua
		appLua := filepath.Join(appDir, "app.lua")
		if _, err := os.Stat(appLua); err == nil {
			target := filepath.Join(luaDir, app+".lua")
			os.Remove(target)
			os.Symlink(filepath.Join("../apps", app, "app.lua"), target)
			fmt.Printf("Linked: %s\n", target)
		}

		// Link app directory: lua/app -> ../apps/app
		appLink := filepath.Join(luaDir, app)
		os.Remove(appLink)
		os.Symlink(filepath.Join("../apps", app), appLink)
		fmt.Printf("Linked: %s\n", appLink)

		// Link viewdefs
		vdDir := filepath.Join(appDir, "viewdefs")
		if entries, err := os.ReadDir(vdDir); err == nil {
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".html" {
					target := filepath.Join(viewdefsDir, e.Name())
					os.Remove(target)
					os.Symlink(filepath.Join("../apps", app, "viewdefs", e.Name()), target)
					fmt.Printf("Linked: %s\n", target)
				}
			}
		}
		fmt.Printf("Done: %s linked\n", app)

	case "remove":
		// Remove lua file symlink
		luaFile := filepath.Join(luaDir, app+".lua")
		if fi, err := os.Lstat(luaFile); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			os.Remove(luaFile)
			fmt.Printf("Removed: %s\n", luaFile)
		}
		// Remove app directory symlink
		appLink := filepath.Join(luaDir, app)
		if fi, err := os.Lstat(appLink); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			os.Remove(appLink)
			fmt.Printf("Removed: %s\n", appLink)
		}
		// Remove viewdefs that point to this app
		if entries, err := os.ReadDir(viewdefsDir); err == nil {
			for _, e := range entries {
				link := filepath.Join(viewdefsDir, e.Name())
				if target, err := os.Readlink(link); err == nil {
					if strings.Contains(target, "/apps/"+app+"/viewdefs/") {
						os.Remove(link)
						fmt.Printf("Removed: %s\n", link)
					}
				}
			}
		}
		fmt.Printf("Done: %s unlinked\n", app)

	default:
		fmt.Fprintln(os.Stderr, "Usage: ark ui linkapp add|remove <app>")
		os.Exit(1)
	}
}

// CRC: crc-CLI.md
func cmdUIPatterns(args []string) {
	patternsDir := filepath.Join(arkDir, "patterns")
	entries, err := os.ReadDir(patternsDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No patterns directory")
		os.Exit(0)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".md" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		// Extract description from frontmatter
		data, err := os.ReadFile(filepath.Join(patternsDir, e.Name()))
		if err != nil {
			continue
		}
		desc := ""
		lines := strings.Split(string(data), "\n")
		inFrontmatter := false
		for _, line := range lines {
			if line == "---" {
				if inFrontmatter {
					break
				}
				inFrontmatter = true
				continue
			}
			if inFrontmatter && strings.HasPrefix(line, "description:") {
				desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			}
		}
		fmt.Printf("- `%s.md` - %s\n", name, desc)
	}
}

// CRC: crc-CLI.md
func cmdUIProgress(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ark ui progress <app> <percent> <stage>")
		os.Exit(1)
	}
	app, pct, stage := args[0], args[1], args[2]
	code := fmt.Sprintf("mcp:appProgress('%s', %s, '%s'); mcp:addAgentThinking('%s')", app, pct, stage, stage)
	body, _ := json.Marshal(map[string]string{"code": code})
	uiRequest("POST", "/api/ui_run", string(body))
}

// CRC: crc-CLI.md
func cmdUIState(args []string) {
	result := uiRequest("GET", "/state", "")
	os.Stdout.Write(result)
	fmt.Println()
}

// CRC: crc-CLI.md
func cmdUITheme(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: ark ui theme list|classes|audit [args]")
		os.Exit(1)
	}
	action := args[0]
	switch action {
	case "list":
		body, _ := json.Marshal(map[string]string{"action": "list"})
		result := uiRequest("POST", "/api/ui_theme", string(body))
		os.Stdout.Write(result)
		fmt.Println()
	case "classes":
		req := map[string]string{"action": "classes"}
		if len(args) > 1 {
			req["theme"] = args[1]
		}
		body, _ := json.Marshal(req)
		result := uiRequest("POST", "/api/ui_theme", string(body))
		os.Stdout.Write(result)
		fmt.Println()
	case "audit":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: ark ui theme audit <app> [theme]")
			os.Exit(1)
		}
		req := map[string]string{"action": "audit", "app": args[1]}
		if len(args) > 2 {
			req["theme"] = args[2]
		}
		body, _ := json.Marshal(req)
		result := uiRequest("POST", "/api/ui_theme", string(body))
		os.Stdout.Write(result)
		fmt.Println()
	default:
		fmt.Fprintln(os.Stderr, "Usage: ark ui theme list|classes|audit [args]")
		os.Exit(1)
	}
}

// CRC: crc-CLI.md
func cmdUIUpdate(args []string) {
	if len(args) > 0 && args[0] == "-t" {
		// Version check only — get current status
		result := uiRequest("GET", "/api/ui_status", "")
		os.Stdout.Write(result)
		fmt.Println()
		return
	}
	result := uiRequest("POST", "/api/ui_update", "{}")
	os.Stdout.Write(result)
	fmt.Println()
}

// CRC: crc-CLI.md
func cmdUIVariables(args []string) {
	result := uiRequest("GET", "/variables", "")
	os.Stdout.Write(result)
	fmt.Println()
}

// normalizeTagName strips a leading '@' and trailing ':' from a tag name so
// callers can paste the rendered form ("@area:") and get the canonical name
// ("area"). Without this, a copy-pasted name flows into TagBlock.Set and
// Render emits "@@area:: VALUE". Mirrors R2449/R2450 for -tag sigil parsing.
// R2483
func normalizeTagName(name string) string {
	name = strings.TrimPrefix(name, "@")
	name = strings.TrimSuffix(name, ":")
	return name
}

// normalizeTagNames applies normalizeTagName across a slice in place.
// R2483
func normalizeTagNames(names []string) []string {
	for i, n := range names {
		names[i] = normalizeTagName(n)
	}
	return names
}

// matchPath returns true if a path passes the include/exclude filters.
func matchPath(path string, include, exclude []string) bool {
	include = ark.ExpandTildeSlice(include)
	exclude = ark.ExpandTildeSlice(exclude)
	m := &ark.Matcher{Dotfiles: true}
	if len(include) > 0 {
		matched := false
		for _, pat := range include {
			if m.Match(pat, path, "", false) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pat := range exclude {
		if m.Match(pat, path, "", false) {
			return false
		}
	}
	return true
}

func cmdTagFilesContext(tags []string, filterFiles, excludeFiles []string) {
	proxyOrLocal(
		func(client *http.Client) error {
			var entries []ark.TagContextEntry
			if err := proxyDecode(client, "POST", "/tags/files", map[string]any{
				"tags": tags, "context": true,
			}, &entries); err != nil {
				return err
			}
			for _, e := range entries {
				if matchPath(e.Path, filterFiles, excludeFiles) {
					fmt.Printf("%s\t%s\n", e.Path, e.Line)
				}
			}
			return nil
		},
		func(d *ark.DB) error {
			entries, err := d.TagContext(tags)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if matchPath(e.Path, filterFiles, excludeFiles) {
					fmt.Printf("%s\t%s\n", e.Path, e.Line)
				}
			}
			return nil
		},
	)
}

// CRC: crc-CLI.md | R607
func cmdTagSet(args []string) {
	if len(args) < 3 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "usage: ark tag set FILE TAG VALUE [TAG VALUE ...]")
		os.Exit(0)
	}

	filePath := args[0]
	pairs := args[1:]
	if len(pairs)%2 != 0 {
		fmt.Fprintln(os.Stderr, "error: tags must be given as TAG VALUE pairs")
		os.Exit(1)
	}

	// R2483: normalize tag names (strip leading @ and trailing :) at every even index.
	for i := 0; i < len(pairs); i += 2 {
		pairs[i] = normalizeTagName(pairs[i])
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		fatal(err)
	}

	tb := ark.ParseTagBlock(data)
	for i := 0; i < len(pairs); i += 2 {
		tb.Set(pairs[i], pairs[i+1])
		// R710, R711: auto-set status-date when setting status
		if pairs[i] == "status" {
			tb.Set("status-date", time.Now().Format("2006-01-02"))
		}
	}

	if err := os.WriteFile(filePath, tb.Render(), 0644); err != nil {
		fatal(err)
	}

	// Crank handle: remind about bookmark tags when setting handled tags
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i] == "response-handled" || pairs[i] == "request-handled" {
			fmt.Fprintf(os.Stderr, "hint: bookmark updated. When fully caught up, set status to reflect it.\n")
			break
		}
	}
}

// CRC: crc-CLI.md | R608, R609
func cmdTagGet(args []string) {
	if len(args) < 1 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "usage: ark tag get FILE [TAG ...]")
		os.Exit(0)
	}

	filePath := args[0]
	requestedTags := normalizeTagNames(args[1:])

	data, err := os.ReadFile(filePath)
	if err != nil {
		fatal(err)
	}

	tb := ark.ParseTagBlock(data)
	exitCode := 0

	if len(requestedTags) > 0 {
		for _, name := range requestedTags {
			v, ok := tb.Get(name)
			if ok {
				fmt.Printf("%s\t%s\n", name, v)
			} else {
				fmt.Fprintf(os.Stderr, "tag not found: %s\n", name)
				exitCode = 1
			}
		}
	} else {
		for _, t := range tb.Tags() {
			fmt.Printf("%s\t%s\n", t.Name, t.Value)
		}
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// CRC: crc-CLI.md | R610, R611, R612
func cmdTagCheck(args []string) {
	if len(args) < 1 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "usage: ark tag check FILE [HEADING ...]")
		os.Exit(0)
	}

	filePath := args[0]
	allowedHeadings := args[1:]

	data, err := os.ReadFile(filePath)
	if err != nil {
		fatal(err)
	}

	tb := ark.ParseTagBlock(data)
	problems := tb.Validate()
	bodyProblems := tb.ScanBody()

	// Heading validation if allowed headings are specified
	if len(allowedHeadings) > 0 {
		bodyProblems = append(bodyProblems, tb.CheckHeadings(allowedHeadings)...)
	}

	if len(problems) == 0 && len(bodyProblems) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "The file %s has format problems:\n", filePath)
	for _, p := range problems {
		fmt.Fprintf(os.Stderr, "- %s\n", p)
	}
	for _, p := range bodyProblems {
		fmt.Fprintf(os.Stderr, "- %s\n", p)
	}
	os.Exit(1)
}

// cmdChunkJSONL is a chunking strategy command: splits a file on newline
// boundaries and outputs range\tcontent lines (microfts2 v0.4 protocol).
func cmdChunkJSONL(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ark chunk-chat-jsonl <file>")
		os.Exit(1)
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		fatal(err)
	}

	lineNum := 1
	start := 0
	for i, b := range data {
		if b == '\n' {
			fmt.Printf("%d-%d\t%s\n", lineNum, lineNum, data[start:i])
			lineNum++
			start = i + 1
		}
	}
	if start < len(data) {
		fmt.Printf("%d-%d\t%s", lineNum, lineNum, data[start:])
	}
}

// CRC: crc-CLI.md | R233
// reorderArgs moves flag arguments (starting with -) before positional
// arguments. Go's flag package stops parsing at the first non-flag
// argument, so flags after positional args are silently ignored.
func reorderArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			// If this flag takes a value (next arg doesn't start with -)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	return append(flags, positional...)
}

// reorderArgsForFlagSet reorders args so flags precede positionals,
// using fs to identify boolean flags (which don't consume the next
// argument). Required when a command mixes bool flags and value flags
// with interleaved positionals — the simpler reorderArgs heuristic
// (treat any non-dash follower as a value) eats a positional after a
// bool flag.
func reorderArgsForFlagSet(fs *flag.FlagSet, args []string) []string {
	type boolFlag interface{ IsBoolFlag() bool }
	isBool := func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		bf, ok := f.Value.(boolFlag)
		return ok && bf.IsBoolFlag()
	}
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		// Strip leading dashes and any =value suffix to get the name.
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			continue // value embedded; no follow-up consumption
		}
		if isBool(name) {
			continue // bool flag stands alone
		}
		// Value flag — pull in the next arg.
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

func parseAliases(s string) map[byte]byte {
	if s == "" {
		return nil
	}
	aliases := make(map[byte]byte)
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if len(parts[0]) == 1 && len(parts[1]) == 1 {
			aliases[parts[0][0]] = parts[1][0]
		}
	}
	return aliases
}

// CRC: crc-CLI.md | R298, R299, R300, R302
func cmdBundle(args []string) {
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	output := fs.String("o", "", "Output path for bundled binary (required)")
	source := fs.String("src", "", "Source binary to bundle (default: current executable)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark bundle [-src <binary>] -o <output> <dir>")
		fmt.Fprintln(os.Stderr, "\nCreate a bundled binary with embedded UI assets.")
		fmt.Fprintln(os.Stderr, "Zip-grafts the contents of <dir> onto the ark binary so")
		fmt.Fprintln(os.Stderr, "that UI assets (html, lua, viewdefs) are self-contained.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *output == "" {
		fmt.Fprintln(os.Stderr, "Error: -o output path is required")
		fs.Usage()
		os.Exit(1)
	}

	dir := fs.Arg(0)
	if dir == "" {
		fmt.Fprintln(os.Stderr, "Error: directory is required")
		fmt.Fprintln(os.Stderr, "Usage: ark bundle [-src <binary>] -o <output> <dir>")
		os.Exit(1)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: directory %s does not exist\n", dir)
		os.Exit(1)
	}

	src := *source
	if src == "" {
		var err error
		src, err = os.Executable()
		if err != nil {
			fatal(fmt.Errorf("failed to get executable path: %w", err))
		}
	}

	if _, err := os.Stat(src); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: source binary %s does not exist\n", src)
		os.Exit(1)
	}

	if err := cli.BundleCreateBundle(src, dir, *output); err != nil {
		fatal(fmt.Errorf("failed to create bundle: %w", err))
	}
	fmt.Printf("Created bundled binary: %s\n", *output)
}

// CRC: crc-CLI.md | R304, R305, R306
func cmdBundleLs(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stderr, "Usage: ark ls\n\nList embedded assets in the bundled binary.")
		os.Exit(0)
	}
	bundled, err := cli.IsBundled()
	if err != nil {
		fatal(fmt.Errorf("failed to check bundle status: %w", err))
	}
	if !bundled {
		fmt.Fprintln(os.Stderr, "Error: binary is not bundled")
		os.Exit(1)
	}

	files, err := cli.BundleListFilesWithInfo()
	if err != nil {
		fatal(fmt.Errorf("failed to list files: %w", err))
	}

	for _, file := range files {
		if file.IsSymlink {
			fmt.Printf("%s -> %s\n", file.Name, file.SymlinkTarget)
		} else {
			fmt.Println(file.Name)
		}
	}
}

// CRC: crc-CLI.md | R307, R308, R309
func cmdBundleCat(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "Usage: ark cat <file>\n\nPrint an embedded file from the bundled binary to stdout.")
		os.Exit(0)
	}

	bundled, err := cli.IsBundled()
	if err != nil {
		fatal(fmt.Errorf("failed to check bundle status: %w", err))
	}
	if !bundled {
		fmt.Fprintln(os.Stderr, "Error: binary is not bundled")
		os.Exit(1)
	}

	content, err := cli.BundleReadFile(args[0])
	if err != nil {
		fatal(fmt.Errorf("failed to read file: %w", err))
	}
	os.Stdout.Write(content)
}

// CRC: crc-CLI.md | R310-R318
func cmdBundleCp(args []string) {
	if len(args) < 2 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "Usage: ark cp <pattern> <dest-dir>\n\nExtract embedded files matching a glob pattern to a directory.\nPreserves permissions and recreates symlinks.")
		os.Exit(0)
	}

	pattern := args[0]
	destDir := args[1]

	bundled, err := cli.IsBundled()
	if err != nil {
		fatal(fmt.Errorf("failed to check bundle status: %w", err))
	}
	if !bundled {
		fmt.Fprintln(os.Stderr, "Error: binary is not bundled")
		os.Exit(1)
	}

	files, err := cli.BundleListFilesWithInfo()
	if err != nil {
		fatal(fmt.Errorf("failed to list files: %w", err))
	}

	copied := 0
	for _, file := range files {
		matched, _ := filepath.Match(pattern, filepath.Base(file.Name))
		if !matched {
			matched, _ = filepath.Match(pattern, file.Name)
		}
		if !matched {
			continue
		}

		destPath := filepath.Join(destDir, filepath.Base(file.Name))
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create directory: %v\n", err)
			continue
		}

		os.Remove(destPath)

		if file.IsSymlink {
			if err := os.Symlink(file.SymlinkTarget, destPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create symlink %s: %v\n", destPath, err)
				continue
			}
			fmt.Printf("Copied: %s -> %s (symlink to %s)\n", file.Name, destPath, file.SymlinkTarget)
		} else {
			content, err := cli.BundleReadFile(file.Name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to read %s: %v\n", file.Name, err)
				continue
			}
			mode := file.Mode.Perm()
			if mode == 0 {
				mode = 0644
			}
			if err := os.WriteFile(destPath, content, mode); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to write %s: %v\n", destPath, err)
				continue
			}
			fmt.Printf("Copied: %s -> %s\n", file.Name, destPath)
		}
		copied++
	}

	if copied == 0 {
		fmt.Fprintln(os.Stderr, "No files matched the pattern")
		os.Exit(1)
	}
}

// cmdSweepCorrelations triggers the hot-correlations sweep on the
// running server and prints the JSON result.
// CRC: crc-CLI.md | R2247
func cmdSweepCorrelations(args []string) {
	fs := flag.NewFlagSet("sweep correlations", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark sweep correlations

Refresh the hot-correlations top-K cache per tag.

Reads the I:hcsweep bookmark, walks the S substrate for changed ED
and EC records since the bookmark, rebuilds tags whose definitions
moved (full top-K recompute) and displaces individual chunks against
unchanged tags. Per-tag write transactions; the bookmark advances
only on full success.

Progress is published through tmp://sweep/hot-correlations.md
(throttled at 250 ms; terminal status flushes immediately).
Subscribers can listen via mcp:subscribe({tag="sweep-status"}, ...).

Requires a running server.`)
	}
	fs.Parse(args)

	client := serverClient(arkDir)
	if client == nil {
		fmt.Fprintln(os.Stderr, "error: ark sweep correlations requires a running server (start with 'ark serve')")
		os.Exit(1)
	}
	var result ark.HCSweepResult
	if err := proxyDecode(client, "POST", "/sweep/correlations", nil, &result); err != nil {
		fatal(err)
	}
	if result.StartedAt.IsZero() {
		fmt.Println("sweep skipped: embedding unavailable")
		return
	}
	fmt.Printf("sweep complete in %d ms\n", result.DurationMS)
	fmt.Printf("  changed EDs:   %d\n", result.ChangedEDs)
	fmt.Printf("  changed ECs:   %d\n", result.ChangedECs)
	fmt.Printf("  tags rebuilt:  %d\n", result.TagsRebuilt)
	fmt.Printf("  tags touched:  %d\n", result.TagsTouched)
	fmt.Printf("  HC entries:    %d\n", result.OrphanTotal)
	if result.FromScratch {
		fmt.Println("  from scratch:  true (bookmark was zero)")
	}
}

// recallNextRedial* bound the CLI-side stubborn-plumbing retry for
// `recall next` (R2903): redial within the budget on a cold dial,
// backing off geometrically, then fall back to a keepalive.
const (
	recallNextRedialBudget     = 20 * time.Second
	recallNextRedialBackoff    = 1 * time.Second
	recallNextRedialMaxBackoff = 5 * time.Second
)

// recallRedialKeepalive is the CLI-synthesized keepalive returned when
// `next` cannot reach the server (a bounce). Shaped like the server's
// own keepalive so the secretary loops without ever seeing an error.
// CRC: crc-RecallAgentBuilder.md | R2903
func recallRedialKeepalive(session string, nonce uint64) string {
	nextCmd := fmt.Sprintf("~/.ark/ark connections recall next %d", nonce)
	if session != "" {
		nextCmd = fmt.Sprintf("~/.ark/ark connections recall next --session %s %d", session, nonce)
	}
	return fmt.Sprintf("The ark server is restarting or briefly unreachable — no work yet. This is normal. Run `%s` again now to keep watching; do not stop, do not wait.\n", nextCmd)
}

// luhmannRedialKeepalive is the CLI-synthesized keepalive `ark luhmann next`
// returns when its connection drops or the redial budget is exhausted (R3015):
// exit 0 with a re-invoke instruction so the orchestrator loop rides out the
// bounce, then re-validates ownership on the next plain call (a reborn server
// answers with the reclaim signal, R3014).
// CRC: crc-LuhmannCLI.md | R3015
func luhmannRedialKeepalive(session string) string {
	return fmt.Sprintf("The ark server connection dropped (it may be restarting). Run `~/.ark/ark luhmann next --session %s` again now to resume — do not stop.\n", session)
}

// popFire returns (fireToken, rest, err) for a recall result-builder
// verb. The fire token is the first positional argument before any
// flags — the composite `<session>-<fire>` cookie the crank-handle
// emits (R2901). It is opaque to the CLI; the server decomposes it.
func popFire(args []string, verb string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("ark connections recall %s: FIRE positional argument required", verb)
	}
	return args[0], args[1:], nil
}

// runConnectionsRecall implements the `ark connections recall`
// substrate command: parses flags, optionally proxies to a running
// server, or runs in-process with the appropriate fallback selected
// by ark.toml's tag_model setting. Writes markdown or JSON to out;
// returns errors instead of exiting. The in-process fallback
// constructs a Librarian unconditionally, relying on NewLibrarian's
// no-claude contract (R2642).
// CRC: crc-CLI.md | Seq: seq-recall.md#1.1 | R2617, R2618, R2619, R2627, R2630, R2631, R2632, R2633, R2634, R2641, R2642, R2646, R2647, R2667, R2676
func runConnectionsRecall(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("connections recall", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we own the error path
	k := fs.Int("k", 20, "top-K chunks (default 20, clamped [1, 200])")
	noContent := fs.Bool("no-content", false, "sets IncludeContent to false")
	jsonOut := fs.Bool("json", false, "emits JSON result")
	all := fs.Bool("all", false, "keep tagless chunks in results (default drops them)")
	typeFlag := fs.String("type", "", "input type: chunk | text (default: auto-detect)")
	session := fs.String("session", "", "load the session's discussed-tag set into the exclusion set")
	discussedFlag := fs.String("discussed", "", "comma-separated @t[:v] exclusions (unioned with --session set)")
	propose := fs.Bool("propose", false, "run the statistical derivation pass; persist derived-tag candidates as RC records and surface them in the result")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark connections recall INPUTS... [options]

Retrieve the top-K chunks from the corpus relevant to a given set of inputs.
INPUTS may mix:
  NNNNNN          chunk ID (decimal)
  PATH:N-M        file path with line range (1-based inclusive)
  PATH:N          file path, single line
  anything else   bare text, embedded on the fly

Without --type, each input is auto-detected. With --type chunk, every
input is treated as a chunk reference (chunkID or path:locator).
With --type text, every input is taken literally.

By default, chunks with no tags are dropped from the result (a
chunk-similarity primitive that returns only tag-bearing chunks is
the right shape for downstream tag-recall consumers). Pass -all to
keep tagless chunks.

Options:`)
		fs.SetOutput(os.Stderr)
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgsForFlagSet(fs, args)); err != nil {
		fs.Usage()
		return err
	}
	posArgs := fs.Args()
	if len(posArgs) == 0 {
		fs.Usage()
		return fmt.Errorf("no inputs given")
	}

	clampedK := *k
	if clampedK <= 0 {
		clampedK = 20
	}
	if clampedK > 200 {
		clampedK = 200
	}

	inputs, err := parseConnectionsInputs(posArgs, *typeFlag)
	if err != nil {
		return err
	}

	discussed, err := parseDiscussedList(*discussedFlag)
	if err != nil {
		return err
	}

	opts := ark.RecallOpts{
		K:              clampedK,
		IncludeContent: !*noContent,
		KeepTagless:    *all,
		Session:        *session,
		Discussed:      discussed,
		Propose:        *propose,
	}

	return runConnectionsRecallParsed(inputs, opts, *jsonOut, out)
}

// runConnectionsRecallParsed runs the substrate recall for already-parsed
// inputs/opts: proxy to a running server, or pick a cold fallback by
// tag_model. Writes markdown or JSON to out. Shared by the legacy
// runConnectionsRecall (its flag prologue) and the urfave recall node's
// default Action (cmdConnectionsRecallDefault).
// CRC: crc-CLITree.md | Seq: seq-recall.md#1.1 | R2630, R2631, R2632, R2633, R2634, R2646, R2647
func runConnectionsRecallParsed(inputs []ark.ConnectionsInput, opts ark.RecallOpts, jsonOut bool, out io.Writer) error {
	if client := serverClient(arkDir); client != nil {
		body := map[string]any{
			"inputs": inputs,
			"opts":   opts,
		}
		var result ark.RecallResult
		if err := proxyDecode(client, "POST", "/recall", body, &result); err != nil {
			return err
		}
		return printRecallResult(out, &result, jsonOut)
	}

	cfg, err := ark.LoadConfig(filepath.Join(arkDir, "ark.toml"))
	if err != nil {
		return err
	}

	// R2632, R2633, R2646: server not running — choose a fallback
	// based on the embedding model. File exists → ask the user to start
	// the server. File missing → gripe loudly so typos surface. Unset →
	// silently degrade to in-process trigram-only.
	if cfg.Embedding.Model != "" {
		modelPath := filepath.Join(arkDir, cfg.Embedding.Model)
		switch _, statErr := os.Stat(modelPath); {
		case statErr == nil:
			return fmt.Errorf("server not running; model configured. Please start the server with: ark serve")
		case os.IsNotExist(statErr):
			return fmt.Errorf("configured embedding model not found at %s", modelPath)
		default:
			return statErr
		}
	}

	var runErr error
	withDB(func(db *ark.DB) {
		lib := ark.NewLibrarian(db, arkDir)
		res, err := lib.Recall(inputs, opts)
		if err != nil {
			runErr = err
			return
		}
		runErr = printRecallResult(out, res, jsonOut)
	})
	return runErr
}

// printRecallResult writes the RecallResult to out in markdown or JSON.
// CRC: crc-CLI.md | R2635, R2636, R2637, R2638, R2645
func printRecallResult(out io.Writer, res *ark.RecallResult, jsonOut bool) error {
	if jsonOut {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}

	if res.Warning != "" {
		fmt.Fprintf(out, "@recall-warning: %s\n\n", res.Warning)
	}

	fmt.Fprintln(out, "## Chunks")
	if len(res.Chunks) == 0 {
		fmt.Fprintln(out, "\n_no results_")
		return nil
	}

	ark.RenderRecallChunks(out, res.Chunks)
	return nil
}

// inspectExitResult is the structured output of `ark luhmann inspect-exit`.
type inspectExitResult struct {
	Label          string `json:"label"`
	LastRecordKind string `json:"last_record_kind,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	TokensAtClose  int    `json:"tokens_at_close,omitempty"`
}

// findSubagentJSONLCold is the CLI-side cold lookup of the subagent
// JSONL paired with a given nonce. Mirrors RecallAgentBuilder.findSubagentJSONL
// but reads from disk directly so `ark luhmann inspect-exit` runs without
// a server. R2796
func findSubagentJSONLCold(nonce int) string {
	parent := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if parent == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".claude", "projects")
	needle := fmt.Sprintf("nonce %d", nonce)
	type cand struct {
		meta  string
		mtime time.Time
	}
	var cands []cand
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name(), parent, "subagents")
		matches, _ := filepath.Glob(filepath.Join(dir, "*.meta.json"))
		for _, meta := range matches {
			jsonl := strings.TrimSuffix(meta, ".meta.json") + ".jsonl"
			info, err := os.Stat(jsonl)
			if err != nil {
				continue
			}
			cands = append(cands, cand{meta: meta, mtime: info.ModTime()})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].mtime.After(cands[j].mtime)
	})
	for _, c := range cands {
		data, err := os.ReadFile(c.meta)
		if err != nil {
			continue
		}
		var doc struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}
		if !strings.Contains(doc.Description, needle) {
			continue
		}
		return strings.TrimSuffix(c.meta, ".meta.json") + ".jsonl"
	}
	return ""
}

// classifySubagentExit reads the subagent JSONL backwards and applies
// the inspect-exit classification rules. R2796
func classifySubagentExit(arkDir, jsonl string, nonce int) inspectExitResult {
	if jsonl == "" {
		return inspectExitResult{Label: "unknown"}
	}
	f, err := os.Open(jsonl)
	if err != nil {
		return inspectExitResult{Label: "unknown"}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var lastKind, lastError string
	var tokensAtClose int
	closeSeen := false
	for scanner.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if t, ok := rec["type"].(string); ok {
			lastKind = t
		}
		if rec["isError"] == true {
			if s, ok := rec["error"].(string); ok {
				lastError = s
			} else {
				lastError = "unspecified"
			}
		}
		// Crude marker for close: a tool_result whose tool_use_id
		// matches a recent ark connections recall close invocation.
		if t, _ := rec["type"].(string); t == "tool_result" {
			if input, ok := rec["input"].(map[string]any); ok {
				if cmd, ok := input["command"].(string); ok && strings.Contains(cmd, "ark connections recall close") {
					closeSeen = true
				}
			}
		}
		if usage, ok := rec["usage"].(map[string]any); ok {
			cc, _ := usage["cache_creation_input_tokens"].(float64)
			cr, _ := usage["cache_read_input_tokens"].(float64)
			tokensAtClose = int(cc + cr)
		}
	}
	label := "crash"
	cleanExit := closeSeen || recallOutcomeIs(arkDir, nonce, "result-emitted", "silent-close", "no-subscriber")
	if cleanExit {
		// R2796: a clean turn boundary is only "healthy" when the
		// generation actually filled — tokens at close at/over the
		// configured limit (filled-and-recycled). A clean stop below
		// the limit is "quit-early", not healthy: the agent stopped
		// before filling. The distinct label keeps the early stop
		// visible instead of masquerading as a clean recycle.
		limit := 150000
		if cfg, err := ark.LoadConfig(filepath.Join(arkDir, "ark.toml")); err == nil {
			limit = cfg.Luhmann.EffectiveContextLimit()
		}
		if limit > 0 && tokensAtClose >= limit {
			label = "healthy"
		} else {
			label = "quit-early"
		}
	}
	return inspectExitResult{
		Label:          label,
		LastRecordKind: lastKind,
		LastError:      lastError,
		TokensAtClose:  tokensAtClose,
	}
}

// recallOutcomeIs walks recall.jsonl backwards and returns true when
// the most recent record matching the nonce has an outcome in the
// allowed set.
func recallOutcomeIs(arkDir string, nonce int, allowed ...string) bool {
	data, err := os.ReadFile(ark.MonitorClassPath(arkDir, "recall"))
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &rec); err != nil {
			continue
		}
		if n, _ := rec["nonce"].(float64); int(n) != nonce {
			continue
		}
		outcome, _ := rec["outcome"].(string)
		return slices.Contains(allowed, outcome)
	}
	return false
}
