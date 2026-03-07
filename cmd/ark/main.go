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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ark"
	"microfts2"
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
	case "init":
		cmdInit(args)
	case "add":
		cmdAdd(args)
	case "remove":
		cmdRemove(args)
	case "scan":
		cmdScan(args)
	case "refresh":
		cmdRefresh(args)
	case "search":
		cmdSearch(args)
	case "serve":
		cmdServe(args)
	case "status":
		cmdStatus(args)
	case "files":
		cmdFiles(args)
	case "stale":
		cmdStale(args)
	case "missing":
		cmdMissing(args)
	case "dismiss":
		cmdDismiss(args)
	case "config":
		cmdConfig(args)
	case "unresolved":
		cmdUnresolved(args)
	case "resolve":
		cmdResolve(args)
	case "tag":
		cmdTag(args)
	case "fetch":
		cmdFetch(args)
	case "stop":
		cmdStop(args)
	case "grams":
		cmdGrams(args)
	case "sources":
		cmdSources(args)
	case "chunk-jsonl":
		cmdChunkJSONL(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: ark <command> [options]

Commands:
  init        Create a new database
  add         Add files to the index
  remove      Remove files from the index
  scan        Walk directories, index new files
  refresh     Re-index stale files
  search      Search the index
  serve       Start the server
  status      Show database status
  files       List indexed files
  stale       List stale files
  missing     List missing files
  dismiss     Dismiss missing files
  config      Show or modify configuration
              add-source, remove-source, add-include, add-exclude,
              remove-pattern, show-why, add-strategy
  grams       Show trigrams for a query (active/inactive, frequency)
  unresolved  List unresolved files
  resolve     Dismiss unresolved files by pattern
  tag         Tag operations (list, counts, files)
  fetch       Return full contents of an indexed file
  stop        Stop the running server`)
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

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	embedCmd := fs.String("embed-cmd", "", "embedding command (optional, enables vector search)")
	queryCmd := fs.String("query-cmd", "", "query embedding command (optional)")
	caseInsensitive := fs.Bool("case-insensitive", true, "case-insensitive indexing")
	aliasStr := fs.String("aliases", "", "byte aliases (from=to,...)")
	fs.Parse(args)

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
	fs.Parse(args)

	tags := fs.Args()
	if len(tags) == 0 {
		fmt.Fprintln(os.Stderr, "error: no tags specified")
		os.Exit(1)
	}

	if *context {
		cmdTagFilesContext(tags)
		return
	}

	if client := serverClient(arkDir); client != nil {
		var files []ark.TagFileInfo
		if err := proxyDecode(client, "POST", "/tags/files", map[string]any{"tags": tags}, &files); err != nil {
			fatal(err)
		}
		for _, f := range files {
			fmt.Printf("%s\t%d\n", f.Path, f.Size)
		}
		return
	}

	withDB(func(d *ark.DB) {
		files, err := d.TagFiles(tags)
		if err != nil {
			fatal(err)
		}
		for _, f := range files {
			fmt.Printf("%s\t%d\n", f.Path, f.Size)
		}
	})
}

func cmdTagFilesContext(tags []string) {
	if client := serverClient(arkDir); client != nil {
		var entries []ark.TagContextEntry
		if err := proxyDecode(client, "POST", "/tags/files", map[string]any{
			"tags": tags, "context": true,
		}, &entries); err != nil {
			fatal(err)
		}
		for _, e := range entries {
			fmt.Printf("%s\t%s\n", e.Path, e.Line)
		}
		return
	}

	withDB(func(d *ark.DB) {
		entries, err := d.TagContext(tags)
		if err != nil {
			fatal(err)
		}
		for _, e := range entries {
			fmt.Printf("%s\t%s\n", e.Path, e.Line)
		}
	})
}

// cmdChunkJSONL is a chunking strategy command: splits a file on newline
// boundaries and outputs range\tcontent lines (microfts2 v0.4 protocol).
func cmdChunkJSONL(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ark chunk-jsonl <file>")
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
