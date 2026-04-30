package ark

// CRC: crc-TvidMap.md
//
// Live in-memory tvid → (tag, value, origin) resolver shared by Store
// (LMDB V records) and TmpTagStore (tmp:// overlay). Loaded once at
// startup from V records, maintained at indexing time via TvidTxn,
// which mirrors LMDB's commit/abort semantics.

import (
	"math"
	"sync"
)

// TvidOrigin marks where a tvid was first registered.
// CRC: crc-TvidMap.md | R1957
type TvidOrigin uint8

const (
	OriginPersistent TvidOrigin = iota // loaded from V records
	OriginOverlay                      // allocated for tmp:// content
)

// tvidEntry is the live-map value: the resolved (tag, value) and origin.
type tvidEntry struct {
	Tag    string
	Value  string
	Origin TvidOrigin
}

// tvidPair keys the reverse-lookup map.
type tvidPair struct {
	Tag   string
	Value string
}

// TvidMap is the live in-memory tvid resolver. Reads take RLock; writes
// (commit, AllocOverlay, removeOverlayLocked) take the write lock.
// CRC: crc-TvidMap.md | R1953
type TvidMap struct {
	mu          sync.RWMutex
	entries     map[uint64]tvidEntry
	byPair      map[tvidPair]uint64
	nextOverlay uint64 // counts down from MaxUint64
}

// NewTvidMap constructs an empty resolver.
func NewTvidMap() *TvidMap {
	return &TvidMap{
		entries:     make(map[uint64]tvidEntry),
		byPair:      make(map[tvidPair]uint64),
		nextOverlay: math.MaxUint64,
	}
}

// LoadFromStore scans the persistent V records and registers every
// tvid with OriginPersistent. Resets existing entries first so a
// repeat call (e.g. after a rebuild) cannot leave stale tvids behind.
// CRC: crc-TvidMap.md | R1958
func (m *TvidMap) LoadFromStore(s *Store) error {
	snap, err := s.scanVRecordTvidsRaw()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[uint64]tvidEntry, len(snap))
	m.byPair = make(map[tvidPair]uint64, len(snap))
	for tvid, ta := range snap {
		m.entries[tvid] = tvidEntry{Tag: ta.Tag, Value: ta.Value, Origin: OriginPersistent}
		m.byPair[tvidPair{ta.Tag, ta.Value}] = tvid
	}
	return nil
}

// Resolve returns the (tag, value) for a tvid.
// CRC: crc-TvidMap.md | R1954
func (m *TvidMap) Resolve(tvid uint64) (tag, value string, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[tvid]
	return e.Tag, e.Value, ok
}

// Lookup returns the existing tvid for a (tag, value), or ok=false.
// CRC: crc-TvidMap.md | R1955
func (m *TvidMap) Lookup(tag, value string) (uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tvid, ok := m.byPair[tvidPair{tag, value}]
	return tvid, ok
}

// Snapshot returns a copy of the live map for diagnostics.
// CRC: crc-TvidMap.md | R1956
func (m *TvidMap) Snapshot() map[uint64]TagAlt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[uint64]TagAlt, len(m.entries))
	for tvid, e := range m.entries {
		out[tvid] = TagAlt{Tag: e.Tag, Value: e.Value}
	}
	return out
}

// AllocOverlay allocates a fresh overlay tvid for (tag, value). The
// counter decrements from MaxUint64 — overlay-issued tvids have the
// high bit set when interpreted as int64.
// CRC: crc-TvidMap.md | R1965
func (m *TvidMap) AllocOverlay(tag, value string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	tvid := m.nextOverlay
	m.nextOverlay--
	m.entries[tvid] = tvidEntry{Tag: tag, Value: value, Origin: OriginOverlay}
	m.byPair[tvidPair{tag, value}] = tvid
	return tvid
}

// RemoveOverlayUnused drops a tvid only if its origin is OriginOverlay.
// Persistent tvids are never removed by tmp:// cleanup — the LMDB
// record still owns them. CRC: crc-TvidMap.md | R1967
func (m *TvidMap) RemoveOverlayUnused(tvid uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[tvid]
	if !ok || e.Origin != OriginOverlay {
		return
	}
	delete(m.entries, tvid)
	delete(m.byPair, tvidPair{e.Tag, e.Value})
}

// Begin opens a fresh write-txn-scoped overlay.
// CRC: crc-TvidMap.md | R1959
func (m *TvidMap) Begin() *TvidTxn {
	return &TvidTxn{m: m}
}

// TvidTxn is an overlay scoped to one LMDB write transaction. Add and
// Remove accumulate into per-txn maps; Commit merges into the live map
// under write lock; Abort discards the overlay. The write-actor
// invariant (one env.Update at a time) keeps a single TvidTxn live
// at any moment. CRC: crc-TvidMap.md | R1959, R1962
type TvidTxn struct {
	m           *TvidMap
	added       map[uint64]tvidEntry
	addedByPair map[tvidPair]uint64
	removed     map[uint64]bool
}

// Add records a tvid registration in the overlay.
// CRC: crc-TvidMap.md | R1960
func (t *TvidTxn) Add(tvid uint64, tag, value string, origin TvidOrigin) {
	if t.added == nil {
		t.added = make(map[uint64]tvidEntry)
		t.addedByPair = make(map[tvidPair]uint64)
	}
	t.added[tvid] = tvidEntry{Tag: tag, Value: value, Origin: origin}
	t.addedByPair[tvidPair{tag, value}] = tvid
	delete(t.removed, tvid)
}

// Remove records a tvid removal in the overlay.
// CRC: crc-TvidMap.md | R1960
func (t *TvidTxn) Remove(tvid uint64) {
	if e, ok := t.added[tvid]; ok {
		delete(t.added, tvid)
		delete(t.addedByPair, tvidPair{e.Tag, e.Value})
		return
	}
	if t.removed == nil {
		t.removed = make(map[uint64]bool)
	}
	t.removed[tvid] = true
}

// Resolve consults the overlay first, then the live map.
// CRC: crc-TvidMap.md | R1961
func (t *TvidTxn) Resolve(tvid uint64) (tag, value string, ok bool) {
	if e, ok := t.added[tvid]; ok {
		return e.Tag, e.Value, true
	}
	if t.removed[tvid] {
		return "", "", false
	}
	return t.m.Resolve(tvid)
}

// Lookup returns an existing tvid for (tag, value), checking overlay
// added entries first, then the live map's reverse index. Live-map
// entries hidden by Remove are skipped. CRC: crc-TvidMap.md | R1955, R1961
func (t *TvidTxn) Lookup(tag, value string) (uint64, bool) {
	if tvid, ok := t.addedByPair[tvidPair{tag, value}]; ok {
		return tvid, true
	}
	tvid, ok := t.m.Lookup(tag, value)
	if !ok || t.removed[tvid] {
		return 0, false
	}
	return tvid, true
}

// Commit merges added/removed entries into the live map under write lock.
// CRC: crc-TvidMap.md | R1962
func (t *TvidTxn) Commit() {
	if len(t.added) == 0 && len(t.removed) == 0 {
		return
	}
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	for tvid := range t.removed {
		if e, ok := t.m.entries[tvid]; ok {
			delete(t.m.byPair, tvidPair{e.Tag, e.Value})
			delete(t.m.entries, tvid)
		}
	}
	for tvid, e := range t.added {
		t.m.entries[tvid] = e
		t.m.byPair[tvidPair{e.Tag, e.Value}] = tvid
	}
	t.added = nil
	t.addedByPair = nil
	t.removed = nil
}

// Abort discards the overlay.
// CRC: crc-TvidMap.md | R1962
func (t *TvidTxn) Abort() {
	t.added = nil
	t.addedByPair = nil
	t.removed = nil
}
