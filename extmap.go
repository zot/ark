package ark

// CRC: crc-ExtMap.md
//
// In-memory routing state for @ext. Six maps maintained alongside DB
// X-record writes; canonical re-resolution flow runs from the
// reindex callback; source-side cleanup runs from the orphan
// callback. Rebuilt at startup by scanning X records.

import (
	"sync"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// ExtMap holds the in-memory state that supports @ext routing.
// CRC: crc-ExtMap.md | R1992
type ExtMap struct {
	mu                sync.RWMutex
	targetToChunk     map[uint64][]uint64 // tvid_ext → chunkids
	chunkToTargets    map[uint64][]uint64 // chunkid → tvid_exts
	fileidToTvids     map[uint64][]uint64 // fileid → tvid_exts (target file)
	extByAnchor       map[string][]uint64 // anchor spec text → tvid_exts
	unresolvedTargets map[uint64]bool     // tvid_exts whose target spec resolves to nothing
	virtualTagCount   map[string]int      // ext-routed contributions per tag
}

// NewExtMap constructs an empty ExtMap.
// CRC: crc-ExtMap.md | R1992
func NewExtMap() *ExtMap {
	return &ExtMap{
		targetToChunk:     make(map[uint64][]uint64),
		chunkToTargets:    make(map[uint64][]uint64),
		fileidToTvids:     make(map[uint64][]uint64),
		extByAnchor:       make(map[string][]uint64),
		unresolvedTargets: make(map[uint64]bool),
		virtualTagCount:   make(map[string]int),
	}
}

// VirtualTagCount returns the ext-routed contribution count for a
// tag. Used by Store.TagCounts for T-total augmentation.
// CRC: crc-ExtMap.md | R2010
func (m *ExtMap) VirtualTagCount(tag string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.virtualTagCount[tag]
}

// VirtualTagCounts returns the ext-routed contribution counts for a
// list of tags under a single RLock. Used by Store.TagCounts to
// avoid per-tag lock acquisitions on the query hot path.
// CRC: crc-ExtMap.md | R2010
func (m *ExtMap) VirtualTagCounts(tags []string) map[string]int {
	out := make(map[string]int, len(tags))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tag := range tags {
		if c := m.virtualTagCount[tag]; c > 0 {
			out[tag] = c
		}
	}
	return out
}

// Rebuild repopulates all six maps by scanning X records. Called
// from DB.Open after the TvidMap is loaded.
// CRC: crc-ExtMap.md | R1993
func (m *ExtMap) Rebuild(db *DB) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targetToChunk = make(map[uint64][]uint64)
	m.chunkToTargets = make(map[uint64][]uint64)
	m.fileidToTvids = make(map[uint64][]uint64)
	m.extByAnchor = make(map[string][]uint64)
	m.unresolvedTargets = make(map[uint64]bool)
	m.virtualTagCount = make(map[string]int)

	return db.store.env.View(func(txn *lmdb.Txn) error {
		return db.store.ScanAllExtRecords(txn, func(tvidExt, targetChunk uint64, routedTvids []uint64) error {
			m.targetToChunk[tvidExt] = append(m.targetToChunk[tvidExt], targetChunk)
			m.chunkToTargets[targetChunk] = appendUnique(m.chunkToTargets[targetChunk], tvidExt)
			if fileID, ok := db.chunkFileID(txn, targetChunk); ok {
				m.fileidToTvids[fileID] = appendUnique(m.fileidToTvids[fileID], tvidExt)
			}
			if _, value, ok := db.store.tvids.Resolve(tvidExt); ok {
				if spec, _, parseOk := ParseExtTarget(value); parseOk {
					m.extByAnchor[spec] = appendUnique(m.extByAnchor[spec], tvidExt)
				}
			}
			for _, rt := range routedTvids {
				if tag, _, ok := db.store.tvids.Resolve(rt); ok {
					m.virtualTagCount[tag]++
				}
			}
			return nil
		})
	})
}

// candidatesForFileChange gathers tvid_exts from
// fileidToTvids[fileID], extByAnchor[F.path], and chunkToTargets for
// each orphaned chunk. The added-UUID branch (extByAnchor[UUID] for
// @id values added in F) requires a txn read of F[chunk][id], so it
// is folded in by the caller during candidate post-processing if
// needed. For now, we approximate "appearing UUID" via the
// chunkToTargets path and extByAnchor[F.path]; a follow-up will read
// added chunk @id values when anchor forms ship.
// CRC: crc-ExtMap.md | R2000
func (m *ExtMap) candidatesForFileChange(db *DB, fileID uint64, addedChunkIDs, orphanedChunkIDs []uint64) map[uint64]bool {
	path, _ := db.fileIDPath(fileID)
	var addedUUIDs []string
	if len(addedChunkIDs) > 0 {
		_ = db.fts.Env().View(func(txn *lmdb.Txn) error {
			for _, cid := range addedChunkIDs {
				addedUUIDs = append(addedUUIDs, db.chunkIDValues(txn, cid)...)
			}
			return nil
		})
	}
	out := make(map[uint64]bool)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tv := range m.fileidToTvids[fileID] {
		out[tv] = true
	}
	for _, tv := range m.extByAnchor[path] {
		out[tv] = true
	}
	for _, cid := range orphanedChunkIDs {
		for _, tv := range m.chunkToTargets[cid] {
			out[tv] = true
		}
	}
	for _, uuid := range addedUUIDs {
		for _, tv := range m.extByAnchor[uuid] {
			out[tv] = true
		}
	}
	return out
}

// applyIndexExt writes records for one pre-resolved index plan.
// Resolution and parsing already happened outside the txn; this
// runs only LMDB writes plus in-memory state updates.
// CRC: crc-ExtMap.md | R1995, R1996, R1997, R1998, R1999
func (m *ExtMap) applyIndexExt(txn *lmdb.Txn, tt *TvidTxn, db *DB, p extIndexPlan) error {
	if len(p.targets) == 0 {
		m.mu.Lock()
		m.extByAnchor[p.target] = appendUnique(m.extByAnchor[p.target], p.tvidExt)
		m.unresolvedTargets[p.tvidExt] = true
		m.mu.Unlock()
		return nil
	}
	type accepted struct {
		chunkID uint64
		fileID  uint64
	}
	var taken []accepted
	for _, t := range p.targets {
		if IsOverlayID(t) {
			// Overlay (tmp://) targets aren't routed in v1 — full
			// support waits on overlay E-records (in-memory error
			// store parallel to TmpTagStore) so the diagnostic can
			// live and die with the overlay context that produced it.
			// See PLAN.md / O87.
			Logv(0, "ext: overlay target skipped (source fileid=%d, target chunk=%d) — overlay routing deferred", p.sourceFileID, t)
			continue
		}
		fid, ok := db.chunkFileID(txn, t)
		if !ok {
			continue
		}
		if fid == p.sourceFileID {
			Logv(0, "ext: self-reference rejected (source fileid=%d, target chunk=%d)", p.sourceFileID, t)
			continue
		}
		taken = append(taken, accepted{chunkID: t, fileID: fid})
	}
	if len(taken) == 0 {
		m.mu.Lock()
		m.extByAnchor[p.target] = appendUnique(m.extByAnchor[p.target], p.tvidExt)
		m.mu.Unlock()
		return nil
	}
	for _, a := range taken {
		tvids := make([]uint64, 0, len(p.routedTags))
		for _, rt := range p.routedTags {
			tvid, err := db.store.addChunkIDToVRecord(txn, tt, rt.Tag, rt.Value, a.chunkID)
			if err != nil {
				return err
			}
			tvids = append(tvids, tvid)
		}
		if err := db.store.WriteExtRecord(txn, p.tvidExt, a.chunkID, tvids); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.extByAnchor[p.target] = appendUnique(m.extByAnchor[p.target], p.tvidExt)
	delete(m.unresolvedTargets, p.tvidExt)
	for _, a := range taken {
		m.targetToChunk[p.tvidExt] = append(m.targetToChunk[p.tvidExt], a.chunkID)
		m.chunkToTargets[a.chunkID] = appendUnique(m.chunkToTargets[a.chunkID], p.tvidExt)
		m.fileidToTvids[a.fileID] = appendUnique(m.fileidToTvids[a.fileID], p.tvidExt)
		for _, rt := range p.routedTags {
			m.virtualTagCount[rt.Tag]++
		}
	}
	m.mu.Unlock()
	return nil
}

// applyReresolve runs steps 3-7 of the canonical re-resolution flow
// for one pre-resolved tvid_ext.
// CRC: crc-ExtMap.md | R1994, R2002, R2003, R2004, R2005, R2006
func (m *ExtMap) applyReresolve(txn *lmdb.Txn, tt *TvidTxn, db *DB, fileID uint64, p extReresolvePlan) error {
	tvidExt := p.tvidExt
	newTargets := p.newTargets
	routed := p.routedTags
	target := p.target

	m.mu.RLock()
	oldAll := append([]uint64(nil), m.targetToChunk[tvidExt]...)
	m.mu.RUnlock()

	oldScoped, removeFids := filterByFileWithIDs(txn, db, oldAll, fileID)
	newScoped, addFids := filterByFileWithIDs(txn, db, newTargets, fileID)

	adds := setDiff(newScoped, oldScoped)
	removes := setDiff(oldScoped, newScoped)

	type remOp struct {
		chunk  uint64
		fileID uint64
		tags   []string
	}
	remOps := make([]remOp, 0, len(removes))
	for _, removed := range removes {
		routings, err := db.store.ReadExtRecord(txn, tvidExt, removed)
		if err != nil {
			return err
		}
		op := remOp{chunk: removed, fileID: removeFids[removed]}
		for _, rt := range routings {
			tag, val, ok := tt.Resolve(rt)
			if !ok {
				continue
			}
			if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, removed); err != nil {
				return err
			}
			op.tags = append(op.tags, tag)
		}
		if err := db.store.DeleteExtRecord(txn, tvidExt, removed); err != nil {
			return err
		}
		remOps = append(remOps, op)
	}
	for _, added := range adds {
		routedTvids := make([]uint64, 0, len(routed))
		for _, rt := range routed {
			tvid, err := db.store.addChunkIDToVRecord(txn, tt, rt.Tag, rt.Value, added)
			if err != nil {
				return err
			}
			routedTvids = append(routedTvids, tvid)
		}
		if err := db.store.WriteExtRecord(txn, tvidExt, added, routedTvids); err != nil {
			return err
		}
	}

	m.mu.Lock()
	for _, op := range remOps {
		m.targetToChunk[tvidExt] = removeUint64(m.targetToChunk[tvidExt], op.chunk)
		m.chunkToTargets[op.chunk] = removeUint64(m.chunkToTargets[op.chunk], tvidExt)
		if op.fileID != 0 {
			m.fileidToTvids[op.fileID] = removeUint64(m.fileidToTvids[op.fileID], tvidExt)
		}
		for _, tag := range op.tags {
			m.virtualTagCount[tag]--
			if m.virtualTagCount[tag] <= 0 {
				delete(m.virtualTagCount, tag)
			}
		}
	}
	for _, added := range adds {
		m.targetToChunk[tvidExt] = append(m.targetToChunk[tvidExt], added)
		m.chunkToTargets[added] = appendUnique(m.chunkToTargets[added], tvidExt)
		if fid := addFids[added]; fid != 0 {
			m.fileidToTvids[fid] = appendUnique(m.fileidToTvids[fid], tvidExt)
		}
		for _, rt := range routed {
			m.virtualTagCount[rt.Tag]++
		}
		delete(m.unresolvedTargets, tvidExt)
	}
	if len(newTargets) == 0 {
		m.unresolvedTargets[tvidExt] = true
		m.extByAnchor[target] = appendUnique(m.extByAnchor[target], tvidExt)
	}
	m.mu.Unlock()
	return nil
}

// CleanupSource runs source-side cleanup when a source chunk is
// orphaned. Walks X[tvid_ext], strikes target_chunkid from each
// routed tag's V record (one occurrence), decrements
// virtualTagCount, drops X records, drops tvid_ext from all maps.
//
// MUST run before tt.Commit drops tvid_ext — we don't need the
// spec here, but the contract for re-resolution paths sharing the
// same TvidTxn does.
// CRC: crc-ExtMap.md | R2008, R2009
func (m *ExtMap) CleanupSource(txn *lmdb.Txn, tt *TvidTxn, db *DB, tvidExt uint64) error {
	routings, err := db.store.ScanExtRecords(txn, tvidExt)
	if err != nil {
		return err
	}
	type cleanedRouting struct {
		chunkID uint64
		fileID  uint64
		tags    []string
	}
	cleaned := make([]cleanedRouting, 0, len(routings))
	for _, r := range routings {
		c := cleanedRouting{chunkID: r.TargetChunkID}
		if fid, ok := db.chunkFileID(txn, r.TargetChunkID); ok {
			c.fileID = fid
		}
		for _, rt := range r.RoutedTvids {
			tag, val, ok := tt.Resolve(rt)
			if !ok {
				continue
			}
			if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, r.TargetChunkID); err != nil {
				return err
			}
			c.tags = append(c.tags, tag)
		}
		if err := db.store.DeleteExtRecord(txn, tvidExt, r.TargetChunkID); err != nil {
			return err
		}
		cleaned = append(cleaned, c)
	}
	m.mu.Lock()
	for _, c := range cleaned {
		m.chunkToTargets[c.chunkID] = removeUint64(m.chunkToTargets[c.chunkID], tvidExt)
		if c.fileID != 0 {
			m.fileidToTvids[c.fileID] = removeUint64(m.fileidToTvids[c.fileID], tvidExt)
		}
		for _, tag := range c.tags {
			m.virtualTagCount[tag]--
			if m.virtualTagCount[tag] <= 0 {
				delete(m.virtualTagCount, tag)
			}
		}
	}
	delete(m.targetToChunk, tvidExt)
	delete(m.unresolvedTargets, tvidExt)
	if _, val, ok := tt.Resolve(tvidExt); ok {
		if spec, _, parseOK := ParseExtTarget(val); parseOK {
			m.extByAnchor[spec] = removeUint64(m.extByAnchor[spec], tvidExt)
			if len(m.extByAnchor[spec]) == 0 {
				delete(m.extByAnchor, spec)
			}
		}
	}
	m.mu.Unlock()
	return nil
}

// appendUnique appends v to xs only if it is not already present.
func appendUnique(xs []uint64, v uint64) []uint64 {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// removeUint64 removes the first occurrence of v from xs.
func removeUint64(xs []uint64, v uint64) []uint64 {
	for i, x := range xs {
		if x == v {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

// setDiff returns elements of a not in b.
func setDiff(a, b []uint64) []uint64 {
	if len(b) == 0 {
		return append([]uint64(nil), a...)
	}
	bSet := make(map[uint64]bool, len(b))
	for _, x := range b {
		bSet[x] = true
	}
	out := a[:0:0]
	for _, x := range a {
		if !bSet[x] {
			out = append(out, x)
		}
	}
	return out
}

// filterByFileWithIDs keeps only chunkids that live in fileID and
// returns the chunkid→fileid map for reuse downstream (the map covers
// every chunk in the input, not only matches, so callers can index
// fileids without repeating the C-record read).
func filterByFileWithIDs(txn *lmdb.Txn, db *DB, chunks []uint64, fileID uint64) ([]uint64, map[uint64]uint64) {
	if len(chunks) == 0 {
		return nil, nil
	}
	fids := make(map[uint64]uint64, len(chunks))
	out := chunks[:0:0]
	for _, c := range chunks {
		fid, ok := db.chunkFileID(txn, c)
		if !ok {
			continue
		}
		fids[c] = fid
		if fid == fileID {
			out = append(out, c)
		}
	}
	return out, fids
}
