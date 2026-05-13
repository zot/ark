package ark

// CRC: crc-Curation.md | R2355, R2356, R2357, R2358, R2359

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
// `ark.curation` Lua-side table that Frictionless watches. The
// `pinned` Lua field on that table is refreshed inside the same
// Lua-executor closure that mutates the Go slice, so the two views
// stay consistent without a separate publisher.
//
// CRC: crc-Curation.md | R2355, R2357
type Curation struct {
	mu       sync.Mutex
	pinned   []PinnedChunk
	luaTable *lua.LTable // ark.curation; set on registerLuaFunctions
}

// newCuration constructs an empty Curation.
// CRC: crc-Curation.md | R2355
func newCuration() *Curation {
	return &Curation{}
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

// refreshLuaTable rebuilds the `pinned` field on the Lua-side
// `ark.curation` table to match the Go slice. Called from inside
// the Lua executor (Frictionless detects the table replacement and
// pushes the diff to the browser).
//
// CRC: crc-Curation.md | R2357
func (c *Curation) refreshLuaTable(L *lua.LState) {
	if c.luaTable == nil {
		return
	}
	c.mu.Lock()
	snap := make([]PinnedChunk, len(c.pinned))
	copy(snap, c.pinned)
	c.mu.Unlock()

	tbl := L.NewTable()
	for i, p := range snap {
		entry := L.NewTable()
		L.SetField(entry, "chunkID", lua.LNumber(p.ChunkID))
		L.SetField(entry, "fileID", lua.LNumber(p.FileID))
		L.SetField(entry, "path", lua.LString(p.Path))
		L.SetField(entry, "pinnedAt", lua.LNumber(p.PinnedAt))
		tbl.RawSetInt(i+1, entry)
	}
	L.SetField(c.luaTable, "pinned", tbl)
}
