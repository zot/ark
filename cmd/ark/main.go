package main

// CRC: crc-CLI.md | Seq: seq-cli-dispatch.md

import (
	"bufio"
	"bytes"
	"context"
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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zot/ark"
	"github.com/zot/microfts2"

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
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	// R724, R725, R726, R727: Parse --dir and -v globally before subcommand dispatch
	expanded := cli.ExpandVerbosityFlags(os.Args[1:])
	arkDir = defaultDB()
	var verbosity int
	var filtered []string
	for i := 0; i < len(expanded); i++ {
		arg := expanded[i]
		if arg == "--dir" && i+1 < len(expanded) {
			arkDir = expanded[i+1]
			i++ // skip value
		} else if strings.HasPrefix(arg, "--dir=") {
			arkDir = strings.TrimPrefix(arg, "--dir=")
		} else if arg == "-v" {
			verbosity++
		} else {
			filtered = append(filtered, arg)
		}
	}
	ark.SetVerbosity(verbosity)
	if len(filtered) == 0 {
		usage()
		os.Exit(0)
	}

	cmd := filtered[0]
	if cmd == "--help" || cmd == "-h" || cmd == "help" {
		usage()
		os.Exit(0)
	}
	args := filtered[1:]

	switch cmd {
	case "add":
		cmdAdd(args)
	case "bundle":
		cmdBundle(args)
	case "cat":
		cmdBundleCat(args)
	case "chunk-chat-jsonl":
		cmdChunkJSONL(args)
	case "chunks":
		cmdChunks(args)
	case "config":
		cmdConfig(args)
	case "chats":
		cmdChats(args)
	case "cp":
		cmdBundleCp(args)
	case "dismiss":
		cmdDismiss(args)
	case "embed":
		cmdEmbed(args)
	case "fetch":
		cmdFetch(args)
	case "files":
		cmdFiles(args)
	case "grams":
		cmdGrams(args)
	case "init":
		cmdInit(args)
	case "rebuild":
		cmdRebuild(args)
	case "ls":
		cmdBundleLs(args)
	case "missing":
		cmdMissing(args)
	case "refresh":
		cmdRefresh(args)
	case "remove":
		cmdRemove(args)
	case "resolve":
		cmdResolve(args)
	case "scan":
		cmdScan(args)
	case "schedule":
		cmdSchedule(args)
	case "search":
		cmdSearch(args)
	case "serve":
		cmdServe(args)
	case "setup":
		cmdSetup(args)
	case "sources":
		cmdSources(args)
	case "stale":
		cmdStale(args)
	case "status":
		cmdStatus(args)
	case "stop":
		cmdStop(args)
	case "tag":
		cmdTag(args)
	case "install":
		cmdUIInstall(args)
	case "message":
		cmdMessage(args)
	case "subscribe":
		cmdSubscribe(args)
	case "listen":
		cmdListen(args)
	case "ui":
		cmdUI(args)
	case "unresolved":
		cmdUnresolved(args)
	case "vec":
		cmdVec(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: ark <command> [options]

Commands:
  add         Add files to the index
  bundle      Graft a directory onto a binary as a zip appendix (build-time)
  cat         Print an embedded file to stdout
  chats       Show conversation transcripts from JSONL logs
  chunks      Show chunks around a search hit (context expansion)
  config      Show or modify configuration
              add-source, remove-source, add-include, add-exclude,
              remove-pattern, show-why, add-strategy
  cp          Extract embedded files matching a glob pattern
  dismiss     Dismiss missing files
  embed       Embed text or benchmark embedding model
  fetch       Return full contents of an indexed file
  files       List indexed files
  grams       Show trigrams for a query (active/inactive, frequency)
  init        Create a new database
  install     Connect this project to ark (alias for ui install)
  ls          List embedded assets
  message     Messaging (new-request, new-response, set-tags, get-tags, check, inbox)
  missing     List missing files
  rebuild     Drop and rebuild the entire index
  refresh     Re-index stale files
  remove      Remove files from the index
  resolve     Dismiss unresolved files by pattern
  scan        Walk directories, index new files
  schedule    Query and modify scheduled events (requires server)
  search      Search the index (subcommands: expand)
  serve       Start the server
  subscribe   Manage tag subscriptions (requires server)
  listen      Long-poll for tag notifications (requires server)
  setup       Bootstrap ~/.ark/ (extract assets, install skills)
  sources     Manage source directories
  stale       List stale files
  status      Show database status
  stop        Stop the running server
  tag         Tag operations (list, counts, files, defs)
  ui          UI operations (run, display, event, checkpoint, ...)
  unresolved  List unresolved files

Global flags:
  --dir PATH  Database directory (default ~/.ark/)
  -v          Increase verbosity (up to -vvvv)`)

}

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

// serverClient returns an http.Client that connects over Unix socket,
// or nil if no server is running.
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

// Command implementations

// CRC: crc-CLI.md | Seq: seq-install.md
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
		if _, err := os.Stat(filepath.Join(arkDir, "data.mdb")); err == nil {
			return
		}
	}

	// Remove existing database files before creating fresh
	for _, f := range []string{"data.mdb", "lock.mdb"} {
		os.Remove(filepath.Join(arkDir, f))
	}

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
	// scan to re-index all sources
	cmdScan(nil)
	fmt.Println("rebuild complete")
	// R1294: embeddings regenerate on next server start (batch embed post-reconcile)
	cfg, _ := ark.LoadConfig(filepath.Join(arkDir, "ark.toml"))
	if cfg != nil && cfg.TagModel != "" {
		fmt.Println("tag embeddings will regenerate on next 'ark serve'")
	}
}

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
			"path": paths[0], "strategy": strat, "content": string(content),
		}); err != nil {
			fatal(err)
		}
		return
	}

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/add", map[string]any{
			"paths": paths, "strategy": *strategy,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withDB(func(d *ark.DB) {
		if err := d.Add(paths, *strategy); err != nil {
			fatal(err)
		}
	})
}

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

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/remove", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		return
	}

	withDB(func(d *ark.DB) {
		if err := d.Remove(patterns); err != nil {
			fatal(err)
		}
	})
}

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark scan\n\nWalk configured source directories, index new files, flag unresolved.\nDoes not re-check existing files (use refresh for that).")
	}
	fs.Parse(args)

	if client := serverClient(arkDir); client != nil {
		var result struct {
			NewFiles      int `json:"newFiles"`
			NewUnresolved int `json:"newUnresolved"`
		}
		if err := proxyDecode(client, "POST", "/scan", nil, &result); err != nil {
			fatal(err)
		}
		fmt.Printf("new files: %d, new unresolved: %d\n", result.NewFiles, result.NewUnresolved)
		return
	}

	withDB(func(d *ark.DB) {
		results, err := d.Scan()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("new files: %d, new unresolved: %d\n",
			len(results.NewFiles), len(results.NewUnresolved))
	})
}

func cmdRefresh(args []string) {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark refresh [PATTERN...]\n\nRe-index stale files. Optional glob patterns to scope the refresh.")
	}
	fs.Parse(args)

	patterns := fs.Args()

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/refresh", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		fmt.Println("refresh complete")
		return
	}

	withDB(func(d *ark.DB) {
		if err := d.Refresh(patterns); err != nil {
			fatal(err)
		}
		fmt.Println("refresh complete")
	})
}

func cmdSearch(args []string) {
	// Subcommand dispatch
	if len(args) > 0 && args[0] == "expand" {
		cmdSearchExpand(args[1:])
		return
	}
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	k := fs.Int("k", 20, "max results")
	scores := fs.Bool("scores", false, "show scores")
	after := fs.String("after", "", "only results after date")
	before := fs.String("before", "", "only results before date")
	about := fs.String("about", "", "semantic search query")
	contains := fs.String("contains", "", "exact match query")
	var regex, exceptRegex stringSlice
	fs.Var(&regex, "regex", "regex query (repeatable, AND logic)")
	fs.Var(&exceptRegex, "except-regex", "regex exclude filter (repeatable, any match rejects)")
	likeFile := fs.String("like-file", "", "find similar files using FTS density scoring")
	score := fs.String("score", "", "scoring strategy: auto (default), coverage, density")
	multi := fs.Bool("multi", false, "run all strategies (coverage, density, overlap, bm25)")
	fuzzy := fs.Bool("fuzzy", false, "typo-tolerant search (OR-union of trigrams with re-scoring)")
	proximity := fs.Bool("proximity", false, "rerank top results by query term proximity")
	session := fs.String("session", "", "named session for cross-query cache (requires server)")
	noTmp := fs.Bool("no-tmp", false, "exclude tmp:// documents from results")
	tags := fs.Bool("tags", false, "output extracted tags instead of content")
	chunks := fs.Bool("chunks", false, "emit chunk text as JSONL")
	files := fs.Bool("files", false, "emit full file content as JSONL")
	preview := fs.Int("preview", 0, "with --chunks: extract N-char preview window around match")
	wrap := fs.String("wrap", "", "wrap output in XML tags (e.g. memory, knowledge)")
	cpuProfile := fs.String("cpuprofile", "", "write CPU profile to file")
	memProfile := fs.String("memprofile", "", "write memory profile to file")
	traceFile := fs.String("trace", "", "write execution trace to file (view with go tool trace or Chrome DevTools)")
	var filter, except, filterFiles, excludeFiles, filterFileTags, excludeFileTags stringSlice
	fs.Var(&filter, "filter", "content-based positive filter (repeatable, FTS query)")
	fs.Var(&except, "except", "content-based negative filter (repeatable, FTS query)")
	fs.Var(&filterFiles, "filter-files", "path-based positive filter (repeatable, glob pattern)")
	fs.Var(&excludeFiles, "exclude-files", "path-based negative filter (repeatable, glob pattern)")
	fs.Var(&filterFileTags, "filter-file-tags", "tag-based positive filter (repeatable, tag name)")
	fs.Var(&excludeFileTags, "exclude-file-tags", "tag-based negative filter (repeatable, tag name)")
	fs.Parse(args)

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

	if *chunks && *files {
		fmt.Fprintln(os.Stderr, "error: --chunks and --files are mutually exclusive")
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

	switch *score {
	case "", "auto", "coverage", "density":
		// valid
	default:
		fmt.Fprintf(os.Stderr, "error: unknown --score mode: %s\n", *score)
		os.Exit(1)
	}

	// R590: --multi is mutually exclusive with --score
	if *multi && *score != "" {
		fmt.Fprintln(os.Stderr, "error: --multi and --score are mutually exclusive")
		os.Exit(1)
	}
	// R592: --multi does not apply to --regex, --about, or --like-file
	if *multi && (*about != "" || len(regex) > 0 || *likeFile != "") {
		fmt.Fprintln(os.Stderr, "error: --multi cannot be used with --about, --regex, or --like-file")
		os.Exit(1)
	}

	isSplit := *about != "" || *contains != "" || len(regex) > 0 || *likeFile != ""

	// Server-first: proxy to server if available, fall back to local.
	// Server keeps caches warm (file name map, LMDB pages).
	if client := serverClient(arkDir); client != nil {
		req := struct {
			Query           string   `json:"query"`
			About           string   `json:"about,omitempty"`
			Contains        string   `json:"contains,omitempty"`
			Regex           []string `json:"regex,omitempty"`
			ExceptRegex     []string `json:"exceptRegex,omitempty"`
			LikeFile        string   `json:"likeFile,omitempty"`
			K               int      `json:"k"`
			Scores          bool     `json:"scores,omitempty"`
			After           string   `json:"after,omitempty"`
			Before          string   `json:"before,omitempty"`
			Chunks          bool     `json:"chunks,omitempty"`
			Files           bool     `json:"files,omitempty"`
			Tags            bool     `json:"tags,omitempty"`
			Filter          []string `json:"filter,omitempty"`
			Except          []string `json:"except,omitempty"`
			FilterFiles     []string `json:"filterFiles,omitempty"`
			ExcludeFiles    []string `json:"excludeFiles,omitempty"`
			FilterFileTags  []string `json:"filterFileTags,omitempty"`
			ExcludeFileTags []string `json:"excludeFileTags,omitempty"`
			Session         string   `json:"session,omitempty"`
			NoTmp           bool     `json:"noTmp,omitempty"`
		}{
			Query:           strings.Join(fs.Args(), " "),
			About:           *about,
			Contains:        *contains,
			Regex:           []string(regex),
			ExceptRegex:     []string(exceptRegex),
			LikeFile:        *likeFile,
			K:               *k,
			Scores:          *scores,
			After:           *after,
			Before:          *before,
			Chunks:          *chunks,
			Files:           *files,
			Tags:            *tags,
			Filter:          []string(filter),
			Except:          []string(except),
			FilterFiles:     ark.ExpandTildeSlice([]string(filterFiles)),
			ExcludeFiles:    ark.ExpandTildeSlice([]string(excludeFiles)),
			FilterFileTags:  []string(filterFileTags),
			ExcludeFileTags: []string(excludeFileTags),
			Session:         *session,
			NoTmp:           *noTmp,
		}
		var results []ark.SearchResultEntry
		if err := proxyDecode(client, "POST", "/search", req, &results); err == nil {
			var queryStr string
			if *contains != "" {
				queryStr = *contains
			} else if *about != "" {
				queryStr = *about
			} else if len(regex) > 0 {
				queryStr = regex[0]
			} else {
				queryStr = strings.Join(fs.Args(), " ")
			}
			if *tags {
				printTagResults(results, *scores)
			} else {
				printSearchResults(results, *scores, *chunks, *files, *wrap, *preview, queryStr)
			}
			return
		}
		// Server proxy failed — fall through to local search
	}

	// Local LMDB path (fallback when server not running)
	withDB(func(d *ark.DB) {
		done := d.NewSearchCache()
		defer done()
		opts := ark.SearchOpts{
			K:               *k,
			Scores:          *scores,
			After:           afterTime,
			Before:          beforeTime,
			About:           *about,
			Contains:        *contains,
			Regex:           []string(regex),
			ExceptRegex:     []string(exceptRegex),
			LikeFile:        *likeFile,
			Tags:            *tags,
			Filter:          []string(filter),
			Except:          []string(except),
			FilterFiles:     ark.ExpandTildeSlice([]string(filterFiles)),
			ExcludeFiles:    ark.ExpandTildeSlice([]string(excludeFiles)),
			FilterFileTags:  []string(filterFileTags),
			ExcludeFileTags: []string(excludeFileTags),
			Score:           *score,
			Multi:           *multi,
			Fuzzy:           *fuzzy,
			Proximity:       *proximity,
			NoTmp:           *noTmp,
		}

		var results []ark.SearchResultEntry
		var err error
		if *multi {
			// R585: multi-strategy search
			query := strings.Join(fs.Args(), " ")
			if query == "" && *contains == "" {
				fmt.Fprintln(os.Stderr, "error: no search query")
				os.Exit(1)
			}
			if query == "" {
				query = *contains
			}
			results, err = d.SearchMulti(query, opts)
		} else if *fuzzy {
			// R738: typo-tolerant search
			query := strings.Join(fs.Args(), " ")
			if query == "" {
				fmt.Fprintln(os.Stderr, "error: no search query")
				os.Exit(1)
			}
			results, err = d.SearchFuzzy(query, opts)
		} else if isSplit {
			results, err = d.SearchSplit(opts)
		} else {
			query := strings.Join(fs.Args(), " ")
			if query == "" {
				fmt.Fprintln(os.Stderr, "error: no search query")
				os.Exit(1)
			}
			results, err = d.SearchCombined(query, opts)
		}
		if err != nil {
			fatal(err)
		}

		// Fill content if requested
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

		// Determine query for preview extraction
		var query string
		if *contains != "" {
			query = *contains
		} else if *about != "" {
			query = *about
		} else if len(regex) > 0 {
			query = regex[0]
		} else {
			query = strings.Join(fs.Args(), " ")
		}

		if *tags {
			printTagResults(results, *scores)
		} else {
			printSearchResults(results, *scores, *chunks, *files, *wrap, *preview, query)
		}
	})
}

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
			fmt.Printf("%s:%s\n", r.Path, r.Range)
		}
	}
}

func printTagResults(results []ark.SearchResultEntry, scores bool) {
	printTagResultsDirect(ark.ExtractResultTags(results), scores)
}

func printTagResultsDirect(tags []ark.TagResult, scores bool) {
	for _, t := range tags {
		if scores {
			fmt.Printf("%s\t%d\t%.4f\n", t.Tag, t.Count, t.BestScore)
		} else {
			fmt.Printf("%s\t%d\n", t.Tag, t.Count)
		}
	}
}

// CRC: crc-CLI.md | Seq: seq-spectral-expand.md | R1243
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

// CRC: crc-CLI.md | R1302-R1305
func cmdEmbed(args []string) {
	fs := flag.NewFlagSet("embed", flag.ExitOnError)
	bench := fs.String("bench", "", "benchmark mode: tags or chunks")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark embed [options] <text>

Embed text using the configured tag model. Prints the vector as JSON.

Options:
  --bench tags     Embed all tag values, report timing
  --bench chunks   Embed random file chunks, report timing`)
		fs.PrintDefaults()
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fs.Usage()
		os.Exit(0)
	}
	fs.Parse(args)

	withDB(func(db *ark.DB) {
		lib := ark.NewLibrarian(db, arkDir)
		if lib == nil {
			fatal(fmt.Errorf("claude not on PATH"))
		}
		if !lib.EmbeddingAvailable() {
			fatal(fmt.Errorf("tag_model not configured in ark.toml or model file not found"))
		}

		if *bench == "tags" {
			// Benchmark: embed all tag values (batch vs single)
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

			// Batch benchmark
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

			// Single benchmark
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
			return
		}

		if *bench == "chunks" {
			// Sample 200 real chunks using the chunker, then benchmark batch vs single
			files, err := db.Files()
			if err != nil {
				fatal(err)
			}
			if len(files) == 0 {
				fmt.Println("no indexed files")
				return
			}

			// Sample 200 chunks: pick file at random, read its chunks, pick one.
			// File-first sampling prevents large files (JSONL) from dominating.
			const sampleSize = 200
			fileChunkCache := make(map[string][]string)
			var chunks []string

			for len(chunks) < sampleSize {
				// Pick a random file
				fpath := files[rand.Intn(len(files))]
				cached, ok := fileChunkCache[fpath]
				if !ok {
					// Read file content and split into ~512-byte chunks
					// (approximates real chunker boundaries)
					data, err := os.ReadFile(fpath)
					if err != nil || len(data) == 0 {
						fileChunkCache[fpath] = nil
						continue
					}
					text := string(data)
					for i := 0; i < len(text); i += 512 {
						end := min(i+512, len(text))
						cached = append(cached, text[i:end])
					}
					fileChunkCache[fpath] = cached
				}
				if len(cached) == 0 {
					continue
				}
				chunks = append(chunks, cached[rand.Intn(len(cached))])
			}

			// Stats
			var totalBytes int
			for _, c := range chunks {
				totalBytes += len(c)
			}
			fmt.Printf("sampled %d chunks from %d files (avg %d bytes/chunk)\n",
				len(chunks), len(fileChunkCache), totalBytes/max(len(chunks), 1))

			// Batch benchmark
			batchSize := 50
			start := time.Now()
			var batchTotal int
			for i := 0; i < len(chunks); i += batchSize {
				end := min(i+batchSize, len(chunks))
				vecs, err := lib.EmbedBatch(chunks[i:end])
				if err != nil {
					fmt.Fprintf(os.Stderr, "batch error at %d: %v\n", i, err)
					continue
				}
				batchTotal += len(vecs)
			}
			batchElapsed := time.Since(start)
			fmt.Printf("batch(%d): embedded %d chunks in %v (%.1f ms/chunk)\n",
				batchSize, batchTotal, batchElapsed,
				float64(batchElapsed.Milliseconds())/float64(max(batchTotal, 1)))

			// Single benchmark
			start = time.Now()
			var embedded int
			for _, c := range chunks {
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
			return
		}

		// Single text embedding
		rest := fs.Args()
		if len(rest) < 1 {
			fs.Usage()
			os.Exit(1)
		}
		text := strings.Join(rest, " ")
		vec, err := lib.EmbedQuery(text)
		if err != nil {
			fatal(err)
		}
		out, err := json.Marshal(vec)
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(out)
		fmt.Fprintln(os.Stdout)
	})
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	noScan := fs.Bool("no-scan", false, "skip startup reconciliation")
	fs.Parse(args)

	err := ark.Serve(arkDir, ark.ServeOpts{NoScan: *noScan})
	if errors.Is(err, ark.ServerAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "ark server already running")
		os.Exit(0)
	}
	if err != nil {
		fatal(err)
	}
}

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
		for name, count := range status.Strategies {
			fmt.Printf(" %s=%d", name, count)
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

// CRC: crc-CLI.md | R899, R900, R901, R902
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
		fmt.Printf("  %s %-14s %7d  keys %-10s  vals %s\n",
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
			if m.Match(pat, p, false) {
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

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	showDB := fs.Bool("db", false, "show LMDB record counts by type")
	fs.Parse(args)

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
		return
	}

	withDB(func(d *ark.DB) {
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
	})
}

func cmdFiles(args []string) {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	showStatus := fs.Bool("status", false, "show file status (G/S/M)")
	fs.Parse(args)
	patterns := fs.Args()

	if !*showStatus {
		// Simple path: just list files
		if client := serverClient(arkDir); client != nil {
			var files []string
			if err := proxyDecode(client, "GET", "/files", nil, &files); err != nil {
				fatal(err)
			}
			printLines(filterPaths(files, patterns))
			return
		}
		withDB(func(d *ark.DB) {
			files, err := d.Files()
			if err != nil {
				fatal(err)
			}
			printLines(filterPaths(files, patterns))
		})
		return
	}

	// --status: need files + stale + missing to compute status
	var files, stale []string
	var missing []ark.MissingRecord
	if client := serverClient(arkDir); client != nil {
		if err := proxyDecode(client, "GET", "/files", nil, &files); err != nil {
			fatal(err)
		}
		if err := proxyDecode(client, "GET", "/stale", nil, &stale); err != nil {
			fatal(err)
		}
		if err := proxyDecode(client, "GET", "/missing", nil, &missing); err != nil {
			fatal(err)
		}
	} else {
		withDB(func(d *ark.DB) {
			var err error
			files, err = d.Files()
			if err != nil {
				fatal(err)
			}
			stale, err = d.Stale()
			if err != nil {
				fatal(err)
			}
			missing, err = d.Missing()
			if err != nil {
				fatal(err)
			}
		})
	}

	staleSet := make(map[string]bool, len(stale))
	for _, s := range stale {
		staleSet[s] = true
	}
	missingSet := make(map[string]bool, len(missing))
	for _, m := range missing {
		missingSet[m.Path] = true
	}

	files = filterPaths(files, patterns)
	for _, f := range files {
		switch {
		case missingSet[f]:
			fmt.Printf("M %s\n", f)
		case staleSet[f]:
			fmt.Printf("S %s\n", f)
		default:
			fmt.Printf("G %s\n", f)
		}
	}
}

func cmdStale(args []string) {
	fs := flag.NewFlagSet("stale", flag.ExitOnError)
	fs.Parse(args)
	patterns := fs.Args()

	if client := serverClient(arkDir); client != nil {
		var stale []string
		if err := proxyDecode(client, "GET", "/stale", nil, &stale); err != nil {
			fatal(err)
		}
		printLines(filterPaths(stale, patterns))
		return
	}

	withDB(func(d *ark.DB) {
		stale, err := d.Stale()
		if err != nil {
			fatal(err)
		}
		printLines(filterPaths(stale, patterns))
	})
}

func cmdMissing(args []string) {
	fs := flag.NewFlagSet("missing", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark missing [PATTERN...]\n\nList files that are indexed but no longer exist on disk.\nOptional patterns to filter the list.")
	}
	fs.Parse(args)
	patterns := fs.Args()

	if client := serverClient(arkDir); client != nil {
		var missing []ark.MissingRecord
		if err := proxyDecode(client, "GET", "/missing", nil, &missing); err != nil {
			fatal(err)
		}
		var paths []string
		for _, m := range missing {
			paths = append(paths, m.Path)
		}
		printLines(filterPaths(paths, patterns))
		return
	}

	withDB(func(d *ark.DB) {
		missing, err := d.Missing()
		if err != nil {
			fatal(err)
		}
		var paths []string
		for _, m := range missing {
			paths = append(paths, m.Path)
		}
		printLines(filterPaths(paths, patterns))
	})
}

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

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/dismiss", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		return
	}

	withDB(func(d *ark.DB) {
		if err := d.Dismiss(patterns); err != nil {
			fatal(err)
		}
	})
}

// Seq: seq-config-mutate.md
func cmdConfig(args []string) {
	if len(args) == 0 {
		cmdConfigShow(args)
		return
	}
	// Check for sub-subcommand before flag parsing
	switch args[0] {
	case "add-source":
		cmdConfigAddSource(args[1:])
	case "remove-source":
		cmdConfigRemoveSource(args[1:])
	case "add-include":
		cmdConfigAddInclude(args[1:])
	case "add-exclude":
		cmdConfigAddExclude(args[1:])
	case "remove-pattern":
		cmdConfigRemovePattern(args[1:])
	case "show-why":
		cmdConfigShowWhy(args[1:])
	case "add-strategy":
		cmdConfigAddStrategy(args[1:])
	default:
		// No sub-subcommand — treat as flags for "show"
		cmdConfigShow(args)
	}
}

func cmdConfigShow(args []string) {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	fs.Parse(args)

	if client := serverClient(arkDir); client != nil {
		data, err := proxyRaw(client, "GET", "/config", nil)
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(data)
		return
	}

	configPath := filepath.Join(arkDir, "ark.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		fatal(err)
	}
	os.Stdout.Write(data)
}

// withConfig loads ark.toml, applies a mutation, and saves it back.
func withConfig(dbPath string, fn func(cfg *ark.Config) error) {
	configPath := filepath.Join(dbPath, "ark.toml")
	cfg, err := ark.LoadConfig(configPath)
	if err != nil {
		fatal(err)
	}
	if err := fn(cfg); err != nil {
		fatal(err)
	}
	if err := cfg.SaveConfig(configPath); err != nil {
		fatal(err)
	}
}

func cmdConfigAddSource(args []string) {
	fs := flag.NewFlagSet("config add-source", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark config add-source <dir>\n\nAdd a source directory (or glob pattern) to ark.toml.")
	}
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: directory path required")
		os.Exit(1)
	}
	dir := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-source", map[string]string{
			"dir": dir,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddSource(dir) })
}

func cmdConfigRemoveSource(args []string) {
	fs := flag.NewFlagSet("config remove-source", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark config remove-source <dir>\n\nRemove a source directory from ark.toml.\nIndexed files become orphans until dismissed or re-added.")
	}
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: directory path required")
		os.Exit(1)
	}
	dir := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/remove-source", map[string]string{
			"dir": dir,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.RemoveSource(dir) })
}

func cmdConfigAddInclude(args []string) {
	fs := flag.NewFlagSet("config add-include", flag.ExitOnError)
	source := fs.String("source", "", "source directory (empty for global)")
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: pattern required")
		os.Exit(1)
	}
	pattern := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-include", map[string]string{
			"pattern": pattern, "source": *source,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddInclude(pattern, *source) })
}

func cmdConfigAddExclude(args []string) {
	fs := flag.NewFlagSet("config add-exclude", flag.ExitOnError)
	source := fs.String("source", "", "source directory (empty for global)")
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: pattern required")
		os.Exit(1)
	}
	pattern := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-exclude", map[string]string{
			"pattern": pattern, "source": *source,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddExclude(pattern, *source) })
}

func cmdConfigRemovePattern(args []string) {
	fs := flag.NewFlagSet("config remove-pattern", flag.ExitOnError)
	source := fs.String("source", "", "source directory (empty for global)")
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: pattern required")
		os.Exit(1)
	}
	pattern := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/remove-pattern", map[string]string{
			"pattern": pattern, "source": *source,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.RemovePattern(pattern, *source) })
}

func cmdConfigShowWhy(args []string) {
	fs := flag.NewFlagSet("config show-why", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: file path required")
		os.Exit(1)
	}
	filePath := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		var result ark.WhyResult
		if err := proxyDecode(client, "POST", "/config/show-why", map[string]string{
			"path": filePath,
		}, &result); err != nil {
			fatal(err)
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return
	}

	configPath := filepath.Join(arkDir, "ark.toml")
	cfg, err := ark.LoadConfig(configPath)
	if err != nil {
		fatal(err)
	}
	result, err := cfg.ShowWhy(filePath)
	if err != nil {
		fatal(err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func cmdConfigAddStrategy(args []string) {
	fs := flag.NewFlagSet("config add-strategy", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: pattern and strategy required (e.g. '*.md' markdown)")
		os.Exit(1)
	}
	pattern := fs.Arg(0)
	strategy := fs.Arg(1)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-strategy", map[string]string{
			"pattern": pattern, "strategy": strategy,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error {
		return cfg.AddStrategy(pattern, strategy)
	})
}

func cmdGrams(args []string) {
	fs := flag.NewFlagSet("grams", flag.ExitOnError)
	fs.Parse(args)

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: query required")
		os.Exit(1)
	}

	withDB(func(d *ark.DB) {
		trigrams, err := d.QueryTrigramCounts(query)
		if err != nil {
			fatal(err)
		}
		for _, t := range trigrams {
			fmt.Printf("%q\t%d\n", microfts2.DecodeTrigram(t.Trigram), t.Count)
		}
	})
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
		if client := serverClient(arkDir); client != nil {
			var result ark.SourcesCheckResult
			if err := proxyDecode(client, "POST", "/config/sources-check", nil, &result); err != nil {
				fatal(err)
			}
			printSourcesCheck(&result)
			return
		}
		withDB(func(d *ark.DB) {
			result, err := d.SourcesCheck()
			if err != nil {
				fatal(err)
			}
			printSourcesCheck(result)
		})
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

func cmdUnresolved(args []string) {
	fs := flag.NewFlagSet("unresolved", flag.ExitOnError)
	fs.Parse(args)

	if client := serverClient(arkDir); client != nil {
		var unresolved []ark.UnresolvedRecord
		if err := proxyDecode(client, "GET", "/unresolved", nil, &unresolved); err != nil {
			fatal(err)
		}
		for _, u := range unresolved {
			fmt.Println(u.Path)
		}
		return
	}

	withDB(func(d *ark.DB) {
		unresolved, err := d.Unresolved()
		if err != nil {
			fatal(err)
		}
		for _, u := range unresolved {
			fmt.Println(u.Path)
		}
	})
}

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

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/resolve", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		return
	}

	withDB(func(d *ark.DB) {
		if err := d.Resolve(patterns); err != nil {
			fatal(err)
		}
	})
}

func cmdFetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	wrap := fs.String("wrap", "", "wrap output in XML tags (e.g. memory, knowledge)")
	fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "error: file path(s) required")
		os.Exit(1)
	}

	// R692: tmp:// paths must proxy to server (content is in server memory)
	hasTmp := false
	for _, p := range paths {
		if strings.HasPrefix(p, "tmp://") {
			hasTmp = true
			break
		}
	}

	if hasTmp {
		client := serverClient(arkDir)
		if client == nil {
			fmt.Fprintln(os.Stderr, "error: tmp:// requires a running server")
			os.Exit(1)
		}
		for _, filePath := range paths {
			data, err := proxyRaw(client, "POST", "/fetch", map[string]string{"path": filePath})
			if err != nil {
				fatal(err)
			}
			var resp struct{ Content string }
			if err := json.Unmarshal(data, &resp); err != nil {
				fatal(fmt.Errorf("decode fetch response: %w", err))
			}
			content := resp.Content
			if *wrap != "" {
				fmt.Printf("<%s source=%q>\n", *wrap, filePath)
				writeEscaped(os.Stdout, content, *wrap)
				fmt.Printf("</%s>\n", *wrap)
			} else {
				os.Stdout.WriteString(content)
			}
		}
		return
	}

	// Local LMDB — pure read, mmap shares pages with server.
	withDB(func(d *ark.DB) {
		for _, filePath := range paths {
			data, err := d.Fetch(filePath)
			if err != nil {
				fatal(err)
			}
			content := string(data)

			if *wrap != "" {
				absPath, _ := filepath.Abs(filePath)
				fmt.Printf("<%s source=%q>\n", *wrap, absPath)
				writeEscaped(os.Stdout, content, *wrap)
				fmt.Printf("</%s>\n", *wrap)
			} else {
				os.Stdout.WriteString(content)
			}
		}
	})
}

// CRC: crc-CLI.md
func cmdChunks(args []string) {
	fs := flag.NewFlagSet("chunks", flag.ExitOnError)
	before := fs.Int("before", 0, "number of chunks before target")
	after := fs.Int("after", 0, "number of chunks after target")
	wrap := fs.String("wrap", "", "wrap output in XML tags")
	fs.Parse(reorderArgs(args))

	posArgs := fs.Args()
	if len(posArgs) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ark chunks <path> <range> [-before N] [-after N]")
		os.Exit(1)
	}
	filePath := posArgs[0]
	chunkRange := posArgs[1]

	withDB(func(d *ark.DB) {
		results, err := d.GetChunks(filePath, chunkRange, *before, *after)
		if err != nil {
			fatal(err)
		}
		if *wrap != "" {
			for _, c := range results {
				fmt.Printf("<%s source=%q range=%q>\n", *wrap, c.Path, c.Range)
				writeEscaped(os.Stdout, c.Content, *wrap)
				fmt.Printf("</%s>\n", *wrap)
			}
		} else {
			enc := json.NewEncoder(os.Stdout)
			for _, c := range results {
				enc.Encode(c)
			}
		}
	})
}

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

// CRC: crc-CLI.md
func cmdUI(args []string) {
	if len(args) == 0 {
		cmdUIOpen(nil)
		return
	}
	sub := args[0]
	if sub == "--help" || sub == "-h" {
		uiUsage("")
		os.Exit(0)
	}
	subArgs := args[1:]
	switch sub {
	case "audit":
		cmdUIAudit(subArgs)
	case "open":
		cmdUIOpen(subArgs)
	case "checkpoint":
		cmdUICheckpoint(subArgs)
	case "display":
		cmdUIDisplay(subArgs)
	case "event":
		cmdUIEvent(subArgs)
	case "install":
		cmdUIInstall(subArgs)
	case "linkapp":
		cmdUILinkapp(subArgs)
	case "patterns":
		cmdUIPatterns(subArgs)
	case "progress":
		cmdUIProgress(subArgs)
	case "reload":
		cmdUIReload(subArgs)
	case "run":
		cmdUIRun(subArgs)
	case "state":
		cmdUIState(subArgs)
	case "status":
		cmdUIStatus(subArgs)
	case "theme":
		cmdUITheme(subArgs)
	case "update":
		cmdUIUpdate(subArgs)
	case "variables":
		cmdUIVariables(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown ui subcommand: %s\n", sub)
		uiUsage("")
		os.Exit(1)
	}
}

func uiUsage(prefix string) {
	if prefix != "" {
		fmt.Fprintln(os.Stderr, prefix)
	}
	fmt.Fprintln(os.Stderr, `Usage: ark ui [subcommand]

Subcommands:
  (none)                     open browser to UI
  audit <app>                run code quality audit
  browser                    open browser to current session
  checkpoint <cmd> <app>     manage app checkpoints
  display <app>              display app in the browser
  event                      wait for next UI event (120s timeout)
  install                    connect this project to ark
  linkapp add|remove <app>   manage app symlinks
  patterns                   list available patterns
  progress <app> <pct> <msg> report build progress
  reload                     reload UI (fresh Lua VM)
  run '<lua>'                execute Lua code in UI session
  state                      get current session state
  status                     ui-engine server status
  theme list|classes|audit   theme management
  update [-t]                smart update or version check
  variables                  get current variable values`)
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

// CRC: crc-CLI.md | Seq: seq-install.md
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

// CRC: crc-CLI.md
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

// CRC: crc-CLI.md | R607, R608, R609, R610, R611, R612, R615
func cmdTag(args []string) {
	tagUsage := `Usage: ark tag <subcommand>

Subcommands:
  list              List all known tags with counts
  counts <tag>...   Show count for each specified tag
  files <tag>...    Show files containing specified tags
  values <tag>...   Show known values for tags
  defs [TAG...]     Show tag definitions (from tags.md)
  set FILE TAG VAL  Update or add tags in a file's tag block
  get FILE [TAG...] Read tags from a file's tag block
  check FILE [H...] Validate tag block structure`

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, tagUsage)
		os.Exit(0)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "list":
		cmdTagList(subArgs)
	case "counts":
		cmdTagCounts(subArgs)
	case "files":
		cmdTagFiles(subArgs)
	case "values":
		cmdTagValues(subArgs)
	case "defs":
		cmdTagDefs(subArgs)
	case "set":
		cmdTagSet(subArgs)
	case "get":
		cmdTagGet(subArgs)
	case "check":
		cmdTagCheck(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown tag subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, tagUsage)
		os.Exit(1)
	}
}

func cmdTagList(args []string) {
	fs := flag.NewFlagSet("tag list", flag.ExitOnError)
	fs.Parse(args)

	if client := serverClient(arkDir); client != nil {
		var tags []ark.TagCount
		if err := proxyDecode(client, "GET", "/tags", nil, &tags); err != nil {
			fatal(err)
		}
		for _, t := range tags {
			fmt.Printf("%s\t%d\n", t.Tag, t.Count)
		}
		return
	}

	withDB(func(d *ark.DB) {
		tags, err := d.TagList()
		if err != nil {
			fatal(err)
		}
		for _, t := range tags {
			fmt.Printf("%s\t%d\n", t.Tag, t.Count)
		}
	})
}

func cmdTagCounts(args []string) {
	fs := flag.NewFlagSet("tag counts", flag.ExitOnError)
	fs.Parse(args)

	tags := fs.Args()
	if len(tags) == 0 {
		fmt.Fprintln(os.Stderr, "error: no tags specified")
		os.Exit(1)
	}

	if client := serverClient(arkDir); client != nil {
		var counts []ark.TagCount
		if err := proxyDecode(client, "POST", "/tags/counts", map[string]any{"tags": tags}, &counts); err != nil {
			fatal(err)
		}
		for _, t := range counts {
			fmt.Printf("%s\t%d\n", t.Tag, t.Count)
		}
		return
	}

	withDB(func(d *ark.DB) {
		counts, err := d.TagCounts(tags)
		if err != nil {
			fatal(err)
		}
		for _, t := range counts {
			fmt.Printf("%s\t%d\n", t.Tag, t.Count)
		}
	})
}

func cmdTagFiles(args []string) {
	fs := flag.NewFlagSet("tag files", flag.ExitOnError)
	context := fs.Bool("context", false, "show tag occurrences with context")
	var filterFiles, excludeFiles stringSlice
	fs.Var(&filterFiles, "filter-files", "path-based positive filter (repeatable, glob pattern)")
	fs.Var(&excludeFiles, "exclude-files", "path-based negative filter (repeatable, glob pattern)")
	fs.Parse(args)

	tags := fs.Args()
	if len(tags) == 0 {
		fmt.Fprintln(os.Stderr, "error: no tags specified")
		os.Exit(1)
	}

	if *context {
		cmdTagFilesContext(tags, filterFiles, excludeFiles)
		return
	}

	if client := serverClient(arkDir); client != nil {
		var files []ark.TagFileInfo
		if err := proxyDecode(client, "POST", "/tags/files", map[string]any{"tags": tags}, &files); err != nil {
			fatal(err)
		}
		for _, f := range files {
			if matchPath(f.Path, filterFiles, excludeFiles) {
				fmt.Printf("%s\t%d\n", f.Path, f.Size)
			}
		}
		return
	}

	withDB(func(d *ark.DB) {
		files, err := d.TagFiles(tags)
		if err != nil {
			fatal(err)
		}
		for _, f := range files {
			if matchPath(f.Path, filterFiles, excludeFiles) {
				fmt.Printf("%s\t%d\n", f.Path, f.Size)
			}
		}
	})
}

// matchPath returns true if a path passes the include/exclude filters.
func matchPath(path string, include, exclude []string) bool {
	include = ark.ExpandTildeSlice(include)
	exclude = ark.ExpandTildeSlice(exclude)
	m := &ark.Matcher{Dotfiles: true}
	if len(include) > 0 {
		matched := false
		for _, pat := range include {
			if m.Match(pat, path, false) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pat := range exclude {
		if m.Match(pat, path, false) {
			return false
		}
	}
	return true
}

// R1131, R1132, R1133, R1134, R1135, R1136, R1137, R1138
func cmdTagValues(args []string) {
	fs := flag.NewFlagSet("tag values", flag.ExitOnError)
	files := fs.Bool("files", false, "show file paths for each value")
	var filterFiles, excludeFiles stringSlice
	fs.Var(&filterFiles, "filter-files", "path-based positive filter (repeatable, glob pattern)")
	fs.Var(&excludeFiles, "exclude-files", "path-based negative filter (repeatable, glob pattern)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark tag values [-files] [-filter-files GLOB] [-exclude-files GLOB] <tag>...")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	tags := fs.Args()
	if len(tags) == 0 {
		fmt.Fprintln(os.Stderr, "error: no tags specified")
		os.Exit(1)
	}

	filtering := len(filterFiles) > 0 || len(excludeFiles) > 0

	withDB(func(d *ark.DB) {
		for _, tag := range tags {
			if *files || filtering {
				values, err := d.TagValuesWithFiles(tag, "")
				if err != nil {
					fatal(err)
				}
				for _, v := range values {
					matched := v.Files
					if filtering {
						matched = nil
						for _, f := range v.Files {
							if matchPath(f, filterFiles, excludeFiles) {
								matched = append(matched, f)
							}
						}
						if len(matched) == 0 {
							continue
						}
					}
					fmt.Printf("%s\t%s\t%d\n", tag, v.Value, len(matched))
					if *files {
						for _, f := range matched {
							fmt.Printf("\t%s\n", f)
						}
					}
				}
			} else {
				values, err := d.TagValues(tag, "")
				if err != nil {
					fatal(err)
				}
				for _, v := range values {
					fmt.Printf("%s\t%s\t%d\n", tag, v.Value, v.Count)
				}
			}
		}
	})
}

func cmdTagFilesContext(tags []string, filterFiles, excludeFiles []string) {
	if client := serverClient(arkDir); client != nil {
		var entries []ark.TagContextEntry
		if err := proxyDecode(client, "POST", "/tags/files", map[string]any{
			"tags": tags, "context": true,
		}, &entries); err != nil {
			fatal(err)
		}
		for _, e := range entries {
			if matchPath(e.Path, filterFiles, excludeFiles) {
				fmt.Printf("%s\t%s\n", e.Path, e.Line)
			}
		}
		return
	}

	withDB(func(d *ark.DB) {
		entries, err := d.TagContext(tags)
		if err != nil {
			fatal(err)
		}
		for _, e := range entries {
			if matchPath(e.Path, filterFiles, excludeFiles) {
				fmt.Printf("%s\t%s\n", e.Path, e.Line)
			}
		}
	})
}

// CRC: crc-CLI.md
func cmdTagDefs(args []string) {
	fs := flag.NewFlagSet("tag defs", flag.ExitOnError)
	showPath := fs.Bool("path", false, "show source file path, not deduplicated")
	fs.Parse(args)
	tags := fs.Args()

	printDefs := func(defs []ark.TagDefInfo) {
		if *showPath {
			sort.Slice(defs, func(i, j int) bool {
				if defs[i].Path != defs[j].Path {
					return defs[i].Path < defs[j].Path
				}
				if defs[i].Tag != defs[j].Tag {
					return defs[i].Tag < defs[j].Tag
				}
				return defs[i].Description < defs[j].Description
			})
			for _, d := range defs {
				path := strings.ReplaceAll(d.Path, " ", "\\ ")
				fmt.Printf("%s %s %s\n", path, d.Tag, d.Description)
			}
		} else {
			sort.Slice(defs, func(i, j int) bool {
				if defs[i].Tag != defs[j].Tag {
					return defs[i].Tag < defs[j].Tag
				}
				return defs[i].Description < defs[j].Description
			})
			seen := make(map[string]bool)
			for _, d := range defs {
				key := d.Tag + "\t" + d.Description
				if seen[key] {
					continue
				}
				seen[key] = true
				fmt.Printf("%s %s\n", d.Tag, d.Description)
			}
		}
	}

	if client := serverClient(arkDir); client != nil {
		var defs []ark.TagDefInfo
		if err := proxyDecode(client, "POST", "/tags/defs", map[string]any{"tags": tags}, &defs); err != nil {
			fatal(err)
		}
		printDefs(defs)
		return
	}

	withDB(func(d *ark.DB) {
		defs, err := d.TagDefs(tags)
		if err != nil {
			fatal(err)
		}
		printDefs(defs)
	})
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
	requestedTags := args[1:]

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

// CRC: crc-CLI.md
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

// CRC: crc-CLI.md
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

// CRC: crc-CLI.md
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

// CRC: crc-CLI.md
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

// CRC: crc-CLI.md
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

// CRC: crc-CLI.md | Seq: seq-message.md
// R450, R451, R452, R453, R454, R455, R456, R457, R458, R462, R464, R466, R467, R468, R469, R470, R471, R477
func cmdMessage(args []string) {
	messageUsage := `Usage: ark message <subcommand>

Subcommands:
  new-request   Create a new request file
  new-response  Create a new response file (response = ack)
  set-tags      Update or add tags in a file's tag block
  get-tags      Read tags from a file's tag block
  check         Validate file format
  inbox         List non-completed messages
  dm            Send a direct message between agents (tmp://)`

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, messageUsage)
		os.Exit(0)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "new-request":
		cmdMessageNewRequest(subArgs)
	case "new-response":
		cmdMessageNewResponse(subArgs)
	case "set-tags":
		cmdMessageSetTags(subArgs)
	case "get-tags":
		cmdMessageGetTags(subArgs)
	case "check":
		cmdMessageCheck(subArgs)
	case "inbox":
		cmdMessageInbox(subArgs)
	case "dm":
		cmdMessageDM(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown message subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, messageUsage)
		os.Exit(1)
	}
}

// CRC: crc-CLI.md | R580, R581, R582, R583, R584
func readStdinBody() string {
	info, err := os.Stdin.Stat()
	if err != nil {
		return ""
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "" // terminal, not piped
	}
	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "." {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func cmdMessageNewRequest(args []string) {
	fs := flag.NewFlagSet("message new-request", flag.ExitOnError)
	from := fs.String("from", "", "source project name (required)")
	to := fs.String("to", "", "target project name (required)")
	issue := fs.String("issue", "", "one-line issue description (required)")
	content := fs.String("content", "", "body text (alternative to stdin)")
	fs.Parse(args)

	if *from == "" || *to == "" || *issue == "" {
		fmt.Fprintln(os.Stderr, "error: --from, --to, and --issue are required")
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: FILE path required")
		os.Exit(1)
	}
	filePath := fs.Arg(0)

	if _, err := os.Stat(filePath); err == nil {
		fmt.Fprintf(os.Stderr, "error: file already exists: %s\n", filePath)
		os.Exit(1)
	}

	// Derive request ID from filename
	base := filepath.Base(filePath)
	id := strings.TrimSuffix(base, filepath.Ext(base))

	tb := ark.ParseTagBlock(nil)
	tb.Set("ark-request", id)
	tb.Set("from-project", *from)
	tb.Set("to-project", *to)
	tb.Set("status", "open")
	tb.Set("status-date", time.Now().Format("2006-01-02"))
	tb.Set("issue", *issue)

	var buf bytes.Buffer
	buf.Write(tb.Render())
	fmt.Fprintf(&buf, "# %s\n\n%s\n", id, *issue)
	// R849-R851: --content flag preferred over stdin
	if *content != "" {
		fmt.Fprintf(&buf, "\n%s\n", *content)
	} else if body := readStdinBody(); body != "" {
		fmt.Fprintf(&buf, "\n%s", body)
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "hint: when the response arrives, track your progress with @response-handled:\n")
	fmt.Fprintf(os.Stderr, "  ~/.ark/ark tag set %s response-handled accepted\n", filePath)
}

func cmdMessageNewResponse(args []string) {
	fs := flag.NewFlagSet("message new-response", flag.ExitOnError)
	from := fs.String("from", "", "source project name (required)")
	to := fs.String("to", "", "target project name (required)")
	request := fs.String("request", "", "request ID being responded to (required)")
	content := fs.String("content", "", "body text (alternative to stdin)")
	fs.Parse(args)

	if *from == "" || *to == "" || *request == "" {
		fmt.Fprintln(os.Stderr, "error: --from, --to, and --request are required")
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: FILE path required")
		os.Exit(1)
	}
	filePath := fs.Arg(0)

	if _, err := os.Stat(filePath); err == nil {
		fmt.Fprintf(os.Stderr, "error: file already exists: %s\n", filePath)
		os.Exit(1)
	}

	tb := ark.ParseTagBlock(nil)
	tb.Set("ark-response", *request)
	tb.Set("from-project", *from)
	tb.Set("to-project", *to)
	tb.Set("status", "accepted")
	tb.Set("status-date", time.Now().Format("2006-01-02"))

	var buf bytes.Buffer
	buf.Write(tb.Render())
	fmt.Fprintf(&buf, "# RESP %s\n\n", *request)
	// R852: --content flag preferred over stdin
	if *content != "" {
		fmt.Fprintf(&buf, "%s\n", *content)
	} else if body := readStdinBody(); body != "" {
		buf.WriteString(body)
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
		fatal(err)
	}
}

// R614, R616: alias — delegates to ark tag set
func cmdMessageSetTags(args []string) { cmdTagSet(args) }

// R614, R616: alias — delegates to ark tag get
func cmdMessageGetTags(args []string) { cmdTagGet(args) }

// R613: wrapper — calls ark tag check with standard message headings
func cmdMessageCheck(args []string) {
	// Prepend the file arg, then the allowed message headings
	if len(args) < 1 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, "usage: ark message check FILE")
		os.Exit(0)
	}
	// Pass through to cmdTagCheck with message-specific headings appended
	cmdTagCheck(args)
}

// CRC: crc-CLI.md | Seq: seq-message.md | R708, R709, R710, R713, R718
func cmdMessageInbox(args []string) {
	fs := flag.NewFlagSet("message inbox", flag.ExitOnError)
	project := fs.String("project", "", "filter by to-project")
	from := fs.String("from", "", "filter by from-project")
	all := fs.Bool("all", false, "include completed/denied messages")
	includeArchived := fs.Bool("include-archived", false, "include archived messages")
	counts := fs.Bool("counts", false, "output status counts instead of rows")
	unmatched := fs.Bool("unmatched", false, "show only requests with no matching response")
	fs.Parse(args)

	printEntries := func(entries []ark.InboxEntry) {
		// CLI-specific post-filters (project, from)
		var filtered []ark.InboxEntry
		for _, e := range entries {
			if *project != "" && e.To != *project {
				continue
			}
			if *from != "" && e.From != *from {
				continue
			}
			filtered = append(filtered, e)
		}

		// R714, R723: pair entries by requestId for unmatched and lag
		type pair struct {
			request  *ark.InboxEntry
			response *ark.InboxEntry
		}
		byID := make(map[string]*pair)
		for i := range filtered {
			e := &filtered[i]
			id := e.RequestID
			if id == "" {
				id = e.Path
			}
			p, ok := byID[id]
			if !ok {
				p = &pair{}
				byID[id] = p
			}
			if e.Kind == "response" {
				p.response = e
			} else {
				p.request = e
			}
		}

		// R713, R716: --unmatched keeps only requests with no response
		if *unmatched {
			var um []ark.InboxEntry
			for _, e := range filtered {
				if e.Kind == "response" {
					continue
				}
				id := e.RequestID
				if id == "" {
					id = e.Path
				}
				if p := byID[id]; p != nil && p.response == nil {
					um = append(um, e)
				}
			}
			filtered = um
		}

		if *counts {
			statusCounts := make(map[string]int)
			for _, e := range filtered {
				statusCounts[e.Status]++
			}
			var keys []string
			for k := range statusCounts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("%s\t%d\n", k, statusCounts[k])
			}
			return
		}

		// R718, R719, R720, R721, R722: compute bookmark lag per entry
		lagFor := func(e ark.InboxEntry) string {
			id := e.RequestID
			if id == "" {
				id = e.Path
			}
			p := byID[id]
			if p == nil {
				return ""
			}
			if e.Kind != "response" && p.response != nil {
				// Request side: is reqResponseHandled behind respStatus?
				if p.response.Status != "" && e.ResponseHandled != p.response.Status {
					return "lag:" + e.From + ":" + p.response.Status
				}
			}
			if e.Kind == "response" && p.request != nil {
				// Response side: is respRequestHandled behind reqStatus?
				if p.request.Status != "" && e.RequestHandled != p.request.Status {
					return "lag:" + e.From + ":" + p.request.Status
				}
			}
			return ""
		}

		// Pre-compute display date: most recent status-date from paired request/response
		type dated struct {
			entry ark.InboxEntry
			date  string
		}
		datedEntries := make([]dated, len(filtered))
		for i, e := range filtered {
			id := e.RequestID
			if id == "" {
				id = e.Path
			}
			best := e.StatusDate
			if p := byID[id]; p != nil {
				if p.request != nil && p.request.StatusDate > best {
					best = p.request.StatusDate
				}
				if p.response != nil && p.response.StatusDate > best {
					best = p.response.StatusDate
				}
			}
			datedEntries[i] = dated{entry: e, date: best}
		}

		// Sort by display date descending (most recent first, empty last)
		sort.SliceStable(datedEntries, func(i, j int) bool {
			di, dj := datedEntries[i].date, datedEntries[j].date
			if di == "" && dj != "" {
				return false
			}
			if di != "" && dj == "" {
				return true
			}
			return di > dj
		})

		fmt.Printf("# inbox %s\n", time.Now().Format("2006-01-02"))
		for _, d := range datedEntries {
			lag := lagFor(d.entry)
			date := d.date
			if date == "" {
				date = "-"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				date, d.entry.Status, d.entry.To, d.entry.From, d.entry.Summary, d.entry.Path, lag)
		}
	}

	withDB(func(d *ark.DB) {
		entries, err := d.Inbox(*all, *includeArchived)
		if err != nil {
			fatal(err)
		}
		printEntries(entries)
	})
}

// CRC: crc-CLI.md | Seq: seq-pubsub.md
func cmdMessageDM(args []string) {
	fs := flag.NewFlagSet("message dm", flag.ExitOnError)
	from := fs.String("from", "", "sender session ID (required)")
	to := fs.String("to", "", "recipient session ID (required)")
	ref := fs.String("ref", "", "reference ID (for threading replies)")
	content := fs.String("content", "", "message content (markdown)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark message dm --from SESSION --to SESSION [--ref ID] --content CONTENT")
		fmt.Fprintln(os.Stderr, "\nSend a direct message between agents via tmp:// files.")
		fmt.Fprintln(os.Stderr, "Content can include newlines (bash allows them in quoted args).")
		fmt.Fprintln(os.Stderr, "\nExample:")
		fmt.Fprintln(os.Stderr, `  ark message dm --from abc123 --to def456 --content "Found 3 @decision: tags"`)
		fmt.Fprintln(os.Stderr, `  ark message dm --from def456 --to abc123 --ref msg-1 --content "Yes, pull them"`)
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *from == "" || *to == "" || *content == "" {
		fatal(fmt.Errorf("--from, --to, and --content are all required"))
	}

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (dm requires server)"))
	}

	// Build the tagged chunk: blank line (chunk boundary) + tags + content
	var buf strings.Builder
	buf.WriteString("\n@dm: ")
	buf.WriteString(*to)
	buf.WriteString("\n@from: ")
	buf.WriteString(*from)
	if *ref != "" {
		buf.WriteString("\n@ref: ")
		buf.WriteString(*ref)
	}
	buf.WriteString("\n")
	buf.WriteString(*content)
	buf.WriteString("\n")

	tmpPath := fmt.Sprintf("tmp://%s/dm-%s", *from, *to)
	if err := proxyOK(client, "POST", "/tmp/append", map[string]any{
		"path":     tmpPath,
		"strategy": "markdown",
		"content":  buf.String(),
	}); err != nil {
		fatal(err)
	}
}

// CRC: crc-CLI.md | Seq: seq-pubsub.md
// CRC: crc-CLI.md | R937
func cmdSubscribe(args []string) {
	fs := flag.NewFlagSet("subscribe", flag.ExitOnError)
	session := fs.String("session", "", "session ID (required)")
	cancel := fs.Bool("cancel", false, "cancel subscriptions")
	list := fs.Bool("list", false, "list active subscriptions")
	stats := fs.Bool("stats", false, "show hit/drop statistics")
	tag := fs.String("tag", "", "tag name")
	value := fs.String("value", "", "value regex filter")
	var filterFiles, excludeFiles stringSlice
	fs.Var(&filterFiles, "filter-files", "only match files matching glob (repeatable)")
	fs.Var(&excludeFiles, "exclude-files", "exclude files matching glob (repeatable)") // R944: renamed from --except-files
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark subscribe [options]")
		fmt.Fprintln(os.Stderr, "\nSubscribe to tag notifications, manage subscriptions.")
		fmt.Fprintln(os.Stderr, "\n--value takes an RE2 regex matched against the tag value.")
		fmt.Fprintln(os.Stderr, "Omit --value to match all values for that tag.")
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, "  ark subscribe --session $ID --tag status")
		fmt.Fprintln(os.Stderr, "  ark subscribe --session $ID --tag status --value 'open|accepted'")
		fmt.Fprintln(os.Stderr, "  ark subscribe --session $ID --tag to-project --value 'ark'")
		fmt.Fprintln(os.Stderr, "  ark subscribe --session $ID --tag standup")
		fmt.Fprintln(os.Stderr, "  ark subscribe --session $ID --cancel")
		fmt.Fprintln(os.Stderr, "  ark subscribe --list")
		fmt.Fprintln(os.Stderr, "  ark subscribe --stats")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	// R937: normalize tag name — strip leading @ and trailing :
	if *tag != "" {
		t := *tag
		t = strings.TrimPrefix(t, "@")
		t = strings.TrimSuffix(t, ":")
		*tag = t
	}

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (subscribe requires server)"))
	}

	if *list {
		var infos []ark.SubInfo
		if err := proxyDecode(client, "POST", "/subscribe", map[string]any{
			"session": *session,
			"list":    true,
		}, &infos); err != nil {
			fatal(err)
		}
		for _, info := range infos {
			valStr := ""
			if info.ValueRE != "" {
				valStr = info.ValueRE
			}
			fmt.Printf("%s\t%s\t%s\t%d\t%d\n", info.SessionID, info.Tag, valStr, info.Hits, info.Drops)
		}
		return
	}

	if *stats {
		var st []ark.SubStats
		if err := proxyDecode(client, "POST", "/subscribe", map[string]any{
			"session": *session,
			"stats":   true,
		}, &st); err != nil {
			fatal(err)
		}
		for _, s := range st {
			fmt.Printf("%s\t%d subs\t%d hits\t%d drops\n", s.SessionID, s.SubCount, s.Hits, s.Drops)
		}
		return
	}

	if *session == "" {
		fatal(fmt.Errorf("--session is required"))
	}

	if *cancel {
		if err := proxyOK(client, "POST", "/subscribe", map[string]any{
			"session": *session,
			"cancel":  true,
			"tag":     *tag,
			"value":   *value,
		}); err != nil {
			fatal(err)
		}
		return
	}

	if *tag == "" {
		fatal(fmt.Errorf("--tag is required for subscribe"))
	}

	sub := map[string]any{
		"tag":   *tag,
		"value": *value,
	}
	if len(filterFiles) > 0 {
		sub["filter_files"] = ark.ExpandTildeSlice([]string(filterFiles))
	}
	if len(excludeFiles) > 0 {
		sub["exclude_files"] = ark.ExpandTildeSlice([]string(excludeFiles))
	}

	if err := proxyOK(client, "POST", "/subscribe", map[string]any{
		"session": *session,
		"subs":    []any{sub},
	}); err != nil {
		fatal(err)
	}
}

// CRC: crc-CLI.md | Seq: seq-pubsub.md
func cmdListen(args []string) {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	session := fs.String("session", "", "session ID (required)")
	timeout := fs.Int("timeout", 120, "long-poll timeout in seconds")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark listen --session ID [--timeout N]")
		fmt.Fprintln(os.Stderr, "\nLong-poll for tag notifications. Outputs markdown crank handles.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *session == "" {
		fatal(fmt.Errorf("--session is required"))
	}

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (listen requires server)"))
	}

	path := fmt.Sprintf("/listen?session=%s&timeout=%d", url.QueryEscape(*session), *timeout)
	data, err := proxyRaw(client, "GET", path, nil)
	if err != nil {
		// 204 No Content = timeout with no events, not an error
		errMsg := err.Error()
		if strings.HasPrefix(errMsg, "server error (204)") {
			return
		}
		fatal(err)
	}
	fmt.Print(string(data))
}

// CRC: crc-CLI.md | Seq: seq-scheduling.md | R926
func cmdSchedule(args []string) {
	scheduleUsage := `Usage: ark schedule <subcommand> [options]

Subcommands:
  search    Query scheduled events
  change    Modify a scheduled event's date
  tags      Show configured schedule tags
  parse     Parse a date expression and show the result`

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		os.Exit(0)
	}
	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "search":
		cmdScheduleSearch(subArgs)
	case "change":
		cmdScheduleChange(subArgs)
	case "tags":
		cmdScheduleTags(subArgs)
	case "parse":
		cmdScheduleParse(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown schedule subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, scheduleUsage)
		os.Exit(1)
	}
}

// CRC: crc-CLI.md | Seq: seq-scheduling.md | R914-R920
func cmdScheduleSearch(args []string) {
	fs := flag.NewFlagSet("schedule search", flag.ExitOnError)
	tag := fs.String("tag", "", "filter to a specific schedule tag")
	gaps := fs.Bool("gaps", false, "show only past events with no acknowledgment")
	jsonOut := fs.Bool("json", false, "output JSON instead of markdown")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark schedule search DATE [options]")
		fmt.Fprintln(os.Stderr, "\nQuery scheduled events. DATE uses the same format as schedule tags:")
		fmt.Fprintln(os.Stderr, "  Single date:  2026-04-15 or \"April 15 2026\"")
		fmt.Fprintln(os.Stderr, "  Date range:   2026-04-01..2026-06-30")
		fmt.Fprintln(os.Stderr, "  With text:    2026-04-01..2026-06-30 (trailing text ignored)")
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, "  ark schedule search 2026-04-15")
		fmt.Fprintln(os.Stderr, "  ark schedule search 2026-03-01..2026-06-30")
		fmt.Fprintln(os.Stderr, "  ark schedule search 2026-03-01..2026-06-30 --tag standup")
		fmt.Fprintln(os.Stderr, "  ark schedule search 2026-03-01..2026-06-30 --gaps")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	// Reorder so flags can come after positional args
	args = reorderArgs(args)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}
	// Join all positional args — allows "April 15 2026" as separate words
	dateArg := strings.Join(fs.Args(), " ")

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule requires server)"))
	}

	reqBody := map[string]any{
		"date": dateArg,
	}
	if *tag != "" {
		reqBody["tag"] = *tag
	}
	if *gaps {
		reqBody["gaps"] = true
	}

	if *jsonOut {
		data, err := proxyRaw(client, "POST", "/schedule/search", reqBody)
		if err != nil {
			fatal(err)
		}
		fmt.Println(string(data))
		return
	}

	// Decode and format as markdown R916
	var events []ark.ScheduleEvent
	if err := proxyDecode(client, "POST", "/schedule/search", reqBody, &events); err != nil {
		fatal(err)
	}
	lastDate := ""
	for _, ev := range events {
		if ev.Date != lastDate {
			if lastDate != "" {
				fmt.Println()
			}
			fmt.Printf("## %s — @%s: (%s)\n\n", ev.Date, ev.Tag, ev.Source)
			lastDate = ev.Date
		}
		if ev.AllDay {
			fmt.Printf("- all day: %s\n", ev.Summary)
		} else {
			fmt.Printf("- %s–%s: %s\n",
				ev.Start.Format("15:04"),
				ev.End.Format("15:04"),
				ev.Summary)
		}
		fmt.Println()
	}
}

// CRC: crc-CLI.md | Seq: seq-scheduling.md | R921-R925
func cmdScheduleChange(args []string) {
	fs := flag.NewFlagSet("schedule change", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ark schedule change PATH TAG NEWSTART [NEWEND] [options]")
		fmt.Fprintln(os.Stderr, "\nRewrite the date in a schedule tag value.")
		fmt.Fprintln(os.Stderr, "Trailing description text is preserved.")
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, "  ark schedule change ~/notes/appts.md dentist '2026-05-01 09:00'")
		fmt.Fprintln(os.Stderr, "  ark schedule change ~/notes/appts.md dentist '2026-05-01 09:00' '10:30'")
		fmt.Fprintln(os.Stderr, "  ark schedule change ~/notes/appts.md dentist '2026-05-01 09:00' --dry-run")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	args = reorderArgs(args)
	fs.Parse(args)

	if fs.NArg() < 3 {
		fs.Usage()
		os.Exit(1)
	}
	path := fs.Arg(0)
	tagName := fs.Arg(1)
	newStart := fs.Arg(2)
	newEnd := ""
	if fs.NArg() > 3 {
		newEnd = fs.Arg(3)
	}

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule requires server)"))
	}

	reqBody := map[string]any{
		"path":      path,
		"tag":       tagName,
		"new_start": newStart,
	}
	if newEnd != "" {
		reqBody["new_end"] = newEnd
	}
	if *dryRun {
		reqBody["dry_run"] = true
		var result map[string]string
		if err := proxyDecode(client, "POST", "/schedule/change", reqBody, &result); err != nil {
			fatal(err)
		}
		fmt.Printf("old: %s\nnew: %s\n", result["old"], result["new"])
		return
	}

	if err := proxyOK(client, "POST", "/schedule/change", reqBody); err != nil {
		fatal(err)
	}
}

func cmdScheduleTags(args []string) {
	fs := flag.NewFlagSet("schedule tags", flag.ExitOnError)
	values := fs.Bool("values", false, "show tag values and next upcoming dates")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: ark schedule tags [--values]

Show configured schedule tags and their default durations.
With --values: also show tag values and next upcoming dates from schedule logs.`)
	}
	fs.Parse(args)
	withDB(func(db *ark.DB) {
		tags := db.Config().ScheduleTags()
		if len(tags) == 0 {
			fmt.Println("no schedule tags configured")
			fmt.Println("add tags to [schedule] in ark.toml")
			return
		}
		cfg := db.Config()
		for _, t := range cfg.Schedule.Tags {
			def := tags[t]
			lifecycle := cfg.IsLifecycleTag(t)
			line := "@" + t + ":"
			if def != "" {
				line += " (default " + def + ")"
			}
			if !lifecycle {
				line += " [no-lifecycle]"
			}
			if tc, ok := cfg.Schedule.TagConfig[t]; ok {
				if len(tc.FilterFiles) > 0 {
					line += " filter=" + strings.Join(tc.FilterFiles, ",")
				}
				if len(tc.ExcludeFiles) > 0 {
					line += " exclude=" + strings.Join(tc.ExcludeFiles, ",")
				}
			}
			fmt.Println(line)
		}
		if len(cfg.Schedule.ExcludeFiles) > 0 {
			fmt.Printf("\nexclude: %s\n", strings.Join(cfg.Schedule.ExcludeFiles, ", "))
		}
		if len(cfg.Schedule.FilterFiles) > 0 {
			fmt.Printf("filter: %s\n", strings.Join(cfg.Schedule.FilterFiles, ", "))
		}
		// R1033, R1034: show values and upcoming from schedule logs
		if *values {
			schedDir := filepath.Join(arkDir, "schedule")
			entries, err := os.ReadDir(schedDir)
			if err != nil {
				return
			}
			fmt.Println()
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				chunks, err := ark.ReadLogFile(filepath.Join(schedDir, entry.Name()))
				if err != nil {
					continue
				}
				for _, c := range chunks {
					upcoming := "(none)"
					if len(c.Upcoming) > 0 {
						upcoming = c.Upcoming[0]
					}
					fmt.Printf("@%s: %s\n  source: %s\n  next: %s\n",
						c.Event, c.Spec, c.Source, upcoming)
				}
			}
		}
	})
}

func cmdScheduleParse(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, `Usage: ark schedule parse DATE

Parse a date expression and show the result. Uses the same format as
schedule tag values: single dates, ranges with .., keyword prefixes.

Examples:
  ark schedule parse 2026-04-15
  ark schedule parse "April 15 2026"
  ark schedule parse 2026-04-01..2026-06-30
  ark schedule parse "on April 15 2026 dentist"
  ark schedule parse "every Monday at 9am starting March 1 to June 30"`)
		os.Exit(0)
	}
	input := strings.Join(args, " ")
	loc := time.Now().Location()

	// Check for recurring with bounds
	if ark.IsRecurringSpec(input) {
		nb, na, spec := ark.ExtractBounds(input, loc)
		fmt.Printf("recurring: %s\n", spec)
		if !nb.IsZero() {
			fmt.Printf("start:     %s\n", nb.Format("2006-01-02"))
		}
		if !na.IsZero() {
			fmt.Printf("end:       %s\n", na.Format("2006-01-02"))
		}
		next := ark.ComputeNext(spec, time.Now(), na)
		if !next.IsZero() {
			fmt.Printf("next:      %s\n", next.Format("2006-01-02 15:04"))
		} else {
			fmt.Println("next:      (none — past end bound)")
		}
		return
	}

	dr, err := ark.ParseDateValue(input, "", loc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("start:     %s\n", dr.Start.Format("2006-01-02 15:04"))
	if dr.End != dr.Start {
		fmt.Printf("end:       %s\n", dr.End.Format("2006-01-02 15:04"))
	}
	if dr.AllDay {
		fmt.Println("all-day:   true")
	}
	if dr.Description != "" {
		fmt.Printf("text:      %s\n", dr.Description)
	}
}
