package ark

// CRC: crc-Server.md, crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleRecallReserveNonce returns the next per-server monotonic nonce. With
// `{"luhmann": true}` it also registers the nonce as a CLI-bloodhound pool
// secretary in the watcher (the reservation doubles as pool registration, so
// the go-between learns the nonce Luhmann will spawn with). R3033
// CRC: crc-Server.md | R2755, R3033
func (srv *Server) handleRecallReserveNonce(w http.ResponseWriter, r *http.Request) {
	n := srv.recallAgentBuilder.ReserveNonce()
	var req struct {
		Luhmann       bool   `json:"luhmann"`
		Class         string `json:"class"`
		ParentSession string `json:"parent_session"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional
	// R3305: record who reserved this nonce, so a later transcript lookup has a
	// parent to resolve against. Independent of --luhmann: a session's own
	// secretary needs this exactly as much as a pool one.
	srv.recallAgentBuilder.RegisterParentSession(uint32(n), req.ParentSession)
	if req.Luhmann && srv.recallWatcher != nil {
		// R3299: the class is declared by the reserving caller. Default to
		// bloodhound so an older Luhmann that predates the flag keeps working.
		class := req.Class
		if class == "" {
			class = bloodhoundPoolClass
		}
		srv.recallWatcher.RegisterPoolSecretary(class, uint64(n)) // R3033, R3299
	}
	writeJSON(w, map[string]any{"nonce": n})
}

// recallSurfaceRequest mirrors the CLI body for `surface`. R2900
type recallSurfaceRequest struct {
	Fire   string `json:"fire"` // composite <session>-<fire> token (R2901)
	Loc    string `json:"loc"`  // candidate <path>:<range> (R2900)
	Reason string `json:"reason"`
}

// handleRecallSurface appends one `## Surface:` item to the
// in-flight result-doc builder. CRC: crc-Server.md | R2900
func (srv *Server) handleRecallSurface(w http.ResponseWriter, r *http.Request) {
	var req recallSurfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Fire == "" || req.Loc == "" || req.Reason == "" {
		http.Error(w, "fire, loc, reason required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.SurfaceItem(req.Fire, req.Loc, req.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// recallRecommendRequest mirrors the CLI body for `recommend`. R2757, R2900
type recallRecommendRequest struct {
	Fire   string `json:"fire"` // composite <session>-<fire> token (R2901)
	Loc    string `json:"loc"`  // candidate <path>:<range> (R2900)
	Tag    string `json:"tag"`
	Reason string `json:"reason"`
}

// handleRecallRecommend appends one `## Recommend:` item to the
// in-flight result-doc builder. CRC: crc-Server.md | R2757
func (srv *Server) handleRecallRecommend(w http.ResponseWriter, r *http.Request) {
	var req recallRecommendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Fire == "" || req.Loc == "" || req.Tag == "" || req.Reason == "" {
		http.Error(w, "fire, loc, tag, reason required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.RecommendItem(req.Fire, req.Loc, req.Tag, req.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// recallFindingRequest mirrors the CLI body for `finding`. R2943
type recallFindingRequest struct {
	Cookie string `json:"cookie"` // bloodhound cookie <session>-b<B>
	Loc    string `json:"loc"`    // optional candidate <path>:<range>
	Answer string `json:"answer"` // optional synthesized answer text
	Note   string `json:"note"`   // optional one-line note on a -loc finding
}

// handleRecallFinding appends one `## Finding:` item to the in-flight
// directed-search builder. CRC: crc-Server.md | R2943
func (srv *Server) handleRecallFinding(w http.ResponseWriter, r *http.Request) {
	var req recallFindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Cookie == "" {
		http.Error(w, "cookie required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.FindingItem(req.Cookie, req.Loc, req.Answer, req.Note); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// recallCloseRequest mirrors the CLI body for `close`. R2758
type recallCloseRequest struct {
	Fire             string `json:"fire"` // composite <session>-<fire> token (R2901)
	Nonce            uint32 `json:"nonce"`
	PreserveCuration bool   `json:"preserveCuration"`
}

// recallContextRequest is the CLI body for `context`. R2777
type recallContextRequest struct {
	Nonce uint32 `json:"nonce"`
}

// handleRecallContext reports the calling subagent's current
// context fill (cache_creation + cache_read from the most recent
// assistant turn in its JSONL). Used by the lotto-tube recall
// agent to self-recycle when context grows past a limit.
// CRC: crc-Server.md | R2777
func (srv *Server) handleRecallContext(w http.ResponseWriter, r *http.Request) {
	var req recallContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Nonce == 0 {
		http.Error(w, "nonce required", http.StatusBadRequest)
		return
	}
	tokens, ok := srv.recallAgentBuilder.ContextTokens(req.Nonce)
	writeJSON(w, map[string]any{"tokens": tokens, "found": ok})
}

// handleRecallClose is the single cleanup verb. CRC: crc-Server.md | R2758
func (srv *Server) handleRecallClose(w http.ResponseWriter, r *http.Request) {
	var req recallCloseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Fire == "" {
		http.Error(w, "fire required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.CloseResult(req.Fire, req.Nonce, req.PreserveCuration); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// handleRecallNext is the daemon's single loop verb (R2857, R2858):
// idempotent subscribe + context-gate + lowest-fire pending curation
// doc, blocking (true lotto-tube) until one exists. GET so any client
// — the agent's CLI, a bash loop, an IDE plugin — drives it the same
// way. The `X-Recall-Exit` header signals the exit directive; the
// body is the crank-handle prose.
// ClientSessionField is the query/body field a client's own session identity
// travels in (R3317). Exported because it is a wire contract shared by both
// ends: `cmd/ark` writes it, these handlers read it. Deliberately not
// `session` — that word already means "which queue" on some verbs, and one
// word meaning two things is the overload this field exists to end.
const ClientSessionField = "client_session"

// CRC: crc-Server.md | R2857, R2858, R3317, R3318, R3319
func (srv *Server) handleRecallNext(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.ParseUint(r.URL.Query().Get("nonce"), 10, 32)
	if err != nil || n == 0 {
		http.Error(w, "nonce required", http.StatusBadRequest)
		return
	}
	limit, _ := Sync(srv.db, func(db *DB) (int, error) {
		return db.Config().Luhmann.EffectiveContextLimit(), nil
	})
	// R2888/R3318: the optional `tube` value-scopes the curate subscription to
	// one subscription address (the per-session secretary, or a pool
	// secretary's composite). Empty = legacy bare-curate path. Spelled `tube`
	// rather than `session` because it is never a claim of identity.
	tube := r.URL.Query().Get("tube")
	// R3319: the caller's identity rides along on every iteration, so the
	// nonce → parent registry re-registers itself. R3305 populates it only at
	// reserve-nonce and it is in-memory, so without this a nonce reserved
	// before a bounce reports unmeasured for the rest of its life. Empty is a
	// no-op inside RegisterParentSession.
	clientSession := r.URL.Query().Get(ClientSessionField)
	srv.recallAgentBuilder.RegisterParentSession(uint32(n), clientSession)
	body, exit, rerr := srv.recallAgentBuilder.RecallNext(r.Context(), uint32(n), tube, limit)
	if rerr != nil {
		// Client disconnect or transient — nothing to deliver.
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if exit {
		w.Header().Set("X-Recall-Exit", "1")
	}
	w.Write([]byte(body))
}

// handleRecallListen is the consumer-side loop verb: subscribe (idempotent)
// per capability (bloodhound-result always; recall-result with --ambient),
// block until a result doc arrives, return it plus a crank-handle.
// CRC: crc-Server.md | R2865, R2950, R3317
func (srv *Server) handleRecallListen(w http.ResponseWriter, r *http.Request) {
	// R3317: the subscribing session identifies itself in the identity field.
	session := r.URL.Query().Get(ClientSessionField)
	if session == "" {
		http.Error(w, ClientSessionField+" required", http.StatusBadRequest)
		return
	}
	ambient := r.URL.Query().Get("ambient") == "true" // R2950: ambient opt-in
	body, rerr := srv.recallAgentBuilder.RecallListen(r.Context(), session, ambient)
	if rerr != nil {
		// Client disconnect or cancellation — nothing to deliver.
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(body))
}
