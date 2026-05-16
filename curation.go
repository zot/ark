package ark

// CRC: crc-Curation.md | R2355, R2356, R2357, R2358, R2359, R2360, R2361, R2362, R2381, R2382, R2383, R2384, R2385

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	lua "github.com/yuin/gopher-lua"
)

const curationStateVersion = 1

// PinnedChunk is a curation workshop pin. Lives in the Go-owned
// Curation slice; mirrored into Lua as a table for Frictionless's
// variable system to observe.
//
// CRC: crc-Curation.md | R2359, R2382
type PinnedChunk struct {
	ChunkID  uint64 `json:"chunkID" toml:"chunkID"`
	FileID   uint64 `json:"fileID" toml:"fileID"`
	Path     string `json:"path" toml:"path"`
	PinnedAt int64  `json:"pinnedAt" toml:"pinnedAt"`
}

// curationFile is the on-disk shape of curation.toml.
// CRC: crc-Curation.md | R2382
type curationFile struct {
	Version int           `toml:"version"`
	Pinned  []PinnedChunk `toml:"pinned"`
}

// Curation owns the curation workshop's pinned-chunks state. The
// canonical store is the Go slice `pinned`; `luaTable` is the
// `sys.curation` Lua-side table that Frictionless watches. The
// `pinned` Lua field on that table is refreshed inside the same
// Lua-executor closure that mutates the Go slice, so the two views
// stay consistent without a separate publisher.
//
// CRC: crc-Curation.md | R2355, R2357, R2381
type Curation struct {
	mu          sync.Mutex
	pinned      []PinnedChunk
	luaTable    *lua.LTable            // sys.curation; set on registerLuaFunctions
	entryTables map[uint64]*lua.LTable // per-ChunkID entry sub-tables; identity-preserved across refreshes (R2362)
	statePath   string                 // absolute path to curation.toml; "" disables persistence (R2381)
}

// newCuration constructs an empty Curation. `dbPath` is the database
// directory; the curation.toml lives directly under it. An empty
// dbPath disables persistence (used by tests).
// CRC: crc-Curation.md | R2355, R2381
func newCuration(dbPath string) *Curation {
	c := &Curation{
		entryTables: make(map[uint64]*lua.LTable),
	}
	if dbPath != "" {
		c.statePath = filepath.Join(dbPath, "curation.toml")
	}
	return c
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
	c.save()
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
	c.save()
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
	c.save()
}

// Load reads the curation state from `statePath`, populating the
// pinned slice. Missing file → silent no-op. Malformed TOML or
// unknown version → log and leave the slice empty (the next
// mutation's save will overwrite the broken file). Must be called
// during server startup so the Lua mirror reflects loaded state
// once registerLuaFunctions wires up `luaTable`.
// CRC: crc-Curation.md | R2382, R2383
func (c *Curation) Load() {
	if c.statePath == "" {
		return
	}
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("curation: load %s: %v", c.statePath, err)
		}
		return
	}
	var f curationFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		log.Printf("curation: parse %s: %v", c.statePath, err)
		return
	}
	if f.Version != curationStateVersion {
		log.Printf("curation: %s version %d (expected %d) — ignoring", c.statePath, f.Version, curationStateVersion)
		return
	}
	c.mu.Lock()
	c.pinned = f.Pinned
	c.mu.Unlock()
}

// save writes the current pinned slice to `statePath` atomically.
// Called inside pin/dismiss/sweepOlder after the mutation and Lua
// mirror refresh. On disk failure, logs and retains in-memory
// state; the next mutation retries.
// CRC: crc-Curation.md | R2384, R2385
func (c *Curation) save() {
	if c.statePath == "" {
		return
	}
	c.mu.Lock()
	snap := make([]PinnedChunk, len(c.pinned))
	copy(snap, c.pinned)
	c.mu.Unlock()

	f := curationFile{Version: curationStateVersion, Pinned: snap}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		log.Printf("curation: encode %s: %v", c.statePath, err)
		return
	}
	if err := atomicWriteFile(c.statePath, buf.Bytes(), 0644); err != nil {
		log.Printf("curation: %v", err)
	}
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
