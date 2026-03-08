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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/zot/frictionless/flib"
)

// Server is an HTTP server on a Unix domain socket.
type Server struct {
	db          *DB
	listener    net.Listener
	pidPath     string
	noScan      bool
	uiRuntime   *flib.Runtime
	watcher     *fsnotify.Watcher
	reconcileCh chan struct{}
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
	mux.HandleFunc("POST /config/add-source", srv.handleConfigAddSource)
	mux.HandleFunc("POST /config/remove-source", srv.handleConfigRemoveSource)
	mux.HandleFunc("POST /config/add-include", srv.handleConfigAddInclude)
	mux.HandleFunc("POST /config/add-exclude", srv.handleConfigAddExclude)
	mux.HandleFunc("POST /config/remove-pattern", srv.handleConfigRemovePattern)
	mux.HandleFunc("POST /config/show-why", srv.handleConfigShowWhy)
	mux.HandleFunc("POST /config/add-strategy", srv.handleConfigAddStrategy)
	mux.HandleFunc("POST /fetch", srv.handleFetch)
	mux.HandleFunc("POST /config/sources-check", srv.handleSourcesCheck)

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
		if _, err := rt.Start(); err != nil {
			log.Printf("ui: start failed: %v", err)
			srv.uiRuntime = nil
			return
		}
		log.Printf("ui: engine started (dir: %s)", dbPath)
		// Auto-display the ark app so every new session starts with it
		if _, err := rt.RunLua(`mcp:display("ark")`); err != nil {
			log.Printf("ui: auto-display ark failed: %v", err)
		}
	}()
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
	Query        string   `json:"query"`
	About        string   `json:"about"`
	Contains     string   `json:"contains"`
	Regex        string   `json:"regex"`
	LikeFile     string   `json:"likeFile"`
	K            int      `json:"k"`
	Scores       bool     `json:"scores"`
	After        int64    `json:"after"`
	Chunks       bool     `json:"chunks"`
	Files        bool     `json:"files"`
	Tags         bool     `json:"tags"`
	Filter       []string `json:"filter"`
	Except       []string `json:"except"`
	FilterFiles  []string `json:"filterFiles"`
	ExcludeFiles []string `json:"excludeFiles"`
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

	opts := SearchOpts{
		K:            req.K,
		Scores:       req.Scores,
		After:        req.After,
		About:        req.About,
		Contains:     req.Contains,
		Regex:        req.Regex,
		LikeFile:     req.LikeFile,
		Tags:         req.Tags,
		Filter:       req.Filter,
		Except:       req.Except,
		FilterFiles:  req.FilterFiles,
		ExcludeFiles: req.ExcludeFiles,
	}

	var results []SearchResultEntry
	var err error
	if req.About != "" || req.Contains != "" || req.Regex != "" || req.LikeFile != "" {
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

func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := srv.db.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

// Seq: seq-config-mutate.md

type configPatternRequest struct {
	Pattern string `json:"pattern"`
	Source  string `json:"source"`
}

type configSourceRequest struct {
	Dir      string `json:"dir"`
	Strategy string `json:"strategy"`
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
	srv.configMutate(w, r, &req, func() error { return srv.db.Config().AddSource(req.Dir, req.Strategy) })
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
