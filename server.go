package ark

// CRC: crc-Server.md | Seq: seq-server-startup.md, seq-reconcile.md, seq-file-change.md

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	lua "github.com/yuin/gopher-lua"
	"github.com/zot/frictionless/flib"
	"github.com/zot/microfts2"
	"github.com/zot/ui-engine/cli"
)

// Server is an HTTP server on a Unix domain socket.
type Server struct {
	db              *DB
	listener        net.Listener
	pidPath         string
	noScan          bool
	uiRuntime       *flib.Runtime
	watcher         *fsnotify.Watcher
	reconcileCh     chan struct{}
	ignoredPaths    map[string]struct{} // negative cache: non-indexable paths
	indexingMu      sync.Mutex
	indexingSources []string // source dirs currently being indexed
	uiPort          int      // HTTP port the ui-engine is listening on (0 if not started)
	sessionsMu      sync.Mutex
	sessions        map[string]*Session // R641: named sessions, autocreated on demand
}

// ServeOpts controls server behavior.
type ServeOpts struct {
	NoScan bool
}

// ServerAlreadyRunning is returned when `ark serve` finds an existing server.
// The CLI uses this to exit 0 (idempotent "make sure it's up").
var ServerAlreadyRunning = errors.New("server already running")

const maxLogSize = 10 * 1024 * 1024 // 10MB
const keepLogSize = 1 * 1024 * 1024 // 1MB

// setupLogging configures file logging for the server.
// Logs go to both stderr and ~/.ark/logs/ark.log.
func setupLogging(dbPath string) {
	logsDir := filepath.Join(dbPath, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Printf("warning: could not create logs dir: %v", err)
		return
	}
	logPath := filepath.Join(logsDir, "ark.log")

	// Truncate if over size cap — keep the last 1MB
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogSize {
		data, err := os.ReadFile(logPath)
		if err == nil && len(data) > keepLogSize {
			os.WriteFile(logPath, data[len(data)-keepLogSize:], 0644)
		}
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("warning: could not open log file: %v", err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

// Serve starts the ark server: binds socket, opens DB, reconciles, serves.
func Serve(dbPath string, opts ServeOpts) error {
	setupLogging(dbPath)
	socketPath := filepath.Join(dbPath, "ark.sock")

	// Highlander: try to bind the socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		// Check if it's a stale socket
		conn, dialErr := net.Dial("unix", socketPath)
		if dialErr == nil {
			conn.Close()
			return ServerAlreadyRunning
		}
		// Stale socket — remove and retry
		os.Remove(socketPath)
		listener, err = net.Listen("unix", socketPath)
		if err != nil {
			return fmt.Errorf("bind socket: %w", err)
		}
	}

	// Write PID file — never removed by server (stale PID is safe,
	// ark stop verifies before killing)
	pidPath := PidFilePath(dbPath)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}

	// Open database
	db, err := Open(dbPath)
	if err != nil {
		listener.Close()
		return fmt.Errorf("open database: %w", err)
	}

	// Ensure ~/.ark is always a source (hardcoded, not in ark.toml)
	db.Config().EnsureArkSource()

	srv := &Server{
		db:          db,
		listener:    listener,
		pidPath:     pidPath,
		noScan:      opts.NoScan,
		reconcileCh: make(chan struct{}, 1),
	}

	// Reconciliation goroutine — serializes all reconcile requests.
	// Buffered channel ensures at most one pending request queues
	// behind a running reconciliation.
	go srv.reconcileLoop()

	// Signal handling: catch SIGTERM, shut down UI engine, close socket, close DB, exit 0
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down", sig)
		if srv.watcher != nil {
			srv.watcher.Close()
		}
		if srv.uiRuntime != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			srv.uiRuntime.Shutdown(ctx)
			cancel()
		}
		listener.Close()
		db.Close()
		os.Exit(0)
	}()

	// Start embedded UI engine (optional — failure is non-fatal)
	srv.startUIEngine(dbPath)

	// Start filesystem watches BEFORE reconciliation (R358) so nothing
	// changes unseen during the scan. Watching is optional — failure
	// is non-fatal.
	if !opts.NoScan {
		srv.startWatching()
	}

	// Startup reconciliation — background so server accepts requests immediately
	if !opts.NoScan {
		srv.reconcile()
	}

	// Set up routes
	mux := http.NewServeMux()

	// Mount Frictionless API routes (/api/*, /wait, /state, /variables)
	// on the same unix socket — no separate MCP port needed.
	if srv.uiRuntime != nil {
		srv.uiRuntime.RegisterAPI(mux)
	}

	mux.HandleFunc("POST /search", srv.handleSearch)
	mux.HandleFunc("POST /add", srv.handleAdd)
	mux.HandleFunc("POST /remove", srv.handleRemove)
	mux.HandleFunc("POST /scan", srv.handleScan)
	mux.HandleFunc("POST /refresh", srv.handleRefresh)
	mux.HandleFunc("GET /status", srv.handleStatus)
	mux.HandleFunc("GET /files", srv.handleFiles)
	mux.HandleFunc("GET /stale", srv.handleStale)
	mux.HandleFunc("GET /missing", srv.handleMissing)
	mux.HandleFunc("POST /dismiss", srv.handleDismiss)
	mux.HandleFunc("GET /config", srv.handleConfig)
	mux.HandleFunc("GET /unresolved", srv.handleUnresolved)
	mux.HandleFunc("POST /resolve", srv.handleResolve)
	mux.HandleFunc("GET /tags", srv.handleTags)
	mux.HandleFunc("POST /tags/counts", srv.handleTagCounts)
	mux.HandleFunc("POST /tags/files", srv.handleTagFiles)
	mux.HandleFunc("POST /tags/defs", srv.handleTagDefs)
	mux.HandleFunc("POST /config/add-source", srv.handleConfigAddSource)
	mux.HandleFunc("POST /config/remove-source", srv.handleConfigRemoveSource)
	mux.HandleFunc("POST /config/add-include", srv.handleConfigAddInclude)
	mux.HandleFunc("POST /config/add-exclude", srv.handleConfigAddExclude)
	mux.HandleFunc("POST /config/remove-pattern", srv.handleConfigRemovePattern)
	mux.HandleFunc("POST /config/show-why", srv.handleConfigShowWhy)
	mux.HandleFunc("POST /config/add-strategy", srv.handleConfigAddStrategy)
	mux.HandleFunc("POST /fetch", srv.handleFetch)
	mux.HandleFunc("POST /config/sources-check", srv.handleSourcesCheck)
	mux.HandleFunc("POST /ui/reload", srv.handleUIReload)
	mux.HandleFunc("POST /tmp/add", srv.handleTmpAdd)
	mux.HandleFunc("POST /tmp/update", srv.handleTmpUpdate)
	mux.HandleFunc("POST /tmp/remove", srv.handleTmpRemove)
	mux.HandleFunc("GET /tmp/list", srv.handleTmpList)

	log.Printf("ark server listening on %s", socketPath)
	return http.Serve(listener, mux)
}

// startUIEngine configures and starts the embedded Frictionless runtime.
// If the UI assets aren't present or the server fails to start, it logs
// a warning and continues — the UI is optional.
func (srv *Server) startUIEngine(dbPath string) {
	// Project points to a staging area under ~/.ark/ so auto-install
	// doesn't write skills into uncontrolled directories. Skills land
	// in ~/.ark/staging/.claude/skills/ — ark init copies them to
	// ~/.ark/skills/ where projects can symlink to them.
	stagingDir := filepath.Join(dbPath, "staging")
	rt, err := flib.New(flib.Config{
		Dir:     dbPath,
		Host:    "127.0.0.1",
		Project: stagingDir,
	})
	if err != nil {
		log.Printf("ui: failed to create runtime: %v", err)
		return
	}
	if err := rt.Configure(); err != nil {
		log.Printf("ui: configure failed: %v", err)
		return
	}
	srv.uiRuntime = rt

	go func() {
		url, err := rt.Start()
		if err != nil {
			log.Printf("ui: start failed: %v", err)
			srv.uiRuntime = nil
			return
		}
		if p := parseURLPort(url); p != 0 {
			srv.uiPort = p
		}
		log.Printf("ui: engine started on %s (dir: %s)", url, dbPath)
		// Register Go functions on the Lua mcp table (passive path)
		srv.registerLuaFunctions()
	}()
}

// parseURLPort extracts the port number from a URL like "http://127.0.0.1:8080".
func parseURLPort(url string) int {
	if parts := strings.SplitN(url, ":", 3); len(parts) == 3 {
		if p, err := strconv.Atoi(parts[2]); err == nil {
			return p
		}
	}
	return 0
}

// ReloadUIEngine stops the current ui-engine and starts a fresh one
// on the same port. Re-registers Lua functions and re-displays the ark app.
// CRC: crc-Server.md | Seq: seq-server-startup.md
func (srv *Server) ReloadUIEngine() error {
	if srv.uiRuntime == nil {
		return fmt.Errorf("ui engine not running")
	}
	dbPath := srv.db.Path()
	savedPort := srv.uiPort

	// Shutdown current runtime
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	srv.uiRuntime.Shutdown(ctx)
	cancel()
	srv.uiRuntime = nil

	// Create new runtime with saved port
	stagingDir := filepath.Join(dbPath, "staging")
	rt, err := flib.New(flib.Config{
		Dir:     dbPath,
		Host:    "127.0.0.1",
		Project: stagingDir,
		Port:    savedPort,
	})
	if err != nil {
		return fmt.Errorf("ui reload: create runtime: %w", err)
	}
	if err := rt.Configure(); err != nil {
		return fmt.Errorf("ui reload: configure: %w", err)
	}

	url, err := rt.Start()
	if err != nil {
		return fmt.Errorf("ui reload: start: %w", err)
	}
	srv.uiRuntime = rt

	// Extract new port — may differ if saved port was unavailable
	if p := parseURLPort(url); p != 0 {
		if p != savedPort {
			log.Printf("ui reload: port changed %d → %d (saved port unavailable)", savedPort, p)
		}
		srv.uiPort = p
	}

	log.Printf("ui: reloaded on %s", url)
	srv.registerLuaFunctions()
	return nil
}

// reconcileLoop processes reconciliation requests serially.
// Each request triggers a full sources-check → scan → refresh cycle.
func (srv *Server) reconcileLoop() {
	for range srv.reconcileCh {
		srv.doReconcile()
	}
}

// reconcile requests a reconciliation cycle. Non-blocking — if a
// reconciliation is already running, the request queues (buffer of 1).
// If the buffer is full, the request is a no-op since a pending
// reconciliation will pick up the latest state anyway.
func (srv *Server) reconcile() {
	select {
	case srv.reconcileCh <- struct{}{}:
	default:
		// Already one queued — filesystem state will be current when it runs
	}
}

// doReconcile runs the actual reconciliation: sources-check, scan, refresh.
// After sources-check, updates watches for any new/removed sources (R351).
func (srv *Server) doReconcile() {
	if result, err := srv.db.SourcesCheck(); err != nil {
		log.Printf("reconcile: sources check error: %v", err)
	} else {
		if len(result.Added) > 0 {
			log.Printf("reconcile: added %d new source(s)", len(result.Added))
			for _, dir := range result.Added {
				srv.watchDirRecursive(dir)
			}
		}
		for _, dir := range result.Orphaned {
			srv.unwatchDir(dir)
		}
	}
	// Collect source dirs for indexing state
	var sourceDirs []string
	for _, src := range srv.db.Config().Sources {
		sourceDirs = append(sourceDirs, src.Dir)
	}
	srv.setIndexing(sourceDirs)
	defer srv.setIndexing(nil)

	log.Println("reconcile: scanning...")
	if _, err := srv.db.Scan(); err != nil {
		log.Printf("reconcile: scan error: %v", err)
	}
	log.Println("reconcile: refreshing...")
	if err := srv.db.Refresh(nil); err != nil {
		log.Printf("reconcile: refresh error: %v", err)
	}
	log.Println("reconcile: complete")
}

func PidFilePath(dbPath string) string {
	// PID file outside the database directory, derived from dbPath
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		absPath = dbPath
	}
	// Replace path separators to make a flat filename
	name := strings.ReplaceAll(absPath, string(filepath.Separator), "_")
	return filepath.Join(os.TempDir(), "ark"+name+".pid")
}

// JSON request/response helpers

type searchRequest struct {
	Query           string   `json:"query"`
	About           string   `json:"about"`
	Contains        string   `json:"contains"`
	Regex           []string `json:"regex"`
	ExceptRegex     []string `json:"exceptRegex"`
	LikeFile        string   `json:"likeFile"`
	K               int      `json:"k"`
	Scores          bool     `json:"scores"`
	After           string   `json:"after"`
	Before          string   `json:"before"`
	Chunks          bool     `json:"chunks"`
	Files           bool     `json:"files"`
	Tags            bool     `json:"tags"`
	Filter          []string `json:"filter"`
	Except          []string `json:"except"`
	FilterFiles     []string `json:"filterFiles"`
	ExcludeFiles    []string `json:"excludeFiles"`
	FilterFileTags  []string `json:"filterFileTags"`
	ExcludeFileTags []string `json:"excludeFileTags"`
	Session         string   `json:"session,omitempty"`   // R657: optional session name
	NoTmp           bool     `json:"noTmp,omitempty"`     // R687: exclude tmp:// documents
	OnlyIfTmp       bool     `json:"onlyIfTmp,omitempty"` // R686: return 204 if no tmp files
}

// tmpRequest is the body for tmp:// add/update/remove endpoints.
type tmpRequest struct {
	Path     string `json:"path"`
	Strategy string `json:"strategy,omitempty"`
	Content  string `json:"content,omitempty"` // base64 or raw text
}

type addRequest struct {
	Paths    []string `json:"paths"`
	Strategy string   `json:"strategy"`
}

type removeRequest struct {
	Patterns []string `json:"patterns"`
}

type refreshRequest struct {
	Patterns []string `json:"patterns"`
}

type dismissRequest struct {
	Patterns []string `json:"patterns"`
}

type resolveRequest struct {
	Patterns []string `json:"patterns"`
}

func buildSearchOpts(req searchRequest) SearchOpts {
	opts := SearchOpts{
		K:               req.K,
		Scores:          req.Scores,
		About:           req.About,
		Contains:        req.Contains,
		Regex:           req.Regex,
		ExceptRegex:     req.ExceptRegex,
		LikeFile:        req.LikeFile,
		Tags:            req.Tags,
		Filter:          req.Filter,
		Except:          req.Except,
		FilterFiles:     req.FilterFiles,
		ExcludeFiles:    req.ExcludeFiles,
		FilterFileTags:  req.FilterFileTags,
		ExcludeFileTags: req.ExcludeFileTags,
		NoTmp:           req.NoTmp,
	}
	if req.After != "" {
		if t, err := ParseDate(req.After); err == nil {
			opts.After = t
		}
	}
	if req.Before != "" {
		if t, err := ParseDate(req.Before); err == nil {
			opts.Before = t
		}
	}
	return opts
}

// GetOrCreateSession returns the named session, creating it if needed.
// R641, R648
func (srv *Server) GetOrCreateSession(name string) *Session {
	srv.sessionsMu.Lock()
	defer srv.sessionsMu.Unlock()
	if srv.sessions == nil {
		srv.sessions = make(map[string]*Session)
	}
	s, ok := srv.sessions[name]
	if !ok {
		ttl := srv.db.Config().ParseSessionTTL()
		s = NewSession(name, srv.db.FTS(), ttl)
		srv.sessions[name] = s
	}
	return s
}

func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Chunks && req.Files {
		http.Error(w, "--chunks and --files are mutually exclusive", http.StatusBadRequest)
		return
	}

	// R686: onlyIfTmp — return 204 if no tmp files exist
	if req.OnlyIfTmp && !srv.db.HasTmp() {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	opts := buildSearchOpts(req)

	// R657, R658, R659: session-scoped search
	if req.Session != "" {
		sess := srv.GetOrCreateSession(req.Session)
		var results []SearchResultEntry
		err := sess.RunSearch(req.Query, func(cache *microfts2.ChunkCache) error {
			var searchErr error
			if req.About != "" || req.Contains != "" || len(req.Regex) > 0 || req.LikeFile != "" {
				results, searchErr = srv.db.SearchSplit(opts)
			} else {
				results, searchErr = srv.db.SearchCombined(req.Query, opts)
			}
			if searchErr != nil {
				return searchErr
			}
			if req.Tags || req.Chunks {
				results, searchErr = srv.db.FillChunksUsing(results, cache)
			} else if req.Files {
				results, searchErr = srv.db.FillFiles(results)
			}
			return searchErr
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if req.Tags {
			writeJSON(w, ExtractResultTags(results))
		} else {
			writeJSON(w, results)
		}
		return
	}

	// No session — existing per-query behavior
	var results []SearchResultEntry
	var err error
	if req.About != "" || req.Contains != "" || len(req.Regex) > 0 || req.LikeFile != "" {
		results, err = srv.db.SearchSplit(opts)
	} else {
		results, err = srv.db.SearchCombined(req.Query, opts)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Tags || req.Chunks {
		results, err = srv.db.FillChunks(results)
	} else if req.Files {
		results, err = srv.db.FillFiles(results)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Tags {
		writeJSON(w, ExtractResultTags(results))
	} else {
		writeJSON(w, results)
	}
}

func (srv *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.Add(req.Paths, req.Strategy); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (srv *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	var req removeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.Remove(req.Patterns); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (srv *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	results, err := srv.db.Scan()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"newFiles":      len(results.NewFiles),
		"newUnresolved": len(results.NewUnresolved),
	})
}

func (srv *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.Refresh(req.Patterns); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// CRC: crc-Server.md
func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := srv.db.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// R437-R441: Enrich with UI fields
	if srv.uiRuntime != nil {
		status.UIRunning = true
		status.UIPort = srv.uiPort
	}
	status.UIIndexing = len(srv.currentlyIndexing()) > 0
	writeJSON(w, status)
}

func (srv *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	files, err := srv.db.Files()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, files)
}

func (srv *Server) handleStale(w http.ResponseWriter, r *http.Request) {
	stale, err := srv.db.Stale()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, stale)
}

func (srv *Server) handleMissing(w http.ResponseWriter, r *http.Request) {
	missing, err := srv.db.Missing()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, missing)
}

func (srv *Server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	var req dismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.Dismiss(req.Patterns); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (srv *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, srv.db.Config())
}

func (srv *Server) handleUnresolved(w http.ResponseWriter, r *http.Request) {
	unresolved, err := srv.db.Unresolved()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, unresolved)
}

func (srv *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.Resolve(req.Patterns); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

type tagRequest struct {
	Tags    []string `json:"tags"`
	Context bool     `json:"context"`
}

func (srv *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := srv.db.TagList()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, tags)
}

func (srv *Server) handleTagCounts(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	counts, err := srv.db.TagCounts(req.Tags)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, counts)
}

func (srv *Server) handleTagFiles(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Context {
		entries, err := srv.db.TagContext(req.Tags)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, entries)
		return
	}

	files, err := srv.db.TagFiles(req.Tags)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, files)
}

func (srv *Server) handleTagDefs(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defs, err := srv.db.TagDefs(req.Tags)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, defs)
}

// Seq: seq-config-mutate.md

type configPatternRequest struct {
	Pattern string `json:"pattern"`
	Source  string `json:"source"`
}

type configSourceRequest struct {
	Dir string `json:"dir"`
}

type configWhyRequest struct {
	Path string `json:"path"`
}

// configMutate decodes a request, applies a config mutation, saves,
// and triggers reconciliation so the index reflects the new config.
func (srv *Server) configMutate(w http.ResponseWriter, r *http.Request, v any, fn func() error) {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := fn(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.SaveConfig(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	srv.reconcile()
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) handleConfigAddSource(w http.ResponseWriter, r *http.Request) {
	var req configSourceRequest
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().AddSource(req.Dir) })
}

func (srv *Server) handleConfigRemoveSource(w http.ResponseWriter, r *http.Request) {
	var req configSourceRequest
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().RemoveSource(req.Dir) })
}

func (srv *Server) handleConfigAddInclude(w http.ResponseWriter, r *http.Request) {
	var req configPatternRequest
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().AddInclude(req.Pattern, req.Source) })
}

func (srv *Server) handleConfigAddExclude(w http.ResponseWriter, r *http.Request) {
	var req configPatternRequest
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().AddExclude(req.Pattern, req.Source) })
}

func (srv *Server) handleConfigRemovePattern(w http.ResponseWriter, r *http.Request) {
	var req configPatternRequest
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().RemovePattern(req.Pattern, req.Source) })
}

type configStrategyRequest struct {
	Pattern  string `json:"pattern"`
	Strategy string `json:"strategy"`
}

func (srv *Server) handleConfigAddStrategy(w http.ResponseWriter, r *http.Request) {
	var req configStrategyRequest
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().AddStrategy(req.Pattern, req.Strategy) })
}

func (srv *Server) handleConfigShowWhy(w http.ResponseWriter, r *http.Request) {
	var req configWhyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := srv.db.Config().ShowWhy(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

type fetchRequest struct {
	Path string `json:"path"`
}

func (srv *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	var req fetchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := srv.db.Fetch(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"content": string(data)})
}

func (srv *Server) handleSourcesCheck(w http.ResponseWriter, r *http.Request) {
	result, err := srv.db.SourcesCheck()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
	}
}

// CRC: crc-Server.md | Seq: seq-server-startup.md
func (srv *Server) handleUIReload(w http.ResponseWriter, r *http.Request) {
	if err := srv.ReloadUIEngine(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"status": "ok",
		"port":   srv.uiPort,
	})
}

// currentlyIndexing returns the list of source dirs currently being indexed.
// CRC: crc-Server.md
func (srv *Server) currentlyIndexing() []string {
	srv.indexingMu.Lock()
	defer srv.indexingMu.Unlock()
	if srv.indexingSources == nil {
		return []string{}
	}
	result := make([]string, len(srv.indexingSources))
	copy(result, srv.indexingSources)
	return result
}

// setIndexing updates the list of currently indexing sources.
func (srv *Server) setIndexing(sources []string) {
	srv.indexingMu.Lock()
	defer srv.indexingMu.Unlock()
	if sources == nil {
		srv.indexingSources = nil
		return
	}
	srv.indexingSources = append([]string{}, sources...)
}

// handleTmpAdd adds a tmp:// document.
// CRC: crc-Server.md | R685
func (srv *Server) handleTmpAdd(w http.ResponseWriter, r *http.Request) {
	var req tmpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = "lines"
	}
	fid, err := srv.db.AddTmpFile(req.Path, strategy, []byte(req.Content))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]uint64{"fileid": fid})
}

// handleTmpUpdate updates an existing tmp:// document.
// CRC: crc-Server.md | R685
func (srv *Server) handleTmpUpdate(w http.ResponseWriter, r *http.Request) {
	var req tmpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = "lines"
	}
	if err := srv.db.UpdateTmpFile(req.Path, strategy, []byte(req.Content)); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleTmpRemove removes a tmp:// document.
// CRC: crc-Server.md | R685
func (srv *Server) handleTmpRemove(w http.ResponseWriter, r *http.Request) {
	var req tmpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.db.RemoveTmpFile(req.Path); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleTmpList lists all tmp:// paths.
// CRC: crc-Server.md | R685
func (srv *Server) handleTmpList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, srv.db.TmpFiles())
}

// registerLuaFunctions registers Go functions on the Lua mcp table
// via the passive execution path (no UI update push).
// CRC: crc-Server.md | Seq: seq-search.md
func (srv *Server) registerLuaFunctions() {
	if srv.uiRuntime == nil {
		return
	}
	err := srv.uiRuntime.WithLua(func(rt *cli.LuaRuntime) error {
		L := rt.State
		mcpTable := L.GetGlobal("mcp")
		if mcpTable == lua.LNil {
			return fmt.Errorf("mcp table not found")
		}
		tbl, ok := mcpTable.(*lua.LTable)
		if !ok {
			return fmt.Errorf("mcp is not a table")
		}

		// mcp:indexing() — returns array of source dirs currently being indexed
		L.SetField(tbl, "indexing", L.NewFunction(func(L *lua.LState) int {
			sources := srv.currentlyIndexing()
			result := L.NewTable()
			for i, dir := range sources {
				result.RawSetInt(i+1, lua.LString(dir))
			}
			L.Push(result)
			return 1
		}))

		// luaStringSlice extracts a []string from a Lua value:
		// string → single-element slice, table → iterate array part.
		luaStringSlice := func(v lua.LValue) []string {
			switch val := v.(type) {
			case lua.LString:
				return []string{string(val)}
			case *lua.LTable:
				var ss []string
				val.ForEach(func(k, v lua.LValue) {
					if _, ok := k.(lua.LNumber); ok {
						ss = append(ss, v.String())
					}
				})
				return ss
			}
			return nil
		}

		// mcp:search_grouped(query, opts) — grouped search results as Lua tables
		L.SetField(tbl, "search_grouped", L.NewFunction(func(L *lua.LState) int {
			query := L.CheckString(1)
			opts := SearchOpts{K: 20}
			if L.GetTop() >= 2 {
				optsTable := L.CheckTable(2)
				if v := optsTable.RawGetString("k"); v != lua.LNil {
					if n, ok := v.(lua.LNumber); ok {
						opts.K = int(n)
					}
				}
				if v := optsTable.RawGetString("mode"); v != lua.LNil {
					mode := v.String()
					switch mode {
					case "contains":
						opts.Contains = query
						query = ""
					case "about":
						opts.About = query
						query = ""
					case "regex":
						opts.Regex = []string{query}
						query = ""
					}
					// "combined" is the default — uses query as-is
				}
				if v := optsTable.RawGetString("filter_files"); v != lua.LNil {
					opts.FilterFiles = luaStringSlice(v)
				}
				if v := optsTable.RawGetString("exclude_files"); v != lua.LNil {
					opts.ExcludeFiles = luaStringSlice(v)
				}
				if v := optsTable.RawGetString("filter_file_tags"); v != lua.LNil {
					opts.FilterFileTags = luaStringSlice(v)
				}
				if v := optsTable.RawGetString("exclude_file_tags"); v != lua.LNil {
					opts.ExcludeFileTags = luaStringSlice(v)
				}
				if v := optsTable.RawGetString("filter"); v != lua.LNil {
					opts.Filter = luaStringSlice(v)
				}
				if v := optsTable.RawGetString("except"); v != lua.LNil {
					opts.Except = luaStringSlice(v)
				}
			}

			// R660, R661: session-scoped search for UI
			var sessionName string
			if L.GetTop() >= 2 {
				optsTable := L.CheckTable(2)
				if v := optsTable.RawGetString("session"); v != lua.LNil {
					sessionName = v.String()
				}
			}

			var results []GroupedResult
			var err error
			if sessionName != "" {
				sess := srv.GetOrCreateSession(sessionName)
				err = sess.RunSearch(query, func(cache *microfts2.ChunkCache) error {
					opts.Cache = cache
					var searchErr error
					results, searchErr = srv.db.SearchGrouped(query, opts)
					return searchErr
				})
			} else {
				results, err = srv.db.SearchGrouped(query, opts)
			}
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			resultTable := L.NewTable()
			for i, group := range results {
				groupTable := L.NewTable()
				L.SetField(groupTable, "path", lua.LString(group.Path))
				L.SetField(groupTable, "strategy", lua.LString(group.Strategy))
				chunksTable := L.NewTable()
				for j, chunk := range group.Chunks {
					chunkTable := L.NewTable()
					L.SetField(chunkTable, "range", lua.LString(chunk.Range))
					L.SetField(chunkTable, "score", lua.LNumber(chunk.Score))
					L.SetField(chunkTable, "preview", lua.LString(chunk.Preview))
					chunksTable.RawSetInt(j+1, chunkTable)
				}
				L.SetField(groupTable, "chunks", chunksTable)
				resultTable.RawSetInt(i+1, groupTable)
			}
			L.Push(resultTable)
			return 1
		}))

		// mcp:open(path) — open indexed file with system viewer
		L.SetField(tbl, "open", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			if !srv.db.IsIndexed(path) {
				L.Push(lua.LNil)
				L.Push(lua.LString("file not indexed"))
				return 2
			}
			var cmd *exec.Cmd
			if runtime.GOOS == "darwin" {
				cmd = exec.Command("open", path)
			} else {
				cmd = exec.Command("xdg-open", path)
			}
			if err := cmd.Start(); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(fmt.Sprintf("open: %v", err)))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp:inbox(show_all) — cross-project message entries as Lua tables
		// CRC: crc-Server.md | Seq: seq-message.md | R563-R568, R620, R623
		L.SetField(tbl, "inbox", L.NewFunction(func(L *lua.LState) int {
			showAll := false
			if L.GetTop() >= 1 {
				showAll = L.ToBool(1)
			}
			entries, err := srv.db.Inbox(showAll, false)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, e := range entries {
				row := L.NewTable()
				L.SetField(row, "status", lua.LString(e.Status))
				L.SetField(row, "to", lua.LString(e.To))
				L.SetField(row, "from", lua.LString(e.From))
				L.SetField(row, "summary", lua.LString(e.Summary))
				L.SetField(row, "path", lua.LString(e.Path))
				L.SetField(row, "requestId", lua.LString(e.RequestID))
				L.SetField(row, "kind", lua.LString(e.Kind))
				L.SetField(row, "responseHandled", lua.LString(e.ResponseHandled))
				L.SetField(row, "requestHandled", lua.LString(e.RequestHandled))
				result.RawSetInt(i+1, row)
			}
			L.Push(result)
			return 1
		}))

		// mcp:parseJson(str) — parse JSON string into Lua table
		// R569, R571
		L.SetField(tbl, "parseJson", L.NewFunction(func(L *lua.LState) int {
			str := L.CheckString(1)
			var data any
			if err := json.Unmarshal([]byte(str), &data); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(jsonToLua(L, data))
			return 1
		}))

		// mcp:readJsonFile(path) — read file and parse JSON into Lua table
		// R570, R571
		L.SetField(tbl, "readJsonFile", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			raw, err := os.ReadFile(path)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			var data any
			if err := json.Unmarshal(raw, &data); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(jsonToLua(L, data))
			return 1
		}))

		// mcp.tmp_add(path, content, strategy) — add a tmp:// document
		// R688
		L.SetField(tbl, "tmp_add", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			content := L.CheckString(2)
			strategy := "lines"
			if L.GetTop() >= 3 {
				if s := L.CheckString(3); s != "" {
					strategy = s
				}
			}
			fid, err := srv.db.AddTmpFile(path, strategy, []byte(content))
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LNumber(fid))
			return 1
		}))

		// mcp.tmp_update(path, content, strategy) — update tmp:// document
		// R689
		L.SetField(tbl, "tmp_update", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			content := L.CheckString(2)
			strategy := "lines"
			if L.GetTop() >= 3 {
				if s := L.CheckString(3); s != "" {
					strategy = s
				}
			}
			if err := srv.db.UpdateTmpFile(path, strategy, []byte(content)); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp.tmp_remove(path) — remove tmp:// document
		// R690
		L.SetField(tbl, "tmp_remove", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			if err := srv.db.RemoveTmpFile(path); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp.tmp_list() — list all tmp:// paths
		// R691
		L.SetField(tbl, "tmp_list", L.NewFunction(func(L *lua.LState) int {
			paths := srv.db.TmpFiles()
			result := L.NewTable()
			for i, p := range paths {
				result.RawSetInt(i+1, lua.LString(p))
			}
			L.Push(result)
			return 1
		}))

		return nil
	})
	if err != nil {
		log.Printf("ui: register lua functions failed: %v", err)
	}
}

// jsonToLua converts a Go value (from json.Unmarshal) to a Lua value.
func jsonToLua(L *lua.LState, v any) lua.LValue {
	switch val := v.(type) {
	case map[string]any:
		tbl := L.NewTable()
		for k, v := range val {
			L.SetField(tbl, k, jsonToLua(L, v))
		}
		return tbl
	case []any:
		tbl := L.NewTable()
		for i, v := range val {
			tbl.RawSetInt(i+1, jsonToLua(L, v))
		}
		return tbl
	case string:
		return lua.LString(val)
	case float64:
		return lua.LNumber(val)
	case bool:
		return lua.LBool(val)
	case nil:
		return lua.LNil
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}
