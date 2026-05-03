package ark

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// CRC: crc-TagInspect.md | R2113, R2114, R2115, R2116, R2117, R2118, R2119
//
// This file supersedes the temporary cmd/extdiag tool that was used
// during the @ext debugging push (R2119). All of extdiag's output
// shapes — X records, V[ext] records, F[chunkid][ext] records, plus
// the chunk→fileid→path bridges — are reproduced here, and the
// in-memory ExtMap section is added because extdiag could only see
// disk state.

// ScopeExt is the v1 scope value for `ark tag inspect`. R2114 anticipates
// further scopes; the field on InspectOptions stays a string so they can
// land without a wire change.
const ScopeExt = "ext"

// InspectOptions controls what InspectExt collects and how it is shaped.
type InspectOptions struct {
	Scope  string // ScopeExt (v1)
	Target string // path filter; empty = no filter
}

// ExtInspectReport is the wire and CLI shape — three sections plus
// metadata. Marshals cleanly to JSON.
type ExtInspectReport struct {
	Scope        string                  `json:"scope"`
	Target       string                  `json:"target,omitempty"`
	ServerSide   bool                    `json:"server_side"`
	Disk         ExtDiskSection          `json:"disk"`
	InMemory     *ExtInMemorySection     `json:"in_memory,omitempty"`
	Bridges      []ExtBridgeEntry        `json:"bridges"`
	UnavailNote  string                  `json:"unavailable_note,omitempty"`
}

// ExtDiskSection — every X record, every V[ext] record, every
// F[chunkid][ext] record, decoded enough to be readable.
type ExtDiskSection struct {
	XRecords []ExtXRecord  `json:"x_records"`
	VRecords []ExtVRecord  `json:"v_records"`
	FRecords []ExtFRecord  `json:"f_records"`
}

// ExtXRecord — one decoded X record. Routed_tvids resolved to (tag, value).
type ExtXRecord struct {
	TvidExt       uint64     `json:"tvid_ext"`
	TargetChunkID uint64     `json:"target_chunkid"`
	TargetFileID  uint64     `json:"target_fileid,omitempty"`
	TargetPath    string     `json:"target_path,omitempty"`
	Routed        []TagValue `json:"routed"`
}

// ExtVRecord — V[ext][value][tvid_ext] → []source_chunkid.
type ExtVRecord struct {
	Value         string   `json:"value"`
	TvidExt       uint64   `json:"tvid_ext"`
	SourceChunks  []uint64 `json:"source_chunks"`
}

// ExtFRecord — F[source_chunkid][ext] → []tvid_ext.
type ExtFRecord struct {
	SourceChunkID uint64     `json:"source_chunkid"`
	SourceFileID  uint64     `json:"source_fileid,omitempty"`
	SourcePath    string     `json:"source_path,omitempty"`
	Decls         []ExtDecl  `json:"decls"`
}

// ExtDecl — one @ext declaration on a source chunk, decoded.
type ExtDecl struct {
	TvidExt uint64 `json:"tvid_ext"`
	Value   string `json:"value"`
}

// ExtInMemorySection — snapshot of every map ExtMap holds.
type ExtInMemorySection struct {
	TargetToChunk       map[uint64][]uint64            `json:"target_to_chunk"`
	ChunkToTargets      map[uint64][]uint64            `json:"chunk_to_targets"`
	ExtSource           map[uint64]uint64              `json:"ext_source"`
	RoutedTagsByTvidExt map[uint64][]TagValue          `json:"routed_tags_by_tvid_ext"`
	FileIDToTvids       map[uint64][]uint64            `json:"fileid_to_tvids"`
	ExtByAnchor         map[string][]uint64            `json:"ext_by_anchor"`
	UnresolvedTargets   []uint64                       `json:"unresolved_targets"`
	VirtualTagCount     map[string]int                 `json:"virtual_tag_count"`
	OverlayRoutings     map[uint64]map[uint64][]uint64 `json:"overlay_routings"`
	OverlayValues       map[string]map[string][]uint64 `json:"overlay_values"`
	FileIDPaths         map[uint64]string              `json:"fileid_paths"`
}

// ExtBridgeEntry — per-tvid_ext consolidated view linking on-disk
// and in-memory state with full path/tag decoding.
type ExtBridgeEntry struct {
	TvidExt        uint64     `json:"tvid_ext"`
	Value          string     `json:"value"`
	SourceChunkID  uint64     `json:"source_chunkid"`
	SourcePath     string     `json:"source_path,omitempty"`
	DiskTargets    []uint64   `json:"disk_targets"`
	MapTargets     []uint64   `json:"map_targets"`
	TargetPaths    []string   `json:"target_paths,omitempty"`
	Routed         []TagValue `json:"routed"`
	Unresolved     bool       `json:"unresolved,omitempty"`
}

// InspectExt collects the report. Read-only. Uses one View txn for
// all on-disk reads and one ExtMap RLock for the in-memory snapshot.
// CRC: crc-DB.md | R2113, R2114, R2117
func (db *DB) InspectExt(opts InspectOptions) (*ExtInspectReport, error) {
	rep := &ExtInspectReport{
		Scope:      ScopeExt,
		Target:     opts.Target,
		ServerSide: true,
	}

	// Resolve target filter to chunkid set (if any).
	var targetChunkSet map[uint64]bool
	var targetFileID uint64
	if opts.Target != "" {
		ids := db.ChunkIDsForPath(opts.Target)
		targetChunkSet = make(map[uint64]bool, len(ids))
		for _, c := range ids {
			targetChunkSet[c] = true
		}
		if info, err := db.fts.CheckFile(opts.Target); err == nil {
			targetFileID = info.FileID
		}
	}

	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		disk, err := db.collectExtDisk(txn, targetChunkSet)
		if err != nil {
			return err
		}
		rep.Disk = disk
		return nil
	}); err != nil {
		return nil, err
	}

	if db.extmap != nil {
		mem := db.collectExtInMemory(targetFileID)
		rep.InMemory = mem
	}

	rep.Bridges = db.buildExtBridges(rep.Disk, rep.InMemory)
	return rep, nil
}

// collectExtDisk walks X, V[ext], and F[*][ext] inside a read txn.
// When pathChunks is non-nil, all three sections are filtered to entries
// where some chunk in pathChunks appears as either the target_chunkid of
// an X record or the source chunkid of a V[ext] record. The filter
// derives a "relevant tvid_ext" set from those two memberships, then
// includes every section entry tied to a relevant tvid.
// CRC: crc-Store.md | R2114, R2115
func (db *DB) collectExtDisk(txn *lmdb.Txn, pathChunks map[uint64]bool) (ExtDiskSection, error) {
	var out ExtDiskSection
	relevant := make(map[uint64]bool)

	// First pass: scan all X records, collect tvid_exts whose target is in pathChunks.
	allX := make([]ExtXRecord, 0)
	err := db.store.ScanAllExtRecords(txn, func(tvidExt, targetChunk uint64, routed []uint64) error {
		entry := ExtXRecord{TvidExt: tvidExt, TargetChunkID: targetChunk}
		if fid, ok := db.chunkFileID(txn, targetChunk); ok {
			entry.TargetFileID = fid
			if path, ok := db.resolveFilePath(fid); ok {
				entry.TargetPath = path
			}
		}
		for _, rt := range routed {
			if tag, val, ok := db.store.tvids.Resolve(rt); ok {
				entry.Routed = append(entry.Routed, TagValue{Tag: tag, Value: val})
			}
		}
		allX = append(allX, entry)
		if pathChunks != nil && pathChunks[targetChunk] {
			relevant[tvidExt] = true
		}
		return nil
	})
	if err != nil {
		return out, err
	}

	// First pass: V[ext] — collect entries; mark tvid relevant if any source is in pathChunks.
	allV := make([]ExtVRecord, 0)
	prefix := tagValuePrefix(tagExt, "")
	err = scanPrefix(txn, db.store.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
		tag, value, tvid, ok := parseVKey(k)
		if !ok || tag != tagExt {
			return nil
		}
		srcs := decodeVarints(v)
		allV = append(allV, ExtVRecord{Value: value, TvidExt: tvid, SourceChunks: srcs})
		if pathChunks != nil {
			for _, c := range srcs {
				if pathChunks[c] {
					relevant[tvid] = true
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}

	// Apply the filter. With no path filter, "relevant" is universal.
	keep := func(tvid uint64) bool { return pathChunks == nil || relevant[tvid] }

	for _, x := range allX {
		if keep(x.TvidExt) {
			out.XRecords = append(out.XRecords, x)
		}
	}
	sort.Slice(out.XRecords, func(i, j int) bool {
		if out.XRecords[i].TvidExt != out.XRecords[j].TvidExt {
			return out.XRecords[i].TvidExt < out.XRecords[j].TvidExt
		}
		return out.XRecords[i].TargetChunkID < out.XRecords[j].TargetChunkID
	})

	sourceChunkSet := make(map[uint64]bool)
	for _, v := range allV {
		if !keep(v.TvidExt) {
			continue
		}
		out.VRecords = append(out.VRecords, v)
		for _, c := range v.SourceChunks {
			sourceChunkSet[c] = true
		}
	}
	sort.Slice(out.VRecords, func(i, j int) bool { return out.VRecords[i].TvidExt < out.VRecords[j].TvidExt })

	// F records on each source chunk that surviving V[ext] referenced.
	srcs := make([]uint64, 0, len(sourceChunkSet))
	for c := range sourceChunkSet {
		srcs = append(srcs, c)
	}
	sort.Slice(srcs, func(i, j int) bool { return srcs[i] < srcs[j] })
	for _, srcChunk := range srcs {
		tvids, err := db.store.ReadExtTvidsForChunk(txn, srcChunk)
		if err != nil || len(tvids) == 0 {
			continue
		}
		entry := ExtFRecord{SourceChunkID: srcChunk}
		if fid, ok := db.chunkFileID(txn, srcChunk); ok {
			entry.SourceFileID = fid
			if path, ok := db.resolveFilePath(fid); ok {
				entry.SourcePath = path
			}
		}
		for _, te := range tvids {
			if !keep(te) {
				continue
			}
			if _, val, ok := db.store.tvids.Resolve(te); ok {
				entry.Decls = append(entry.Decls, ExtDecl{TvidExt: te, Value: val})
			}
		}
		if len(entry.Decls) == 0 {
			continue
		}
		out.FRecords = append(out.FRecords, entry)
	}
	return out, nil
}

// collectExtInMemory snapshots every ExtMap map under one RLock.
// CRC: crc-ExtMap.md | R2114
func (db *DB) collectExtInMemory(targetFileID uint64) *ExtInMemorySection {
	m := db.extmap
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Filter helper: when targetFileID != 0, keep only tvid_exts that
	// route to that fileid.
	keepTvid := func(tvid uint64) bool {
		if targetFileID == 0 {
			return true
		}
		for _, t := range m.fileidToTvids[targetFileID] {
			if t == tvid {
				return true
			}
		}
		return false
	}

	out := &ExtInMemorySection{
		TargetToChunk:       make(map[uint64][]uint64),
		ChunkToTargets:      make(map[uint64][]uint64),
		ExtSource:           make(map[uint64]uint64),
		RoutedTagsByTvidExt: make(map[uint64][]TagValue),
		FileIDToTvids:       make(map[uint64][]uint64),
		ExtByAnchor:         make(map[string][]uint64),
		VirtualTagCount:     make(map[string]int, len(m.virtualTagCount)),
		OverlayRoutings:     make(map[uint64]map[uint64][]uint64),
		OverlayValues:       make(map[string]map[string][]uint64),
		FileIDPaths:         make(map[uint64]string),
	}
	for tvid, chunks := range m.targetToChunk {
		if !keepTvid(tvid) {
			continue
		}
		out.TargetToChunk[tvid] = append([]uint64(nil), chunks...)
	}
	for chunk, tvids := range m.chunkToTargets {
		filtered := tvids[:0:0]
		for _, t := range tvids {
			if keepTvid(t) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			out.ChunkToTargets[chunk] = filtered
		}
	}
	for tvid, src := range m.extSource {
		if keepTvid(tvid) {
			out.ExtSource[tvid] = src
		}
	}
	for tvid, routed := range m.routedTagsByTvidExt {
		if keepTvid(tvid) {
			dup := make([]TagValue, len(routed))
			copy(dup, routed)
			out.RoutedTagsByTvidExt[tvid] = dup
		}
	}
	for fid, tvids := range m.fileidToTvids {
		if targetFileID != 0 && fid != targetFileID {
			continue
		}
		out.FileIDToTvids[fid] = append([]uint64(nil), tvids...)
		if path, ok := db.resolveFilePath(fid); ok {
			out.FileIDPaths[fid] = path
		}
	}
	for anchor, tvids := range m.extByAnchor {
		filtered := tvids[:0:0]
		for _, t := range tvids {
			if keepTvid(t) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			out.ExtByAnchor[anchor] = filtered
		}
	}
	for tvid := range m.unresolvedTargets {
		if keepTvid(tvid) {
			out.UnresolvedTargets = append(out.UnresolvedTargets, tvid)
		}
	}
	sort.Slice(out.UnresolvedTargets, func(i, j int) bool { return out.UnresolvedTargets[i] < out.UnresolvedTargets[j] })
	for tag, count := range m.virtualTagCount {
		out.VirtualTagCount[tag] = count
	}
	for tvid, perTarget := range m.overlayRoutings {
		if !keepTvid(tvid) {
			continue
		}
		copyMap := make(map[uint64][]uint64, len(perTarget))
		for tc, rts := range perTarget {
			copyMap[tc] = append([]uint64(nil), rts...)
		}
		out.OverlayRoutings[tvid] = copyMap
	}
	for tag, vals := range m.overlayValues {
		copyVals := make(map[string][]uint64, len(vals))
		for v, chunks := range vals {
			copyVals[v] = append([]uint64(nil), chunks...)
		}
		out.OverlayValues[tag] = copyVals
	}
	return out
}

// buildExtBridges produces the consolidated per-tvid_ext view.
// CRC: crc-TagInspect.md | R2114
func (db *DB) buildExtBridges(disk ExtDiskSection, mem *ExtInMemorySection) []ExtBridgeEntry {
	tvids := make(map[uint64]bool)
	for _, x := range disk.XRecords {
		tvids[x.TvidExt] = true
	}
	for _, v := range disk.VRecords {
		tvids[v.TvidExt] = true
	}
	if mem != nil {
		for tvid := range mem.TargetToChunk {
			tvids[tvid] = true
		}
		for tvid := range mem.ExtSource {
			tvids[tvid] = true
		}
	}

	// Per-tvid_ext value, disk targets, routed pairs, and resolved
	// chunkid → path — all from disk records that already carried this
	// info, so bridges need no fresh LMDB reads.
	valueByTvid := make(map[uint64]string, len(disk.VRecords))
	for _, v := range disk.VRecords {
		valueByTvid[v.TvidExt] = v.Value
	}
	diskTargets := make(map[uint64][]uint64)
	chunkPath := make(map[uint64]string)
	for _, x := range disk.XRecords {
		diskTargets[x.TvidExt] = append(diskTargets[x.TvidExt], x.TargetChunkID)
		if x.TargetPath != "" {
			chunkPath[x.TargetChunkID] = x.TargetPath
		}
	}
	for _, lst := range diskTargets {
		sort.Slice(lst, func(i, j int) bool { return lst[i] < lst[j] })
	}
	for _, f := range disk.FRecords {
		if f.SourcePath != "" {
			chunkPath[f.SourceChunkID] = f.SourcePath
		}
	}
	routedByTvid := make(map[uint64][]TagValue)
	for _, x := range disk.XRecords {
		if _, ok := routedByTvid[x.TvidExt]; !ok && len(x.Routed) > 0 {
			routedByTvid[x.TvidExt] = x.Routed
		}
	}

	keys := make([]uint64, 0, len(tvids))
	for tvid := range tvids {
		keys = append(keys, tvid)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	out := make([]ExtBridgeEntry, 0, len(keys))
	for _, tvid := range keys {
		entry := ExtBridgeEntry{
			TvidExt:     tvid,
			Value:       valueByTvid[tvid],
			DiskTargets: diskTargets[tvid],
			Routed:      routedByTvid[tvid],
		}
		if mem != nil {
			entry.MapTargets = append([]uint64(nil), mem.TargetToChunk[tvid]...)
			sort.Slice(entry.MapTargets, func(i, j int) bool { return entry.MapTargets[i] < entry.MapTargets[j] })
			if src, ok := mem.ExtSource[tvid]; ok {
				entry.SourceChunkID = src
			}
			for _, t := range mem.UnresolvedTargets {
				if t == tvid {
					entry.Unresolved = true
					break
				}
			}
		}
		seen := make(map[uint64]bool)
		for _, c := range append(append([]uint64(nil), entry.DiskTargets...), entry.MapTargets...) {
			if seen[c] {
				continue
			}
			seen[c] = true
			if p, ok := chunkPath[c]; ok {
				entry.TargetPaths = append(entry.TargetPaths, p)
			}
		}
		if p, ok := chunkPath[entry.SourceChunkID]; ok {
			entry.SourcePath = p
		}
		out = append(out, entry)
	}
	return out
}

// WriteText emits a plain-text rendering of the report grouped by section.
// CRC: crc-TagInspect.md | R2116
func (rep *ExtInspectReport) WriteText(w io.Writer) {
	fmt.Fprintf(w, "=== ark tag inspect --scope %s ===\n", rep.Scope)
	if rep.Target != "" {
		fmt.Fprintf(w, "target: %s\n", rep.Target)
	}
	fmt.Fprintf(w, "server-side: %v\n", rep.ServerSide)
	if rep.UnavailNote != "" {
		fmt.Fprintf(w, "%s\n", rep.UnavailNote)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "--- On-disk ---")
	fmt.Fprintf(w, "X records (%d):\n", len(rep.Disk.XRecords))
	for _, x := range rep.Disk.XRecords {
		fmt.Fprintf(w, "  X[tvid_ext=%d, target=%d] → ", x.TvidExt, x.TargetChunkID)
		if x.TargetPath != "" {
			fmt.Fprintf(w, "%s ", x.TargetPath)
		}
		fmt.Fprint(w, "routed=[")
		for i, tv := range x.Routed {
			if i > 0 {
				fmt.Fprint(w, ", ")
			}
			fmt.Fprintf(w, "%s=%q", tv.Tag, tv.Value)
		}
		fmt.Fprintln(w, "]")
	}
	fmt.Fprintf(w, "V[ext] records (%d):\n", len(rep.Disk.VRecords))
	for _, v := range rep.Disk.VRecords {
		fmt.Fprintf(w, "  V[ext][%q][tvid=%d] → sources=%v\n", v.Value, v.TvidExt, v.SourceChunks)
	}
	fmt.Fprintf(w, "F[chunkid][ext] records (%d):\n", len(rep.Disk.FRecords))
	for _, f := range rep.Disk.FRecords {
		fmt.Fprintf(w, "  F[%d][ext]", f.SourceChunkID)
		if f.SourcePath != "" {
			fmt.Fprintf(w, " %s", f.SourcePath)
		}
		fmt.Fprint(w, ":\n")
		for _, d := range f.Decls {
			fmt.Fprintf(w, "    tvid=%d %q\n", d.TvidExt, d.Value)
		}
	}
	fmt.Fprintln(w)

	if rep.InMemory != nil {
		mem := rep.InMemory
		fmt.Fprintln(w, "--- In-memory ExtMap ---")
		fmt.Fprintf(w, "targetToChunk (%d):\n", len(mem.TargetToChunk))
		writeUintMapSorted(w, mem.TargetToChunk, "  ")
		fmt.Fprintf(w, "chunkToTargets (%d):\n", len(mem.ChunkToTargets))
		writeUintMapSorted(w, mem.ChunkToTargets, "  ")
		fmt.Fprintf(w, "extSource (%d):\n", len(mem.ExtSource))
		writeUintScalarSorted(w, mem.ExtSource, "  ")
		fmt.Fprintf(w, "routedTagsByTvidExt (%d):\n", len(mem.RoutedTagsByTvidExt))
		rtKeys := make([]uint64, 0, len(mem.RoutedTagsByTvidExt))
		for k := range mem.RoutedTagsByTvidExt {
			rtKeys = append(rtKeys, k)
		}
		sort.Slice(rtKeys, func(i, j int) bool { return rtKeys[i] < rtKeys[j] })
		for _, k := range rtKeys {
			fmt.Fprintf(w, "  %d → ", k)
			for i, tv := range mem.RoutedTagsByTvidExt[k] {
				if i > 0 {
					fmt.Fprint(w, ", ")
				}
				fmt.Fprintf(w, "%s=%q", tv.Tag, tv.Value)
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "fileidToTvids (%d):\n", len(mem.FileIDToTvids))
		for fid, tvids := range mem.FileIDToTvids {
			path := mem.FileIDPaths[fid]
			fmt.Fprintf(w, "  fileid=%d [%s] → %v\n", fid, path, tvids)
		}
		fmt.Fprintf(w, "extByAnchor (%d):\n", len(mem.ExtByAnchor))
		anchorKeys := make([]string, 0, len(mem.ExtByAnchor))
		for k := range mem.ExtByAnchor {
			anchorKeys = append(anchorKeys, k)
		}
		sort.Strings(anchorKeys)
		for _, k := range anchorKeys {
			fmt.Fprintf(w, "  %q → %v\n", k, mem.ExtByAnchor[k])
		}
		fmt.Fprintf(w, "unresolvedTargets: %v\n", mem.UnresolvedTargets)
		fmt.Fprintf(w, "virtualTagCount (%d):\n", len(mem.VirtualTagCount))
		tagKeys := make([]string, 0, len(mem.VirtualTagCount))
		for k := range mem.VirtualTagCount {
			tagKeys = append(tagKeys, k)
		}
		sort.Strings(tagKeys)
		for _, k := range tagKeys {
			fmt.Fprintf(w, "  %s: %d\n", k, mem.VirtualTagCount[k])
		}
		fmt.Fprintf(w, "overlayRoutings (%d): %d entries\n", len(mem.OverlayRoutings), countOverlayRoutings(mem.OverlayRoutings))
		fmt.Fprintf(w, "overlayValues (%d tags)\n", len(mem.OverlayValues))
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "--- Bridges ---")
	for _, b := range rep.Bridges {
		fmt.Fprintf(w, "tvid_ext=%d %q\n", b.TvidExt, b.Value)
		if b.SourceChunkID != 0 {
			fmt.Fprintf(w, "  source: chunk=%d  %s\n", b.SourceChunkID, b.SourcePath)
		}
		if !uint64SliceEqual(b.DiskTargets, b.MapTargets) {
			fmt.Fprintf(w, "  ⚠ disk targets %v ≠ map targets %v\n", b.DiskTargets, b.MapTargets)
		} else {
			fmt.Fprintf(w, "  targets: %v\n", b.DiskTargets)
		}
		for _, p := range b.TargetPaths {
			fmt.Fprintf(w, "    → %s\n", p)
		}
		if len(b.Routed) > 0 {
			fmt.Fprint(w, "  routed: ")
			for i, tv := range b.Routed {
				if i > 0 {
					fmt.Fprint(w, ", ")
				}
				fmt.Fprintf(w, "%s=%q", tv.Tag, tv.Value)
			}
			fmt.Fprintln(w)
		}
		if b.Unresolved {
			fmt.Fprintln(w, "  unresolved: true")
		}
	}
}

// WriteJSON emits the report as JSON.
// CRC: crc-TagInspect.md | R2116
func (rep *ExtInspectReport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func writeUintMapSorted(w io.Writer, m map[uint64][]uint64, indent string) {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		fmt.Fprintf(w, "%s%d → %v\n", indent, k, m[k])
	}
}

func writeUintScalarSorted(w io.Writer, m map[uint64]uint64, indent string) {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		fmt.Fprintf(w, "%s%d → %d\n", indent, k, m[k])
	}
}

func countOverlayRoutings(m map[uint64]map[uint64][]uint64) int {
	n := 0
	for _, per := range m {
		n += len(per)
	}
	return n
}
