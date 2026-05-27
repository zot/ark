package ark

// CRC: crc-Server.md | Seq: seq-server-startup.md, seq-reconcile.md, seq-file-change.md

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
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
	ignoredPaths    map[string]struct{} // negative cache: non-indexable paths
	indexingMu      sync.Mutex
	indexingSources []string // source dirs currently being indexed
	uiPort          int      // HTTP port the ui-engine is listening on (0 if not started)
	sessionsMu      sync.Mutex
	sessions        map[string]*Session // R641: named sessions, autocreated on demand
	pubsub          *PubSub             // R799: subscription registry
	scheduler       *EventScheduler     // R805: time-based event queue
	librarian       *Librarian          // R1235: Haiku co-process for spectral search
	curation        *Curation           // R2355: Go-owned curation workshop state; sys.curation in Lua
	recallWatcher       *RecallWatcher      // R2687: ambient simple-recall subsystem; nil when [recall].enabled is false
	recallAgentBuilder  *RecallAgentBuilder // R2754, R2755-R2758: curation + result doc builders; in-flight per-fire state

	// R2294, R2299, R2300: Lua-side subscription scaffolding. Each
	// sessionID with at least one mcp.subscribe gets a listening
	// goroutine that drains pubsub.Listen, compresses by (path, tag),
	// and dispatches to its registered onpublish callback via WithLua.
	// listenMu protects both maps below.
	listenMu     sync.Mutex
	listenLoops  map[string]chan struct{}  // sessionID → stop signal
	onpublishCBs map[string]*lua.LFunction // sessionID → onpublish callback (R2291)
}

// ServeOpts controls server behavior.
type ServeOpts struct {
	NoScan    bool
	Verbosity int  // R735: verbose level (0–4)
	Force     bool // R1558: accept config changes, clear E records
	Compact   bool // R2085: compact LMDB via mdb_env_copy2 before opening
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
	// R736: Verbosity can be set via opts or pre-set via SetVerbosity
	if opts.Verbosity > 0 {
		SetVerbosity(opts.Verbosity)
	}
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

	// CRC: crc-Server.md | R2086, R2088, R2089, R2091
	// Compact before Open: socket lock is held, no clients connected,
	// no transactions in flight. Failure logs and continues.
	if opts.Compact {
		if err := CompactDB(dbPath); err != nil {
			log.Printf("compact: %v (continuing with uncompacted DB)", err)
		}
	}

	// Open database
	db, err := Open(dbPath)
	if err != nil {
		listener.Close()
		return fmt.Errorf("open database: %w", err)
	}

	// CRC: crc-Server.md | R1556, R1557, R1558, R1559, R1560
	// Config tracking: diff loaded config against stored I records
	changes, err := db.DiffConfig()
	if err != nil {
		log.Printf("config diff: %v", err)
	}
	if len(changes) > 0 {
		deferred := db.ApplyConfigChanges(changes)
		if len(deferred) > 0 {
			// Check for existing E records too
			eRecords, _ := db.store.ReadERecords()
			if !opts.Force {
				listener.Close()
				db.Close()
				msg := "config changes require --force or ark rebuild:\n"
				for _, c := range deferred {
					msg += fmt.Sprintf("  %s: %q → %q\n", c.Field, c.OldValue, c.NewValue)
				}
				for name := range eRecords {
					msg += fmt.Sprintf("  unresolved: %s\n", name)
				}
				return errors.New(msg)
			}
			// --force: accept all changes, clear E records
			log.Printf("--force: accepting deferred config changes")
			for _, c := range deferred {
				db.store.IPut(c.Field, c.NewValue)
			}
			db.store.ClearERecords()
		}
	} else {
		// No config changes — check for leftover E records
		eRecords, _ := db.store.ReadERecords()
		if len(eRecords) > 0 && !opts.Force {
			listener.Close()
			db.Close()
			msg := "unresolved error conditions (use --force or ark rebuild):\n"
			for name, payload := range eRecords {
				msg += fmt.Sprintf("  %s: %s\n", name, string(payload))
			}
			return errors.New(msg)
		}
		if opts.Force && len(eRecords) > 0 {
			db.store.ClearERecords()
		}
	}

	// Ensure ~/.ark is always a source (hardcoded, not in ark.toml)
	db.Config().EnsureArkSource()

	// R2659, R2663: warn if [recall].discussed_ttl can't be parsed —
	// we fall back to 24h either way, but the user should see typos.
	if _, badTTL := db.Config().Recall.DiscussedTTLDuration(); badTTL {
		log.Printf("warning: [recall].discussed_ttl=%q is not a valid duration; falling back to 24h",
			db.Config().Recall.DiscussedTTL)
	}

	// R799: Create pubsub and scheduler
	ps := NewPubSub(10*time.Minute, 100)
	// R2281: wire centralized tmp:// publish — DB's AddTmpFile /
	// UpdateTmpFile / AppendTmpFile / RemoveTmpFile call into pubsub
	// after the actor write commits. PubSub already has SetDB for
	// its watchdog-write path; the wiring is bidirectional.
	db.SetPubSub(ps)
	schedDir := filepath.Join(dbPath, "schedule")
	sched := NewEventScheduler(ps, nil, schedDir, db.Config()) // TODO: wire ErrorReporter when tmp:// append lands
	// R2780: auto-create ~/.ark/chimes.md so the indexer can pick up
	// chime tag declarations on the normal `[schedule].tags` path.
	// EnsureUpcoming now enqueues live (R2778), so the events fire in
	// this session without waiting for a restart. Source-file
	// duplication (e.g. literal `@chime-15m: every 15m` in code) is
	// prevented at config level via `[schedule].exclude_files`.
	if err := EnsureChimesFile(dbPath); err != nil {
		log.Printf("chimes: ensure file failed: %v", err)
	}

	// R1235, R1248: Create librarian for spectral search
	lib := NewLibrarian(db, dbPath)
	if lib.Available() {
		log.Printf("spectral search: claude available, librarian started")
	}

	srv := &Server{
		db:        db,
		listener:  listener,
		pidPath:   pidPath,
		noScan:    opts.NoScan,
		pubsub:    ps,
		scheduler: sched,
		librarian: lib,
		curation:  newCuration(dbPath), // R2355, R2381
	}
	srv.curation.Load() // R2383: hydrate pinned slice from curation.toml before luaTable is wired

	// Wire librarian into searcher for about filters
	db.search.librarian = lib

	// Wire pubsub into the indexer so tag extraction publishes events
	db.indexer.SetPubSub(ps)
	db.indexer.SetScheduler(sched, db.Config())
	ps.SetDB(db)

	// CRC: crc-Server.md | Seq: seq-recall-watcher.md#1, seq-recall-agent.md#1 | R2687, R2753, R2754
	// Construct the simple-recall v2 agent-builder and watcher.
	// The watcher's master switch lives in [recall].enabled; OnAppend
	// is a no-op when the switch is off, so we wire unconditionally
	// and let the watcher gate itself. The builder is wired
	// unconditionally too — it's a passive state machine until the
	// watcher or the CLI verbs touch it.
	srv.recallAgentBuilder = NewRecallAgentBuilder(db)
	srv.recallWatcher = NewRecallWatcher(db, lib, db.store, srv.recallAgentBuilder)
	srv.recallWatcher.Start()
	db.indexer.SetRecallWatcher(srv.recallWatcher)

	// R804: Start pubsub reaper ticker
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ps.Reap()
		}
	}()

	// Reconciliation goes through the DB actor via srv.reconcile() (R990)

	// Signal handling: catch SIGTERM, shut down UI engine, close socket, close DB, exit 0
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down", sig)
		if srv.scheduler != nil {
			srv.scheduler.Stop()
		}
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

	// R927-R932: Check for schedule config changes, re-materialize if needed
	srv.CheckScheduleConfig()

	// R874, R875, R876: Scan schedule logs and populate queue
	if err := sched.ScanScheduleLogs(); err != nil {
		log.Printf("schedule: scan error: %v", err)
	}
	// R972, R973: scan for unresolved check-gaps on startup
	if missed := sched.ScanCheckGaps(7); len(missed) > 0 && srv.db != nil {
		content := strings.Join(missed, "")
		SyncVoid(srv.db, func(db *DB) error {
			_, err := db.AppendTmpFile("tmp://watchdog/missed-events", "markdown", []byte(content))
			return err
		})
	}
	// R2783: AddChime() retired. Chime ticks now route through the
	// normal schedule-log path — see ~/.ark/chimes.md and
	// EnsureChimesFile (called above before reconcile).

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
	mux.HandleFunc("POST /files/status", srv.handleFilesStatus)
	mux.HandleFunc("GET /stale", srv.handleStale)
	mux.HandleFunc("GET /missing", srv.handleMissing)
	mux.HandleFunc("POST /dismiss", srv.handleDismiss)
	mux.HandleFunc("GET /config", srv.handleConfig)
	mux.HandleFunc("GET /unresolved", srv.handleUnresolved)
	mux.HandleFunc("POST /resolve", srv.handleResolve)
	mux.HandleFunc("GET /tags", srv.handleTags)
	mux.HandleFunc("POST /tags/counts", srv.handleTagCounts)
	mux.HandleFunc("POST /tags/files", srv.handleTagFiles)
	mux.HandleFunc("POST /tags/inspect", srv.handleTagInspect)
	mux.HandleFunc("POST /inbox", srv.handleInbox)
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
	mux.HandleFunc("POST /tmp/append", srv.handleTmpAppend)
	mux.HandleFunc("POST /subscribe", srv.handleSubscribe)
	mux.HandleFunc("GET /subscribers", srv.handleSubscribers)
	mux.HandleFunc("POST /monitor/control", srv.handleMonitorControl)
	mux.HandleFunc("POST /luhmann/record", srv.handleLuhmannRecord)
	mux.HandleFunc("GET /listen", srv.handleListen)
	mux.HandleFunc("POST /schedule/search", srv.handleScheduleSearch)
	mux.HandleFunc("POST /schedule/change", srv.handleScheduleChange)
	mux.HandleFunc("POST /search/grouped", srv.handleSearchGrouped)
	mux.HandleFunc("POST /tags/complete", srv.handleTagComplete)
	mux.HandleFunc("POST /tags/values", srv.handleTagValues)
	mux.HandleFunc("POST /save", srv.handleSave)
	mux.HandleFunc("POST /set-tags", srv.handleSetTags)
	mux.HandleFunc("POST /curation/pin", srv.handleCuratePin)
	mux.HandleFunc("POST /curation/dismiss", srv.handleCurateDismiss)
	mux.HandleFunc("GET /curation/pinned", srv.handleCuratePinned)
	// Discussed-tag store (per-session recall dedup). R2650-R2653
	mux.HandleFunc("POST /discussed/add", srv.handleDiscussedAdd)
	mux.HandleFunc("POST /discussed/list", srv.handleDiscussedList)
	mux.HandleFunc("POST /discussed/clear", srv.handleDiscussedClear)
	mux.HandleFunc("POST /discussed/prune", srv.handleDiscussedPrune)
	if srv.librarian != nil {
		mux.HandleFunc("POST /search/curate", srv.librarian.HandleExpand)
		mux.HandleFunc("GET /search/curate/wait", srv.librarian.HandleExpandWait)
		mux.HandleFunc("POST /search/curate/result", srv.librarian.HandleExpandResult)
		mux.HandleFunc("GET /search/curate/result/{id}", srv.librarian.HandleExpandGet)
		mux.HandleFunc("POST /search/expand/fuzzy", srv.librarian.HandleFuzzyMatch)
		mux.HandleFunc("POST /search/expand/search", srv.librarian.HandleExpandSearch)
		mux.HandleFunc("POST /search/expand/embed", srv.librarian.HandleEmbedMatch)
		mux.HandleFunc("POST /sweep/correlations", srv.librarian.HandleSweepCorrelations)
		// Find Connections (1G) — sidecar lotto-tube endpoints.
		// CRC: crc-Server.md | Seq: seq-find-connections.md | R2315, R2316, R2317, R2318
		mux.HandleFunc("GET /connections/wait", srv.librarian.HandleConnectionsWait)
		mux.HandleFunc("GET /connections/fetch", srv.librarian.HandleConnectionsFetch)
		mux.HandleFunc("POST /connections/result", srv.librarian.HandleConnectionsResult)
		mux.HandleFunc("POST /connections/error", srv.librarian.HandleConnectionsError)
		mux.HandleFunc("POST /connections/find", srv.librarian.HandleConnectionsFind)
		mux.HandleFunc("GET /connections/list", srv.librarian.HandleConnectionsList)
		// Connections cleanup (testing/reset). R2744
		mux.HandleFunc("POST /connections/clean", srv.handleConnectionsClean)
		// Recall v2 result-builder endpoints. R2755, R2756, R2757, R2758
		// CRC: crc-Server.md, crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md
		mux.HandleFunc("POST /connections/recall/reserve-nonce", srv.handleRecallReserveNonce)
		mux.HandleFunc("POST /connections/recall/surface", srv.handleRecallSurface)
		mux.HandleFunc("POST /connections/recall/recommend", srv.handleRecallRecommend)
		mux.HandleFunc("POST /connections/recall/close", srv.handleRecallClose)
		mux.HandleFunc("POST /connections/recall/context", srv.handleRecallContext)
		// Recall (Phase 2B) HTTP endpoint.
		// CRC: crc-Server.md | Seq: seq-recall.md#1.3 | R2629
		mux.HandleFunc("POST /recall", srv.librarian.HandleRecall)
	}

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
	// R737: Propagate ark verbosity to ui-engine
	rt.Cfg.Logging.Verbosity = verbosity
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
		// Inject theme blocks into all HTML files with frictionless markers
		if err := flib.InjectAllThemeBlocks(dbPath); err != nil {
			log.Printf("ui: theme injection: %v", err)
		}
		// Register Go functions on the Lua mcp table (passive path)
		srv.registerLuaFunctions()
		// Register content fetching routes on the UI HTTP server
		srv.registerContentRoutes()
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
	srv.registerContentRoutes()
	return nil
}

// indexPaths schedules per-path index updates through the DB actor.
// Called by the watcher with the set of paths that have changed in a
// throttle window. Fire-and-forget. (R991)
//
// CRC: crc-Server.md | Seq: seq-file-change.md#1.4 | R991
func (srv *Server) indexPaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	srv.db.Do(func(db *DB) {
		if err := db.IndexPathsAsync(paths); err != nil {
			log.Printf("watch: index paths: %v", err)
		}
	})
}

// reconcile sends a reconciliation cycle through the DB actor.
// Fire-and-forget — the watcher doesn't need the result. R987, R990, R992
func (srv *Server) reconcile() {
	srv.db.Do(func(db *DB) {
		// Wire schedule callback for async write goroutines
		db.onWriteComplete = func(items []scheduleItem) {
			go srv.processScheduleItems(items)
		}
		srv.doReconcile(db)
		// Drain any schedule items from synchronous config indexing
		pending := db.indexer.DrainSchedule()
		if len(pending) > 0 {
			go srv.processScheduleItems(pending)
		}
	})
}

// doReconcile runs the actual reconciliation: sources-check, sweep,
// scan, refresh. After sources-check, updates watches for any
// new/removed sources (R351). Sweep drops files that no longer
// classify as Included (R2138, R2142). Called inside the DB actor.
//
// CRC: crc-Server.md | Seq: seq-reconcile.md | R2138, R2142
func (srv *Server) doReconcile(db *DB) {
	if result, err := db.SourcesCheck(); err != nil {
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
	for _, src := range db.Config().Sources {
		sourceDirs = append(sourceDirs, src.Dir)
	}
	srv.setIndexing(sourceDirs)

	log.Println("reconcile: sweeping...")
	if err := db.SweepAsync(); err != nil {
		log.Printf("reconcile: sweep error: %v", err)
	}
	log.Println("reconcile: scanning...")
	if _, err := db.ScanAsync(); err != nil {
		log.Printf("reconcile: scan error: %v", err)
	}
	log.Println("reconcile: refreshing...")
	if err := db.RefreshAsync(); err != nil {
		log.Printf("reconcile: refresh error: %v", err)
	}

	// Clear indexing state after all queued writes complete. R1058
	db.enqueueWrite(func(_ *microfts2.DB) {
		svc(db.svc, func() {
			srv.setIndexing(nil)
			log.Println("reconcile: complete")
		})
	})
	// Batch embed missing embeddings after reconcile. R1292, R1295, R1609
	if srv.librarian != nil && srv.librarian.EmbeddingAvailable() {
		db.enqueueWrite(func(_ *microfts2.DB) {
			if err := srv.librarian.BatchEmbed(); err != nil {
				log.Printf("reconcile: batch embed tags: %v", err)
			}
			if err := srv.librarian.BatchEmbedChunks(); err != nil {
				log.Printf("reconcile: batch embed chunks: %v", err)
			}
		})
	}
	log.Println("reconcile: queued")
}

// processScheduleItems runs EnsureUpcoming for accumulated schedule items
// outside the DB actor so file I/O doesn't block indexing.
func (srv *Server) processScheduleItems(items []scheduleItem) {
	if len(items) == 0 || srv.scheduler == nil {
		return
	}
	// Wire tmp:// log writer so EnsureUpcoming can write ephemeral schedule logs
	srv.scheduler.WriteTmpLog = func(path string, content []byte) error {
		return SyncVoid(srv.db, func(db *DB) error {
			err := db.UpdateTmpFile(path, "markdown", content)
			if err != nil {
				_, err = db.AddTmpFile(path, "markdown", content)
			}
			return err
		})
	}
	for _, item := range items {
		if err := srv.scheduler.EnsureUpcoming(item.tag, item.value, item.path); err != nil {
			log.Printf("schedule: EnsureUpcoming error for @%s in %s: %v", item.tag, item.path, err)
		}
	}
	srv.scheduler.WriteTmpLog = nil // clean up
	// Scan picks up new schedule log files for indexing.
	SyncVoid(srv.db, func(db *DB) error {
		if _, err := db.Scan(); err != nil {
			log.Printf("schedule: post-scan error: %v", err)
		}
		return nil
	})
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
	Query           string           `json:"query"`
	About           string           `json:"about"`
	Contains        string           `json:"contains"`
	Regex           []string         `json:"regex"`
	ExceptRegex     []string         `json:"exceptRegex"`
	LikeFile        string           `json:"likeFile"`
	K               int              `json:"k"`
	Scores          bool             `json:"scores"`
	After           string           `json:"after"`
	Before          string           `json:"before"`
	Chunks          bool             `json:"chunks"`
	Files           bool             `json:"files"`
	Tags            bool             `json:"tags"`
	Filter          []string         `json:"filter"`
	Except          []string         `json:"except"`
	FilterFiles     []string         `json:"filterFiles"`
	ExcludeFiles    []string         `json:"excludeFiles"`
	FilterFileTags  []string         `json:"filterFileTags"`
	ExcludeFileTags []string         `json:"excludeFileTags"`
	Session         string           `json:"session,omitempty"`      // R657: optional session name
	Fuzzy           bool             `json:"fuzzy,omitempty"`        // R748: typo-tolerant search
	NoTmp           bool             `json:"noTmp,omitempty"`        // R687: exclude tmp:// documents
	OnlyIfTmp       bool             `json:"onlyIfTmp,omitempty"`    // R686: return 204 if no tmp files
	ChunkFilters    []ChunkFilterRow `json:"chunkFilters,omitempty"` // CRC: crc-Server.md | R1783, R1784
	// R2442, R2453: primary tag predicate in sigil form. Set when
	// `-tag <sigil>` (or `-file-tag <sigil>`) is the only primary
	// driver; the server resolves chunkIDs via the shared TagMatcher
	// and bypasses FTS entirely. Empty string means no primary tag.
	// The accompanying boolean PrimaryFileTag switches the resolver
	// to a file-scoped predicate (every chunk on a matching file).
	PrimaryTagQuery string `json:"primaryTagQuery,omitempty"`
	PrimaryFileTag  bool   `json:"primaryFileTag,omitempty"`
}

// tmpRequest is the body for tmp:// add/update/remove endpoints.
type tmpRequest struct {
	Path     string `json:"path"`
	Strategy string `json:"strategy,omitempty"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"` // "base64" for binary content
}

// contentBytes returns the decoded content. If Encoding is "base64",
// the content is base64-decoded; otherwise returned as raw bytes.
func (r *tmpRequest) contentBytes() ([]byte, error) {
	if r.Encoding == "base64" {
		return base64.StdEncoding.DecodeString(r.Content)
	}
	return []byte(r.Content), nil
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
		Fuzzy:           req.Fuzzy,
		NoTmp:           req.NoTmp,
		ChunkFilters:    req.ChunkFilters,
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

// CRC: crc-Server.md | R986, R988
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

	opts := buildSearchOpts(req)

	// R2442, R2453: primary tag/file-tag predicate. V/F records pin
	// chunkIDs (and fileIDs for file-tag mode) — bypass FTS entirely
	// when no other text primary is set. Stale IDs are skipped.
	if req.PrimaryTagQuery != "" && req.About == "" && req.Contains == "" && len(req.Regex) == 0 && req.LikeFile == "" && !req.Fuzzy {
		chunkIDs := srv.resolvePrimaryTagChunks(req.PrimaryTagQuery, req.PrimaryFileTag)
		results, err := Sync(srv.db, func(db *DB) ([]SearchResultEntry, error) {
			entries, terr := db.SearchTagChunks(chunkIDs, opts)
			if terr != nil {
				return nil, terr
			}
			if req.Tags || req.Chunks {
				return db.FillChunks(entries)
			}
			if req.Files {
				return db.FillFiles(entries)
			}
			return entries, nil
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

	// R657, R658, R659: session-scoped search
	if req.Session != "" {
		sess := srv.GetOrCreateSession(req.Session)
		var results []SearchResultEntry
		err := sess.RunSearch(req.Query, func(cache *microfts2.ChunkCache) error {
			return SyncVoid(srv.db, func(db *DB) error {
				// R1784, R1787, R1935: combined about coordination.
				if len(opts.ChunkFilters) > 0 || opts.About != "" {
					k := opts.K
					if k == 0 {
						k = 20
					}
					ar, err := ResolveAboutFilters(opts.ChunkFilters, opts.About, k*2, srv.librarian, db.Store(), db.Config())
					if err != nil {
						return err
					}
					if ar.HasAboutResults {
						opts.SetAboutResults(ar.AboutResults)
					}
					opts.SetAboutFilterSets(ar.AboutFilterSets)
					opts.extraOpts = append(opts.extraOpts, ar.Early...)
					opts.extraOpts = append(opts.extraOpts, ar.ChunkFilterOpts...)
					if paths, pathErr := db.FTS().FileIDPaths(); pathErr == nil {
						opts.extraOpts = append(opts.extraOpts, BuildChunkFilters(ar.Remaining, cache, paths, db.Store())...)
					}
					opts.extraOpts = append(opts.extraOpts, ar.Late...)
				}
				var searchErr error
				if req.Fuzzy {
					results, searchErr = db.SearchFuzzy(req.Query, opts)
				} else if req.About != "" || req.Contains != "" || len(req.Regex) > 0 || req.LikeFile != "" {
					results, searchErr = db.SearchSplit(opts)
				} else {
					results, searchErr = db.SearchCombined(req.Query, opts)
				}
				if searchErr != nil {
					return searchErr
				}
				if req.Tags || req.Chunks {
					results, searchErr = db.FillChunksUsing(results, cache)
				} else if req.Files {
					results, searchErr = db.FillFiles(results)
				}
				return searchErr
			})
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

	// No session — direct through DB actor
	results, err := Sync(srv.db, func(db *DB) ([]SearchResultEntry, error) {
		// R686: onlyIfTmp — return 204 if no tmp files exist
		if req.OnlyIfTmp && !db.HasTmp() {
			return nil, nil // sentinel: caller checks
		}

		done := db.NewSearchCache()
		defer done()

		// R1784, R1787, R1935: combined about coordination.
		if len(opts.ChunkFilters) > 0 || opts.About != "" {
			k := opts.K
			if k == 0 {
				k = 20
			}
			ar, err := ResolveAboutFilters(opts.ChunkFilters, opts.About, k*2, srv.librarian, db.Store(), db.Config())
			if err != nil {
				return nil, err
			}
			if ar.HasAboutResults {
				opts.SetAboutResults(ar.AboutResults)
			}
			opts.SetAboutFilterSets(ar.AboutFilterSets)
			opts.extraOpts = append(opts.extraOpts, ar.Early...)
			opts.extraOpts = append(opts.extraOpts, ar.ChunkFilterOpts...)
			if paths, pathErr := db.FTS().FileIDPaths(); pathErr == nil {
				cache := db.FTS().NewChunkCache()
				opts.extraOpts = append(opts.extraOpts, BuildChunkFilters(ar.Remaining, cache, paths, db.Store())...)
			}
			opts.extraOpts = append(opts.extraOpts, ar.Late...)
		}

		var results []SearchResultEntry
		var err error
		if req.Fuzzy {
			results, err = db.SearchFuzzy(req.Query, opts)
		} else if req.About != "" || req.Contains != "" || len(req.Regex) > 0 || req.LikeFile != "" {
			results, err = db.SearchSplit(opts)
		} else {
			results, err = db.SearchCombined(req.Query, opts)
		}
		if err != nil {
			return nil, err
		}

		if req.Tags || req.Chunks {
			results, err = db.FillChunks(results)
		} else if req.Files {
			results, err = db.FillFiles(results)
		}
		return results, err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// onlyIfTmp sentinel
	if req.OnlyIfTmp && results == nil {
		w.WriteHeader(http.StatusNoContent)
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
	if err := SyncVoid(srv.db, func(db *DB) error {
		return db.Add(req.Paths, req.Strategy)
	}); err != nil {
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
	if err := SyncVoid(srv.db, func(db *DB) error {
		return db.Remove(req.Patterns)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (srv *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	type scanResult struct {
		results *ScanResults
		pending []scheduleItem
	}
	sr, err := Sync(srv.db, func(db *DB) (scanResult, error) {
		results, err := db.Scan()
		pending := db.indexer.DrainSchedule()
		return scanResult{results, pending}, err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(sr.pending) > 0 {
		go srv.processScheduleItems(sr.pending)
	}
	writeJSON(w, map[string]any{
		"newFiles":      len(sr.results.NewFiles),
		"newUnresolved": len(sr.results.NewUnresolved),
	})
}

func (srv *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var pending []scheduleItem
	if err := SyncVoid(srv.db, func(db *DB) error {
		err := db.Refresh(req.Patterns)
		pending = db.indexer.DrainSchedule()
		return err
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(pending) > 0 {
		go srv.processScheduleItems(pending)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// CRC: crc-Server.md | R2477, R2480
func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	wantDB := r.URL.Query().Get("db") == "true"

	type statusResult struct {
		status   *StatusInfo
		dbCounts *DBRecordCounts
	}
	result, err := Sync(srv.db, func(db *DB) (statusResult, error) {
		status, err := db.Status()
		if err != nil {
			return statusResult{}, err
		}
		var dbCounts *DBRecordCounts
		if wantDB {
			dbCounts, err = db.StatusDB()
			if err != nil {
				return statusResult{}, err
			}
		}
		return statusResult{status, dbCounts}, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// R437-R441: Enrich with UI fields (not DB state — safe outside actor)
	if srv.uiRuntime != nil {
		result.status.UIRunning = true
		result.status.UIPort = srv.uiPort
	}
	result.status.UIIndexing = len(srv.currentlyIndexing()) > 0
	result.status.Spectral = srv.librarian.Available()

	if result.dbCounts != nil {
		writeJSON(w, struct {
			*StatusInfo
			DB *DBRecordCounts `json:"db"`
		}{result.status, result.dbCounts})
		return
	}
	writeJSON(w, result.status)
}

func (srv *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	files, err := Sync(srv.db, func(db *DB) ([]string, error) {
		return db.Files()
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, files)
}

// fileStatusEntry is the JSON response for /files/status.
type fileStatusEntry struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // G=good, S=stale, M=missing, T=tmp
	Bytes      int64  `json:"bytes"`
	ChunkCount int    `json:"chunk_count"`
	ChunkSizes []int  `json:"chunk_sizes,omitempty"`
}

// chunkEntry is the JSON response for /files/chunks.
type chunkEntry struct {
	Path     string `json:"path"`
	Location string `json:"location"`
	Size     int    `json:"size"`
}

func (srv *Server) handleFilesStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Patterns []string `json:"patterns"`
		Chunks   bool     `json:"chunks"` // if true, return per-chunk detail instead
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Chunks {
		srv.handleFilesChunks(w, req.Patterns)
		return
	}

	type result struct {
		entries []fileStatusEntry
		err     error
	}
	res, err := Sync(srv.db, func(db *DB) ([]fileStatusEntry, error) {
		files, err := db.Files()
		if err != nil {
			return nil, err
		}
		staleList, _ := db.Stale()
		missingList, _ := db.Missing()
		staleSet := make(map[string]bool, len(staleList))
		for _, s := range staleList {
			staleSet[s] = true
		}
		missingSet := make(map[string]bool, len(missingList))
		for _, m := range missingList {
			missingSet[m.Path] = true
		}

		// Build tmp lookup
		tmpInfos := make(map[string]microfts2.TmpFileInfo)
		for _, ti := range db.fts.TmpFileInfos() {
			tmpInfos[ti.Path] = ti
		}

		var entries []fileStatusEntry
		for _, f := range files {
			if len(req.Patterns) > 0 && !matchAny(f, req.Patterns) {
				continue
			}
			if ti, ok := tmpInfos[f]; ok {
				entries = append(entries, fileStatusEntry{
					Path:       ti.Path,
					Status:     "T",
					Bytes:      int64(ti.ContentLen),
					ChunkCount: ti.ChunkCount,
					ChunkSizes: ti.ChunkSizes,
				})
				continue
			}
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
			sizes := db.ChunkSizes(f)
			entries = append(entries, fileStatusEntry{
				Path:       f,
				Status:     status,
				Bytes:      fileBytes,
				ChunkCount: len(sizes),
				ChunkSizes: sizes,
			})
		}
		return entries, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

func (srv *Server) handleFilesChunks(w http.ResponseWriter, patterns []string) {
	res, err := Sync(srv.db, func(db *DB) ([]chunkEntry, error) {
		files, err := db.Files()
		if err != nil {
			return nil, err
		}
		// Build tmp lookup for fast path
		tmpInfos := make(map[string]microfts2.TmpFileInfo)
		for _, ti := range db.fts.TmpFileInfos() {
			tmpInfos[ti.Path] = ti
		}

		var entries []chunkEntry
		for _, f := range files {
			if len(patterns) > 0 && !matchAny(f, patterns) {
				continue
			}
			// tmp files: use overlay info directly
			if ti, ok := tmpInfos[f]; ok {
				for _, ci := range ti.Chunks {
					entries = append(entries, chunkEntry{Path: f, Location: ci.Location, Size: ci.Size})
				}
				continue
			}
			// persistent files: use FTS index
			info, err := db.fts.CheckFile(f)
			if err != nil || info.FileID == 0 {
				continue
			}
			finfo, err := db.fts.FileInfoByID(info.FileID)
			if err != nil {
				continue
			}
			lens, err := db.fts.ChunkContentLens(info.FileID)
			if err != nil {
				continue
			}
			for i, fce := range finfo.Chunks {
				sz := 0
				if i < len(lens) {
					sz = lens[i]
				}
				entries = append(entries, chunkEntry{Path: f, Location: fce.Location, Size: sz})
			}
		}
		return entries, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

// matchAny checks if path matches any of the patterns (glob or substring).
func matchAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

func (srv *Server) handleStale(w http.ResponseWriter, r *http.Request) {
	stale, err := Sync(srv.db, func(db *DB) ([]string, error) {
		return db.Stale()
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, stale)
}

func (srv *Server) handleMissing(w http.ResponseWriter, r *http.Request) {
	missing, err := Sync(srv.db, func(db *DB) ([]MissingRecord, error) {
		return db.Missing()
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, missing)
}

// Discussed-tag store handlers (per-session recall dedup).
// CRC: crc-Server.md | Seq: seq-discussed.md | R2650-R2653, R2659

type discussedAddRequest struct {
	Session string      `json:"session"`
	Tags    []Discussed `json:"tags"`
}

func (srv *Server) handleDiscussedAdd(w http.ResponseWriter, r *http.Request) {
	var req discussedAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Session == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}
	if len(req.Tags) == 0 {
		http.Error(w, "no tags specified", http.StatusBadRequest)
		return
	}
	if err := SyncVoid(srv.db, func(db *DB) error {
		for _, t := range req.Tags {
			if err := db.AddDiscussed(req.Session, t.Tag, t.Value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "count": len(req.Tags)})
}

type discussedListRequest struct {
	Session string `json:"session"`
	Since   string `json:"since,omitempty"` // duration string
}

func (srv *Server) handleDiscussedList(w http.ResponseWriter, r *http.Request) {
	var req discussedListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Session == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}
	var since time.Duration
	if req.Since != "" {
		d, err := time.ParseDuration(req.Since)
		if err != nil {
			http.Error(w, "invalid since: "+err.Error(), http.StatusBadRequest)
			return
		}
		since = d
	}
	entries, err := Sync(srv.db, func(db *DB) ([]Discussed, error) {
		return db.ListDiscussed(req.Session, since)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []Discussed{}
	}
	writeJSON(w, entries)
}

type discussedClearRequest struct {
	Session string `json:"session"`
}

func (srv *Server) handleDiscussedClear(w http.ResponseWriter, r *http.Request) {
	var req discussedClearRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Session == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}
	count, err := Sync(srv.db, func(db *DB) (int, error) {
		return db.ClearDiscussed(req.Session)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "count": count})
}

type discussedPruneRequest struct {
	TTL string `json:"ttl,omitempty"`
}

func (srv *Server) handleDiscussedPrune(w http.ResponseWriter, r *http.Request) {
	var req discussedPruneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ttl time.Duration
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil {
			http.Error(w, "invalid ttl: "+err.Error(), http.StatusBadRequest)
			return
		}
		ttl = d
	}
	count, err := Sync(srv.db, func(db *DB) (int, error) {
		return db.PruneDiscussed(ttl)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "count": count})
}

// connectionsCleanRequest carries the parameters for POST
// /connections/clean. Sessions limits RD wipes to a specific list
// (empty = wipe across every session). All toggles whether the
// optional record classes (RF, RJ) and tmp:// artifacts are wiped
// alongside the defaults (RC, RD). Checkpoint toggles advancing
// the indexer's FileLength on session JSONLs scoped by Sessions.
// CRC: crc-Server.md | R2744, R2745
type connectionsCleanRequest struct {
	Sessions   []string `json:"sessions,omitempty"`
	All        bool     `json:"all,omitempty"`
	Checkpoint bool     `json:"checkpoint,omitempty"`
}

// connectionsCleanResponse reports per-class deletion counts. tmp://
// artifact counts are reported under tmpConnections / tmpRecall when
// `all` was set. CheckpointFiles reports how many session JSONLs
// had their FileLength advanced when `checkpoint` was set.
// CRC: crc-Server.md | R2744, R2745
type connectionsCleanResponse struct {
	Status          string `json:"status"`
	RC              int    `json:"rc"`
	RD              int    `json:"rd"`
	RF              int    `json:"rf,omitempty"`
	RJ              int    `json:"rj,omitempty"`
	TmpConnections  int    `json:"tmpConnections,omitempty"`
	TmpRecall       int    `json:"tmpRecall,omitempty"`
	CheckpointFiles int    `json:"checkpointFiles,omitempty"`
}

// handleConnectionsClean wipes recall-substrate accumulated state to
// help with testing. Default scope is RC across the corpus and RD
// across the specified sessions (or all sessions if Sessions is
// empty). With All=true, also wipes RF, RJ, tmp://connections/* and
// tmp://ARK-RECALL/* documents.
// CRC: crc-Server.md | R2744
func (srv *Server) handleConnectionsClean(w http.ResponseWriter, r *http.Request) {
	var req connectionsCleanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resp connectionsCleanResponse
	resp.Status = "ok"

	// RC — always
	rc, err := srv.db.ClearAllDerivedProposals()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.RC = rc

	// RD — scoped by Sessions list or wiped globally
	if len(req.Sessions) == 0 {
		c, err := srv.db.ClearAllDiscussed()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.RD = c
	} else {
		for _, sess := range req.Sessions {
			c, err := srv.db.ClearDiscussed(sess)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			resp.RD += c
		}
	}

	if req.All {
		rf, err := srv.db.ClearAllDerivedFreshness()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.RF = rf

		rj, err := srv.db.ClearAllDerivedRejections()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.RJ = rj

		for _, path := range srv.db.TmpFiles() {
			switch {
			case strings.HasPrefix(path, "tmp://connections/"):
				if err := srv.db.RemoveTmpFile(path); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				resp.TmpConnections++
			case strings.HasPrefix(path, "tmp://ARK-RECALL/"):
				if err := srv.db.RemoveTmpFile(path); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				resp.TmpRecall++
			}
		}
	}
	if req.Checkpoint {
		paths, err := srv.db.SessionJSONLs(req.Sessions)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, p := range paths {
			if _, err := srv.db.CheckpointFile(p); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			resp.CheckpointFiles++
		}
		if srv.recallWatcher != nil {
			srv.recallWatcher.ClearPending(req.Sessions)
		}
	}
	writeJSON(w, resp)
}

func (srv *Server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	var req dismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := SyncVoid(srv.db, func(db *DB) error {
		return db.Dismiss(req.Patterns)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (srv *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg, _ := Sync(srv.db, func(db *DB) (*Config, error) {
		return db.Config(), nil
	})
	writeJSON(w, cfg)
}

func (srv *Server) handleUnresolved(w http.ResponseWriter, r *http.Request) {
	unresolved, err := Sync(srv.db, func(db *DB) ([]UnresolvedRecord, error) {
		return db.Unresolved()
	})
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
	if err := SyncVoid(srv.db, func(db *DB) error {
		return db.Resolve(req.Patterns)
	}); err != nil {
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
	tags, err := Sync(srv.db, func(db *DB) ([]TagCount, error) {
		return db.TagList()
	})
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
	counts, err := Sync(srv.db, func(db *DB) ([]TagCount, error) {
		return db.TagCounts(req.Tags)
	})
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
		entries, err := Sync(srv.db, func(db *DB) ([]TagContextEntry, error) {
			return db.TagContext(req.Tags)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, entries)
		return
	}

	files, err := Sync(srv.db, func(db *DB) ([]TagFileInfo, error) {
		return db.TagFiles(req.Tags)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, files)
}

// handleTagInspect dumps disk + in-memory @ext state. Read-only.
// CRC: crc-Server.md | R2117
func (srv *Server) handleTagInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope  string `json:"scope"`
		Target string `json:"target,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Scope == "" {
		req.Scope = ScopeExt
	}
	rep, err := Sync(srv.db, func(db *DB) (*ExtInspectReport, error) {
		return db.InspectExt(InspectOptions{Scope: req.Scope, Target: req.Target})
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, rep)
}

// handleInbox returns inbox entries from the running server. Lets the
// CLI's `ark message inbox` see tmp:// messages that only live in
// server memory.
// CRC: crc-Server.md | R1952
func (srv *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowAll         bool `json:"showAll,omitempty"`
		IncludeArchived bool `json:"includeArchived,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entries, err := Sync(srv.db, func(db *DB) ([]InboxEntry, error) {
		return db.Inbox(req.ShowAll, req.IncludeArchived)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, entries)
}

func (srv *Server) handleTagDefs(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defs, err := Sync(srv.db, func(db *DB) ([]TagDefInfo, error) {
		return db.TagDefs(req.Tags)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, defs)
}

// curatePinRequest is the JSON payload for POST /curation/pin.
// CRC: crc-Server.md | R2363
type curatePinRequest struct {
	ChunkID uint64 `json:"chunkID"`
	FileID  uint64 `json:"fileID"`
	Path    string `json:"path"`
}

// handleCuratePin pins a chunk from a web-component context that
// can't reach Lua directly (chunk-row buttons in <ark-search>,
// content-view iframes). Enters the Lua executor so the Go mutation
// and Lua mirror refresh share a single tick.
//
// CRC: crc-Server.md | R2363
func (srv *Server) handleCuratePin(w http.ResponseWriter, r *http.Request) {
	var req curatePinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ChunkID == 0 {
		http.Error(w, "chunkID required", http.StatusBadRequest)
		return
	}
	if srv.uiRuntime == nil {
		http.Error(w, "ui runtime not available", http.StatusServiceUnavailable)
		return
	}
	err := srv.uiRuntime.WithLua(func(rt *cli.LuaRuntime) error {
		srv.curation.pin(rt.State, req.ChunkID, req.FileID, req.Path)
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// curateDismissRequest is the JSON payload for POST /curation/dismiss.
type curateDismissRequest struct {
	ChunkID uint64 `json:"chunkID"`
}

// handleCurateDismiss removes a pinned chunk from a web-component
// context (the toggle-off path of the content-view pin button).
// Mirror of handleCuratePin; silent no-op when the chunkID is not
// currently pinned.
//
// CRC: crc-Server.md | R2411, R2412
func (srv *Server) handleCurateDismiss(w http.ResponseWriter, r *http.Request) {
	var req curateDismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ChunkID == 0 {
		http.Error(w, "chunkID required", http.StatusBadRequest)
		return
	}
	if srv.uiRuntime == nil {
		http.Error(w, "ui runtime not available", http.StatusServiceUnavailable)
		return
	}
	err := srv.uiRuntime.WithLua(func(rt *cli.LuaRuntime) error {
		srv.curation.dismiss(rt.State, req.ChunkID)
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleCuratePinned returns the current pinned chunk IDs as JSON
// for web-component consumers (the inline pin-button script reads
// this on DOMContentLoaded to seed visual state). Read-only — no
// Lua-executor entry; reads through Curation.pinnedSnapshot().
//
// CRC: crc-Server.md | R2413, R2414
func (srv *Server) handleCuratePinned(w http.ResponseWriter, r *http.Request) {
	if srv.curation == nil {
		http.Error(w, "curation not available", http.StatusServiceUnavailable)
		return
	}
	snap := srv.curation.pinnedSnapshot()
	ids := make([]uint64, len(snap))
	for i, p := range snap {
		ids[i] = p.ChunkID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"chunkIDs": ids})
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

// configMutate decodes a request, applies a config mutation inside the
// DB actor, saves, and triggers reconciliation.
func (srv *Server) configMutate(w http.ResponseWriter, r *http.Request, v any, fn func(*DB) error) {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := SyncVoid(srv.db, func(db *DB) error {
		if err := fn(db); err != nil {
			return err
		}
		return db.SaveConfig()
	}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	srv.reconcile()
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) handleConfigAddSource(w http.ResponseWriter, r *http.Request) {
	var req configSourceRequest
	srv.configMutate(w, r, &req, func(db *DB) error { return db.Config().AddSource(req.Dir) })
}

func (srv *Server) handleConfigRemoveSource(w http.ResponseWriter, r *http.Request) {
	var req configSourceRequest
	srv.configMutate(w, r, &req, func(db *DB) error { return db.Config().RemoveSource(req.Dir) })
}

func (srv *Server) handleConfigAddInclude(w http.ResponseWriter, r *http.Request) {
	var req configPatternRequest
	srv.configMutate(w, r, &req, func(db *DB) error { return db.Config().AddInclude(req.Pattern, req.Source) })
}

func (srv *Server) handleConfigAddExclude(w http.ResponseWriter, r *http.Request) {
	var req configPatternRequest
	srv.configMutate(w, r, &req, func(db *DB) error { return db.Config().AddExclude(req.Pattern, req.Source) })
}

func (srv *Server) handleConfigRemovePattern(w http.ResponseWriter, r *http.Request) {
	var req configPatternRequest
	srv.configMutate(w, r, &req, func(db *DB) error { return db.Config().RemovePattern(req.Pattern, req.Source) })
}

type configStrategyRequest struct {
	Pattern  string `json:"pattern"`
	Strategy string `json:"strategy"`
}

func (srv *Server) handleConfigAddStrategy(w http.ResponseWriter, r *http.Request) {
	var req configStrategyRequest
	srv.configMutate(w, r, &req, func(db *DB) error { return db.Config().AddStrategy(req.Pattern, req.Strategy) })
}

func (srv *Server) handleConfigShowWhy(w http.ResponseWriter, r *http.Request) {
	var req configWhyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := Sync(srv.db, func(db *DB) (*WhyResult, error) {
		return db.Config().ShowWhy(req.Path)
	})
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
	data, err := Sync(srv.db, func(db *DB) ([]byte, error) {
		return db.Fetch(req.Path)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"content": string(data)})
}

func (srv *Server) handleSourcesCheck(w http.ResponseWriter, r *http.Request) {
	result, err := Sync(srv.db, func(db *DB) (*SourcesCheckResult, error) {
		return db.SourcesCheck()
	})
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
	content, err := req.contentBytes()
	if err != nil {
		http.Error(w, "invalid base64 content: "+err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("tmp add: path=%s encoding=%q content_len=%d raw_len=%d", req.Path, req.Encoding, len(content), len(req.Content))
	fid, err := Sync(srv.db, func(db *DB) (uint64, error) {
		return db.AddTmpFile(req.Path, strategy, content)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	// CRC: crc-Server.md | R2282 — pubsub publish is now centralized
	// in db.AddTmpFile via PubSub.PublishTmpDiff; the manual
	// PublishAndWatch call that used to live here is removed.
	// Schedule processing still needs the extracted tags.
	tagValues := ExtractTagValues(content, strategy)
	if srv.scheduler != nil {
		var pending []scheduleItem
		cfg := srv.db.Config()
		for _, tv := range tagValues {
			if _, ok := cfg.IsScheduleTag(tv.Tag); ok && tv.Value != "" {
				pending = append(pending, scheduleItem{tag: tv.Tag, value: tv.Value, path: req.Path})
			}
		}
		if len(pending) > 0 {
			go srv.processScheduleItems(pending)
		}
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
	content, err := req.contentBytes()
	if err != nil {
		http.Error(w, "invalid base64 content: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := SyncVoid(srv.db, func(db *DB) error {
		return db.UpdateTmpFile(req.Path, strategy, content)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// R2282: publish is now centralized in db.UpdateTmpFile.
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
	if err := SyncVoid(srv.db, func(db *DB) error {
		return db.RemoveTmpFile(req.Path)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleTmpList lists all tmp:// paths.
// CRC: crc-Server.md | R685
func (srv *Server) handleTmpList(w http.ResponseWriter, r *http.Request) {
	paths, _ := Sync(srv.db, func(db *DB) ([]string, error) {
		return db.TmpFiles(), nil
	})
	writeJSON(w, paths)
}

// handleTmpAppend appends content to a tmp:// document (creating it if needed).
// CRC: crc-Server.md | Seq: seq-pubsub.md
func (srv *Server) handleTmpAppend(w http.ResponseWriter, r *http.Request) {
	var req tmpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" || req.Content == "" {
		http.Error(w, "path and content required", http.StatusBadRequest)
		return
	}
	if req.Strategy == "" {
		req.Strategy = "markdown"
	}
	content, err := req.contentBytes()
	if err != nil {
		http.Error(w, "invalid base64 content: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := SyncVoid(srv.db, func(db *DB) error {
		_, err := db.AppendTmpFile(req.Path, req.Strategy, content)
		return err
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// R2282: publish is now centralized in db.AppendTmpFile.
	w.WriteHeader(http.StatusOK)
}

// handleSubscribe processes subscribe/cancel/list/stats requests. R778-R788, R814-R820
// CRC: crc-Server.md | Seq: seq-pubsub.md | R2782
func (srv *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
		Cancel  bool   `json:"cancel"`
		List    bool   `json:"list"`
		Stats   bool   `json:"stats"`
		Subs []struct {
			// R2442, R2457, R2460: sigil-form tag query. The leading
			// sigil and internal separator carry name/value match
			// modes. `Kind` selects "tag" (chunk-precise) or
			// "file-tag" (file-scoped). Default is "tag".
			Tag          string   `json:"tag"`
			Kind         string   `json:"kind"`
			FilterFiles  []string `json:"filter_files"`
			ExcludeFiles []string `json:"exclude_files"`
		} `json:"subs"`
		// For cancel: the sigil-form predicate to drop. Name-only
		// cancels all subs whose name matches; name+value cancels
		// subs whose stored predicate accepts (name, value).
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.List {
		writeJSON(w, srv.pubsub.List(req.Session))
		return
	}
	if req.Stats {
		writeJSON(w, srv.pubsub.Stats(req.Session))
		return
	}
	if req.Cancel {
		// R2458: the cancel target is a sigil-form predicate; we
		// forward the name and value strings to PubSub.Cancel which
		// drops every entry whose stored Predicate accepts them.
		name, value := "", ""
		if req.Tag != "" {
			p, err := ParseMatchSyntax(req.Tag)
			if err != nil {
				http.Error(w, fmt.Sprintf("bad cancel tag %q: %v", req.Tag, err), http.StatusBadRequest)
				return
			}
			name = strings.ToLower(p.NameStr)
			if p.ValueMode != ValueAny {
				value = p.ValueStr
			}
		}
		srv.pubsub.Cancel(req.Session, name, value)
		w.WriteHeader(http.StatusOK)
		return
	}
	// Subscribe
	var subs []*TagSub
	for _, s := range req.Subs {
		// R941, R942: inherit search_exclude when no explicit file filters
		excludeFiles := s.ExcludeFiles
		if len(s.FilterFiles) == 0 && len(s.ExcludeFiles) == 0 {
			defaultExcl, _ := Sync(srv.db, func(db *DB) ([]string, error) {
				return db.Config().SearchExclude, nil
			})
			if len(defaultExcl) > 0 {
				excludeFiles = defaultExcl
			}
		}
		p, err := ParseMatchSyntax(s.Tag)
		if err != nil {
			http.Error(w, fmt.Sprintf("bad tag %q: %v", s.Tag, err), http.StatusBadRequest)
			return
		}
		kind := TagSubChunk
		if s.Kind == "file-tag" {
			kind = TagSubFile
		}
		sub := &TagSub{
			Kind:         kind,
			Predicate:    p,
			FilterFiles:  s.FilterFiles,
			ExcludeFiles: excludeFiles,
		}
		subs = append(subs, sub)
	}
	srv.pubsub.Subscribe(req.Session, subs)
	w.WriteHeader(http.StatusOK)
}

// handleSubscribers returns the count of subscriptions whose predicate
// would accept a synthesized (tag, value) pair. Read-only.
// CRC: crc-Server.md | Seq: seq-subscriber-presence.md | R2805
func (srv *Server) handleSubscribers(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		http.Error(w, "tag required", http.StatusBadRequest)
		return
	}
	p, err := ParseMatchSyntax(tag)
	if err != nil {
		http.Error(w, fmt.Sprintf("bad tag %q: %v", tag, err), http.StatusBadRequest)
		return
	}
	name := strings.ToLower(p.NameStr)
	value := ""
	if p.ValueMode != ValueAny {
		value = p.ValueStr
	}
	count := srv.pubsub.SubscriberCount(name, value)
	writeJSON(w, map[string]int{"count": count})
}

// handleMonitorControl appends one pause/resume record to
// `~/.ark/monitoring/<class>.jsonl` via the write actor. Enforces the
// state-already-set guard before the append.
// CRC: crc-Server.md | Seq: seq-luhmann-supervisor.md | R2787, R2788
func (srv *Server) handleMonitorControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Class string `json:"class"`
		Kind  string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !IsKnownMonitorClass(req.Class) {
		http.Error(w, fmt.Sprintf("unknown monitor class %q", req.Class), http.StatusBadRequest)
		return
	}
	if req.Kind != "pause" && req.Kind != "resume" {
		http.Error(w, fmt.Sprintf("kind must be pause or resume, got %q", req.Kind), http.StatusBadRequest)
		return
	}
	if err := CheckMonitorControlGuard(srv.db.Path(), req.Class, req.Kind); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := AppendMonitorControl(srv.db, srv.db.Path(), req.Class, req.Kind); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleLuhmannRecord appends one supervisor record to
// `~/.ark/monitoring/luhmann.jsonl` via the write actor.
// CRC: crc-Server.md | Seq: seq-luhmann-supervisor.md | R2794, R2795
func (srv *Server) handleLuhmannRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string `json:"kind"`
		Class   string `json:"class"`
		Nonce   int    `json:"nonce"`
		TaskID  string `json:"task_id"`
		Reason  string `json:"reason"`
		Crashes *int   `json:"crashes,omitempty"`
		Backoff int    `json:"backoff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Class == "" {
		http.Error(w, "class required", http.StatusBadRequest)
		return
	}
	switch req.Kind {
	case "spawn", "respawn":
		if req.TaskID == "" {
			http.Error(w, "task_id required for spawn/respawn", http.StatusBadRequest)
			return
		}
	case "exit", "crash":
		if req.Reason == "" {
			http.Error(w, "reason required for exit/crash", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, fmt.Sprintf("kind must be spawn/respawn/exit/crash, got %q", req.Kind), http.StatusBadRequest)
		return
	}
	var crashes int
	switch {
	case req.Crashes != nil:
		crashes = *req.Crashes
	case req.Kind == "exit":
		crashes = 0
	case req.Kind == "crash":
		prev, _ := PrevCrashes(srv.db.Path(), req.Class)
		crashes = prev + 1
	default:
		crashes, _ = PrevCrashes(srv.db.Path(), req.Class)
	}
	rec := LuhmannRecord{
		Kind:    req.Kind,
		Class:   req.Class,
		Nonce:   req.Nonce,
		TaskID:  req.TaskID,
		Reason:  req.Reason,
		Crashes: crashes,
		Backoff: req.Backoff,
	}
	if err := AppendLuhmannRecord(srv.db, srv.db.Path(), rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int{"crashes": crashes})
}

// handleListen long-polls for notifications. R789-R794
// CRC: crc-Server.md | Seq: seq-pubsub.md
func (srv *Server) handleListen(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		http.Error(w, "session required", http.StatusBadRequest)
		return
	}
	timeoutStr := r.URL.Query().Get("timeout")
	timeout := 120 * time.Second
	if timeoutStr != "" {
		if secs, err := strconv.Atoi(timeoutStr); err == nil {
			timeout = time.Duration(secs) * time.Second
		}
	}
	events := srv.pubsub.Listen(session, timeout)
	if len(events) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(FormatMarkdown(events)))
}

// registerLuaFunctions registers Go functions on the Lua mcp table
// via the passive execution path (no UI update push).
// handleScheduleSearch queries day buckets for a date range. R914-R920
// CRC: crc-Server.md | Seq: seq-scheduling.md
func (srv *Server) handleScheduleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Date string `json:"date"`
		Tag  string `json:"tag"`
		Gaps bool   `json:"gaps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Parse using same grammar as schedule tags R915
	loc := time.Now().Location()
	dr, err := ParseDateValue(req.Date, "", loc)
	if err != nil {
		http.Error(w, "bad date: "+err.Error(), http.StatusBadRequest)
		return
	}
	// R1027: compute events from schedule logs
	events := srv.scheduler.QueryRange(dr.Start, dr.End, req.Tag, req.Gaps)
	writeJSON(w, events)
}

// handleScheduleChange rewrites a date in a schedule tag value. R921-R925
// CRC: crc-Server.md | Seq: seq-scheduling.md
func (srv *Server) handleScheduleChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		Tag      string `json:"tag"`
		NewStart string `json:"new_start"`
		NewEnd   string `json:"new_end"`
		DryRun   bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" || req.Tag == "" || req.NewStart == "" {
		http.Error(w, "path, tag, and new_start are required", http.StatusBadRequest)
		return
	}

	// Read the file
	content, err := os.ReadFile(req.Path)
	if err != nil {
		http.Error(w, "read file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Find the tag line and rewrite the date portion R922
	prefix := "@" + req.Tag + ":"
	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		pos := strings.Index(trimmed, prefix)
		if pos < 0 {
			continue
		}
		oldValue := strings.TrimSpace(trimmed[pos+len(prefix):])
		// Parse old value to extract description text
		loc := time.Now().Location()
		dr, err := ParseDateValue(oldValue, "", loc)
		if err != nil {
			continue
		}

		// Build new value: newStart [..newEnd] description
		newValue := req.NewStart
		if req.NewEnd != "" {
			newValue += ".." + req.NewEnd
		}
		if dr.Description != "" {
			newValue += " " + dr.Description
		}

		if req.DryRun {
			writeJSON(w, map[string]string{
				"old": oldValue,
				"new": newValue,
			})
			return
		}

		// Replace the line
		newLine := strings.Replace(line, oldValue, newValue, 1)
		lines[i] = newLine
		found = true
		break
	}

	if !found {
		http.Error(w, "tag not found in file", http.StatusNotFound)
		return
	}

	// Write back and re-index R923
	if err := os.WriteFile(req.Path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		http.Error(w, "write file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger re-index via reconcile
	srv.reconcile()

	w.WriteHeader(http.StatusOK)
}

// handleSearchGrouped serves grouped search results for the standalone editor.
// CRC: crc-Server.md | Seq: seq-editor-endpoints.md | R1069-R1075
func (srv *Server) handleSearchGrouped(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query           string           `json:"query"`
		Mode            string           `json:"mode"`
		K               int              `json:"k"`
		Session         string           `json:"session"`
		FilterFiles     []string         `json:"filter_files"`
		ExcludeFiles    []string         `json:"exclude_files"`
		FilterFileTags  []string         `json:"filter_file_tags"`
		ExcludeFileTags []string         `json:"exclude_file_tags"`
		Filter          []string         `json:"filter"`
		Except          []string         `json:"except"`
		ChunkFilters    []ChunkFilterRow `json:"chunk_filters"`
		// R2442, R2453: primary tag predicate in sigil form. Same
		// semantics as the /search field of the same name.
		PrimaryTagQuery string `json:"primary_tag_query"`
		PrimaryFileTag  bool   `json:"primary_file_tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	opts := SearchOpts{K: req.K}
	if opts.K == 0 {
		opts.K = 20
	}
	opts.FilterFiles = req.FilterFiles
	opts.ExcludeFiles = req.ExcludeFiles
	opts.FilterFileTags = req.FilterFileTags
	opts.ExcludeFileTags = req.ExcludeFileTags
	opts.Filter = req.Filter
	opts.Except = req.Except
	opts.ChunkFilters = req.ChunkFilters

	query := req.Query

	// CRC: crc-Server.md | R2442, R2453, R2129
	// Primary tag predicate — V/F records pin chunkIDs, so when no
	// other text primary is set we bypass FTS entirely and build
	// results straight from ChunksByID. R2129: empty query means no
	// text primary regardless of mode, so leftover UI mode state
	// doesn't force a regex-with-chunk-filter.
	tagOnly := false
	var tagChunkIDs []uint64
	if req.PrimaryTagQuery != "" {
		tagChunkIDs = srv.resolvePrimaryTagChunks(req.PrimaryTagQuery, req.PrimaryFileTag)
		hasTextPrimary := query != "" && (req.Mode == "contains" || req.Mode == "about" || req.Mode == "regex" || req.Mode == "fuzzy")
		tagOnly = !hasTextPrimary
		if !tagOnly {
			set := make(map[uint64]bool, len(tagChunkIDs))
			for _, cid := range tagChunkIDs {
				set[cid] = true
			}
			opts.extraOpts = append(opts.extraOpts, microfts2.WithChunkFilter(chunkIDChunkFilter(set)))
		}
	}
	if !tagOnly {
		switch req.Mode {
		case "contains":
			opts.Contains = query
			query = ""
		case "about":
			opts.About = query
			query = ""
		case "regex": // R1228
			opts.Regex = []string{query}
			query = ""
		case "fuzzy":
			opts.Fuzzy = true
		}
	}

	// Multi-strategy for combined queries — exclude regex (R1229)
	if !tagOnly && !opts.Fuzzy && opts.Contains == "" && opts.About == "" && len(opts.Regex) == 0 {
		opts.Multi = true
	}

	var results []GroupedResult
	var err error
	if tagOnly {
		results, err = Sync(srv.db, func(db *DB) ([]GroupedResult, error) {
			return db.GroupTagChunks(tagChunkIDs, opts)
		})
	} else if req.Session != "" {
		sess := srv.GetOrCreateSession(req.Session)
		err = sess.RunSearch(query, func(cache *microfts2.ChunkCache) error {
			return SyncVoid(srv.db, func(db *DB) error {
				opts.Cache = cache
				var searchErr error
				results, searchErr = db.SearchGrouped(query, opts)
				return searchErr
			})
		})
	} else {
		results, err = Sync(srv.db, func(db *DB) ([]GroupedResult, error) {
			return db.SearchGrouped(query, opts)
		})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, results)
}

// resolvePrimaryTagChunks parses the sigil-form predicate and returns
// the chunkIDs (file-scoped or tag-scoped) selected by the predicate.
// CRC: crc-Server.md | R2442, R2453
func (srv *Server) resolvePrimaryTagChunks(query string, fileTag bool) []uint64 {
	p, err := ParseMatchSyntax(query)
	if err != nil {
		return nil
	}
	ids, _ := Sync(srv.db, func(db *DB) ([]uint64, error) {
		chunkIDs, fileIDs := resolvePredicateLocations(p, db.Store())
		if !fileTag {
			out := make([]uint64, 0, len(chunkIDs))
			for cid := range chunkIDs {
				out = append(out, cid)
			}
			return out, nil
		}
		// File-tag primary: every chunk on a matching file. Walk the
		// known files and pull each one's chunks.
		out := make([]uint64, 0)
		fts := db.FTS()
		if fts == nil {
			return out, nil
		}
		for fid := range fileIDs {
			info, err := fts.FileInfoByID(fid)
			if err != nil {
				continue
			}
			for _, ch := range info.Chunks {
				out = append(out, ch.ChunkID)
			}
		}
		return out, nil
	})
	return ids
}

// handleTagComplete returns tag name completions matching a prefix.
// CRC: crc-Server.md | Seq: seq-editor-endpoints.md | R1076-R1080
func (srv *Server) handleTagComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prefix string `json:"prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	type completion struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}

	prefix := strings.ToLower(req.Prefix)

	// Get all D records for descriptions
	defs, err := Sync(srv.db, func(db *DB) ([]TagDefInfo, error) {
		return db.TagDefs(nil)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build description map (first description wins)
	descMap := make(map[string]string)
	for _, d := range defs {
		if _, ok := descMap[d.Tag]; !ok {
			descMap[d.Tag] = d.Description
		}
	}

	if prefix == "" {
		// Return all tags with descriptions
		tags, err := Sync(srv.db, func(db *DB) ([]TagCount, error) {
			return db.TagList()
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result := make([]completion, 0, len(tags))
		for _, t := range tags {
			result = append(result, completion{
				Name:        t.Tag,
				Description: descMap[t.Tag],
			})
		}
		writeJSON(w, result)
		return
	}

	// Filter D records by prefix, deduplicate
	seen := make(map[string]bool)
	var result []completion
	for _, d := range defs {
		if strings.HasPrefix(d.Tag, prefix) && !seen[d.Tag] {
			seen[d.Tag] = true
			result = append(result, completion{
				Name:        d.Tag,
				Description: d.Description,
			})
		}
	}
	writeJSON(w, result)
}

// handleTagValues returns known values for a tag, optionally filtered by prefix.
// CRC: crc-Server.md | Seq: seq-editor-endpoints.md, seq-tag-value-index.md | R1081-R1085, R1111
func (srv *Server) handleTagValues(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag    string `json:"tag"`
		Prefix string `json:"prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Tag == "" {
		http.Error(w, "tag is required", http.StatusBadRequest)
		return
	}

	tag := strings.ToLower(req.Tag)
	prefix := strings.ToLower(req.Prefix)

	results, err := Sync(srv.db, func(db *DB) ([]TagValueCount, error) {
		return db.store.QueryTagValues(tag, prefix)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Count > results[j].Count
	})
	writeJSON(w, results)
}

// handleSave writes file content and triggers re-indexing.
// CRC: crc-Server.md | Seq: seq-editor-endpoints.md | R1086-R1089
func (srv *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	req.Path = filepath.Clean(req.Path)

	// Verify path is within an indexed source
	indexed, _ := Sync(srv.db, func(db *DB) (bool, error) {
		return db.IsIndexed(req.Path), nil
	})
	if !indexed {
		http.Error(w, "path not within indexed source", http.StatusForbidden)
		return
	}

	content := NormalizeTagLines([]byte(req.Content)) // R1193
	if err := os.WriteFile(req.Path, content, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger single-file refresh for immediate re-indexing.
	// Look up the existing strategy from the index.
	SyncVoid(srv.db, func(db *DB) error {
		strategy := ""
		if info, err := db.fts.CheckFile(req.Path); err == nil {
			if finfo, err := db.fts.FileInfoByID(info.FileID); err == nil {
				strategy = finfo.Strategy
			}
		}
		return db.indexer.RefreshFile(req.Path, strategy)
	})

	w.WriteHeader(http.StatusOK)
}

// handleSetTags atomically updates tags in a file's tag block.
// CRC: crc-Server.md | Seq: seq-editor-endpoints.md | R1090-R1093
func (srv *Server) handleSetTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string            `json:"path"`
		Tags map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" || len(req.Tags) == 0 {
		http.Error(w, "path and tags are required", http.StatusBadRequest)
		return
	}
	req.Path = filepath.Clean(req.Path)

	data, err := os.ReadFile(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	tb := ParseTagBlock(data)
	for name, value := range req.Tags {
		tb.Set(name, value)
		if name == "status" {
			tb.Set("status-date", time.Now().Format("2006-01-02"))
		}
	}

	if err := os.WriteFile(req.Path, tb.Render(), 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleShowInFolder opens the native file manager with the file selected.
// CRC: crc-Server.md | Seq: seq-editor-endpoints.md | R1216-R1221
func (srv *Server) handleShowInFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	req.Path = filepath.Clean(req.Path)

	// Validate path is within an indexed source
	inSource, _ := Sync(srv.db, func(db *DB) (bool, error) {
		return db.Config().IsInSource(req.Path), nil
	})
	if !inSource {
		http.Error(w, "path not within indexed source", http.StatusForbidden)
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", req.Path)
	case "windows":
		cmd = exec.Command("explorer.exe", "/select,"+req.Path)
	default: // Linux
		cmd = exec.Command("gdbus", "call", "--session",
			"--dest", "org.freedesktop.FileManager1",
			"--object-path", "/org/freedesktop/FileManager1",
			"--method", "org.freedesktop.FileManager1.ShowItems",
			fmt.Sprintf("['file://%s']", req.Path), "")
	}
	if err := cmd.Start(); err != nil {
		http.Error(w, "show in folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// registerContentRoutes registers GET routes on the UI server for serving
// indexed file content to the browser.
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1151-R1153
func (srv *Server) registerContentRoutes() {
	srv.uiRuntime.UIHandleFunc("GET /fetch/", srv.handleContentFetch)
	srv.uiRuntime.UIHandleFunc("GET /content/", srv.handleContentView)
	srv.uiRuntime.UIHandleFunc("GET /raw/", srv.handleContentRaw)
	// Mirror editor endpoints on the UI server so browser JS can reach them.
	// These are the same handlers registered on the unix socket API mux.
	srv.uiRuntime.UIHandleFunc("POST /search/grouped", srv.handleSearchGrouped)
	srv.uiRuntime.UIHandleFunc("POST /tags/complete", srv.handleTagComplete)
	srv.uiRuntime.UIHandleFunc("POST /tags/values", srv.handleTagValues)
	srv.uiRuntime.UIHandleFunc("POST /file/save", srv.handleSave)
	srv.uiRuntime.UIHandleFunc("POST /tags/set", srv.handleSetTags)
	srv.uiRuntime.UIHandleFunc("POST /file/show", srv.handleShowInFolder)
	// Curation endpoints — pin/dismiss/pinned are also registered on the
	// unix-socket mux for CLI/test access; the UI mirrors here let the
	// content-view inline JS reach them from the browser. R2411-R2414
	srv.uiRuntime.UIHandleFunc("POST /curation/pin", srv.handleCuratePin)
	srv.uiRuntime.UIHandleFunc("POST /curation/dismiss", srv.handleCurateDismiss)
	srv.uiRuntime.UIHandleFunc("GET /curation/pinned", srv.handleCuratePinned)
	log.Printf("ui: content routes registered (/fetch/, /content/, /raw/, editor endpoints)")
	// NOTE: /content/ markdown shell references /ark-markdown-editor.js.
	// The UI server serves from ~/.ark/html/ — the Makefile must copy
	// the built bundle there (see O48 in design.md gaps).
}

// contentPath extracts and validates the file path from a content route URL.
// Returns the cleaned absolute path, or writes an error response and returns "".
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1154-R1156
func (srv *Server) contentPath(w http.ResponseWriter, r *http.Request, prefix string) (path string, data []byte, ok bool) {
	path = strings.TrimPrefix(r.URL.Path, prefix)
	if path == "" || path[0] != '/' {
		http.Error(w, "absolute path required", http.StatusBadRequest)
		return "", nil, false
	}
	path = filepath.Clean(path)

	result, err := Sync(srv.db, func(db *DB) ([]byte, error) {
		return db.ReadSourceFile(path)
	})
	if err != nil {
		if strings.Contains(err.Error(), "not in source") {
			http.Error(w, "forbidden", http.StatusForbidden)
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
		return "", nil, false
	}
	return path, result, true
}

// handleContentFetch returns file content as JSON with path, content, and contentType.
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1157-R1159
func (srv *Server) handleContentFetch(w http.ResponseWriter, r *http.Request) {
	path, data, ok := srv.contentPath(w, r, "/fetch")
	if !ok {
		return
	}
	// R1423: resolve chunk range if specified
	if rangeParam := r.URL.Query().Get("range"); rangeParam != "" {
		chunkData, _ := Sync(srv.db, func(db *DB) ([]byte, error) {
			return db.ChunkText(path, rangeParam), nil
		})
		if chunkData != nil {
			data = chunkData
		}
	}
	strategy, _ := Sync(srv.db, func(db *DB) (string, error) {
		return db.FileStrategy(path), nil
	})
	writeJSON(w, map[string]string{
		"path":        path,
		"content":     string(data),
		"contentType": StrategyToContentType(strategy),
	})
}

// writeArkChunkOpen writes the opening tag of a chunk div with range,
// chunkID, and fileID data attributes for the curate-button inline JS.
// chunkID == 0 emits an empty attribute (the inline JS treats those
// chunks as not-pinnable). R2415
func writeArkChunkOpen(buf *strings.Builder, rangeStr string, chunkID, fileID uint64) {
	buf.WriteString(`<div class="ark-chunk" data-range="`)
	buf.WriteString(template.HTMLEscapeString(rangeStr))
	buf.WriteString(`" data-chunkid="`)
	if chunkID != 0 {
		fmt.Fprintf(buf, "%d", chunkID)
	}
	buf.WriteString(`" data-fileid="`)
	if fileID != 0 {
		fmt.Fprintf(buf, "%d", fileID)
	}
	buf.WriteString(`">`)
}

// handleContentView returns an HTML page that presents the file richly.
// Markdown: goldmark-rendered HTML with pencil/eye toggle to ink-mde editor.
// Other types: <pre> block.
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1160-R1164, R1168-R1189
func (srv *Server) handleContentView(w http.ResponseWriter, r *http.Request) {
	path, data, ok := srv.contentPath(w, r, "/content")
	if !ok {
		return
	}

	// R1423-R1427: query params for iframe previews
	rangeParam := r.URL.Query().Get("range")
	hideToggle := r.URL.Query().Get("toggle") == "false"
	autoEdit := r.URL.Query().Get("edit") == "true"
	isChunk := false

	// R1423-R1425: resolve chunk range if specified
	if rangeParam != "" {
		chunkData, _ := Sync(srv.db, func(db *DB) ([]byte, error) {
			return db.ChunkText(path, rangeParam), nil
		})
		if chunkData != nil {
			data = chunkData
			isChunk = true
		}
		// R1425: if range invalid, fall back to full file (data unchanged)
	}

	strategy, _ := Sync(srv.db, func(db *DB) (string, error) {
		return db.FileStrategy(path), nil
	})

	// R2415: per-file fileID for the curate-button data attributes.
	// One CheckFile call here is shared across every chunk div emitted
	// below; the inline pin-button script reads data-fileid from each
	// div to POST {chunkID, fileID, path}.
	fileID, _ := Sync(srv.db, func(db *DB) (uint64, error) {
		info, err := db.fts.CheckFile(path)
		if err != nil {
			return 0, nil
		}
		return info.FileID, nil
	})

	// Cache-busting hash for JS bundle
	dbPath := srv.db.Path()
	bundleHash := ""
	if info, err := os.Stat(filepath.Join(dbPath, "html", "ark-markdown-editor.js")); err == nil {
		bundleHash = fmt.Sprintf("?v=%d", info.ModTime().Unix())
	}

	shell := contentShellData{
		Title:      path,
		BundleHash: bundleHash,
		HideToggle: hideToggle,
		AutoEdit:   autoEdit,
		IsChunk:    isChunk,
		IsSearch:   r.URL.Query().Get("highlight") != "",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if StrategyToContentType(strategy) == "markdown" || strings.HasSuffix(path, ".md") {
		tmpl, err := srv.loadContentTemplate("content-markdown.html")
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// R2065, R2073, R2079: per-chunk markdown rendering so each chunk
		// can carry its own <ark-ext-tags> indicator. Single-chunk views
		// (?range=) render just that chunk; full-file views walk AllChunks.
		// Falls back to single-blob rendering when the file is not indexed.
		var buf strings.Builder
		if isChunk {
			rendered := wrapTagElements(renderMarkdownForContent(data, path), srv.db)
			cid := srv.db.ChunkIDByLocation(path, rangeParam)
			var extBlock string
			if cid != 0 {
				extBlock = renderExtTagsBlock(srv.db.extmap.ExtRoutingsForTargetChunk(cid, srv.db), "")
			}
			writeArkChunkOpen(&buf, rangeParam, cid, fileID)
			buf.WriteString(extBlock)
			buf.WriteString(rendered)
			buf.WriteString("</div>\n")
		} else {
			chunks, _ := Sync(srv.db, func(db *DB) ([]microfts2.ChunkResult, error) {
				return db.AllChunks(path), nil
			})
			if len(chunks) > 0 {
				chunkIDs := srv.db.ChunkIDsForPath(path)
				for i, ch := range chunks {
					rendered := wrapTagElements(renderMarkdownForContent([]byte(ch.Content), path), srv.db)
					var extBlock string
					var cid uint64
					if i < len(chunkIDs) {
						cid = chunkIDs[i]
						extBlock = renderExtTagsBlock(srv.db.extmap.ExtRoutingsForTargetChunk(cid, srv.db), "")
					}
					writeArkChunkOpen(&buf, ch.Range, cid, fileID)
					buf.WriteString(extBlock)
					buf.WriteString(rendered)
					buf.WriteString("</div>\n")
				}
			} else {
				buf.WriteString(wrapTagElements(renderMarkdownForContent(data, path), srv.db))
			}
		}
		// R2074, R2075: id anchors for sidebar — applied across the
		// assembled chunked HTML.
		shell.Content = template.HTML(assignSidebarIDs(buf.String()))
		tmpl.Execute(w, shell)
	} else {
		tmpl, err := srv.loadContentTemplate("content-plain.html")
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// R1495-R1496, R1499, R1501: render chunks as divs for non-markdown files.
		// Single-chunk views (isChunk) use the already-resolved data.
		// Full-file views use AllChunks. Falls back to raw <pre> if no chunks.
		var chunks []microfts2.ChunkResult
		if !isChunk {
			chunks, _ = Sync(srv.db, func(db *DB) ([]microfts2.ChunkResult, error) {
				return db.AllChunks(path), nil
			})
		}
		if len(chunks) > 0 {
			shell.IsChunked = true
			// R1739, R1740: PDFs use page-level aggregation — one <pdf-chunk>
			// per page covering the full page, carrying every tag_rects
			// entry from every chunk on that page. Block rects alone would
			// leave gaps between text regions.
			if strategy == "pdf" {
				// R2074, R2075, R2076: id anchors for sidebar — applied to
				// <ark-tag> and <ark-heading> overlays inside <pdf-chunk>.
				shell.Content = template.HTML(assignSidebarIDs(renderPdfChunksByPage(chunks, path, fileID, srv.db)))
				tmpl.Execute(w, shell)
				return
			}
			// R1505-R1506: JSONL chunks are markdown (human/AI conversation);
			// render through goldmark. Other strategies stay pre-wrapped.
			useMarkdown := strategy == "chat-jsonl"
			// R2065, R2073, R2079: chunkIDs for per-chunk ext-routing lookup.
			chunkIDs := srv.db.ChunkIDsForPath(path)
			var buf strings.Builder
			prevRole := ""
			groupOpen := false
			for i, ch := range chunks {
				var rendered string
				if useMarkdown {
					rendered = wrapTagElements(renderMarkdownForContent([]byte(ch.Content), path), srv.db)
				} else {
					rendered = wrapTagElements(template.HTMLEscapeString(ch.Content), srv.db)
				}
				var extBlock string
				var cid uint64
				if i < len(chunkIDs) {
					cid = chunkIDs[i]
					extBlock = renderExtTagsBlock(srv.db.extmap.ExtRoutingsForTargetChunk(cid, srv.db), "")
				}
				// R1509-R1512: role grouping for chat-jsonl chunks.
				role, _ := microfts2.PairGet(ch.Attrs, "role")
				roleStr := string(role)
				if roleStr != "" && roleStr != prevRole {
					if groupOpen {
						// Close skill details if needed.
						if prevRole == "skill" {
							buf.WriteString("</details>")
						}
						buf.WriteString("</div>\n")
					}
					buf.WriteString(`<div class="ark-role-group ark-role-`)
					buf.WriteString(roleStr)
					buf.WriteString(`">`)
					switch roleStr {
					case "skill":
						skillName, _ := microfts2.PairGet(ch.Attrs, "skill")
						buf.WriteString(`<details><summary class="ark-role-header">📋 `)
						if len(skillName) > 0 {
							buf.WriteString(template.HTMLEscapeString(string(skillName)))
						} else {
							buf.WriteString("skill")
						}
						buf.WriteString("</summary>")
					case "human":
						buf.WriteString(`<div class="ark-role-header">👤</div>`)
					case "assistant":
						buf.WriteString(`<div class="ark-role-header">🤖</div>`)
					}
					groupOpen = true
					prevRole = roleStr
				}
				writeArkChunkOpen(&buf, ch.Range, cid, fileID)
				buf.WriteString(extBlock)
				buf.WriteString(rendered)
				buf.WriteString("</div>\n")
			}
			if groupOpen {
				if prevRole == "skill" {
					buf.WriteString("</details>")
				}
				buf.WriteString("</div>\n")
			}
			// R2074, R2075: id anchors for sidebar — applied across the
			// assembled chunked HTML (covers inline <ark-tag> from
			// wrapTagElements and <ark-tag> children inside <ark-ext-tags>).
			shell.Content = template.HTML(assignSidebarIDs(buf.String()))
		} else {
			// Single chunk or unchunked file — render through goldmark for JSONL,
			// as <pdf-chunk> for PDF chunks with a rect, plain-text fallback
			// otherwise. R1703-R1708
			//
			// When the URL specifies a range (?range=...) we know exactly
			// which chunk we're showing, so wrap the rendered content in a
			// <div class="ark-chunk" data-chunkid data-fileid> so the
			// curate-pin inline JS can install a pin button. R2415 R2417
			var body strings.Builder
			switch {
			case strategy == "chat-jsonl":
				body.WriteString(wrapTagElements(renderMarkdownForContent(data, path), srv.db))
			case strategy == "pdf" && isChunk:
				attrs, _ := Sync(srv.db, func(db *DB) ([]microfts2.Pair, error) {
					return db.ChunkAttrs(path, rangeParam), nil
				})
				pdfZoom := srv.db.config.PdfPreviewZoom
				if pdfZoom <= 0 {
					pdfZoom = 1.5
				}
				if pdfHTML, ok := renderPdfPreview(attrs, path, pdfZoom); ok {
					body.WriteString(pdfHTML)
				} else {
					body.WriteString(wrapTagElements(template.HTMLEscapeString(string(data)), srv.db))
				}
			default:
				body.WriteString(wrapTagElements(template.HTMLEscapeString(string(data)), srv.db))
			}
			if isChunk {
				cid := srv.db.ChunkIDByLocation(path, rangeParam)
				var buf strings.Builder
				writeArkChunkOpen(&buf, rangeParam, cid, fileID)
				buf.WriteString(body.String())
				buf.WriteString("</div>")
				shell.IsChunked = true
				shell.Content = template.HTML(buf.String())
			} else {
				shell.Content = template.HTML(body.String())
			}
		}
		tmpl.Execute(w, shell)
	}
}

// handleContentRaw returns file content verbatim with appropriate Content-Type.
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1165-R1167
func (srv *Server) handleContentRaw(w http.ResponseWriter, r *http.Request) {
	path, data, ok := srv.contentPath(w, r, "/raw")
	if !ok {
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data)
}

// contentLinkRewriter rewrites relative links and images in goldmark AST.
// Images: relative src → /raw/BASEDIR/src
// Links: relative .md href → /content/BASEDIR/href
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1170-R1173
type contentLinkRewriter struct {
	baseDir string
}

func isRelativeURL(s string) bool {
	return s != "" && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://")
}

func (lr *contentLinkRewriter) Transform(node *ast.Document, reader text.Reader, pc parser.Context) {
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Image:
			if dest := string(v.Destination); isRelativeURL(dest) {
				v.Destination = []byte("/raw" + lr.baseDir + "/" + dest)
			}
		case *ast.Link:
			if dest := string(v.Destination); isRelativeURL(dest) && strings.HasSuffix(dest, ".md") {
				v.Destination = []byte("/content" + lr.baseDir + "/" + dest)
			}
		}
		return ast.WalkContinue, nil
	})
}

// renderMarkdownForContent renders markdown to HTML with link/image rewriting
// for the /content/ route. R1168-R1173
func renderMarkdownForContent(data []byte, filePath string) string {
	baseDir := filepath.Dir(filePath)
	// R1194: Normalize tag lines so they render as line breaks even if the file
	// on disk lacks trailing spaces (hand-edited files).
	data = NormalizeTagLines(data)
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Typographer,
			extension.DefinitionList,
		),
		goldmark.WithParserOptions(
			parser.WithAttribute(),
			parser.WithASTTransformers(
				util.Prioritized(&contentLinkRewriter{baseDir: baseDir}, 100),
			),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(data, &buf); err != nil {
		return template.HTMLEscapeString(string(data))
	}
	return buf.String()
}

// wrapTagElements post-processes rendered HTML to wrap @tag: value patterns
// in <ark-tag> elements for interactive tag widgets in read views.
// Skips tags preceded by backtick (code context) or inside <code> elements.
// CRC: crc-Server.md | Seq: seq-ark-tag-click.md | R1485-R1489, R1979, R1980, R1981
var arkTagRe = regexp.MustCompile(`(?m)@([a-zA-Z][\w.-]*):[ \t]*([^\n<]*)`)

func wrapTagElements(html string, db *DB) string {
	matches := arkTagRe.FindAllStringSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return html
	}
	var buf strings.Builder
	buf.Grow(len(html) + len(matches)*40)
	prev := 0
	for _, m := range matches {
		start := m[0]
		// Skip if preceded by backtick (mentioned in code context).
		if start > 0 && html[start-1] == '`' {
			continue
		}
		// Skip if inside a <code> element (scan backward for unclosed <code>).
		// Match "<code" to catch both <code> and <code class="...">.
		prefix := html[:start]
		lastOpen := strings.LastIndex(prefix, "<code")
		lastClose := strings.LastIndex(prefix, "</code>")
		if lastOpen > lastClose {
			continue
		}
		buf.WriteString(html[prev:start])
		name := html[m[2]:m[3]]
		value := strings.TrimRight(html[m[4]:m[5]], " \t")
		if name == "link" && value != "" {
			renderLinkElement(&buf, value, db)
		} else if value == "" {
			buf.WriteString(`<ark-tag><name>` + name + `</name></ark-tag>`)
		} else {
			buf.WriteString(`<ark-tag><name>` + name + `</name> <value>` + value + `</value></ark-tag>`)
		}
		prev = m[1]
	}
	buf.WriteString(html[prev:])
	return buf.String()
}

// renderLinkElement emits the @link: rendering — an <a> for resolved
// links, an <ark-tag class="ark-link-broken"> wrapper for broken or
// unresolvable values. The value text comes from a regex match over
// already-rendered HTML, so it is inserted into element text without
// re-escaping. The href is HTML-attribute-escaped because we assemble
// it from the path. CRC: crc-Server.md | R1980, R1981
func renderLinkElement(buf *strings.Builder, value string, db *DB) {
	if db != nil {
		if path, loc, ok := db.ResolveLink(value); ok {
			href := "/content" + path
			if loc != "" {
				href += "?range=" + url.QueryEscape(loc)
			}
			buf.WriteString(`<a class="ark-link" href="`)
			buf.WriteString(template.HTMLEscapeString(href))
			buf.WriteString(`">@link: `)
			buf.WriteString(value)
			buf.WriteString(`</a>`)
			return
		}
	}
	buf.WriteString(`<ark-tag class="ark-link-broken"><name>link</name> <value>`)
	buf.WriteString(value)
	buf.WriteString(`</value></ark-tag>`)
}

// renderExtTagsBlock emits the <ark-ext-tags> indicator block for a
// chunk's incoming ext routings. Each routing contributes one or
// more <ark-tag> children carrying externalFile and externalTarget.
// rect is empty for HTML chunks (the element positions itself at the
// top of its `<div class="ark-chunk">` container) and "X,Y,W,H" for
// PDF chunks (the element positions absolutely over the `<pdf-chunk>`
// canvas). Returns "" when routings is empty.
// CRC: crc-Server.md | R2065, R2073, R2082
func renderExtTagsBlock(routings []IncomingExtRouting, rect string) string {
	if len(routings) == 0 {
		return ""
	}
	var buf strings.Builder
	buf.WriteString(`<ark-ext-tags`)
	if rect != "" {
		buf.WriteString(` rect="`)
		buf.WriteString(template.HTMLEscapeString(rect))
		buf.WriteString(`"`)
	}
	buf.WriteString(`>`)
	for _, r := range routings {
		for _, tv := range r.Routed {
			buf.WriteString(`<ark-tag externalFile="`)
			buf.WriteString(template.HTMLEscapeString(r.SourceFilePath))
			buf.WriteString(`" externalTarget="`)
			buf.WriteString(template.HTMLEscapeString(r.TargetAnchor))
			buf.WriteString(`"><name>`)
			buf.WriteString(template.HTMLEscapeString(tv.Tag))
			buf.WriteString(`</name>`)
			if tv.Value != "" {
				buf.WriteString(` <value>`)
				buf.WriteString(template.HTMLEscapeString(tv.Value))
				buf.WriteString(`</value>`)
			}
			buf.WriteString(`</ark-tag>`)
		}
	}
	buf.WriteString(`</ark-ext-tags>`)
	return buf.String()
}

// arkTagOpenRe and headingOpenRe match the open-tag of <ark-tag>,
// HTML headings (<h1>-<h6>), and <ark-heading> (PDF) for sidebar
// id-anchor assignment.
var arkTagOpenRe = regexp.MustCompile(`<ark-tag(\s|>)`)
var headingOpenRe = regexp.MustCompile(`<(h[1-6]|ark-heading)(\s|>)`)

// assignSidebarIDs adds id attributes to <ark-tag> elements
// (id="ark-tag-N"), HTML headings, and <ark-heading> (id="ark-heading-N")
// so the sidebar can DOM-anchor entries. Numbers are sequential within
// the supplied HTML; the heading counter spans <h1>-<h6> and
// <ark-heading> together.
// CRC: crc-Server.md | R2074, R2075, R2076
func assignSidebarIDs(html string) string {
	var tagN int
	html = arkTagOpenRe.ReplaceAllStringFunc(html, func(m string) string {
		tagN++
		return fmt.Sprintf(`<ark-tag id="ark-tag-%d"%s`, tagN, m[len("<ark-tag"):])
	})
	var hN int
	html = headingOpenRe.ReplaceAllStringFunc(html, func(m string) string {
		hN++
		boundary := strings.IndexAny(m[1:], " \t\n>") + 1
		return fmt.Sprintf(`<%s id="ark-heading-%d"%s`, m[1:boundary], hN, m[boundary:])
	})
	return html
}

// contentShellData holds the template data for content HTML shells.
type contentShellData struct {
	Title      string
	Content    template.HTML // raw HTML, not escaped
	BundleHash string        // cache-busting query param for JS bundle
	HideToggle bool          // R1426, R1429: hide pencil/eye toggle button
	AutoEdit   bool          // R1427, R1430: auto-load CM6 editor on page load
	IsChunk    bool          // R1423: serving a chunk range, not full file
	IsChunked  bool          // R1495: content is chunk divs, not raw text
	IsSearch   bool          // highlight query params present
}

// loadContentTemplate reads an HTML template from the html/ dir under dbPath.
// Templates are read from disk on each request so CSS edits take effect immediately.
// Injects the frictionless theme block between <!-- #frictionless --> markers.
// CRC: crc-Server.md | Seq: seq-content-fetching.md | R1196-R1199
func (srv *Server) loadContentTemplate(name string) (*template.Template, error) {
	dbPath := srv.db.Path()
	path := filepath.Join(dbPath, "html", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Theme block is already injected on disk at startup by InjectAllThemeBlocks.
	// No per-request injection needed.
	return template.New(name).Parse(string(data))
}

// parseDateToYMD parses a flexible date string into YYYYMMDD format.

// CheckScheduleConfig compares current [schedule] config with stored version.
// If different, re-materializes day buckets for affected tags. R927-R932
// CRC: crc-Server.md
func (srv *Server) CheckScheduleConfig() {
	changed, _ := Sync(srv.db, func(db *DB) (bool, error) {
		cfg := db.Config()
		current := serializeScheduleConfig(cfg)

		stored, err := db.store.GetScheduleConfig()
		if err != nil {
			log.Printf("schedule: cannot read stored config: %v", err)
			stored = ""
		}

		if current == stored {
			return false, nil
		}

		log.Printf("schedule: config changed, triggering reconcile")
		if err := db.store.PutScheduleConfig(current); err != nil {
			log.Printf("schedule: cannot store config: %v", err)
		}
		return true, nil
	})
	if changed {
		srv.reconcile()
	}
}

// serializeScheduleConfig produces a deterministic string from the schedule config.
// R975: includes filter/lifecycle fields so changes trigger re-materialization.
func serializeScheduleConfig(cfg *Config) string {
	tags := cfg.ScheduleTags()
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+tags[k])
	}
	s := cfg.Schedule
	ff := make([]string, len(s.FilterFiles))
	copy(ff, s.FilterFiles)
	sort.Strings(ff)
	ef := make([]string, len(s.ExcludeFiles))
	copy(ef, s.ExcludeFiles)
	sort.Strings(ef)
	li := make([]string, len(s.LifecycleInclude))
	copy(li, s.LifecycleInclude)
	sort.Strings(li)
	le := make([]string, len(s.LifecycleExclude))
	copy(le, s.LifecycleExclude)
	sort.Strings(le)
	parts = append(parts,
		"ff:"+strings.Join(ff, ";"),
		"ef:"+strings.Join(ef, ";"),
		"li:"+strings.Join(li, ";"),
		"le:"+strings.Join(le, ";"),
	)
	return strings.Join(parts, ",")
}

// ensureListenLoop starts a per-session listening goroutine if one
// isn't already running for sessionID. Idempotent; called from
// mcp.subscribe after the sub is registered.
// CRC: crc-Server.md | Seq: seq-tmp-subscription.md | R2294, R2299
func (srv *Server) ensureListenLoop(sessionID string) {
	srv.listenMu.Lock()
	if srv.listenLoops == nil {
		srv.listenLoops = make(map[string]chan struct{})
	}
	if _, running := srv.listenLoops[sessionID]; running {
		srv.listenMu.Unlock()
		return
	}
	stop := make(chan struct{})
	srv.listenLoops[sessionID] = stop
	srv.listenMu.Unlock()
	go srv.runListenLoop(sessionID, stop)
}

// maybeStopListenLoop stops the per-session listening goroutine and
// clears the onpublish callback when the session has no remaining
// subscriptions. Called from mcp.cancel after pubsub.Cancel returns.
// CRC: crc-Server.md | R2300
func (srv *Server) maybeStopListenLoop(sessionID string) {
	if srv.pubsub == nil {
		return
	}
	if srv.pubsub.SubCount(sessionID) > 0 {
		return
	}
	srv.listenMu.Lock()
	if stop, ok := srv.listenLoops[sessionID]; ok {
		close(stop)
		delete(srv.listenLoops, sessionID)
	}
	delete(srv.onpublishCBs, sessionID)
	srv.listenMu.Unlock()
}

// runListenLoop drains pubsub.Listen for sessionID, compresses each
// batch by (path, tag), and dispatches the survivors into the Lua
// VM via uiRuntime.WithLua. One WithLua call per batch, not per
// event. Exits when stop is closed or when the session's pubsub
// state is gone.
// CRC: crc-Server.md | Seq: seq-tmp-subscription.md | R2294, R2295, R2296, R2297, R2298, R2302
func (srv *Server) runListenLoop(sessionID string, stop <-chan struct{}) {
	const listenTimeout = 5 * time.Second
	for {
		// Cheap pre-check so a fast cancel doesn't pay one more Listen.
		select {
		case <-stop:
			return
		default:
		}
		if srv.pubsub == nil {
			return
		}
		events := srv.pubsub.Listen(sessionID, listenTimeout)
		// After Listen returns, re-check stop — Cancel might have run
		// concurrently and we don't want to dispatch into a dead session.
		select {
		case <-stop:
			return
		default:
		}
		if len(events) == 0 {
			continue
		}
		compressed := CompressBatch(events)
		srv.listenMu.Lock()
		cb := srv.onpublishCBs[sessionID]
		srv.listenMu.Unlock()
		if cb == nil || srv.uiRuntime == nil {
			continue
		}
		_ = srv.uiRuntime.WithLua(func(rt *cli.LuaRuntime) error {
			L := rt.State
			arr := buildEventArray(L, compressed)
			if err := L.CallByParam(lua.P{Fn: cb, NRet: 0, Protect: true}, arr); err != nil {
				log.Printf("onpublish(%s): %v", sessionID, err)
			}
			return nil
		})
	}
}

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

		// R2356: register the global `sys` Lua table with a curation subtable.
		// State is Go-owned (srv.curation.pinned); sys.curation.pinned is the
		// Lua-side mirror Frictionless watches. Mutators (sys.curation.pin /
		// .dismiss / .sweepOlder) run in the Lua executor — they update the
		// Go slice and refresh the mirror in the same tick.
		sysTable := L.NewTable()
		curationTable := L.NewTable()
		L.SetField(curationTable, "pinned", L.NewTable())
		L.SetField(sysTable, "curation", curationTable)
		L.SetGlobal("sys", sysTable)
		srv.curation.luaTable = curationTable

		// sys.findConnections(inputs, opts) — unified entry point for
		// both normal and turbo modes. inputs is a Lua array of entry
		// tables ({chunkID = N} | {path = P, range = R} | {text = T})
		// OR a bare integer array (sugar for chunkID entries; preserves
		// 1G call shape). opts is a table with mode/purpose/k/timeoutSeconds.
		// Returns (requestID, nil) on success or (nil, errstring) on failure.
		// R2567, R2568, R2600, R2601, R2602, R2603
		L.SetField(sysTable, "findConnections", L.NewFunction(func(L *lua.LState) int {
			arr := L.CheckTable(1)
			inputs := luaTableToConnectionsInputs(arr)
			opts := FindConnectionsOpts{}
			if optsTbl, ok := L.Get(2).(*lua.LTable); ok && optsTbl != nil {
				if v, ok := optsTbl.RawGetString("timeoutSeconds").(lua.LNumber); ok {
					opts.TimeoutSeconds = int(v)
				}
				if v, ok := optsTbl.RawGetString("mode").(lua.LString); ok {
					opts.Mode = string(v)
				}
				if v, ok := optsTbl.RawGetString("purpose").(lua.LString); ok {
					opts.Purpose = string(v)
				}
				if v, ok := optsTbl.RawGetString("k").(lua.LNumber); ok {
					opts.K = int(v)
				}
			}
			if srv.librarian == nil {
				L.Push(lua.LNil)
				L.Push(lua.LString("librarian unavailable"))
				return 2
			}
			id, err := srv.librarian.FindConnections(inputs, opts)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LString(id))
			return 1
		}))

		// sys.recall(inputs, opts) — delegates to Librarian.Recall.
		// inputs is a Lua array of entry tables or bare integers (chunkIDs).
		// opts is a table with k/includeContent.
		// Returns (resultTable, nil) on success or (nil, errstring) on failure.
		// CRC: crc-Server.md | Seq: seq-recall.md#2.1 | R2628
		L.SetField(sysTable, "recall", L.NewFunction(func(L *lua.LState) int {
			arr := L.CheckTable(1)
			inputs := luaTableToConnectionsInputs(arr)
			opts := RecallOpts{
				IncludeContent: true,
			}
			if optsTbl, ok := L.Get(2).(*lua.LTable); ok && optsTbl != nil {
				if v, ok := optsTbl.RawGetString("includeContent").(lua.LBool); ok {
					opts.IncludeContent = bool(v)
				}
				if v, ok := optsTbl.RawGetString("k").(lua.LNumber); ok {
					opts.K = int(v)
				}
				// R2647: substrate filter exposed via Lua.
				if v, ok := optsTbl.RawGetString("keepTagless").(lua.LBool); ok {
					opts.KeepTagless = bool(v)
				}
				// R2655: --session SID equivalent — substrate loads the
				// session's RD records into the exclusion set.
				if v, ok := optsTbl.RawGetString("session").(lua.LString); ok {
					opts.Session = string(v)
				}
				// R2660: caller-supplied discussed list, as
				// {{tag=..., value=...}, ...}.
				if dt, ok := optsTbl.RawGetString("discussed").(*lua.LTable); ok && dt != nil {
					dt.ForEach(func(_, item lua.LValue) {
						entry, _ := item.(*lua.LTable)
						if entry == nil {
							return
						}
						var d Discussed
						if v, ok := entry.RawGetString("tag").(lua.LString); ok {
							d.Tag = string(v)
						}
						if v, ok := entry.RawGetString("value").(lua.LString); ok {
							d.Value = string(v)
						}
						if d.Tag == "" {
							return
						}
						opts.Discussed = append(opts.Discussed, d)
					})
				}
				// R2677: --propose equivalent — run the statistical
				// derivation pass and surface proposedTags on result chunks.
				if v, ok := optsTbl.RawGetString("propose").(lua.LBool); ok {
					opts.Propose = bool(v)
				}
			}
			if srv.librarian == nil {
				L.Push(lua.LNil)
				L.Push(lua.LString("librarian unavailable"))
				return 2
			}
			res, err := srv.librarian.Recall(inputs, opts)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			resTbl := L.NewTable()
			if res.Warning != "" {
				L.SetField(resTbl, "warning", lua.LString(res.Warning))
			}
			chunksTbl := L.NewTable()
			for _, chunk := range res.Chunks {
				chunkTbl := L.NewTable()
				L.SetField(chunkTbl, "chunkID", lua.LNumber(chunk.ChunkID))
				L.SetField(chunkTbl, "path", lua.LString(chunk.Path))
				L.SetField(chunkTbl, "range", lua.LString(chunk.Range))
				L.SetField(chunkTbl, "score", lua.LNumber(chunk.Score))

				subTbl := L.NewTable()
				L.SetField(subTbl, "vectorEc", lua.LNumber(chunk.PerSubstrate.VectorEC))
				L.SetField(subTbl, "trigramEc", lua.LNumber(chunk.PerSubstrate.TrigramEC))
				L.SetField(chunkTbl, "perSubstrate", subTbl)

				tagsTbl := L.NewTable()
				for _, tv := range chunk.Tags {
					tagTbl := L.NewTable()
					L.SetField(tagTbl, "tag", lua.LString(tv.Tag))
					if tv.Value != "" {
						L.SetField(tagTbl, "value", lua.LString(tv.Value))
					}
					tagsTbl.Append(tagTbl)
				}
				L.SetField(chunkTbl, "tags", tagsTbl)

				if chunk.Content != "" {
					L.SetField(chunkTbl, "content", lua.LString(chunk.Content))
				}
				// R2686: derived-tag candidate names, similarity-desc order.
				// Present only when opts.propose was set and the chunk has
				// at least one accumulated RC record. R2743 mirrors scores.
				if len(chunk.ProposedTags) > 0 {
					propTbl := L.NewTable()
					for _, name := range chunk.ProposedTags {
						propTbl.Append(lua.LString(name))
					}
					L.SetField(chunkTbl, "proposedTags", propTbl)
					if len(chunk.ProposedTagScores) > 0 {
						scoreTbl := L.NewTable()
						for _, s := range chunk.ProposedTagScores {
							scoreTbl.Append(lua.LNumber(s))
						}
						L.SetField(chunkTbl, "proposedTagScores", scoreTbl)
					}
				}
				chunksTbl.Append(chunkTbl)
			}
			L.SetField(resTbl, "chunks", chunksTbl)

			L.Push(resTbl)
			return 1
		}))

		// sys.discussed — per-session recall dedup state. Four methods
		// mirroring `ark discussed add/list/clear/prune`.
		// CRC: crc-Server.md | Seq: seq-discussed.md | R2661
		discussedTable := L.NewTable()
		L.SetField(sysTable, "discussed", discussedTable)
		L.SetField(discussedTable, "add", L.NewFunction(func(L *lua.LState) int {
			session := L.CheckString(1)
			if session == "" {
				L.Push(lua.LNil)
				L.Push(lua.LString("session ID required"))
				return 2
			}
			tagsTbl, ok := L.Get(2).(*lua.LTable)
			if !ok || tagsTbl == nil || tagsTbl.Len() == 0 {
				L.Push(lua.LNil)
				L.Push(lua.LString("no tags specified"))
				return 2
			}
			var count int
			var firstErr error
			tagsTbl.ForEach(func(_, item lua.LValue) {
				if firstErr != nil {
					return
				}
				entry, _ := item.(*lua.LTable)
				if entry == nil {
					return
				}
				tag, _ := entry.RawGetString("tag").(lua.LString)
				val, _ := entry.RawGetString("value").(lua.LString)
				if tag == "" {
					return
				}
				if err := srv.db.AddDiscussed(session, string(tag), string(val)); err != nil {
					firstErr = err
					return
				}
				count++
			})
			if firstErr != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(firstErr.Error()))
				return 2
			}
			L.Push(lua.LNumber(count))
			return 1
		}))
		L.SetField(discussedTable, "list", L.NewFunction(func(L *lua.LState) int {
			session := L.CheckString(1)
			if session == "" {
				L.Push(lua.LNil)
				L.Push(lua.LString("session ID required"))
				return 2
			}
			var since time.Duration
			if optsTbl, ok := L.Get(2).(*lua.LTable); ok && optsTbl != nil {
				if v, ok := optsTbl.RawGetString("since").(lua.LString); ok && string(v) != "" {
					d, err := time.ParseDuration(string(v))
					if err != nil {
						L.Push(lua.LNil)
						L.Push(lua.LString("invalid since: " + err.Error()))
						return 2
					}
					since = d
				}
			}
			entries, err := srv.db.ListDiscussed(session, since)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			out := L.NewTable()
			for _, e := range entries {
				row := L.NewTable()
				L.SetField(row, "tag", lua.LString(e.Tag))
				L.SetField(row, "value", lua.LString(e.Value))
				L.SetField(row, "timestamp", lua.LString(e.Timestamp.Format(time.RFC3339Nano)))
				out.Append(row)
			}
			L.Push(out)
			return 1
		}))
		L.SetField(discussedTable, "clear", L.NewFunction(func(L *lua.LState) int {
			session := L.CheckString(1)
			if session == "" {
				L.Push(lua.LNil)
				L.Push(lua.LString("session ID required"))
				return 2
			}
			count, err := srv.db.ClearDiscussed(session)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LNumber(count))
			return 1
		}))
		L.SetField(discussedTable, "prune", L.NewFunction(func(L *lua.LState) int {
			var ttl time.Duration
			if optsTbl, ok := L.Get(1).(*lua.LTable); ok && optsTbl != nil {
				if v, ok := optsTbl.RawGetString("ttl").(lua.LString); ok && string(v) != "" {
					d, err := time.ParseDuration(string(v))
					if err != nil {
						L.Push(lua.LNil)
						L.Push(lua.LString("invalid ttl: " + err.Error()))
						return 2
					}
					ttl = d
				}
			}
			count, err := srv.db.PruneDiscussed(ttl)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LNumber(count))
			return 1
		}))

		// R2383: populate the Lua mirror from state loaded during Server.New.
		srv.curation.refreshLuaTable(L)
		// R2358: pin mutator — append/move-to-top, refresh mirror.
		L.SetField(curationTable, "pin", L.NewFunction(func(L *lua.LState) int {
			chunkID := uint64(L.CheckNumber(1))
			fileID := uint64(L.OptNumber(2, 0))
			path := L.OptString(3, "")
			srv.curation.pin(L, chunkID, fileID, path)
			return 0
		}))
		// R2360: dismiss mutator — remove by chunkID, silent no-op when absent.
		L.SetField(curationTable, "dismiss", L.NewFunction(func(L *lua.LState) int {
			chunkID := uint64(L.CheckNumber(1))
			srv.curation.dismiss(L, chunkID)
			return 0
		}))
		// R2361: sweepOlder mutator — keep only the topmost pin.
		L.SetField(curationTable, "sweepOlder", L.NewFunction(func(L *lua.LState) int {
			srv.curation.sweepOlder(L)
			return 0
		}))

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
					case "fuzzy":
						opts.Fuzzy = true
					}
					// "combined" is the default — uses query as-is
				}
				if v := optsTable.RawGetString("filter_files"); v != lua.LNil {
					opts.FilterFiles = ExpandTildeSlice(luaStringSlice(v))
				}
				if v := optsTable.RawGetString("exclude_files"); v != lua.LNil {
					opts.ExcludeFiles = ExpandTildeSlice(luaStringSlice(v))
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
			// Multi-strategy only for combined queries; split modes
			// (contains/about/regex) need SearchSplit, not SearchMulti.
			if !opts.Fuzzy && opts.Contains == "" && opts.About == "" && len(opts.Regex) == 0 {
				opts.Multi = true
			}
			if sessionName != "" {
				sess := srv.GetOrCreateSession(sessionName)
				err = sess.RunSearch(query, func(cache *microfts2.ChunkCache) error {
					return SyncVoid(srv.db, func(db *DB) error {
						opts.Cache = cache
						var searchErr error
						results, searchErr = db.SearchGrouped(query, opts)
						return searchErr
					})
				})
			} else {
				results, err = Sync(srv.db, func(db *DB) ([]GroupedResult, error) {
					return db.SearchGrouped(query, opts)
				})
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
					L.SetField(chunkTable, "content", lua.LString(chunk.Content))
					L.SetField(chunkTable, "contentType", lua.LString(chunk.ContentType))
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
			indexed, _ := Sync(srv.db, func(db *DB) (bool, error) {
				return db.IsIndexed(path), nil
			})
			if !indexed {
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
			entries, err := Sync(srv.db, func(db *DB) ([]InboxEntry, error) {
				return db.Inbox(showAll, false)
			})
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
				L.SetField(row, "statusDate", lua.LString(e.StatusDate)) // R767
				result.RawSetInt(i+1, row)
			}
			L.Push(result)
			return 1
		}))

		// mcp:sort(table, property, isDate, descending) — sort Lua table in place
		// R758-R764
		L.SetField(tbl, "sort", L.NewFunction(func(L *lua.LState) int {
			tbl := L.CheckTable(1)
			property := L.CheckString(2)
			isDate := false
			if L.GetTop() >= 3 {
				isDate = L.CheckBool(3)
			}
			descending := false
			if L.GetTop() >= 4 {
				descending = L.CheckBool(4)
			}
			_ = isDate // date format (YYYY-MM-DD) sorts correctly as string

			// Collect array entries
			n := tbl.Len()
			entries := make([]lua.LValue, n)
			for i := 1; i <= n; i++ {
				entries[i-1] = tbl.RawGetInt(i)
			}

			// Sort
			sort.SliceStable(entries, func(i, j int) bool {
				vi := getField(entries[i], property)
				vj := getField(entries[j], property)
				// Nil/missing sort to end
				if vi == "" && vj == "" {
					return false
				}
				if vi == "" {
					return false
				}
				if vj == "" {
					return true
				}
				cmp := strings.Compare(strings.ToLower(vi), strings.ToLower(vj))
				if descending {
					return cmp > 0
				}
				return cmp < 0
			})

			// Write back
			for i, v := range entries {
				tbl.RawSetInt(i+1, v)
			}

			L.Push(tbl)
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

		// mcp.setTags(path, tags) — bulk tag write on a file
		// CRC: crc-Server.md | Seq: seq-message.md | R768-R772
		L.SetField(tbl, "setTags", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			tagsTable := L.CheckTable(2)

			data, err := os.ReadFile(path)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			tb := ParseTagBlock(data)
			tagsTable.ForEach(func(k, v lua.LValue) {
				name := k.String()
				value := v.String()
				tb.Set(name, value)
				if name == "status" {
					tb.Set("status-date", time.Now().Format("2006-01-02"))
				}
			})

			if err := os.WriteFile(path, tb.Render(), 0644); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp.readMessage(path) — read message file, return tags + rendered body
		// CRC: crc-Server.md | Seq: seq-message.md | R773-R777
		L.SetField(tbl, "readMessage", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)

			data, err := os.ReadFile(path)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			tb := ParseTagBlock(data)

			// Build tags table from tag block only (R776)
			tagsResult := L.NewTable()
			for _, tag := range tb.Tags() {
				L.SetField(tagsResult, tag.Name, lua.LString(tag.Value))
			}

			// Render body via goldmark
			var buf bytes.Buffer
			body := tb.Body()
			if body != nil {
				goldmark.Convert(body, &buf)
			}

			result := L.NewTable()
			L.SetField(result, "tags", tagsResult)
			L.SetField(result, "html", lua.LString(buf.String()))
			L.Push(result)
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
			fid, err := Sync(srv.db, func(db *DB) (uint64, error) {
				return db.AddTmpFile(path, strategy, []byte(content))
			})
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
			if err := SyncVoid(srv.db, func(db *DB) error {
				return db.UpdateTmpFile(path, strategy, []byte(content))
			}); err != nil {
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
			if err := SyncVoid(srv.db, func(db *DB) error {
				return db.RemoveTmpFile(path)
			}); err != nil {
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
			paths, _ := Sync(srv.db, func(db *DB) ([]string, error) {
				return db.TmpFiles(), nil
			})
			result := L.NewTable()
			for i, p := range paths {
				result.RawSetInt(i+1, lua.LString(p))
			}
			L.Push(result)
			return 1
		}))

		// mcp.tmp_get(path) — return the stored content of an existing
		// tmp:// document. Errors when the path lacks the tmp:// prefix
		// or the document is absent from the overlay.
		// CRC: crc-Server.md | R2406
		L.SetField(tbl, "tmp_get", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			content, err := Sync(srv.db, func(db *DB) ([]byte, error) {
				return db.TmpContent(path)
			})
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LString(content))
			return 1
		}))

		// mcp:listSource(sourcePath, prototype) — list one directory level
		// with in-process classification. Replaces per-node subprocess calls.
		// CRC: crc-Server.md | R835-R848
		L.SetField(tbl, "listSource", L.NewFunction(func(L *lua.LState) int {
			sourcePath := L.CheckString(1)

			// Find matching source in config
			cfg, _ := Sync(srv.db, func(db *DB) (*Config, error) {
				return db.Config(), nil
			})
			var matchedSource *Source
			for i := range cfg.Sources {
				if cfg.Sources[i].Dir == sourcePath || strings.HasPrefix(sourcePath, cfg.Sources[i].Dir+"/") {
					matchedSource = &cfg.Sources[i]
					break
				}
			}
			if matchedSource == nil {
				L.Push(L.NewTable()) // R837: empty table if not in a source
				return 1
			}

			// Check prototype argument (R845-R848)
			var prototype *lua.LTable
			var hasNew bool
			var sessionCreate *lua.LFunction
			if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
				proto, ok := L.Get(2).(*lua.LTable)
				if !ok {
					L.ArgError(2, "prototype must be a table")
					return 0
				}
				prototype = proto
				// Check for type-specific "new" method via rawget (only on
				// the prototype itself). The inherited new() from the root
				// prototype captures the wrong prototype in its closure,
				// creating Object instances instead of the target type.
				// Types with their own new() override this correctly.
				newMethod := prototype.RawGetString("new")
				hasNew = newMethod != lua.LNil && newMethod.Type() == lua.LTFunction
				// Get session:create for the standard path
				sessionGlobal := L.GetGlobal("session")
				if sessionTbl, ok := sessionGlobal.(*lua.LTable); ok {
					if createVal := L.GetField(sessionTbl, "create"); createVal.Type() == lua.LTFunction {
						sessionCreate = createVal.(*lua.LFunction)
					}
				}
			}

			// wirePrototype applies prototype wiring to an entry table (R845-R848)
			wirePrototype := func(entryTbl *lua.LTable) *lua.LTable {
				if prototype == nil {
					return entryTbl
				}
				if hasNew {
					if err := L.CallByParam(lua.P{
						Fn:      L.GetField(prototype, "new"),
						NRet:    1,
						Protect: true,
					}, prototype, entryTbl); err != nil {
						log.Printf("listSource: prototype:new() failed: %v", err)
					} else {
						entryTbl = L.CheckTable(-1)
						L.Pop(1)
					}
				} else if sessionCreate != nil {
					if err := L.CallByParam(lua.P{
						Fn:      sessionCreate,
						NRet:    1,
						Protect: true,
					}, L.GetGlobal("session"), prototype, entryTbl); err != nil {
						log.Printf("listSource: session:create() failed: %v", err)
					} else {
						entryTbl = L.CheckTable(-1)
						L.Pop(1)
					}
				} else {
					L.SetMetatable(entryTbl, prototype)
				}
				return entryTbl
			}

			// Read directory entries (R836)
			entries, err := os.ReadDir(sourcePath)
			if err != nil {
				L.Push(L.NewTable())
				return 1
			}

			// Sort: dirs first, then alphabetically (R841)
			sort.Slice(entries, func(i, j int) bool {
				di, dj := entries[i].IsDir(), entries[j].IsDir()
				if di != dj {
					return di
				}
				return entries[i].Name() < entries[j].Name()
			})

			// Get missing files for this source (R843, R844)
			missingPaths := make(map[string]bool)
			missing, _ := Sync(srv.db, func(db *DB) ([]MissingRecord, error) {
				return db.Missing()
			})
			for _, m := range missing {
				// Only track missing files at the listed directory level
				if strings.HasPrefix(m.Path, sourcePath+"/") {
					rel := m.Path[len(sourcePath)+1:]
					if !strings.Contains(rel, "/") {
						missingPaths[m.Path] = true
					}
				}
			}

			result := L.NewTable()
			idx := 0
			seenNames := make(map[string]bool)

			for _, entry := range entries {
				name := entry.Name()
				fullPath := filepath.Join(sourcePath, name)
				isDir := entry.IsDir()

				// Compute relPath from source root
				relPath, _ := filepath.Rel(matchedSource.Dir, fullPath)

				// Classify via ShowWhy (R839, R840)
				why, err := cfg.ShowWhy(fullPath)
				state := "unresolved"
				var whyPatterns, whySources string
				whyConflict := false
				if err == nil && why != nil {
					state = why.Status
					whyPatterns = strings.Join(why.Patterns, ", ")
					whySources = strings.Join(why.Sources, ", ")
					whyConflict = why.Conflict
				}

				// Check for ignore files in directories (R842)
				hasIgnoreFile := false
				if isDir {
					for _, ignName := range []string{".gitignore", ".arkignore"} {
						if _, err := os.Stat(filepath.Join(fullPath, ignName)); err == nil {
							hasIgnoreFile = true
							break
						}
					}
				}

				// Check if missing (R843)
				isMissing := missingPaths[fullPath]

				// Build entry table (R838)
				entryTbl := L.NewTable()
				L.SetField(entryTbl, "name", lua.LString(name))
				L.SetField(entryTbl, "relPath", lua.LString(relPath))
				L.SetField(entryTbl, "fullPath", lua.LString(fullPath))
				L.SetField(entryTbl, "isDir", lua.LBool(isDir))
				L.SetField(entryTbl, "state", lua.LString(state))
				L.SetField(entryTbl, "whyPatterns", lua.LString(whyPatterns))
				L.SetField(entryTbl, "whySources", lua.LString(whySources))
				L.SetField(entryTbl, "whyConflict", lua.LBool(whyConflict))
				L.SetField(entryTbl, "isMissing", lua.LBool(isMissing))
				L.SetField(entryTbl, "hasIgnoreFile", lua.LBool(hasIgnoreFile))

				entryTbl = wirePrototype(entryTbl)

				idx++
				result.RawSetInt(idx, entryTbl)
				seenNames[name] = true
			}

			// Add missing files not on disk at this directory level (R843)
			for path := range missingPaths {
				name := filepath.Base(path)
				if seenNames[name] {
					continue
				}
				relPath, _ := filepath.Rel(matchedSource.Dir, path)

				entryTbl := L.NewTable()
				L.SetField(entryTbl, "name", lua.LString(name))
				L.SetField(entryTbl, "relPath", lua.LString(relPath))
				L.SetField(entryTbl, "fullPath", lua.LString(path))
				L.SetField(entryTbl, "isDir", lua.LBool(false))
				L.SetField(entryTbl, "state", lua.LString("included"))
				L.SetField(entryTbl, "whyPatterns", lua.LString(""))
				L.SetField(entryTbl, "whySources", lua.LString(""))
				L.SetField(entryTbl, "whyConflict", lua.LBool(false))
				L.SetField(entryTbl, "isMissing", lua.LBool(true))
				L.SetField(entryTbl, "hasIgnoreFile", lua.LBool(false))

				entryTbl = wirePrototype(entryTbl)

				idx++
				result.RawSetInt(idx, entryTbl)
			}

			L.Push(result)
			return 1
		}))

		// mcp.definedTags() — read-only list of defined tags + descriptions.
		// Same store as POST /tags/defs. Sorted ascending by tag; duplicate
		// tag names deduplicated, keeping the first non-empty description.
		// CRC: crc-Server.md | R2364
		L.SetField(tbl, "definedTags", L.NewFunction(func(L *lua.LState) int {
			defs, err := Sync(srv.db, func(db *DB) ([]TagDefInfo, error) {
				return db.TagDefs(nil)
			})
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			seen := make(map[string]int, len(defs))
			unique := defs[:0]
			for _, d := range defs {
				if i, ok := seen[d.Tag]; ok {
					if unique[i].Description == "" && d.Description != "" {
						unique[i].Description = d.Description
					}
					continue
				}
				seen[d.Tag] = len(unique)
				unique = append(unique, d)
			}
			sort.Slice(unique, func(i, j int) bool { return unique[i].Tag < unique[j].Tag })
			result := L.NewTable()
			for i, d := range unique {
				row := L.NewTable()
				L.SetField(row, "tag", lua.LString(d.Tag))
				L.SetField(row, "description", lua.LString(d.Description))
				result.RawSetInt(i+1, row)
			}
			L.Push(result)
			return 1
		}))

		// mcp.chunkInfo(chunkID) — per-chunk metadata bundle for the
		// curation workshop's chunk card. Sync read; errors follow
		// (nil, errstring); unknown chunk → (nil, "chunk not found").
		// CRC: crc-Server.md | R2386, R2389
		L.SetField(tbl, "chunkInfo", L.NewFunction(func(L *lua.LState) int {
			chunkID := uint64(L.CheckNumber(1))
			info, err := Sync(srv.db, func(db *DB) (ChunkInfo, error) {
				return db.ChunkInfo(chunkID)
			})
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			row := L.NewTable()
			L.SetField(row, "chunkID", lua.LNumber(info.ChunkID))
			L.SetField(row, "fileID", lua.LNumber(info.FileID))
			L.SetField(row, "path", lua.LString(info.Path))
			L.SetField(row, "range", lua.LString(info.Range))
			L.SetField(row, "byteStart", lua.LNumber(info.ByteStart))
			L.SetField(row, "byteEnd", lua.LNumber(info.ByteEnd))
			L.SetField(row, "writable", lua.LBool(info.Writable))
			L.SetField(row, "commentSyntax", lua.LString(info.CommentSyntax))
			L.Push(row)
			return 1
		}))

		// mcp.chunkText(chunkID) — chunk text bytes by chunkID. Wraps
		// DB.ChunkTextByID, which routes through ChunkInfo + ChunkText.
		// CRC: crc-Server.md | R2402
		L.SetField(tbl, "chunkText", L.NewFunction(func(L *lua.LState) int {
			chunkID := uint64(L.CheckNumber(1))
			text, err := Sync(srv.db, func(db *DB) ([]byte, error) {
				return db.ChunkTextByID(chunkID)
			})
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LString(text))
			return 1
		}))

		// mcp.parseTagBlock(text) — parse the leading @name: value block
		// of text. Pure function; wraps ParseTagBlock. Returns a Lua
		// table {tags = [{name, value}, ...], body}.
		// CRC: crc-Server.md | R2404, R2405
		L.SetField(tbl, "parseTagBlock", L.NewFunction(func(L *lua.LState) int {
			text := L.CheckString(1)
			tb := ParseTagBlock([]byte(text))
			result := L.NewTable()
			tags := L.NewTable()
			for i, t := range tb.Tags() {
				row := L.NewTable()
				L.SetField(row, "name", lua.LString(t.Name))
				L.SetField(row, "value", lua.LString(t.Value))
				tags.RawSetInt(i+1, row)
			}
			L.SetField(result, "tags", tags)
			L.SetField(result, "body", lua.LString(tb.Body()))
			L.Push(result)
			return 1
		}))

		// mcp.extractTagValues(text, strategy) — extract every
		// @name: value found anywhere in the text, using the same
		// scanner the indexer uses. Catches mid-chunk tags and @id
		// lines that ParseTagBlock (leading-block-only) misses. Pure
		// function over content + chunker strategy name.
		// CRC: crc-Server.md | R2426
		L.SetField(tbl, "extractTagValues", L.NewFunction(func(L *lua.LState) int {
			text := L.CheckString(1)
			strategy := L.OptString(2, "markdown")
			values := ExtractTagValues([]byte(text), strategy)
			result := L.NewTable()
			for i, tv := range values {
				row := L.NewTable()
				L.SetField(row, "name", lua.LString(tv.Tag))
				L.SetField(row, "value", lua.LString(tv.Value))
				result.RawSetInt(i+1, row)
			}
			L.Push(result)
			return 1
		}))

		// mcp.suggestExtLocator(chunkID) — three-layer locator algorithm.
		// CRC: crc-Server.md | R2397, R2398, R2399, R2400, R2401
		L.SetField(tbl, "suggestExtLocator", L.NewFunction(func(L *lua.LState) int {
			chunkID := uint64(L.CheckNumber(1))
			sug, err := Sync(srv.db, func(db *DB) (LocatorSuggestion, error) {
				return db.SuggestExtLocator(chunkID)
			})
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			row := L.NewTable()
			L.SetField(row, "base", lua.LString(sug.Base))
			L.SetField(row, "baseValue", lua.LString(sug.BaseValue))
			L.SetField(row, "locator", lua.LString(sug.LocatorText))
			L.SetField(row, "locatorKind", lua.LString(sug.LocatorKind))
			L.SetField(row, "locatorText", lua.LString(sug.LocatorText))
			L.SetField(row, "withinFileDupCount", lua.LNumber(sug.WithinFileDupCount))
			scope := L.NewTable()
			L.SetField(scope, "chunks", lua.LNumber(sug.CrossFileScope.Chunks))
			L.SetField(scope, "files", lua.LNumber(sug.CrossFileScope.Files))
			L.SetField(row, "crossFileScope", scope)
			L.Push(row)
			return 1
		}))

		// mcp.setExtTag(targetSpec, tag, value) — author an @ext routing
		// into the mirror tree. CRC: crc-Server.md | R2393, R2395
		L.SetField(tbl, "setExtTag", L.NewFunction(func(L *lua.LState) int {
			targetSpec := L.CheckString(1)
			tag := L.CheckString(2)
			value := L.CheckString(3)
			err := SyncVoid(srv.db, func(db *DB) error {
				return db.SetExtTag(targetSpec, tag, value)
			})
			if err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp.removeExtTag(targetSpec, tag) — remove an @ext routing
		// from its mirror file (silent no-op when missing).
		// CRC: crc-Server.md | R2396
		L.SetField(tbl, "removeExtTag", L.NewFunction(func(L *lua.LState) int {
			targetSpec := L.CheckString(1)
			tag := L.CheckString(2)
			err := SyncVoid(srv.db, func(db *DB) error {
				return db.RemoveExtTag(targetSpec, tag)
			})
			if err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp.replaceRegion(path, byteStart, byteEnd, newText) — atomic
		// byte-range write through DB.ReplaceRegion. Returns (true, nil)
		// or (false, errstring). CRC: crc-Server.md | R2390, R2391
		L.SetField(tbl, "replaceRegion", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			byteStart := uint64(L.CheckNumber(2))
			byteEnd := uint64(L.CheckNumber(3))
			newText := L.CheckString(4)
			err := SyncVoid(srv.db, func(db *DB) error {
				return db.ReplaceRegion(path, byteStart, byteEnd, []byte(newText))
			})
			if err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		// mcp.suggestTagNames(chunkID, k) — chunk → tag-name candidates.
		// CRC: crc-Server.md | R2258, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "suggestTagNames", L.NewFunction(func(L *lua.LState) int {
			chunkID := uint64(L.CheckNumber(1))
			k := int(L.CheckNumber(2))
			out, err := srv.librarian.SuggestTagNames(chunkID, k)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, s := range out {
				row := L.NewTable()
				L.SetField(row, "tag", lua.LString(s.Tag))
				L.SetField(row, "score", lua.LNumber(s.Score))
				files := L.NewTable()
				for j, f := range s.MotivatingFiles {
					fr := L.NewTable()
					L.SetField(fr, "fileID", lua.LNumber(f.FileID))
					L.SetField(fr, "path", lua.LString(f.Path))
					L.SetField(fr, "score", lua.LNumber(f.Score))
					files.RawSetInt(j+1, fr)
				}
				L.SetField(row, "motivatingFiles", files)
				result.RawSetInt(i+1, row)
			}
			L.Push(result)
			return 1
		}))

		// mcp.chunksForTag(tag, k) — tag → chunk candidates (live).
		// CRC: crc-Server.md | R2259, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "chunksForTag", L.NewFunction(func(L *lua.LState) int {
			tag := L.CheckString(1)
			k := int(L.CheckNumber(2))
			out, err := srv.librarian.ChunksForTag(tag, k)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, s := range out {
				result.RawSetInt(i+1, chunkSuggestionToLua(L, s))
			}
			L.Push(result)
			return 1
		}))

		// mcp.chunksForTagDef(tag, fileID, k) — tag-def → chunk candidates (live).
		// CRC: crc-Server.md | R2260, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "chunksForTagDef", L.NewFunction(func(L *lua.LState) int {
			tag := L.CheckString(1)
			fileID := uint64(L.CheckNumber(2))
			k := int(L.CheckNumber(3))
			out, err := srv.librarian.ChunksForTagDef(tag, fileID, k)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, s := range out {
				result.RawSetInt(i+1, chunkSuggestionToLua(L, s))
			}
			L.Push(result)
			return 1
		}))

		// mcp.topKChunksForTag(tag, k) — cached top-K with alibi-stamp filter.
		// CRC: crc-Server.md | R2261, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "topKChunksForTag", L.NewFunction(func(L *lua.LState) int {
			tag := L.CheckString(1)
			k := int(L.CheckNumber(2))
			out, err := srv.librarian.TopKChunksForTag(tag, k)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, s := range out {
				result.RawSetInt(i+1, chunkSuggestionToLua(L, s))
			}
			L.Push(result)
			return 1
		}))

		// mcp.relatedTags(tag, k) — tags whose ED vectors are nearest the
		// focused tag's ED records.
		// CRC: crc-Server.md | R2262, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "relatedTags", L.NewFunction(func(L *lua.LState) int {
			tag := L.CheckString(1)
			k := int(L.CheckNumber(2))
			out, err := srv.librarian.RelatedTags(tag, k)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, s := range out {
				result.RawSetInt(i+1, tagSimilarityToLua(L, s))
			}
			L.Push(result)
			return 1
		}))

		// mcp.tagPairConflict(tagA, tagB) — max-pair cosine across two tags'
		// ED records. Returns a single table (not an array).
		// CRC: crc-Server.md | R2263, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "tagPairConflict", L.NewFunction(func(L *lua.LState) int {
			tagA := L.CheckString(1)
			tagB := L.CheckString(2)
			out, err := srv.librarian.TagPairConflict(tagA, tagB)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(tagSimilarityToLua(L, out))
			return 1
		}))

		// mcp.tagDrift(tag) — within-tag pairwise cosine across one tag's
		// ED records, sorted by score descending.
		// CRC: crc-Server.md | R2264, R2266, R2267, R2268, R2269, R2270
		L.SetField(tbl, "tagDrift", L.NewFunction(func(L *lua.LState) int {
			tag := L.CheckString(1)
			out, err := srv.librarian.TagDrift(tag)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			result := L.NewTable()
			for i, p := range out {
				row := L.NewTable()
				L.SetField(row, "fileIDA", lua.LNumber(p.FileIDA))
				L.SetField(row, "pathA", lua.LString(p.PathA))
				L.SetField(row, "fileIDB", lua.LNumber(p.FileIDB))
				L.SetField(row, "pathB", lua.LString(p.PathB))
				L.SetField(row, "score", lua.LNumber(p.Score))
				result.RawSetInt(i+1, row)
			}
			L.Push(result)
			return 1
		}))

		// mcp.sweepHotCorrelationsAsync() — fire-and-forget HC sweep.
		// Enqueues through Librarian.SweepHotCorrelationsAsync; returns
		// nothing. Caller subscribes to tmp://sweep/hot-correlations.md
		// via mcp.subscribe for progress/terminal state.
		// CRC: crc-Server.md | R2408, R2410
		L.SetField(tbl, "sweepHotCorrelationsAsync", L.NewFunction(func(L *lua.LState) int {
			srv.librarian.SweepHotCorrelationsAsync()
			return 0
		}))

		// mcp.sweepHotCorrelations() — corpus-wide HC sweep through
		// enqueueWrite. Mirrors HandleSweepCorrelations exactly.
		// CRC: crc-Server.md | R2265, R2270
		L.SetField(tbl, "sweepHotCorrelations", L.NewFunction(func(L *lua.LState) int {
			type outcome struct {
				result *HCSweepResult
				err    error
			}
			ch := make(chan outcome, 1)
			if err := SyncVoid(srv.db, func(db *DB) error {
				db.enqueueWrite(func(_ *microfts2.DB) {
					res, err := srv.librarian.SweepHotCorrelations()
					ch <- outcome{res, err}
				})
				return nil
			}); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			o := <-ch
			if o.err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(o.err.Error()))
				return 2
			}
			result := L.NewTable()
			if o.result == nil {
				L.SetField(result, "status", lua.LString("embedding-unavailable"))
				L.Push(result)
				return 1
			}
			L.SetField(result, "startedAt", lua.LString(o.result.StartedAt.Format(time.RFC3339)))
			L.SetField(result, "completedAt", lua.LString(o.result.CompletedAt.Format(time.RFC3339)))
			L.SetField(result, "durationMs", lua.LNumber(o.result.DurationMS))
			L.SetField(result, "changedEDs", lua.LNumber(o.result.ChangedEDs))
			L.SetField(result, "changedECs", lua.LNumber(o.result.ChangedECs))
			L.SetField(result, "tagsRebuilt", lua.LNumber(o.result.TagsRebuilt))
			L.SetField(result, "tagsTouched", lua.LNumber(o.result.TagsTouched))
			L.SetField(result, "orphanTotal", lua.LNumber(o.result.OrphanTotal))
			L.SetField(result, "fromScratch", lua.LBool(o.result.FromScratch))
			L.Push(result)
			return 1
		}))

		// mcp.findConnections(chunkIDs, opts) — fire-and-forget bridge
		// for the find-connections sidecar. Returns the request ID
		// string immediately. Returns (nil, "agent unavailable") when
		// no `ark connections --wait` consumer has been observed
		// inside the availability window; returns (nil, "chunkIDs
		// empty") when the array is empty.
		// CRC: crc-Server.md | Seq: seq-find-connections.md | R2313, R2322, R2323, R2324, R2325
		L.SetField(tbl, "findConnections", L.NewFunction(func(L *lua.LState) int {
			arr := L.CheckTable(1)
			ids := make([]uint64, 0, arr.Len())
			arr.ForEach(func(_, v lua.LValue) {
				if n, ok := v.(lua.LNumber); ok {
					if n >= 0 {
						ids = append(ids, uint64(n))
					}
				}
			})
			opts := FindConnectionsOpts{}
			if optsTbl, ok := L.Get(2).(*lua.LTable); ok && optsTbl != nil {
				if v, ok := optsTbl.RawGetString("timeoutSeconds").(lua.LNumber); ok {
					opts.TimeoutSeconds = int(v)
				}
			}
			if srv.librarian == nil {
				L.Push(lua.LNil)
				L.Push(lua.LString("agent unavailable"))
				return 2
			}
			// mcp.findConnections is the 1G call shape (chunkID array,
			// turbo mode). The unified sys.findConnections supports
			// path:range + text inputs and the normal-mode pipeline.
			// R2568
			id, err := srv.librarian.FindConnectionsByChunkIDs(ids, opts)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LString(id))
			return 1
		}))

		// mcp.subscribe(sessionID, filter) — register/replace a
		// subscription for sessionID. Filter mirrors TagSub with
		// lowerCamelCase fields. Replace-by-(session, tag): drops any
		// prior sub on the same tag for this session, then appends
		// the new one. Starts the listening goroutine on first sub
		// for the session.
		// CRC: crc-Server.md | Seq: seq-tmp-subscription.md | R2277, R2288, R2289, R2290, R2293, R2299
		L.SetField(tbl, "subscribe", L.NewFunction(func(L *lua.LState) int {
			sessionID := L.CheckString(1)
			filterTbl := L.CheckTable(2)
			sub, err := buildTagSubFromLua(filterTbl)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			if srv.pubsub == nil {
				L.Push(lua.LNil)
				L.Push(lua.LString("pubsub unavailable"))
				return 2
			}
			// R2289: replace-by-(session, predicate). Drop any prior
			// sub on the same name for this session, then append the
			// new one.
			srv.pubsub.Cancel(sessionID, strings.ToLower(sub.Predicate.NameStr), "")
			srv.pubsub.Subscribe(sessionID, []*TagSub{sub})
			srv.ensureListenLoop(sessionID)
			return 0
		}))

		// mcp.onpublish(sessionID, callback) — register/replace the
		// per-session callback. One callback per session.
		// CRC: crc-Server.md | Seq: seq-tmp-subscription.md | R2291
		L.SetField(tbl, "onpublish", L.NewFunction(func(L *lua.LState) int {
			sessionID := L.CheckString(1)
			cb := L.CheckFunction(2)
			srv.listenMu.Lock()
			if srv.onpublishCBs == nil {
				srv.onpublishCBs = make(map[string]*lua.LFunction)
			}
			srv.onpublishCBs[sessionID] = cb
			srv.listenMu.Unlock()
			return 0
		}))

		// mcp.cancel(sessionID, tag) — drop the subscription on tag
		// for session. Empty tag drops all subs for the session and
		// stops the listening goroutine.
		// CRC: crc-Server.md | Seq: seq-tmp-subscription.md | R2292, R2300
		L.SetField(tbl, "cancel", L.NewFunction(func(L *lua.LState) int {
			sessionID := L.CheckString(1)
			tag := L.OptString(2, "")
			if srv.pubsub == nil {
				return 0
			}
			srv.pubsub.Cancel(sessionID, tag, "")
			srv.maybeStopListenLoop(sessionID)
			return 0
		}))

		return nil
	})
	if err != nil {
		log.Printf("ui: register lua functions failed: %v", err)
	}
}

// buildTagSubFromLua decodes a Lua filter table into a TagSub.
// Field shape (R2289, R2442, R2462):
//   - `tag` (required string): sigil-form match query
//   - `kind` (optional string): "tag" (default) or "file-tag"
//   - `filterFiles` / `excludeFiles` (optional string arrays)
//
// CRC: crc-Server.md | R2289, R2442, R2462
func buildTagSubFromLua(t *lua.LTable) (*TagSub, error) {
	tagV := t.RawGetString("tag")
	tag, ok := tagV.(lua.LString)
	if !ok || string(tag) == "" {
		return nil, fmt.Errorf("subscribe: tag (string) required")
	}
	p, err := ParseMatchSyntax(string(tag))
	if err != nil {
		return nil, fmt.Errorf("subscribe: bad tag %q: %w", string(tag), err)
	}
	kind := TagSubChunk
	if v := t.RawGetString("kind"); v != lua.LNil {
		s, ok := v.(lua.LString)
		if !ok {
			return nil, fmt.Errorf("subscribe: kind must be a string")
		}
		switch string(s) {
		case "", "tag":
			kind = TagSubChunk
		case "file-tag":
			kind = TagSubFile
		default:
			return nil, fmt.Errorf("subscribe: unknown kind %q (want \"tag\" or \"file-tag\")", string(s))
		}
	}
	sub := &TagSub{Kind: kind, Predicate: p}
	if v := t.RawGetString("filterFiles"); v != lua.LNil {
		arr, ok := v.(*lua.LTable)
		if !ok {
			return nil, fmt.Errorf("subscribe: filterFiles must be an array")
		}
		sub.FilterFiles = luaArrayToStrings(arr)
	}
	if v := t.RawGetString("excludeFiles"); v != lua.LNil {
		arr, ok := v.(*lua.LTable)
		if !ok {
			return nil, fmt.Errorf("subscribe: excludeFiles must be an array")
		}
		sub.ExcludeFiles = luaArrayToStrings(arr)
	}
	return sub, nil
}

// luaArrayToStrings extracts the array part of a Lua table into a
// []string. Non-string entries are skipped.
func luaArrayToStrings(t *lua.LTable) []string {
	var out []string
	t.ForEach(func(k, v lua.LValue) {
		if _, ok := k.(lua.LNumber); !ok {
			return
		}
		if s, ok := v.(lua.LString); ok {
			out = append(out, string(s))
		}
	})
	return out
}

// buildEventArray builds a 1-indexed Lua array of event tables from
// the compressed []Event. Each event table mirrors the Go Event
// struct field-for-field with lowerCamelCase naming (R2266).
// Future Event fields can be added to the struct and surfaced here.
// CRC: crc-Server.md | R2297, R2298
func buildEventArray(L *lua.LState, events []Event) *lua.LTable {
	arr := L.NewTable()
	for i, e := range events {
		row := L.NewTable()
		L.SetField(row, "tag", lua.LString(e.Tag))
		L.SetField(row, "value", lua.LString(e.Value))
		L.SetField(row, "path", lua.LString(e.Path))
		L.SetField(row, "time", lua.LString(e.Time.Format(time.RFC3339Nano)))
		arr.RawSetInt(i+1, row)
	}
	return arr
}

// chunkSuggestionToLua converts a ChunkSuggestion into the Lua table
// shape shared by mcp.chunksForTag, mcp.chunksForTagDef, and
// mcp.topKChunksForTag (R2266 lowerCamelCase fields, R2267 IDs as
// numbers).
func chunkSuggestionToLua(L *lua.LState, s ChunkSuggestion) *lua.LTable {
	row := L.NewTable()
	L.SetField(row, "chunkID", lua.LNumber(s.ChunkID))
	L.SetField(row, "fileID", lua.LNumber(s.FileID))
	L.SetField(row, "path", lua.LString(s.Path))
	L.SetField(row, "score", lua.LNumber(s.Score))
	defs := L.NewTable()
	for i, d := range s.MotivatingDefs {
		dr := L.NewTable()
		L.SetField(dr, "fileID", lua.LNumber(d.FileID))
		L.SetField(dr, "path", lua.LString(d.Path))
		L.SetField(dr, "score", lua.LNumber(d.Score))
		defs.RawSetInt(i+1, dr)
	}
	L.SetField(row, "motivatingDefs", defs)
	return row
}

// tagSimilarityToLua converts a TagSimilarity into the Lua table
// shape shared by mcp.relatedTags and mcp.tagPairConflict (R2266,
// R2267).
func tagSimilarityToLua(L *lua.LState, s TagSimilarity) *lua.LTable {
	row := L.NewTable()
	L.SetField(row, "tag", lua.LString(s.Tag))
	L.SetField(row, "score", lua.LNumber(s.Score))
	L.SetField(row, "srcFileID", lua.LNumber(s.SrcFileID))
	L.SetField(row, "srcPath", lua.LString(s.SrcPath))
	L.SetField(row, "dstFileID", lua.LNumber(s.DstFileID))
	L.SetField(row, "dstPath", lua.LString(s.DstPath))
	return row
}

// getField extracts a string field from a Lua table value.
// Returns "" if the value is not a table or the field is nil.
func getField(v lua.LValue, field string) string {
	if tbl, ok := v.(*lua.LTable); ok {
		if fv := tbl.RawGetString(field); fv != lua.LNil {
			return fv.String()
		}
	}
	return ""
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
