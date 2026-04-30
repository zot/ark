package ark

// CRC: crc-TmpTagStore.md
//
// In-memory tag overlay for tmp:// content. Mirrors the persistent
// V/F/T runtime API so callers stay tmp-unaware. Per-chunk entries
// store tvids resolved through the shared TvidMap; (tag, value)
// strings live in one place. Lives for the server's lifetime; no
// LMDB writes, no schema marker.

import (
	"sync"
)

// IsOverlayID reports whether an id (chunkid or fileid) was issued
// by the microfts2 overlay. Overlay ids count down from MaxUint64,
// so the high bit (interpreted as int64) is set on every overlay id.
// CRC: crc-TmpTagStore.md | R1950
func IsOverlayID(id uint64) bool {
	return int64(id) < 0
}

// chunkTagEntry holds one chunk's tvids grouped by tag name. (tag,
// value) strings are resolved on demand via TvidMap.Resolve.
// CRC: crc-TmpTagStore.md | R1964
type chunkTagEntry struct {
	fileID uint64
	tvids  map[string][]uint64 // tag → tvids
}

// TmpTagStore is the in-memory tag overlay for tmp:// fileids.
// CRC: crc-TmpTagStore.md | R1941
type TmpTagStore struct {
	mu            sync.RWMutex
	chunks        map[uint64]*chunkTagEntry // chunkID → entry
	fileChunks    map[uint64][]uint64       // fileID → chunkIDs
	tagCounts     map[string]int            // tag name → chunk count
	tvidProducers map[uint64]int            // tvid → producer chunk count
	tvids         *TvidMap                  // shared resolver (R1964, R1965)
	extmap        *ExtMap                   // ext routing cleanup (R2023)
	db            *DB                       // passed through to ExtMap.CleanupSource (R2023)
}

// NewTmpTagStore creates an empty overlay backed by the supplied tvid
// resolver. The TvidMap is shared with Store so persistent and overlay
// tvids resolve through one map.
// CRC: crc-TmpTagStore.md | R1964
func NewTmpTagStore(tvids *TvidMap) *TmpTagStore {
	return &TmpTagStore{
		chunks:        make(map[uint64]*chunkTagEntry),
		fileChunks:    make(map[uint64][]uint64),
		tagCounts:     make(map[string]int),
		tvidProducers: make(map[uint64]int),
		tvids:         tvids,
	}
}

// UpdateTagValues replaces the per-chunk entries for a fileid. Drops
// existing chunkids registered to the fileid, then writes new ones.
// Drives ExtMap @ext cleanup for the dropped chunks before re-adding.
// CRC: crc-TmpTagStore.md | R1942, R1966, R2023
func (t *TmpTagStore) UpdateTagValues(fileID uint64, chunkTags []ChunkTagValues) {
	t.mu.RLock()
	var pairs []extCleanupPair
	for _, cid := range t.fileChunks[fileID] {
		for _, te := range t.extTvidsForChunkLocked(cid) {
			pairs = append(pairs, extCleanupPair{chunkID: cid, tvidExt: te})
		}
	}
	t.mu.RUnlock()
	t.runExtCleanup(pairs)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeFileLocked(fileID)
	t.appendLocked(fileID, chunkTags)
}

// AppendTagValues adds entries for newly-emitted chunks without
// touching prior chunks for the fileid.
// CRC: crc-TmpTagStore.md | R1943, R1966
func (t *TmpTagStore) AppendTagValues(fileID uint64, chunkTags []ChunkTagValues) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appendLocked(fileID, chunkTags)
}

// appendLocked writes new chunk entries. Caller holds t.mu.
func (t *TmpTagStore) appendLocked(fileID uint64, chunkTags []ChunkTagValues) {
	for _, ct := range chunkTags {
		if _, exists := t.chunks[ct.ChunkID]; exists {
			continue
		}
		entry := &chunkTagEntry{fileID: fileID, tvids: make(map[string][]uint64)}
		for _, tv := range ct.Values {
			if tv.Tag == "" {
				continue
			}
			tvid := t.resolveOrAlloc(tv.Tag, tv.Value)
			entry.tvids[tv.Tag] = append(entry.tvids[tv.Tag], tvid)
		}
		t.chunks[ct.ChunkID] = entry
		t.fileChunks[fileID] = append(t.fileChunks[fileID], ct.ChunkID)
		for tag, tvids := range entry.tvids {
			t.tagCounts[tag]++
			for _, tvid := range tvids {
				t.tvidProducers[tvid]++
			}
		}
	}
}

// resolveOrAlloc finds an existing tvid for (tag, value) or allocates
// a new overlay tvid. CRC: crc-TmpTagStore.md | R1965, R1966
func (t *TmpTagStore) resolveOrAlloc(tag, value string) uint64 {
	if tvid, ok := t.tvids.Lookup(tag, value); ok {
		return tvid
	}
	return t.tvids.AllocOverlay(tag, value)
}

// RemoveChunk drops a single chunkid's entry. Used when Store
// dispatches a chunkid-keyed RemoveTagValues to the overlay. Drives
// ExtMap @ext cleanup before locking.
// CRC: crc-TmpTagStore.md | R1967, R2023
func (t *TmpTagStore) RemoveChunk(chunkID uint64) {
	t.mu.RLock()
	var pairs []extCleanupPair
	for _, te := range t.extTvidsForChunkLocked(chunkID) {
		pairs = append(pairs, extCleanupPair{chunkID: chunkID, tvidExt: te})
	}
	t.mu.RUnlock()
	t.runExtCleanup(pairs)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeChunkLocked(chunkID)
}

// removeChunkLocked drops the entry, updates fileChunks + tagCounts,
// and prunes any overlay tvids whose last producer was this chunk.
// Caller holds t.mu.
func (t *TmpTagStore) removeChunkLocked(chunkID uint64) {
	t.dropChunkLocked(chunkID, true)
}

// SetExtMap wires the ExtMap so chunk-removal paths can drive
// overlay-source @ext cleanup. Also sets the DB pointer used by
// ExtMap.CleanupSource for spec recovery during the cleanup walk.
// Cleanup runs lock-free (the chunk's @ext tvids are snapshotted
// first under RLock, then CleanupSource is called outside any
// TmpTagStore lock to avoid the t.mu ↔ ExtMap lock cycle that arises
// when CleanupSource calls back through DB.chunkFileID →
// Store.filesForChunk → TmpTagStore.FilesForChunk).
// CRC: crc-TmpTagStore.md | R2023
func (t *TmpTagStore) SetExtMap(m *ExtMap, db *DB) {
	t.mu.Lock()
	t.extmap = m
	t.db = db
	t.mu.Unlock()
}

// extTvidsForChunkLocked snapshots the @ext tvids the chunk
// contributes. Caller holds t.mu (any kind). (R2023)
func (t *TmpTagStore) extTvidsForChunkLocked(chunkID uint64) []uint64 {
	entry, ok := t.chunks[chunkID]
	if !ok {
		return nil
	}
	src := entry.tvids[tagExt]
	if len(src) == 0 {
		return nil
	}
	out := make([]uint64, len(src))
	copy(out, src)
	return out
}

// runExtCleanup drives ExtMap.CleanupSource for each (chunkID, tvid_ext)
// pair. MUST be called outside any TmpTagStore lock. (R2023)
func (t *TmpTagStore) runExtCleanup(pairs []extCleanupPair) {
	if t.extmap == nil {
		return
	}
	for _, p := range pairs {
		_ = t.extmap.CleanupSource(nil, nil, t.db, p.chunkID, p.tvidExt)
	}
}

// extCleanupPair is one (source chunkid, tvid_ext) cleanup directive.
type extCleanupPair struct {
	chunkID uint64
	tvidExt uint64
}

// dropChunkLocked removes one chunk's entry, decrements tag counts and
// per-tvid producer counts, and prunes any overlay tvids that lose
// their last producer. When updateFileList is true, the chunk is also
// removed from its file's chunk list (callers iterating fileChunks
// pass false to avoid mutating the slice they're walking). Caller
// holds t.mu. ExtMap @ext cleanup must be driven by the caller before
// invoking this — see runExtCleanup. (R2023)
func (t *TmpTagStore) dropChunkLocked(chunkID uint64, updateFileList bool) {
	entry, ok := t.chunks[chunkID]
	if !ok {
		return
	}
	for tag, tvids := range entry.tvids {
		t.tagCounts[tag]--
		if t.tagCounts[tag] <= 0 {
			delete(t.tagCounts, tag)
		}
		for _, tvid := range tvids {
			t.tvidProducers[tvid]--
			if t.tvidProducers[tvid] <= 0 {
				delete(t.tvidProducers, tvid)
				t.tvids.RemoveOverlayUnused(tvid)
			}
		}
	}
	if updateFileList {
		chunks := t.fileChunks[entry.fileID]
		for i, c := range chunks {
			if c == chunkID {
				t.fileChunks[entry.fileID] = append(chunks[:i], chunks[i+1:]...)
				break
			}
		}
		if len(t.fileChunks[entry.fileID]) == 0 {
			delete(t.fileChunks, entry.fileID)
		}
	}
	delete(t.chunks, chunkID)
}

// RemoveFile drops every entry belonging to a tmp:// fileid. Tvids
// whose last producer was this file and whose origin is OriginOverlay
// are pruned from the shared TvidMap; persistent tvids are left alone.
// Drives ExtMap @ext cleanup for each chunk before locking.
// CRC: crc-TmpTagStore.md | R1944, R1951, R1967, R2023
func (t *TmpTagStore) RemoveFile(fileID uint64) {
	t.mu.RLock()
	var pairs []extCleanupPair
	for _, cid := range t.fileChunks[fileID] {
		for _, te := range t.extTvidsForChunkLocked(cid) {
			pairs = append(pairs, extCleanupPair{chunkID: cid, tvidExt: te})
		}
	}
	t.mu.RUnlock()
	t.runExtCleanup(pairs)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeFileLocked(fileID)
}

// removeFileLocked drops all chunks for fileID and prunes any overlay
// tvids whose last producer departed with the file. Caller holds t.mu.
func (t *TmpTagStore) removeFileLocked(fileID uint64) {
	for _, chunkID := range t.fileChunks[fileID] {
		t.dropChunkLocked(chunkID, false)
	}
	delete(t.fileChunks, fileID)
}

// TagFiles returns overlay TagFileRecord entries for the requested
// tag names. Mirrors Store.TagFiles output shape.
// CRC: crc-TmpTagStore.md | R1945
func (t *TmpTagStore) TagFiles(tags []string) []TagFileRecord {
	if len(tags) == 0 {
		return nil
	}
	tagSet := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tagSet[tag] = true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []TagFileRecord
	for chunkID, entry := range t.chunks {
		for tag, tvids := range entry.tvids {
			if !tagSet[tag] {
				continue
			}
			out = append(out, TagFileRecord{
				ChunkID: chunkID,
				FileID:  entry.fileID,
				Tag:     tag,
				Count:   uint32(len(tvids)),
			})
		}
	}
	return out
}

// TagValueFiles returns the overlay's chunkids carrying the given
// (tag, value). Resolves via TvidMap.Lookup so the lookup is O(1) in
// the in-memory map plus a per-entry membership check.
// CRC: crc-TmpTagStore.md | R1945, R1964
func (t *TmpTagStore) TagValueFiles(tag, value string) []uint64 {
	tvid, ok := t.tvids.Lookup(tag, value)
	if !ok {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []uint64
	for chunkID, entry := range t.chunks {
		for _, ct := range entry.tvids[tag] {
			if ct == tvid {
				out = append(out, chunkID)
				break
			}
		}
	}
	return out
}

// FileTagValues returns the first value found per requested tag for
// a tmp:// fileid. Mirrors Store.FileTagValues semantics.
// CRC: crc-TmpTagStore.md | R1945, R1964
func (t *TmpTagStore) FileTagValues(fileID uint64, tags []string) map[string]string {
	result := make(map[string]string, len(tags))
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, chunkID := range t.fileChunks[fileID] {
		entry := t.chunks[chunkID]
		if entry == nil {
			continue
		}
		for _, tag := range tags {
			if _, found := result[tag]; found {
				continue
			}
			tvids := entry.tvids[tag]
			if len(tvids) == 0 {
				continue
			}
			if _, value, ok := t.tvids.Resolve(tvids[0]); ok {
				result[tag] = value
			}
		}
		if len(result) == len(tags) {
			break
		}
	}
	return result
}

// FilesForChunk returns the fileid associated with a chunkid, or nil.
// Used by Store's chunkid→fileid resolver dispatch.
// CRC: crc-TmpTagStore.md
func (t *TmpTagStore) FilesForChunk(chunkID uint64) []uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if entry, ok := t.chunks[chunkID]; ok {
		return []uint64{entry.fileID}
	}
	return nil
}

// ChunksForFile returns the chunkids registered to a tmp:// fileid.
// Used by Store's fileid→chunks resolver dispatch.
// CRC: crc-TmpTagStore.md
func (t *TmpTagStore) ChunksForFile(fileID uint64) []uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	chunks := t.fileChunks[fileID]
	out := make([]uint64, len(chunks))
	copy(out, chunks)
	return out
}

// HasFile returns true if any chunkids are tracked for the fileid.
// CRC: crc-TmpTagStore.md
func (t *TmpTagStore) HasFile(fileID uint64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.fileChunks[fileID]) > 0
}
