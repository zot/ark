package main

// CRC: crc-CLI.md | Seq: seq-cli-dispatch.md

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ark"
	"microfts2"

	cli "github.com/zot/ui-engine/cli"
)

// arkDir is the ark directory, set from --dir flag (global, parsed before subcommand).
var arkDir string

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

	// Parse --dir globally before subcommand dispatch
	arkDir = defaultDB()
	var filtered []string
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--dir" && i+1 < len(os.Args) {
			arkDir = os.Args[i+1]
			i++ // skip value
		} else if strings.HasPrefix(arg, "--dir=") {
			arkDir = strings.TrimPrefix(arg, "--dir=")
		} else {
			filtered = append(filtered, arg)
		}
	}
	if len(filtered) == 0 {
		usage()
		os.Exit(1)
	}

	cmd := filtered[0]
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
	case "config":
		cmdConfig(args)
	case "cp":
		cmdBundleCp(args)
	case "dismiss":
		cmdDismiss(args)
	case "fetch":
		cmdFetch(args)
	case "files":
		cmdFiles(args)
	case "grams":
		cmdGrams(args)
	case "init":
		cmdInit(args)
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
	case "ui":
		cmdUI(args)
	case "unresolved":
		cmdUnresolved(args)
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
  config      Show or modify configuration
              add-source, remove-source, add-include, add-exclude,
              remove-pattern, show-why, add-strategy
  cp          Extract embedded files matching a glob pattern
  dismiss     Dismiss missing files
  fetch       Return full contents of an indexed file
  files       List indexed files
  grams       Show trigrams for a query (active/inactive, frequency)
  init        Create a new database
  ls          List embedded assets
  setup       Bootstrap ~/.ark/ (extract assets, install skills)
  missing     List missing files
  refresh     Re-index stale files
  remove      Remove files from the index
  resolve     Dismiss unresolved files by pattern
  scan        Walk directories, index new files
  search      Search the index
  serve       Start the server
  sources     Manage source directories
  stale       List stale files
  status      Show database status
  stop        Stop the running server
  tag         Tag operations (list, counts, files)
  ui          UI operations (run, display, event, checkpoint, ...)
  unresolved  List unresolved files`)
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

	// Auto-setup if not bootstrapped (unless --no-setup)
	if !*noSetup && !isBootstrapped() {
		if err := runSetup(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup failed: %v\n", err)
		}
	}

	aliases := parseAliases(*aliasStr)

	opts := ark.InitOpts{
		EmbedCmd:        *embedCmd,
		QueryCmd:        *queryCmd,
		CaseInsensitive: *caseInsensitive,
		Aliases:         aliases,
	}
	if err := ark.Init(arkDir, opts); err != nil {
		fatal(err)
	}
	fmt.Printf("initialized ark database at %s\n", arkDir)
}

func cmdAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	strategy := fs.String("strategy", "", "chunking strategy")
	fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or directories specified")
		os.Exit(1)
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
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or patterns specified")
		os.Exit(1)
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
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	k := fs.Int("k", 20, "max results")
	scores := fs.Bool("scores", false, "show scores")
	after := fs.String("after", "", "only results after date")
	about := fs.String("about", "", "semantic search query")
	contains := fs.String("contains", "", "exact match query")
	regex := fs.String("regex", "", "regex query")
	likeFile := fs.String("like-file", "", "find similar files using FTS density scoring")
	tags := fs.Bool("tags", false, "output extracted tags instead of content")
	chunks := fs.Bool("chunks", false, "emit chunk text as JSONL")
	files := fs.Bool("files", false, "emit full file content as JSONL")
	preview := fs.Int("preview", 0, "with --chunks: extract N-char preview window around match")
	wrap := fs.String("wrap", "", "wrap output in XML tags (e.g. memory, knowledge)")
	var filter, except, filterFiles, excludeFiles stringSlice
	fs.Var(&filter, "filter", "content-based positive filter (repeatable, FTS query)")
	fs.Var(&except, "except", "content-based negative filter (repeatable, FTS query)")
	fs.Var(&filterFiles, "filter-files", "path-based positive filter (repeatable, glob pattern)")
	fs.Var(&excludeFiles, "exclude-files", "path-based negative filter (repeatable, glob pattern)")
	fs.Parse(args)

	if *chunks && *files {
		fmt.Fprintln(os.Stderr, "error: --chunks and --files are mutually exclusive")
		os.Exit(1)
	}

	var afterNano int64
	if *after != "" {
		t, err := parseDate(*after)
		if err != nil {
			fatal(fmt.Errorf("parse --after: %w", err))
		}
		afterNano = t.UnixNano()
	}

	isSplit := *about != "" || *contains != "" || *regex != "" || *likeFile != ""

	if client := serverClient(arkDir); client != nil {
		body := map[string]any{
			"k":      *k,
			"scores": *scores,
			"after":  afterNano,
			"chunks": *chunks,
			"files":  *files,
			"tags":   *tags,
		}
		if len(filter) > 0 {
			body["filter"] = []string(filter)
		}
		if len(except) > 0 {
			body["except"] = []string(except)
		}
		if len(filterFiles) > 0 {
			body["filterFiles"] = []string(filterFiles)
		}
		if len(excludeFiles) > 0 {
			body["excludeFiles"] = []string(excludeFiles)
		}
		if isSplit {
			body["about"] = *about
			body["contains"] = *contains
			body["regex"] = *regex
			body["likeFile"] = *likeFile
		} else {
			query := strings.Join(fs.Args(), " ")
			if query == "" {
				fmt.Fprintln(os.Stderr, "error: no search query")
				os.Exit(1)
			}
			body["query"] = query
		}
		if *tags {
			var tagResults []ark.TagResult
			if err := proxyDecode(client, "POST", "/search", body, &tagResults); err != nil {
				fatal(err)
			}
			printTagResultsDirect(tagResults, *scores)
		} else {
			var results []ark.SearchResultEntry
			if err := proxyDecode(client, "POST", "/search", body, &results); err != nil {
				fatal(err)
			}
			// Extract query for preview
			var pq string
			if v, ok := body["contains"].(string); ok && v != "" {
				pq = v
			} else if v, ok := body["about"].(string); ok && v != "" {
				pq = v
			} else if v, ok := body["query"].(string); ok {
				pq = v
			}
			printSearchResults(results, *scores, *chunks, *files, *wrap, *preview, pq)
		}
		return
	}

	withDB(func(d *ark.DB) {
		opts := ark.SearchOpts{
			K:            *k,
			Scores:       *scores,
			After:        afterNano,
			About:        *about,
			Contains:     *contains,
			Regex:        *regex,
			LikeFile:     *likeFile,
			Tags:         *tags,
			Filter:       []string(filter),
			Except:       []string(except),
			FilterFiles:  []string(filterFiles),
			ExcludeFiles: []string(excludeFiles),
		}

		var results []ark.SearchResultEntry
		var err error
		if isSplit {
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
		} else if *regex != "" {
			query = *regex
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
			fmt.Printf("%s:%s\t%.4f\n", r.Path, r.Range, r.Score)
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
	fmt.Printf("files: %d\nstale: %d\nmissing: %d\nunresolved: %d\n",
		status.Files, status.Stale, status.Missing, status.Unresolved)
	fmt.Printf("chunks: %d\nsources: %d\n", status.Chunks, status.Sources)
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
	fs.Parse(args)

	if client := serverClient(arkDir); client != nil {
		var status ark.StatusInfo
		if err := proxyDecode(client, "GET", "/status", nil, &status); err != nil {
			fatal(err)
		}
		printStatus(&status, true)
		if status.Version != ark.Version {
			fmt.Fprintf(os.Stderr, "WARNING: server is v%s but CLI is v%s — restart server to match\n",
				status.Version, ark.Version)
		}
		return
	}

	withDB(func(d *ark.DB) {
		status, err := d.Status()
		if err != nil {
			fatal(err)
		}
		printStatus(status, false)
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
	strategy := fs.String("strategy", "", "chunking strategy (optional for globs using global strategies)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: directory path required")
		os.Exit(1)
	}
	dir := fs.Arg(0)

	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-source", map[string]string{
			"dir": dir, "strategy": *strategy,
		}); err != nil {
			fatal(err)
		}
		return
	}

	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddSource(dir, *strategy) })
}

func cmdConfigRemoveSource(args []string) {
	fs := flag.NewFlagSet("config remove-source", flag.ExitOnError)
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
	fs.Parse(args)

	sub := "check"
	if fs.NArg() > 0 {
		sub = fs.Arg(0)
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

	client := serverClient(arkDir)
	var db *ark.DB
	if client == nil {
		var err error
		db, err = ark.Open(arkDir)
		if err != nil {
			fatal(err)
		}
		defer db.Close()
	}

	for _, filePath := range paths {
		var content string
		if client != nil {
			var result struct {
				Content string `json:"content"`
			}
			if err := proxyDecode(client, "POST", "/fetch", map[string]string{
				"path": filePath,
			}, &result); err != nil {
				fatal(err)
			}
			content = result.Content
		} else {
			data, err := db.Fetch(filePath)
			if err != nil {
				fatal(err)
			}
			content = string(data)
		}

		if *wrap != "" {
			absPath, _ := filepath.Abs(filePath)
			fmt.Printf("<%s source=%q>\n", *wrap, absPath)
			writeEscaped(os.Stdout, content, *wrap)
			fmt.Printf("</%s>\n", *wrap)
		} else {
			os.Stdout.WriteString(content)
		}
	}
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
		cmdUIBrowser(nil)
		return
	}
	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "audit":
		cmdUIAudit(subArgs)
	case "browser":
		cmdUIBrowser(subArgs)
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
		fmt.Fprintf(os.Stderr, `unknown ui subcommand: %s

Usage: ark ui [subcommand]

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
  variables                  get current variable values
`, sub)
		os.Exit(1)
	}
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
	// Bootstrap: ensure setup and database exist
	cmdInit([]string{"--if-needed"})

	// Create project .claude/skills/ if needed
	cwd, err := os.Getwd()
	if err != nil {
		fatal(fmt.Errorf("get working directory: %w", err))
	}
	skillsDir := filepath.Join(cwd, ".claude", "skills")
	os.MkdirAll(skillsDir, 0755)

	// Symlink skills from ~/.ark/skills/ into project
	for _, skill := range []string{"ark", "ui"} {
		src := filepath.Join(arkDir, "skills", skill)
		if _, err := os.Stat(src); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s not found in %s\n", skill, filepath.Join(arkDir, "skills"))
			continue
		}
		dst := filepath.Join(skillsDir, skill)
		// Remove existing (file, symlink, or directory)
		os.RemoveAll(dst)
		if err := os.Symlink(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "warning: symlink %s: %v\n", skill, err)
			continue
		}
		fmt.Printf("Linked: %s → %s\n", dst, src)
	}

	// Crank-handle: tell Claude what to do next
	fmt.Println()
	fmt.Println("---")
	fmt.Println("Add the following line near the top of this project's CLAUDE.md:")
	fmt.Println()
	fmt.Printf("  Use /ark to query the ark knowledge base. Binary: %s\n", filepath.Join(arkDir, "ark"))
	fmt.Println()
	fmt.Println("Then start the server with: " + filepath.Join(arkDir, "ark") + " serve")
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

func cmdUIReload(args []string) {
	body, _ := json.Marshal(map[string]string{"base_dir": arkDir})
	result := uiRequest("POST", "/api/ui_configure", string(body))
	os.Stdout.Write(result)
	fmt.Println()
}

func cmdUIStatus(args []string) {
	result := uiRequest("GET", "/api/ui_status", "")
	os.Stdout.Write(result)
	fmt.Println()
}

func cmdUIBrowser(args []string) {
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

// parseDate parses a date string: "2006-01-02", "2006-01-02T15:04:05", or
// a duration suffix like "24h", "7d" (meaning that long ago from now).
func parseDate(s string) (time.Time, error) {
	// Try duration-ago: "24h", "7d"
	if len(s) > 1 {
		suffix := s[len(s)-1]
		numStr := s[:len(s)-1]
		switch suffix {
		case 'h', 'm', 's':
			d, err := time.ParseDuration(s)
			if err == nil {
				return time.Now().Add(-d), nil
			}
		case 'd':
			var days int
			if _, err := fmt.Sscanf(numStr, "%d", &days); err == nil {
				return time.Now().AddDate(0, 0, -days), nil
			}
		}
	}
	// Try date formats
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %s (use 2006-01-02, 2006-01-02T15:04:05, or 24h/7d)", s)
}

func cmdTag(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `Usage: ark tag <subcommand>

Subcommands:
  list              List all known tags with counts
  counts <tag>...   Show count for each specified tag
  files <tag>...    Show files containing specified tags`)
		os.Exit(1)
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
	default:
		fmt.Fprintf(os.Stderr, "unknown tag subcommand: %s\n", sub)
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
	fs.Parse(args)

	if *output == "" {
		fmt.Fprintln(os.Stderr, "Error: -o output path is required")
		fmt.Fprintln(os.Stderr, "Usage: ark bundle [-src <binary>] -o <output> <dir>")
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: file path is required")
		fmt.Fprintln(os.Stderr, "Usage: ark cat <file>")
		os.Exit(1)
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
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Error: pattern and destination directory are required")
		fmt.Fprintln(os.Stderr, "Usage: ark cp <pattern> <dest-dir>")
		os.Exit(1)
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
