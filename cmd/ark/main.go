package main

// CRC: crc-CLI.md | Seq: seq-cli-dispatch.md

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ark"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

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
  config      Show configuration
  unresolved  List unresolved files
  resolve     Dismiss unresolved files by pattern`)
}

func defaultDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ark"
	}
	return filepath.Join(home, ".ark")
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
	db := fs.String("db", defaultDB(), "database path")
	embedCmd := fs.String("embed-cmd", "", "embedding command (required)")
	queryCmd := fs.String("query-cmd", "", "query embedding command (optional)")
	charset := fs.String("charset", "", "character set")
	caseInsensitive := fs.Bool("case-insensitive", false, "case-insensitive indexing")
	aliasStr := fs.String("aliases", "", "character aliases (from=to,...)")
	fs.Parse(args)

	if *embedCmd == "" {
		fmt.Fprintln(os.Stderr, "error: -embed-cmd is required")
		os.Exit(1)
	}

	aliases := parseAliases(*aliasStr)

	opts := ark.InitOpts{
		EmbedCmd:        *embedCmd,
		QueryCmd:        *queryCmd,
		CharSet:         *charset,
		CaseInsensitive: *caseInsensitive,
		Aliases:         aliases,
	}
	if err := ark.Init(*db, opts); err != nil {
		fatal(err)
	}
	fmt.Printf("initialized ark database at %s\n", *db)
}

func cmdAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	strategy := fs.String("strategy", "", "chunking strategy")
	fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or directories specified")
		os.Exit(1)
	}

	if client := serverClient(*db); client != nil {
		if err := proxyOK(client, "POST", "/add", map[string]any{
			"paths": paths, "strategy": *strategy,
		}); err != nil {
			fatal(err)
		}
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	if err := d.Add(paths, *strategy); err != nil {
		fatal(err)
	}
}

func cmdRemove(args []string) {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or patterns specified")
		os.Exit(1)
	}

	if client := serverClient(*db); client != nil {
		if err := proxyOK(client, "POST", "/remove", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	if err := d.Remove(patterns); err != nil {
		fatal(err)
	}
}

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
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

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	results, err := d.Scan()
	if err != nil {
		fatal(err)
	}
	fmt.Printf("new files: %d, new unresolved: %d\n",
		len(results.NewFiles), len(results.NewUnresolved))
}

func cmdRefresh(args []string) {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	patterns := fs.Args()

	if client := serverClient(*db); client != nil {
		if err := proxyOK(client, "POST", "/refresh", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		fmt.Println("refresh complete")
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	if err := d.Refresh(patterns); err != nil {
		fatal(err)
	}
	fmt.Println("refresh complete")
}

func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	k := fs.Int("k", 20, "max results")
	scores := fs.Bool("scores", false, "show scores")
	after := fs.String("after", "", "only results after date")
	about := fs.String("about", "", "semantic search query")
	contains := fs.String("contains", "", "exact match query")
	regex := fs.String("regex", "", "regex query")
	fs.Parse(args)

	var afterNano int64
	if *after != "" {
		t, err := parseDate(*after)
		if err != nil {
			fatal(fmt.Errorf("parse --after: %w", err))
		}
		afterNano = t.UnixNano()
	}

	isSplit := *about != "" || *contains != "" || *regex != ""

	if client := serverClient(*db); client != nil {
		body := map[string]any{
			"k":      *k,
			"scores": *scores,
			"after":  afterNano,
		}
		if isSplit {
			body["about"] = *about
			body["contains"] = *contains
			body["regex"] = *regex
		} else {
			body["query"] = strings.Join(fs.Args(), " ")
		}
		var results []ark.SearchResultEntry
		if err := proxyDecode(client, "POST", "/search", body, &results); err != nil {
			fatal(err)
		}
		printSearchResults(results, *scores)
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	opts := ark.SearchOpts{
		K:        *k,
		Scores:   *scores,
		After:    afterNano,
		About:    *about,
		Contains: *contains,
		Regex:    *regex,
	}

	var results []ark.SearchResultEntry
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

	printSearchResults(results, *scores)
}

func printSearchResults(results []ark.SearchResultEntry, scores bool) {
	for _, r := range results {
		if scores {
			fmt.Printf("%s:%d-%d\t%.4f\n", r.Path, r.StartLine, r.EndLine, r.Score)
		} else {
			fmt.Printf("%s:%d-%d\n", r.Path, r.StartLine, r.EndLine)
		}
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	noScan := fs.Bool("no-scan", false, "skip startup reconciliation")
	fs.Parse(args)

	if err := ark.Serve(*db, ark.ServeOpts{NoScan: *noScan}); err != nil {
		fatal(err)
	}
}

func printStatus(status *ark.StatusInfo, serverRunning bool) {
	server := "not running"
	if serverRunning {
		server = "running"
	}
	fmt.Printf("files: %d\nstale: %d\nmissing: %d\nunresolved: %d\nserver: %s\n",
		status.Files, status.Stale, status.Missing, status.Unresolved, server)
}

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
		var status ark.StatusInfo
		if err := proxyDecode(client, "GET", "/status", nil, &status); err != nil {
			fatal(err)
		}
		printStatus(&status, true)
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	status, err := d.Status()
	if err != nil {
		fatal(err)
	}
	printStatus(status, false)
}

func cmdFiles(args []string) {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
		var files []string
		if err := proxyDecode(client, "GET", "/files", nil, &files); err != nil {
			fatal(err)
		}
		printLines(files)
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	files, err := d.Files()
	if err != nil {
		fatal(err)
	}
	printLines(files)
}

func cmdStale(args []string) {
	fs := flag.NewFlagSet("stale", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
		var stale []string
		if err := proxyDecode(client, "GET", "/stale", nil, &stale); err != nil {
			fatal(err)
		}
		printLines(stale)
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	stale, err := d.Stale()
	if err != nil {
		fatal(err)
	}
	printLines(stale)
}

func cmdMissing(args []string) {
	fs := flag.NewFlagSet("missing", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
		var missing []ark.MissingRecord
		if err := proxyDecode(client, "GET", "/missing", nil, &missing); err != nil {
			fatal(err)
		}
		for _, m := range missing {
			fmt.Println(m.Path)
		}
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	missing, err := d.Missing()
	if err != nil {
		fatal(err)
	}
	for _, m := range missing {
		fmt.Println(m.Path)
	}
}

func cmdDismiss(args []string) {
	fs := flag.NewFlagSet("dismiss", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no files or patterns specified")
		os.Exit(1)
	}

	if client := serverClient(*db); client != nil {
		if err := proxyOK(client, "POST", "/dismiss", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	if err := d.Dismiss(patterns); err != nil {
		fatal(err)
	}
}

func cmdConfig(args []string) {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
		// Config is TOML on disk but JSON from server — just dump the raw response
		data, err := proxyRaw(client, "GET", "/config", nil)
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(data)
		return
	}

	configPath := filepath.Join(*db, "ark.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		fatal(err)
	}
	os.Stdout.Write(data)
}

func cmdUnresolved(args []string) {
	fs := flag.NewFlagSet("unresolved", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	if client := serverClient(*db); client != nil {
		var unresolved []ark.UnresolvedRecord
		if err := proxyDecode(client, "GET", "/unresolved", nil, &unresolved); err != nil {
			fatal(err)
		}
		for _, u := range unresolved {
			fmt.Println(u.Path)
		}
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	unresolved, err := d.Unresolved()
	if err != nil {
		fatal(err)
	}
	for _, u := range unresolved {
		fmt.Println(u.Path)
	}
}

func cmdResolve(args []string) {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "database path")
	fs.Parse(args)

	patterns := fs.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no patterns specified")
		os.Exit(1)
	}

	if client := serverClient(*db); client != nil {
		if err := proxyOK(client, "POST", "/resolve", map[string]any{"patterns": patterns}); err != nil {
			fatal(err)
		}
		return
	}

	d, err := ark.Open(*db)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	if err := d.Resolve(patterns); err != nil {
		fatal(err)
	}
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

func parseAliases(s string) map[rune]rune {
	if s == "" {
		return nil
	}
	aliases := make(map[rune]rune)
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		from := []rune(parts[0])
		to := []rune(parts[1])
		if len(from) == 1 && len(to) == 1 {
			aliases[from[0]] = to[0]
		}
	}
	return aliases
}
