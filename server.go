package ark

// CRC: crc-Server.md | Seq: seq-server-startup.md

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Server is an HTTP server on a Unix domain socket.
type Server struct {
	db       *DB
	listener net.Listener
	pidPath  string
	noScan   bool
}

// ServeOpts controls server behavior.
type ServeOpts struct {
	NoScan bool
}

// Serve starts the ark server: binds socket, opens DB, reconciles, serves.
func Serve(dbPath string, opts ServeOpts) error {
	socketPath := filepath.Join(dbPath, "ark.sock")

	// Highlander: try to bind the socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		// Check if it's a stale socket
		conn, dialErr := net.Dial("unix", socketPath)
		if dialErr == nil {
			conn.Close()
			return fmt.Errorf("another ark server is already running for %s", dbPath)
		}
		// Stale socket — remove and retry
		os.Remove(socketPath)
		listener, err = net.Listen("unix", socketPath)
		if err != nil {
			return fmt.Errorf("bind socket: %w", err)
		}
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	// Write PID file
	pidPath := pidFilePath(dbPath)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}
	defer os.Remove(pidPath)

	// Open database
	db, err := Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	srv := &Server{
		db:       db,
		listener: listener,
		pidPath:  pidPath,
		noScan:   opts.NoScan,
	}

	// Startup reconciliation
	if !opts.NoScan {
		log.Println("startup reconciliation: scanning...")
		if _, err := db.Scan(); err != nil {
			log.Printf("scan error: %v", err)
		}
		log.Println("startup reconciliation: refreshing...")
		if err := db.Refresh(nil); err != nil {
			log.Printf("refresh error: %v", err)
		}
		log.Println("startup reconciliation complete")
	}

	// Set up routes
	mux := http.NewServeMux()
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

	log.Printf("ark server listening on %s", socketPath)
	return http.Serve(listener, mux)
}

func pidFilePath(dbPath string) string {
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
	Query    string `json:"query"`
	About    string `json:"about"`
	Contains string `json:"contains"`
	Regex    string `json:"regex"`
	K        int    `json:"k"`
	Scores   bool   `json:"scores"`
	After    int64  `json:"after"`
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

	opts := SearchOpts{
		K:        req.K,
		Scores:   req.Scores,
		After:    req.After,
		About:    req.About,
		Contains: req.Contains,
		Regex:    req.Regex,
	}

	var results []SearchResultEntry
	var err error
	if req.About != "" || req.Contains != "" || req.Regex != "" {
		results, err = srv.db.SearchSplit(opts)
	} else {
		results, err = srv.db.SearchCombined(req.Query, opts)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, results)
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
	}
}
