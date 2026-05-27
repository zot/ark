package ark

// CRC: crc-Server.md, crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md

import (
	"encoding/json"
	"net/http"
)

// handleRecallReserveNonce returns the next per-server monotonic
// nonce. CRC: crc-Server.md | R2755
func (srv *Server) handleRecallReserveNonce(w http.ResponseWriter, _ *http.Request) {
	n := srv.recallAgentBuilder.ReserveNonce()
	writeJSON(w, map[string]any{"nonce": n})
}

// recallSurfaceRequest mirrors the CLI body for `surface`. R2756
type recallSurfaceRequest struct {
	Fire   uint64 `json:"fire"`
	Chunk  uint64 `json:"chunk"`
	Reason string `json:"reason"`
}

// handleRecallSurface appends one `## Surface:` item to the
// in-flight result-doc builder. CRC: crc-Server.md | R2756
func (srv *Server) handleRecallSurface(w http.ResponseWriter, r *http.Request) {
	var req recallSurfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Fire == 0 || req.Chunk == 0 || req.Reason == "" {
		http.Error(w, "fire, chunk, reason required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.SurfaceItem(req.Fire, req.Chunk, req.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// recallRecommendRequest mirrors the CLI body for `recommend`. R2757
type recallRecommendRequest struct {
	Fire   uint64 `json:"fire"`
	Chunk  uint64 `json:"chunk"`
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
	if req.Fire == 0 || req.Chunk == 0 || req.Tag == "" || req.Reason == "" {
		http.Error(w, "fire, chunk, tag, reason required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.RecommendItem(req.Fire, req.Chunk, req.Tag, req.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// recallCloseRequest mirrors the CLI body for `close`. R2758
type recallCloseRequest struct {
	Fire             uint64 `json:"fire"`
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
	if req.Fire == 0 {
		http.Error(w, "fire required", http.StatusBadRequest)
		return
	}
	if err := srv.recallAgentBuilder.CloseResult(req.Fire, req.Nonce, req.PreserveCuration); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}
