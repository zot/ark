package ark

// CRC: crc-Curation.md | R2355, R2356, R2357, R2358, R2359, R2360, R2361, R2362

import (
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// PinnedChunk is a curation workshop pin. Lives in the Go-owned
// Curation slice; mirrored into Lua as a table for Frictionless's
// variable system to observe.
//
// CRC: crc-Curation.md | R2359
type PinnedChunk struct {
	ChunkID  uint64 `json:"chunkID"`
	FileID   uint64 `json:"fileID"`
	Path     string `json:"path"`
	PinnedAt int64  `json:"pinnedAt"`
}

// Curation owns the curation workshop's pinned-chunks state. The
// canonical store is the Go slice `pinned`; `luaTable` is the
// `sys.curation` Lua-side table that Frictionless watches. The
// `pinned` Lua field on that table is refreshed inside the same
// Lua-executor closure that mutates the Go slice, so the two views
// stay consistent without a separate publisher.
//
// CRC: crc-Curation.md | R2355, R2357
type Curation struct {
	mu          sync.Mutex
	pinned      []PinnedChunk
	luaTable    *lua.LTable           // sys.curation; set on registerLuaFunctions
	entryTables map[uint64]*lua.LTable // per-ChunkID entry sub-tables; identity-preserved across refreshes (R2362)
}

// newCuration constructs an empty Curation.
// CRC: crc-Curation.md | R2355
func newCuration() *Curation {
	return &Curation{
		entryTables: make(map[uint64]*lua.LTable),
	}
}

// pinnedSnapshot returns a copy of the pinned slice under the mutex.
// CRC: crc-Curation.md | R2357
func (c *Curation) pinnedSnapshot() []PinnedChunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]PinnedChunk, len(c.pinned))
	copy(out, c.pinned)
	return out
}

// pin appends or moves-to-top a PinnedChunk for chunkID. Always-add
// never-flip — the pin lands on top of the workspace. Caller must
// hold the Lua executor (i.e. call from inside WithLua). Refreshes
// the Lua mirror in the same tick so Frictionless sees the change.
//
// CRC: crc-Curation.md | R2358
func (c *Curation) pin(L *lua.LState, chunkID, fileID uint64, path string) {
	c.mu.Lock()
	idx := -1
	for i, p := range c.pinned {
		if p.ChunkID == chunkID {
			idx = i
			break
		}
	}
	now := time.Now().Unix()
	if idx >= 0 {
		existing := c.pinned[idx]
		c.pinned = append(c.pinned[:idx], c.pinned[idx+1:]...)
		existing.PinnedAt = now
		if fileID != 0 {
			existing.FileID = fileID
		}
		if path != "" {
			existing.Path = path
		}
		c.pinned = append([]PinnedChunk{existing}, c.pinned...)
	} else {
		c.pinned = append([]PinnedChunk{{
			ChunkID:  chunkID,
			FileID:   fileID,
			Path:     path,
			PinnedAt: now,
		}}, c.pinned...)
	}
	c.mu.Unlock()
	c.refreshLuaTable(L)
}

// dismiss removes the pinned entry whose ChunkID matches, if any.
// Silent no-op when the chunkID is not pinned. Refreshes the Lua
// mirror in the same tick. Lua-executor-only — call from inside
// WithLua (or a function registered on a Lua table).
//
// CRC: crc-Curation.md | R2360
func (c *Curation) dismiss(L *lua.LState, chunkID uint64) {
	c.mu.Lock()
	idx := -1
	for i, p := range c.pinned {
		if p.ChunkID == chunkID {
			idx = i
			break
		}
	}
	if idx < 0 {
		c.mu.Unlock()
		return
	}
	c.pinned = append(c.pinned[:idx], c.pinned[idx+1:]...)
	c.mu.Unlock()
	c.refreshLuaTable(L)
}

// sweepOlder drops every pinned entry except the topmost. Silent
// no-op for ≤1 pin. Refreshes the Lua mirror in the same tick.
// Lua-executor-only.
//
// CRC: crc-Curation.md | R2361
func (c *Curation) sweepOlder(L *lua.LState) {
	c.mu.Lock()
	if len(c.pinned) <= 1 {
		c.mu.Unlock()
		return
	}
	c.pinned = c.pinned[:1]
	c.mu.Unlock()
	c.refreshLuaTable(L)
}

// refreshLuaTable rebuilds the `pinned` field on the Lua-side
// `sys.curation` table to match the Go slice. Entry sub-tables for
// surviving ChunkIDs keep their Lua identity across refreshes — the
// per-ChunkID cache mutates each table's fields in place. Allocates
// a fresh table only for newly pinned ChunkIDs; drops cache entries
// for ChunkIDs that left the slice. Frictionless's ViewList reuses
// presenters only when view.baseItem == item, so identity-preservation
// is what keeps per-pin reactive state alive across mutations.
//
// CRC: crc-Curation.md | R2357, R2362
func (c *Curation) refreshLuaTable(L *lua.LState) {
	if c.luaTable == nil {
		return
	}
	c.mu.Lock()
	snap := make([]PinnedChunk, len(c.pinned))
	copy(snap, c.pinned)
	c.mu.Unlock()

	live := make(map[uint64]struct{}, len(snap))
	tbl := L.NewTable()
	for i, p := range snap {
		live[p.ChunkID] = struct{}{}
		entry := c.entryTables[p.ChunkID]
		if entry == nil {
			entry = L.NewTable()
			c.entryTables[p.ChunkID] = entry
		}
		L.SetField(entry, "chunkID", lua.LNumber(p.ChunkID))
		L.SetField(entry, "fileID", lua.LNumber(p.FileID))
		L.SetField(entry, "path", lua.LString(p.Path))
		L.SetField(entry, "pinnedAt", lua.LNumber(p.PinnedAt))
		tbl.RawSetInt(i+1, entry)
	}
	for chunkID := range c.entryTables {
		if _, ok := live[chunkID]; !ok {
			delete(c.entryTables, chunkID)
		}
	}
	L.SetField(c.luaTable, "pinned", tbl)
}
