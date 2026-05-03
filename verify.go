package ark

import (
	"fmt"
	"io"
	"sort"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// CRC: crc-TagVerify.md | R2092, R2093, R2094, R2095, R2096, R2097, R2098, R2099, R2100, R2101, R2102

// VerifyOptions controls the scope and side-effects of `ark tag verify`.
type VerifyOptions struct {
	Repair bool
	Scope  string // "ext", "tag-totals", "all" (default "all")
}

// VerifyResult summarizes a verify run.
type VerifyResult struct {
	Issues   int
	Repaired int
}

type extDecl struct {
	tvidExt   uint64
	value     string
	expected  []uint64 // ResolveExtTarget result
	actual    map[uint64][]uint64
	routedTvs []TagValue
}

type extOrphan struct {
	tvidExt     uint64
	targetChunk uint64
	routedTvids []uint64
}

type extIssue struct {
	kind        string // "missing", "stale", "routed-drift"
	tvidExt     uint64
	targetChunk uint64
	expected    []uint64
	current     []uint64
}

// Verify runs the requested checks and writes one issue per line to w.
// Returns the result counts. Errors (DB problems, etc.) propagate up.
func (db *DB) Verify(opts VerifyOptions, w io.Writer) (VerifyResult, error) {
	scope := opts.Scope
	if scope == "" {
		scope = "all"
	}
	res := VerifyResult{}

	if scope == "ext" || scope == "all" {
		if err := db.verifyExt(opts.Repair, w, &res); err != nil {
			return res, err
		}
	}
	if scope == "tag-totals" || scope == "all" {
		if err := db.verifyTagTotals(opts.Repair, w, &res); err != nil {
			return res, err
		}
	}

	fmt.Fprintf(w, "verify: %d issues found, %d repaired\n", res.Issues, res.Repaired)
	return res, nil
}

// verifyExt walks every V[ext] entry, re-resolves its target, and
// compares against the X record set keyed by the same tvid_ext.
// Then walks all X records to find orphans (X without V[ext] backing).
// Then cross-checks ExtMap state against the X record set.
// CRC: crc-TagVerify.md | R2094, R2095, R2096
func (db *DB) verifyExt(repair bool, w io.Writer, res *VerifyResult) error {
	decls, orphans, err := db.loadExtState()
	if err != nil {
		return err
	}

	issues := db.computeExtIssues(decls)

	for _, iss := range issues {
		switch iss.kind {
		case "missing":
			fmt.Fprintf(w, "ext: missing X record for tvid_ext=%d target_chunk=%d (expected resolution)\n", iss.tvidExt, iss.targetChunk)
		case "stale":
			fmt.Fprintf(w, "ext: stale X record tvid_ext=%d → chunk %d (no longer resolves)\n", iss.tvidExt, iss.targetChunk)
		case "routed-drift":
			fmt.Fprintf(w, "ext: routed-tvid drift tvid_ext=%d chunk=%d expected=%v current=%v\n", iss.tvidExt, iss.targetChunk, iss.expected, iss.current)
		}
		res.Issues++
	}
	for _, o := range orphans {
		fmt.Fprintf(w, "ext: orphan X record tvid_ext=%d chunk=%d (no matching V[ext] declaration)\n", o.tvidExt, o.targetChunk)
		res.Issues++
	}

	mismatches := db.crossCheckExtMap(decls)
	for _, m := range mismatches {
		fmt.Fprintln(w, m)
		res.Issues++
	}

	if !repair || (len(issues) == 0 && len(orphans) == 0 && len(mismatches) == 0) {
		return nil
	}
	return db.repairExt(decls, issues, orphans, w, res)
}

// loadExtState reads the V[ext] decls and X record set into memory.
// CRC: crc-TagVerify.md | R2094, R2095
func (db *DB) loadExtState() (map[uint64]*extDecl, []extOrphan, error) {
	decls := make(map[uint64]*extDecl)
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		prefix := tagValuePrefix(tagExt, "")
		return scanPrefix(txn, db.store.dbi, prefix, func(_ *lmdb.Cursor, k, _ []byte) error {
			tag, value, tvid, ok := parseVKey(k)
			if !ok || tag != tagExt {
				return nil
			}
			target, routed, parseOK := ParseExtTarget(value)
			if !parseOK {
				return nil
			}
			d := &extDecl{
				tvidExt:   tvid,
				value:     value,
				expected:  db.ResolveExtTarget(target),
				actual:    make(map[uint64][]uint64),
				routedTvs: routed,
			}
			recs, err := db.store.ScanExtRecords(txn, tvid)
			if err != nil {
				return err
			}
			for _, r := range recs {
				d.actual[r.TargetChunkID] = r.RoutedTvids
			}
			decls[tvid] = d
			return nil
		})
	}); err != nil {
		return nil, nil, err
	}

	var orphans []extOrphan
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		return db.store.ScanAllExtRecords(txn, func(tvidExt, targetChunk uint64, routedTvids []uint64) error {
			if _, ok := decls[tvidExt]; !ok {
				orphans = append(orphans, extOrphan{tvidExt, targetChunk, routedTvids})
			}
			return nil
		})
	}); err != nil {
		return nil, nil, err
	}
	return decls, orphans, nil
}

// computeExtIssues classifies missing / stale / routed-drift per declaration.
func (db *DB) computeExtIssues(decls map[uint64]*extDecl) []extIssue {
	var issues []extIssue
	for tvid, d := range decls {
		expected := uniqueSorted(d.expected)
		actual := make([]uint64, 0, len(d.actual))
		for c := range d.actual {
			actual = append(actual, c)
		}
		actual = uniqueSorted(actual)

		expectedSet := make(map[uint64]bool, len(expected))
		for _, c := range expected {
			expectedSet[c] = true
		}
		for _, c := range expected {
			if _, ok := d.actual[c]; !ok {
				issues = append(issues, extIssue{kind: "missing", tvidExt: tvid, targetChunk: c, expected: expected})
			}
		}
		for _, c := range actual {
			if !expectedSet[c] {
				issues = append(issues, extIssue{kind: "stale", tvidExt: tvid, targetChunk: c, current: d.actual[c]})
			}
		}
		// Routed-tvid drift: routed_tvids in X record should match the routed
		// tags in the V[ext] value (looked up via TvidMap).
		want := make([]uint64, 0, len(d.routedTvs))
		for _, rt := range d.routedTvs {
			if t, ok := db.store.tvids.Lookup(rt.Tag, rt.Value); ok {
				want = append(want, t)
			}
		}
		want = uniqueSorted(want)
		for _, c := range expected {
			cur, ok := d.actual[c]
			if !ok {
				continue
			}
			cs := uniqueSorted(append([]uint64(nil), cur...))
			if !uint64SliceEqual(cs, want) {
				issues = append(issues, extIssue{kind: "routed-drift", tvidExt: tvid, targetChunk: c, expected: want, current: cs})
			}
		}
	}
	return issues
}

// crossCheckExtMap compares in-memory ExtMap state against the on-disk X
// record set and reports divergence as one string per issue.
// CRC: crc-TagVerify.md | R2096
func (db *DB) crossCheckExtMap(decls map[uint64]*extDecl) []string {
	if db.extmap == nil {
		return nil
	}
	var msgs []string

	// targetToChunk[tvidExt] should match the X record chunk set.
	db.extmap.mu.RLock()
	for tvid, d := range decls {
		mapTargets := append([]uint64(nil), db.extmap.targetToChunk[tvid]...)
		mapTargets = uniqueSorted(mapTargets)
		actual := make([]uint64, 0, len(d.actual))
		for c := range d.actual {
			actual = append(actual, c)
		}
		actual = uniqueSorted(actual)
		if !uint64SliceEqual(mapTargets, actual) {
			msgs = append(msgs, fmt.Sprintf("ext: ExtMap.targetToChunk[%d]=%v but X records=%v", tvid, mapTargets, actual))
		}
	}
	// extByAnchor: every value present in any anchor list must exist as a decl.
	for anchor, tvids := range db.extmap.extByAnchor {
		for _, tvid := range tvids {
			if _, ok := decls[tvid]; !ok {
				msgs = append(msgs, fmt.Sprintf("ext: ExtMap.extByAnchor[%q] references tvid_ext=%d with no V[ext] backing", anchor, tvid))
			}
		}
	}
	db.extmap.mu.RUnlock()
	sort.Strings(msgs)
	return msgs
}

// repairExt applies ext corrections in a single LMDB write transaction.
// CRC: crc-TagVerify.md | R2100, R2101
func (db *DB) repairExt(decls map[uint64]*extDecl, issues []extIssue, orphans []extOrphan, w io.Writer, res *VerifyResult) error {
	return db.store.WithTvidTxn(func(tt *TvidTxn) error {
		return db.store.env.Update(func(txn *lmdb.Txn) error {
			for _, iss := range issues {
				d := decls[iss.tvidExt]
				if d == nil {
					continue
				}
				switch iss.kind {
				case "missing":
					tvids, err := allocRoutedTvids(txn, tt, db, d.routedTvs, iss.targetChunk)
					if err != nil {
						return err
					}
					if err := db.store.WriteExtRecord(txn, iss.tvidExt, iss.targetChunk, tvids); err != nil {
						return err
					}
					res.Repaired++
				case "stale":
					if err := db.store.DeleteExtRecord(txn, iss.tvidExt, iss.targetChunk); err != nil {
						return err
					}
					for _, rt := range iss.current {
						if tag, val, ok := resolveTvidLookup(tt, db, rt); ok {
							if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, iss.targetChunk); err != nil {
								return err
							}
						}
					}
					res.Repaired++
				case "routed-drift":
					// Remove old, write new.
					if err := db.store.DeleteExtRecord(txn, iss.tvidExt, iss.targetChunk); err != nil {
						return err
					}
					for _, rt := range iss.current {
						if tag, val, ok := resolveTvidLookup(tt, db, rt); ok {
							if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, iss.targetChunk); err != nil {
								return err
							}
						}
					}
					tvids, err := allocRoutedTvids(txn, tt, db, d.routedTvs, iss.targetChunk)
					if err != nil {
						return err
					}
					if err := db.store.WriteExtRecord(txn, iss.tvidExt, iss.targetChunk, tvids); err != nil {
						return err
					}
					res.Repaired++
				}
			}
			for _, o := range orphans {
				if err := db.store.DeleteExtRecord(txn, o.tvidExt, o.targetChunk); err != nil {
					return err
				}
				for _, rt := range o.routedTvids {
					if tag, val, ok := resolveTvidLookup(tt, db, rt); ok {
						if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, o.targetChunk); err != nil {
							return err
						}
					}
				}
				res.Repaired++
			}
			return nil
		})
	})
	// Note: callers can run ExtMap.Rebuild after Verify returns; we don't
	// touch in-memory ExtMap state from inside this txn.
}

func allocRoutedTvids(txn *lmdb.Txn, tt *TvidTxn, db *DB, routedTvs []TagValue, targetChunk uint64) ([]uint64, error) {
	tvids := make([]uint64, 0, len(routedTvs))
	for _, rt := range routedTvs {
		t, err := db.store.addChunkIDToVRecord(txn, tt, rt.Tag, rt.Value, targetChunk)
		if err != nil {
			return nil, err
		}
		tvids = append(tvids, t)
	}
	return tvids, nil
}

func resolveTvidLookup(tt *TvidTxn, db *DB, tvid uint64) (string, string, bool) {
	return resolveTvid(tt, db, tvid)
}

// verifyTagTotals recomputes each T total from V multi-set sizes plus
// ExtMap.virtualTagCount and reports drift.
// CRC: crc-TagVerify.md | R2097
func (db *DB) verifyTagTotals(repair bool, w io.Writer, res *VerifyResult) error {
	stored := make(map[string]uint32)
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, db.store.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 2 || len(v) < 4 {
				return nil
			}
			stored[string(k[1:])] = decodeUint32(v[:4])
			return nil
		})
	}); err != nil {
		return err
	}

	// T tracks distinct (chunk, tag) pairs — the F-record count per tag.
	// A chunk with two `@connection:` values contributes one F entry
	// (+1 to T) but two V multi-set slots, so V counts overcount T.
	// Walk F records directly to compute the right number.
	computed := make(map[string]int)
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, db.store.dbi, []byte{byte(prefixTagFile)}, func(_ *lmdb.Cursor, k, _ []byte) error {
			_, tag, ok := parseFKey(k)
			if !ok {
				return nil
			}
			computed[tag]++
			return nil
		})
	}); err != nil {
		return err
	}

	type drift struct {
		tag      string
		stored   uint32
		computed int
	}
	var drifts []drift
	for tag, s := range stored {
		if int(s) != computed[tag] {
			drifts = append(drifts, drift{tag, s, computed[tag]})
		}
	}
	for tag, c := range computed {
		if _, ok := stored[tag]; !ok && c > 0 {
			drifts = append(drifts, drift{tag, 0, c})
		}
	}
	sort.Slice(drifts, func(i, j int) bool { return drifts[i].tag < drifts[j].tag })
	for _, d := range drifts {
		diff := d.computed - int(d.stored)
		fmt.Fprintf(w, "tag-total: drift on @%s: stored=%d computed=%d (diff=%+d)\n", d.tag, d.stored, d.computed, diff)
		res.Issues++
	}

	if !repair || len(drifts) == 0 {
		return nil
	}
	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		for _, d := range drifts {
			tk := tagTotalKey(d.tag)
			if d.computed == 0 {
				if err := txn.Del(db.store.dbi, tk, nil); err != nil && !lmdb.IsNotFound(err) {
					return err
				}
				continue
			}
			buf := make([]byte, 4)
			encodeUint32(buf, uint32(d.computed))
			if err := txn.Put(db.store.dbi, tk, buf, 0); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	res.Repaired += len(drifts)
	return nil
}

func uniqueSorted(xs []uint64) []uint64 {
	if len(xs) == 0 {
		return nil
	}
	seen := make(map[uint64]bool, len(xs))
	out := make([]uint64, 0, len(xs))
	for _, v := range xs {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uint64SliceEqual(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func decodeUint32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func encodeUint32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}
