package ark

// CRC: crc-TmpTagStore.md
//
// In-memory tag overlay for tmp:// content. Mirrors the persistent
// V/F/T runtime API so callers stay tmp-unaware. Lives for the
// server's lifetime; no LMDB writes, no schema marker.

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

// chunkTagEntry holds one chunk's tags grouped by tag name. Mirrors
// the runtime shape of the persistent V records.
type chunkTagEntry struct {
	fileID uint64
	values map[string][]string // tag → values
}

// TmpTagStore is the in-memory tag overlay for tmp:// fileids.
// CRC: crc-TmpTagStore.md | R1941
type TmpTagStore struct {
	mu         sync.RWMutex
	chunks     map[uint64]*chunkTagEntry // chunkID → entry
	fileChunks map[uint64][]uint64       // fileID → chunkIDs
	tagCounts  map[string]int            // tag name → chunk count
}

// NewTmpTagStore creates an empty overlay.
func NewTmpTagStore() *TmpTagStore {
	return &TmpTagStore{
		chunks:     make(map[uint64]*chunkTagEntry),
		fileChunks: make(map[uint64][]uint64),
		tagCounts:  make(map[string]int),
	}
}

// UpdateTagValues replaces the per-chunk entries for a fileid. Drops
// existing chunkids registered to the fileid, then writes new ones.
// CRC: crc-TmpTagStore.md | R1942
func (t *TmpTagStore) UpdateTagValues(fileID uint64, chunkTags []ChunkTagValues) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeFileLocked(fileID)
	t.appendLocked(fileID, chunkTags)
}

// AppendTagValues adds entries for newly-emitted chunks without
// touching prior chunks for the fileid.
// CRC: crc-TmpTagStore.md | R1943
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
		entry := &chunkTagEntry{fileID: fileID, values: make(map[string][]string)}
		for _, tv := range ct.Values {
			if tv.Tag == "" {
				continue
			}
			entry.values[tv.Tag] = append(entry.values[tv.Tag], tv.Value)
		}
		t.chunks[ct.ChunkID] = entry
		t.fileChunks[fileID] = append(t.fileChunks[fileID], ct.ChunkID)
		for tag := range entry.values {
			t.tagCounts[tag]++
		}
	}
}

// RemoveChunk drops a single chunkid's entry. Used when Store
// dispatches a chunkid-keyed RemoveTagValues to the overlay.
// CRC: crc-TmpTagStore.md
func (t *TmpTagStore) RemoveChunk(chunkID uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeChunkLocked(chunkID)
}

// removeChunkLocked drops the entry and updates fileChunks + tagCounts.
// Caller holds t.mu.
func (t *TmpTagStore) removeChunkLocked(chunkID uint64) {
	entry, ok := t.chunks[chunkID]
	if !ok {
		return
	}
	for tag := range entry.values {
		t.tagCounts[tag]--
		if t.tagCounts[tag] <= 0 {
			delete(t.tagCounts, tag)
		}
	}
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
	delete(t.chunks, chunkID)
}

// RemoveFile drops every entry belonging to a tmp:// fileid.
// CRC: crc-TmpTagStore.md | R1944
func (t *TmpTagStore) RemoveFile(fileID uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeFileLocked(fileID)
}

// removeFileLocked drops all chunks for fileID. Caller holds t.mu.
func (t *TmpTagStore) removeFileLocked(fileID uint64) {
	for _, chunkID := range t.fileChunks[fileID] {
		entry := t.chunks[chunkID]
		if entry == nil {
			continue
		}
		for tag := range entry.values {
			t.tagCounts[tag]--
			if t.tagCounts[tag] <= 0 {
				delete(t.tagCounts, tag)
			}
		}
		delete(t.chunks, chunkID)
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
		for tag, vals := range entry.values {
			if !tagSet[tag] {
				continue
			}
			out = append(out, TagFileRecord{
				ChunkID: chunkID,
				FileID:  entry.fileID,
				Tag:     tag,
				Count:   uint32(len(vals)),
			})
		}
	}
	return out
}

// TagValueFiles returns the overlay's chunkids carrying the given
// (tag, value). Mirrors Store.TagValueFiles' chunkid-returning shape.
// CRC: crc-TmpTagStore.md | R1945
func (t *TmpTagStore) TagValueFiles(tag, value string) []uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []uint64
	for chunkID, entry := range t.chunks {
		for _, v := range entry.values[tag] {
			if v == value {
				out = append(out, chunkID)
				break
			}
		}
	}
	return out
}

// FileTagValues returns the first value found per requested tag for
// a tmp:// fileid. Mirrors Store.FileTagValues semantics.
// CRC: crc-TmpTagStore.md | R1945
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
			if vals := entry.values[tag]; len(vals) > 0 {
				result[tag] = vals[0]
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
