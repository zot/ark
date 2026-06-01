package ark

// CRC: crc-Store.md

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// Store manages ark's own LMDB subdatabase for missing files,
// unresolved files, settings, and tag tracking.
type Store struct {
	env *lmdb.Env
	dbi lmdb.DBI
	// filesForChunk resolves a chunkID to the fileids that reference it,
	// using the provided LMDB txn (must read microfts2's C records).
	// Set by the DB during Open via SetChunkResolver. May be nil during
	// Init (e.g. in tests that exercise Store directly without microfts2).
	// CRC: crc-Store.md | R1887, R1888
	filesForChunk func(txn *lmdb.Txn, chunkID uint64) []uint64
	// chunksForFile resolves a fileID to the chunkids it references.
	// Opens its own View (microfts2 FileInfoByID isn't txn-aware). Called
	// before the V-record scan so the resolver runs outside the scan's
	// txn. CRC: crc-Store.md | R1889
	chunksForFile func(fileID uint64) []uint64
	// tmp is the in-memory tag overlay for tmp:// content. Reads union
	// LMDB results with overlay results; chunkid- and fileid-keyed
	// writes dispatch by high bit of the id (overlay ids count down
	// from MaxUint64).
	// CRC: crc-Store.md | R1946, R1947
	tmp *TmpTagStore
	// tvids is the shared tvid → (tag, value) resolver. Loaded once
	// from V records during DB.Open via LoadTvidMap; maintained by
	// TvidTxn during indexing writes.
	// CRC: crc-Store.md | R1953, R1958
	tvids *TvidMap
	// extmap supplies per-tag virtual contribution counts for the
	// T-total query path. Set by DB.Open after ExtMap.Rebuild; nil
	// in test paths that don't go through DB.
	// CRC: crc-Store.md | R2010
	extmap *ExtMap
}

// SetTmpTagStore wires the in-memory tag overlay. Called by DB after
// the overlay is constructed.
// CRC: crc-Store.md | R1941, R1946
func (s *Store) SetTmpTagStore(tmp *TmpTagStore) {
	s.tmp = tmp
}

// SetExtMap wires the in-memory @ext routing state. Called by DB.Open
// after ExtMap.Rebuild so TagCounts can augment T-totals with
// virtual ext-routed contributions.
// CRC: crc-Store.md | R2010
func (s *Store) SetExtMap(m *ExtMap) {
	s.extmap = m
}

// TvidMap returns the resolver. Always non-nil after OpenStore.
// CRC: crc-Store.md | R1953
func (s *Store) TvidMap() *TvidMap {
	return s.tvids
}

// LoadTvidMap performs the one-time V-prefix scan that populates the
// resolver with OriginPersistent entries. Called by DB.Open after the
// tag-store schema check passes. CRC: crc-Store.md | R1958
func (s *Store) LoadTvidMap() error {
	return s.tvids.LoadFromStore(s)
}

// SetChunkResolver wires both directions of the chunkID↔fileID resolver.
// Called by DB.Open after microfts2 is ready.
//   - toFiles runs INSIDE the caller's txn (used by TagFiles).
//   - toChunks opens its OWN view (microfts2 FileInfoByID is not txn-aware);
//     called by FileTagValues before its main scan.
//
// CRC: crc-Store.md | R1887, R1889
func (s *Store) SetChunkResolver(toFiles func(txn *lmdb.Txn, chunkID uint64) []uint64, toChunks func(fileID uint64) []uint64) {
	s.filesForChunk = toFiles
	s.chunksForFile = toChunks
}

// MissingRecord is a file that was indexed but no longer exists at its path.
type MissingRecord struct {
	FileID   uint64 `json:"fileId"`
	Path     string `json:"path"`
	LastSeen int64  `json:"lastSeen"`
}

// UnresolvedRecord is a file that doesn't match any include or exclude pattern.
type UnresolvedRecord struct {
	Path      string `json:"path"`
	FirstSeen int64  `json:"firstSeen"`
	Dir       string `json:"dir"`
}

// I record field names — pseudo-enum for known config and operational fields.
// CRC: crc-Store.md | R1532, R1533
const (
	IFieldDotfiles        = "dotfiles"
	IFieldCaseInsensitive = "case_insensitive"
	IFieldEmbedCmd        = "embed_cmd"
	IFieldQueryCmd        = "query_cmd"
	IFieldTagModel        = "tag_model"
	IFieldDefaultInclude  = "default_include"
	IFieldDefaultExclude  = "default_exclude"
	IFieldStrategies      = "strategies"
	IFieldSources         = "sources"
	IFieldChunkers        = "chunkers"
	IFieldSessionTTL      = "session_ttl"
	IFieldSearchExclude   = "search_exclude"
	IFieldEmbedTiers      = "embed_tiers"
	IFieldSchedule        = "schedule"
	// Operational fields
	IFieldNextTvid       = "next_tvid"
	IFieldScheduleConfig = "schedule_config"
)

// E record condition names.
// CRC: crc-Store.md | R1546
const (
	ECondModelMismatch     = "model_mismatch"
	ECondIndexStale        = "index_stale"
	ECondConfigCatastrophe = "config_catastrophe"
)

// TagFileRecord is a per-(chunk, file) tag count returned by TagFiles.
// FileID is resolved from ChunkID via microfts2 FilesForChunk; a chunk
// shared across N files yields N records. File-level callers dedupe
// by FileID.
// CRC: crc-Store.md | R1888
type TagFileRecord struct {
	ChunkID uint64
	FileID  uint64
	Tag     string
	Count   uint32
}

// ChunkTagValues groups a chunkid with the tag-values extracted from
// that chunk. Used by UpdateTagValues / AppendTagValues to write
// V/F records keyed by chunkid. FileID is optional and populated by
// tmp:// callers so the overlay dispatcher can route the entry to
// TmpTagStore by fileid; persistent callers leave it zero.
// CRC: crc-Store.md | R1883, R1884, R1947
type ChunkTagValues struct {
	ChunkID uint64
	FileID  uint64
	Values  []TagValue
}

// TagCount is a tag name with its total count.
type TagCount struct {
	Tag   string `json:"tag"`
	Count uint32 `json:"count"`
}

// Key prefixes for the ark subdatabase.
const (
	prefixMissing        = 'M'
	prefixUnresolved     = 'U'
	prefixInfo           = 'I'
	prefixTagTotal       = 'T'
	prefixTagFile        = 'F'
	prefixTagDef         = 'D'
	prefixTagValue       = 'V'
	prefixEmbedValue     = "EV" // R1290: tag-value compound embeddings
	prefixEmbedChunk     = "EC" // R1598: chunk-level embeddings
	prefixEmbedFileCent  = "EF" // R1599: file centroid (running sum + count)
	prefixEmbedDef       = "ED" // R2151: tag-definition embeddings
	prefixError          = 'E'  // R1543: persistent error conditions (E + name → JSON)
	prefixPageContent    = "PC" // R1720: per-page zlib-compressed chunk text blob
	prefixExtRouting     = 'X'  // R1989: @ext provenance (X[tvid_ext][target_chunkid] → routed_tvid varints)
	prefixSerial         = 'S'  // R2174: vector freshness side-index (S + original-key → varint serial)
	prefixHotCorrelation = "HC" // R2226: tag → top-K chunks cosine cache (HC + tag + chunkid:8 → score:float64)
	// prefixDiscussed is the first occupant of the `R` recall-feature
	// namespace; future R* records (proposals, processed-stamps, etc.)
	// follow the same two-letter convention. R2648, R2649
	prefixDiscussed = "RD"
	// Derived-tag records (statistical attach-proposal pass). The
	// derivation pass writes RC + RF as a side effect of
	// `ark connections recall --propose`; the Tag Forge writes RJ via
	// Store.RejectDerived. R2664–R2666
	prefixDerivedCandidate = "RC" // R2664 — RC + chunkid varint + tagname → 8-byte BE tally
	prefixDerivedRejection = "RJ" // R2665, R2874 (Recall Judgment) -- RJ + chunkid varint + tagname -> signed-varint(score) + 8-byte BE unix nanos
	prefixDerivedFreshness = "RF" // R2666 — RF + chunkid varint → varint serial
	// prefixSurfaceCooldown is the per-(session, chunk) surface-cooldown
	// sibling of RD — keyed by chunk instead of tag-value. R2882
	prefixSurfaceCooldown = "RM" // R2882 — RM + session + \x00 + chunkid varint → 8-byte BE unix nanos (last surfaced)
)

// Reserved tag names used by the routing/identity machinery.
const (
	tagExt = "ext" // R1991: @ext compound tag (source-side)
	tagID  = "id"  // R1986: @id identity tag (UUID branch of ext target resolution)
)

// OpenStore opens or creates the ark subdatabase within the given LMDB environment.
func OpenStore(env *lmdb.Env) (*Store, error) {
	var dbi lmdb.DBI
	err := env.Update(func(txn *lmdb.Txn) error {
		var err error
		dbi, err = txn.OpenDBI("ark", lmdb.Create)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("open ark subdatabase: %w", err)
	}
	return &Store{env: env, dbi: dbi, tvids: NewTvidMap()}, nil
}

// AddMissing records a missing file.
func (s *Store) AddMissing(fileid uint64, path string, lastSeen time.Time) error {
	rec := MissingRecord{Path: path, LastSeen: lastSeen.UnixNano()}
	val, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := missingKey(fileid)
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, key, val, 0)
	})
}

// RemoveMissing removes a missing file record.
func (s *Store) RemoveMissing(fileid uint64) error {
	key := missingKey(fileid)
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Del(s.dbi, key, nil)
	})
}

// ListMissing returns all missing file records.
func (s *Store) ListMissing() ([]MissingRecord, error) {
	var records []MissingRecord
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixMissing)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 9 {
				return nil
			}
			var rec MissingRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				rec.FileID = binary.BigEndian.Uint64(k[1:9])
				records = append(records, rec)
			}
			return nil
		})
	})
	return records, err
}

// AddUnresolved records an unresolved file.
func (s *Store) AddUnresolved(path, dir string) error {
	rec := UnresolvedRecord{Path: path, FirstSeen: time.Now().UnixNano(), Dir: dir}
	val, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := unresolvedKey(path)
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, key, val, 0)
	})
}

// RemoveUnresolved removes an unresolved file record.
func (s *Store) RemoveUnresolved(path string) error {
	key := unresolvedKey(path)
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Del(s.dbi, key, nil)
	})
}

// ListUnresolved returns all unresolved file records.
func (s *Store) ListUnresolved() ([]UnresolvedRecord, error) {
	var records []UnresolvedRecord
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixUnresolved)}, func(_ *lmdb.Cursor, k, v []byte) error {
			var rec UnresolvedRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				records = append(records, rec)
			}
			return nil
		})
	})
	return records, err
}

// CleanUnresolved removes unresolved entries for files no longer on disk.
func (s *Store) CleanUnresolved() error {
	records, err := s.ListUnresolved()
	if err != nil {
		return err
	}
	for _, rec := range records {
		if _, err := os.Stat(rec.Path); os.IsNotExist(err) {
			if err := s.RemoveUnresolved(rec.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// DismissByPattern removes missing records where the path matches any pattern.
// Returns the dismissed records (with FileID populated) for engine cleanup.
func (s *Store) DismissByPattern(patterns []string, matcher *Matcher) ([]MissingRecord, error) {
	var dismissed []MissingRecord
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixMissing)}, func(cur *lmdb.Cursor, k, v []byte) error {
			if len(k) < 9 {
				return nil
			}
			var rec MissingRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				for _, pat := range patterns {
					if matcher.Match(pat, rec.Path, "", false) {
						rec.FileID = binary.BigEndian.Uint64(k[1:9])
						dismissed = append(dismissed, rec)
						return cur.Del(0)
					}
				}
			}
			return nil
		})
	})
	return dismissed, err
}

// ResolveByPattern removes unresolved records where the path matches any pattern.
func (s *Store) ResolveByPattern(patterns []string, matcher *Matcher) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixUnresolved)}, func(cur *lmdb.Cursor, k, v []byte) error {
			var rec UnresolvedRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				for _, pat := range patterns {
					if matcher.Match(pat, rec.Path, "", false) {
						return cur.Del(0)
					}
				}
			}
			return nil
		})
	})
}

// --- I record helpers (per-field config storage) ---
// CRC: crc-Store.md | R1537, R1538

func makeIKey(name string) []byte {
	key := make([]byte, 1+len(name))
	key[0] = byte(prefixInfo)
	copy(key[1:], name)
	return key
}

// IGet reads a single I record string value. Returns "" if not found.
func (s *Store) IGet(name string) (string, error) {
	var val string
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, makeIKey(name))
		if err != nil {
			return err
		}
		val = string(v)
		return nil
	})
	if lmdb.IsNotFound(err) {
		return "", nil
	}
	return val, err
}

// IPut writes a single I record string value.
func (s *Store) IPut(name, value string) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, makeIKey(name), []byte(value), 0)
	})
}

// IDel deletes a single I record.
func (s *Store) IDel(name string) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		err := txn.Del(s.dbi, makeIKey(name), nil)
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// ISetCounter writes an arbitrary uint64 to a counter I record. Used by
// callers that need to set bookmarks to specific values (e.g. the
// hot-correlations sweep advancing I:hcsweep to its high-water serial).
// CRC: crc-Store.md | R2230, R2236
func (s *Store) ISetCounter(name string, val uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, makeIKey(name), []byte(strconv.FormatUint(val, 10)), 0)
	})
}

// IGetCounter reads a uint64 counter I record. Returns 0 if not found.
func (s *Store) IGetCounter(name string) (uint64, error) {
	var val uint64
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, makeIKey(name))
		if err != nil {
			return err
		}
		val, _ = strconv.ParseUint(string(v), 10, 64)
		return nil
	})
	if lmdb.IsNotFound(err) {
		return 0, nil
	}
	return val, err
}

// WriteConfig writes all Config fields to per-name I records.
// CRC: crc-Store.md | R1532, R1534, R1535, R1539
func (s *Store) WriteConfig(cfg *Config) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		put := func(name, value string) error {
			return txn.Put(s.dbi, makeIKey(name), []byte(value), 0)
		}
		putJSON := func(name string, v any) error {
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			return put(name, string(data))
		}

		if err := put(IFieldDotfiles, strconv.FormatBool(cfg.Dotfiles)); err != nil {
			return err
		}
		if err := put(IFieldCaseInsensitive, strconv.FormatBool(cfg.CaseInsensitive)); err != nil {
			return err
		}
		if err := put(IFieldEmbedCmd, cfg.EmbedCmd); err != nil {
			return err
		}
		if err := put(IFieldQueryCmd, cfg.QueryCmd); err != nil {
			return err
		}
		if err := put(IFieldTagModel, cfg.TagModel); err != nil {
			return err
		}
		if err := put(IFieldSessionTTL, cfg.SessionTTL); err != nil {
			return err
		}
		if err := putJSON(IFieldDefaultInclude, cfg.DefaultInclude); err != nil {
			return err
		}
		if err := putJSON(IFieldDefaultExclude, cfg.DefaultExclude); err != nil {
			return err
		}
		if err := putJSON(IFieldStrategies, cfg.Strategies); err != nil {
			return err
		}
		if err := putJSON(IFieldSources, cfg.Sources); err != nil {
			return err
		}
		if err := putJSON(IFieldChunkers, cfg.Chunkers); err != nil {
			return err
		}
		if err := putJSON(IFieldSearchExclude, cfg.SearchExclude); err != nil {
			return err
		}
		if err := putJSON(IFieldEmbedTiers, cfg.EmbedTiers); err != nil {
			return err
		}
		return putJSON(IFieldSchedule, cfg.Schedule)
	})
}

// ReadConfig reads all known I record names and reconstructs a Config.
// Returns nil if no I records exist (fresh DB before Init).
// CRC: crc-Store.md | R1532, R1540
func (s *Store) ReadConfig() (*Config, error) {
	var cfg Config
	found := false
	err := s.env.View(func(txn *lmdb.Txn) error {
		get := func(name string) string {
			v, err := txn.Get(s.dbi, makeIKey(name))
			if err != nil {
				return ""
			}
			found = true
			return string(v)
		}
		getJSON := func(name string, dest any) {
			v := get(name)
			if v != "" {
				json.Unmarshal([]byte(v), dest)
			}
		}

		cfg.Dotfiles, _ = strconv.ParseBool(get(IFieldDotfiles))
		cfg.CaseInsensitive, _ = strconv.ParseBool(get(IFieldCaseInsensitive))
		cfg.EmbedCmd = get(IFieldEmbedCmd)
		cfg.QueryCmd = get(IFieldQueryCmd)
		cfg.TagModel = get(IFieldTagModel)
		cfg.SessionTTL = get(IFieldSessionTTL)
		getJSON(IFieldDefaultInclude, &cfg.DefaultInclude)
		getJSON(IFieldDefaultExclude, &cfg.DefaultExclude)
		getJSON(IFieldStrategies, &cfg.Strategies)
		getJSON(IFieldSources, &cfg.Sources)
		getJSON(IFieldChunkers, &cfg.Chunkers)
		getJSON(IFieldSearchExclude, &cfg.SearchExclude)
		getJSON(IFieldEmbedTiers, &cfg.EmbedTiers)
		getJSON(IFieldSchedule, &cfg.Schedule)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &cfg, nil
}

// --- E record helpers (persistent error conditions) ---
// CRC: crc-Store.md | R1543, R1544, R1545

func makeEKey(name string) []byte {
	// E prefix is shared with EV, so use "E:" + name to avoid collision
	key := make([]byte, 2+len(name))
	key[0] = byte(prefixError)
	key[1] = ':'
	copy(key[2:], name)
	return key
}

// WriteERecord writes a persistent error condition.
func (s *Store) WriteERecord(name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, makeEKey(name), data, 0)
	})
}

// ReadERecords scans all E: prefix records.
func (s *Store) ReadERecords() (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage)
	err := s.env.View(func(txn *lmdb.Txn) error {
		cursor, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cursor.Close()

		prefix := []byte{byte(prefixError), ':'}
		k, v, err := cursor.Get(prefix, nil, lmdb.SetRange)
		for err == nil {
			if len(k) < 2 || k[0] != byte(prefixError) || k[1] != ':' {
				break
			}
			name := string(k[2:])
			cp := make([]byte, len(v))
			copy(cp, v)
			result[name] = json.RawMessage(cp)
			k, v, err = cursor.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
	return result, err
}

// DeleteERecord removes one E record.
func (s *Store) DeleteERecord(name string) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		err := txn.Del(s.dbi, makeEKey(name), nil)
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// ClearERecords deletes all E: prefix records.
func (s *Store) ClearERecords() error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		cursor, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cursor.Close()

		prefix := []byte{byte(prefixError), ':'}
		k, _, err := cursor.Get(prefix, nil, lmdb.SetRange)
		for err == nil {
			if len(k) < 2 || k[0] != byte(prefixError) || k[1] != ':' {
				break
			}
			if err := cursor.Del(0); err != nil {
				return err
			}
			k, _, err = cursor.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// (UpdateTags / AppendTags / RemoveTags removed; their function is now
// expressed via UpdateTagValues / AppendTagValues / RemoveTagValues which
// take ChunkTagValues. CRC: crc-Store.md | R1885)

// ListTags returns all tags with their total counts. Unions inline
// T records with ExtMap virtual tag names and TmpTagStore overlay
// tag names; counts are summed across sources. Honors tag source
// parity.
// CRC: crc-Store.md | R2344, R2345
func (s *Store) ListTags() ([]TagCount, error) {
	counts := make(map[string]uint32)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) >= 2 && len(v) >= 4 {
				count := binary.BigEndian.Uint32(v[:4])
				if count > 0 {
					counts[string(k[1:])] = count
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if s.extmap != nil {
		for _, name := range s.extmap.VirtualTagNames() {
			counts[name] += uint32(s.extmap.VirtualTagCount(name))
		}
	}
	if s.tmp != nil {
		for _, name := range s.tmp.TagNames() {
			counts[name] += uint32(s.tmp.TagCounts([]string{name})[name])
		}
	}
	tags := make([]TagCount, 0, len(counts))
	for name, count := range counts {
		if count > 0 {
			tags = append(tags, TagCount{Tag: name, Count: count})
		}
	}
	return tags, nil
}

// TagCounts returns counts for specific tags. Augments the LMDB
// F-driven T count with ExtMap.VirtualTagCount (ext-routed
// contributions — multi-set V semantics, routed tag tvids do not
// write F records at the target chunkid) and TmpTagStore.TagCounts
// (tmp:// overlay contributions). Honors tag source parity.
// CRC: crc-Store.md | R2010, R2344, R2346
func (s *Store) TagCounts(tags []string) ([]TagCount, error) {
	var virtual map[string]int
	if s.extmap != nil {
		virtual = s.extmap.VirtualTagCounts(tags)
	}
	var overlay map[string]int
	if s.tmp != nil {
		overlay = s.tmp.TagCounts(tags)
	}
	var results []TagCount
	err := s.env.View(func(txn *lmdb.Txn) error {
		for _, tag := range tags {
			tk := tagTotalKey(tag)
			v, err := txn.Get(s.dbi, tk)
			extra := uint32(virtual[tag]) + uint32(overlay[tag])
			if lmdb.IsNotFound(err) {
				results = append(results, TagCount{Tag: tag, Count: extra})
				continue
			}
			if err != nil {
				return err
			}
			if len(v) >= 4 {
				results = append(results, TagCount{
					Tag:   tag,
					Count: binary.BigEndian.Uint32(v[:4]) + extra,
				})
			}
		}
		return nil
	})
	return results, err
}

// TagFiles returns per-(chunk, file) records for the given tags. F records
// are keyed by chunkid; each is fanned out to one record per file
// referencing the chunk (via filesForChunk). File-level callers dedupe
// by FileID.
// CRC: crc-Store.md | R1888
func (s *Store) TagFiles(tags []string) ([]TagFileRecord, error) {
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	var records []TagFileRecord
	err := s.env.View(func(txn *lmdb.Txn) error {
		if err := scanPrefix(txn, s.dbi, []byte{byte(prefixTagFile)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(v) < 4 {
				return nil
			}
			chunkID, tag, ok := parseFKey(k)
			if !ok || !tagSet[tag] {
				return nil
			}
			count := binary.BigEndian.Uint32(v[:4])
			if s.filesForChunk == nil {
				records = append(records, TagFileRecord{
					ChunkID: chunkID,
					Tag:     tag,
					Count:   count,
				})
				return nil
			}
			for _, fid := range s.filesForChunk(txn, chunkID) {
				records = append(records, TagFileRecord{
					ChunkID: chunkID,
					FileID:  fid,
					Tag:     tag,
					Count:   count,
				})
			}
			return nil
		}); err != nil {
			return err
		}
		// CRC: crc-Store.md | R2120 — ext-routed targets need fileid
		// resolution against the same txn; ExtTagFiles is chunkid-only.
		if s.extmap != nil && s.filesForChunk != nil {
			for _, ext := range s.extmap.ExtTagFiles(tags) {
				for _, fid := range s.filesForChunk(txn, ext.ChunkID) {
					records = append(records, TagFileRecord{
						ChunkID: ext.ChunkID,
						FileID:  fid,
						Tag:     ext.Tag,
						Count:   ext.Count,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		return records, err
	}
	if s.tmp != nil {
		records = append(records, s.tmp.TagFiles(tags)...)
	}
	return records, nil
}

// adjustTagTotal increments or decrements a T record within an existing transaction.
func (s *Store) adjustTagTotal(txn *lmdb.Txn, tag string, delta int64) error {
	tk := tagTotalKey(tag)
	var current uint32
	var trailing []byte // preserves embedding vector if present
	v, err := txn.Get(s.dbi, tk)
	if err == nil && len(v) >= 4 {
		current = binary.BigEndian.Uint32(v[:4])
		if len(v) > 4 {
			trailing = v[4:]
		}
	} else if !lmdb.IsNotFound(err) && err != nil {
		return err
	}

	newVal := int64(current) + delta
	if newVal <= 0 {
		// Remove the T record entirely (including any embedding)
		txn.Del(s.dbi, tk, nil)
		return nil
	}

	val := make([]byte, 4, 4+len(trailing))
	binary.BigEndian.PutUint32(val, uint32(newVal))
	val = append(val, trailing...)
	return txn.Put(s.dbi, tk, val, 0)
}

func tagTotalKey(tag string) []byte {
	key := make([]byte, 1+len(tag))
	key[0] = byte(prefixTagTotal)
	copy(key[1:], tag)
	return key
}

// tagFileKey builds an F record key: F + varint(chunkID) + tag.
// CRC: crc-Store.md | R1874
func tagFileKey(chunkID uint64, tag string) []byte {
	key := []byte{byte(prefixTagFile)}
	key = encodeVarint(key, chunkID)
	return append(key, tag...)
}

// parseFKey extracts chunkID and tag from an F record key.
// Returns (chunkID, tag, ok). CRC: crc-Store.md | R1874
func parseFKey(k []byte) (uint64, string, bool) {
	if len(k) < 2 || k[0] != byte(prefixTagFile) {
		return 0, "", false
	}
	chunkID, n := binary.Uvarint(k[1:])
	if n <= 0 {
		return 0, "", false
	}
	return chunkID, string(k[1+n:]), true
}

// scanPrefix iterates all keys with the given prefix, calling fn for each.
// fn receives the cursor (for mutations like Del), key, and value.
func scanPrefix(txn *lmdb.Txn, dbi lmdb.DBI, prefix []byte, fn func(cur *lmdb.Cursor, k, v []byte) error) error {
	cur, err := txn.OpenCursor(dbi)
	if err != nil {
		return err
	}
	defer cur.Close()

	k, v, err := cur.Get(prefix, nil, lmdb.SetRange)
	for err == nil {
		if !bytes.HasPrefix(k, prefix) {
			break
		}
		if err := fn(cur, k, v); err != nil {
			return err
		}
		k, v, err = cur.Get(nil, nil, lmdb.Next)
	}
	if lmdb.IsNotFound(err) {
		return nil
	}
	return err
}

func missingKey(fileid uint64) []byte {
	key := make([]byte, 9)
	key[0] = byte(prefixMissing)
	binary.BigEndian.PutUint64(key[1:], fileid)
	return key
}

func unresolvedKey(path string) []byte {
	key := make([]byte, 1+len(path))
	key[0] = byte(prefixUnresolved)
	copy(key[1:], path)
	return key
}

// tagDefKey builds a D prefix key: D[tagname][fileid].
func tagDefKey(tag string, fileid uint64) []byte {
	key := make([]byte, 1+len(tag)+8)
	key[0] = byte(prefixTagDef)
	copy(key[1:], tag)
	binary.BigEndian.PutUint64(key[1+len(tag):], fileid)
	return key
}

// TagDefRecord is a tag definition from LMDB.
type TagDefRecord struct {
	Tag         string
	Description string
	FileID      uint64
}

// UpdateTagDefs replaces all D records for a fileid with new definitions.
// ED records for the same fileid are dropped in the same transaction so
// stale embeddings never outlive their definitions; the next batch-embed
// pass picks up the new (tag, fileid) pairs as missing. SED side-index
// entries for the dropped EDs are also removed.
// CRC: crc-Store.md | R2154, R2186
func (s *Store) UpdateTagDefs(fileid uint64, defs map[string]string) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Both D and ED keys end with an 8-byte big-endian fileid.
		// Walking each prefix and matching the suffix is bounded by
		// tag-def count (~270 today), not file count.
		delByFileid := func(prefix []byte, prefixLen int) {
			_ = scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, k, _ []byte) error {
				if len(k) < prefixLen+8 {
					return nil
				}
				if binary.BigEndian.Uint64(k[len(k)-8:]) == fileid {
					return cur.Del(0)
				}
				return nil
			})
		}
		delByFileid([]byte{byte(prefixTagDef)}, 1)
		delByFileid([]byte(prefixEmbedDef), len(prefixEmbedDef))                   // R2154
		delByFileid(serialKey([]byte(prefixEmbedDef), nil), 1+len(prefixEmbedDef)) // R2186

		for tag, desc := range defs {
			if err := txn.Put(s.dbi, tagDefKey(tag, fileid), []byte(desc), 0); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveTagDefs deletes all D records for a fileid. ED records for the
// same fileid are dropped in the same txn via UpdateTagDefs.
// CRC: crc-Store.md | R2155
func (s *Store) RemoveTagDefs(fileid uint64) error {
	return s.UpdateTagDefs(fileid, nil)
}

// AppendTagDefs adds D records without removing existing ones. Existing
// ED records for unchanged (tag, fileid) pairs are left intact; newly
// added pairs become "missing" and are picked up by the next BatchEmbed.
// CRC: crc-Store.md | R2156
func (s *Store) AppendTagDefs(fileid uint64, defs map[string]string) error {
	if len(defs) == 0 {
		return nil
	}
	return s.env.Update(func(txn *lmdb.Txn) error {
		for tag, desc := range defs {
			dk := tagDefKey(tag, fileid)
			if err := txn.Put(s.dbi, dk, []byte(desc), 0); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListTagDefs returns tag definitions from D records.
// If tags is empty, returns all. Otherwise filters to requested tags.
func (s *Store) ListTagDefs(tags []string) ([]TagDefRecord, error) {
	var results []TagDefRecord
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	err := s.env.View(func(txn *lmdb.Txn) error {
		prefix := []byte{byte(prefixTagDef)}
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 9 {
				return nil
			}
			tag := string(k[1 : len(k)-8])
			if len(tagSet) > 0 && !tagSet[tag] {
				return nil
			}
			fid := binary.BigEndian.Uint64(k[len(k)-8:])
			results = append(results, TagDefRecord{
				Tag:         tag,
				Description: string(v),
				FileID:      fid,
			})
			return nil
		})
	})
	return results, err
}

// --- Tag value index (V records) ---

// TagValueCount is a tag value with its file count.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1102
type TagValueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// tagValueFullKey builds a V record key with tvid: V[tagname]\x00[value]\x00[tvid varint]
// CRC: crc-Store.md | R1281
func tagValueFullKey(tag, value string, tvid uint64) []byte {
	base := make([]byte, 1+len(tag)+1+len(value)+1)
	base[0] = byte(prefixTagValue)
	copy(base[1:], tag)
	base[1+len(tag)] = 0
	copy(base[2+len(tag):], value)
	base[2+len(tag)+len(value)] = 0
	return encodeVarint(base, tvid)
}

// parseVKey extracts tag, value, and tvid from a V record key.
// Key format: V[tag]\x00[value]\x00[tvid varint]
// CRC: crc-Store.md | R1281, R1310
func parseVKey(k []byte) (tag, value string, tvid uint64, ok bool) {
	if len(k) < 3 || k[0] != byte(prefixTagValue) {
		return "", "", 0, false
	}
	// Find first null (tag/value separator)
	firstNull := bytes.IndexByte(k[1:], 0)
	if firstNull < 0 {
		return "", "", 0, false
	}
	firstNull++ // adjust for k[1:] offset
	// Find last null (value/tvid separator)
	lastNull := bytes.LastIndexByte(k, 0)
	if lastNull <= firstNull {
		// Old format without tvid — treat as tvid=0
		tag = string(k[1:firstNull])
		value = string(k[firstNull+1:])
		return tag, value, 0, true
	}
	tag = string(k[1:firstNull])
	value = string(k[firstNull+1 : lastNull])
	tvid, _ = binary.Uvarint(k[lastNull+1:])
	return tag, value, tvid, true
}

// tagValuePrefix builds the scan prefix: V[tagname]\x00[prefix]
func tagValuePrefix(tag, prefix string) []byte {
	p := make([]byte, 1+len(tag)+1+len(prefix))
	p[0] = byte(prefixTagValue)
	copy(p[1:], tag)
	p[1+len(tag)] = 0
	copy(p[2+len(tag):], prefix)
	return p
}

// encodeVarint appends a uint64 as unsigned LEB128.
func encodeVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// decodeVarints decodes all unsigned LEB128 values from a byte slice.
func decodeVarints(data []byte) []uint64 {
	var result []uint64
	i := 0
	for i < len(data) {
		var v uint64
		var shift uint
		for i < len(data) {
			b := data[i]
			i++
			v |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
			shift += 7
		}
		result = append(result, v)
	}
	return result
}

// removeVarint removes a specific value from a varint-encoded blob.
// Returns the new blob and whether the value was found.
func removeVarint(data []byte, target uint64) ([]byte, bool) {
	var result []byte
	found := false
	i := 0
	for i < len(data) {
		start := i
		var v uint64
		var shift uint
		for i < len(data) {
			b := data[i]
			i++
			v |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
			shift += 7
		}
		if v == target {
			found = true
		} else {
			result = append(result, data[start:i]...)
		}
	}
	return result, found
}

// UpdateTagValues writes V/F records keyed by chunkid for the given
// per-chunk tag-value bundles. Each chunkid is treated independently:
// V records gain the chunkid in their value; F[chunkid][tag] is written
// with count + tvid trailer; T totals are incremented for new
// (chunkid, tag) pairs.
//
// Idempotent: writing the same chunkid twice is safe — V records dedupe
// chunkids; F records are overwritten with the same content.
//
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1873, R1874, R1875, R1876, R1883
func (s *Store) UpdateTagValues(chunkTags []ChunkTagValues) error {
	persistent, overlay := s.partitionChunkTags(chunkTags)
	for fileID, ct := range overlay {
		s.tmp.UpdateTagValues(fileID, ct)
	}
	if len(persistent) == 0 {
		return nil
	}
	tt := s.tvids.Begin()
	err := s.env.Update(func(txn *lmdb.Txn) error {
		for _, ct := range persistent {
			if err := s.writeChunkTagValuesInTxn(txn, tt, ct.ChunkID, ct.Values); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tt.Abort()
		return err
	}
	tt.Commit()
	return nil
}

// partitionChunkTags splits chunk-tag groups by origin: LMDB-bound
// entries (chunkid high bit clear) go to a flat slice; overlay-bound
// entries (chunkid high bit set) bucket by FileID for the overlay
// dispatcher. Returns empty maps when the overlay isn't configured —
// overlay entries are dropped silently in that case (test paths).
// CRC: crc-Store.md | R1947
func (s *Store) partitionChunkTags(chunkTags []ChunkTagValues) ([]ChunkTagValues, map[uint64][]ChunkTagValues) {
	var persistent []ChunkTagValues
	overlay := map[uint64][]ChunkTagValues{}
	for _, ct := range chunkTags {
		if !IsOverlayID(ct.ChunkID) {
			persistent = append(persistent, ct)
			continue
		}
		if s.tmp == nil {
			continue
		}
		overlay[ct.FileID] = append(overlay[ct.FileID], ct)
	}
	return persistent, overlay
}

// AppendTagValues mirrors UpdateTagValues for the append path. Internally
// the same idempotent write for the persistent path — chunkid-keyed
// records don't distinguish "first write" from "append." Overlay
// entries route through TmpTagStore.AppendTagValues so existing
// chunks for the fileid are preserved.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1884, R1947
func (s *Store) AppendTagValues(chunkTags []ChunkTagValues) error {
	persistent, overlay := s.partitionChunkTags(chunkTags)
	for fileID, ct := range overlay {
		s.tmp.AppendTagValues(fileID, ct)
	}
	if len(persistent) == 0 {
		return nil
	}
	tt := s.tvids.Begin()
	err := s.env.Update(func(txn *lmdb.Txn) error {
		for _, ct := range persistent {
			if err := s.writeChunkTagValuesInTxn(txn, tt, ct.ChunkID, ct.Values); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tt.Abort()
		return err
	}
	tt.Commit()
	return nil
}

// RemoveTagValues drops all F/V/T contributions of a chunkid. Used for
// orphan-chunkid cleanup driven by microfts2 callbacks (R1899, R1900).
// Dispatches by chunkid high bit: overlay chunkids route to
// TmpTagStore; persistent chunkids touch LMDB.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1899, R1900, R1947
func (s *Store) RemoveTagValues(chunkID uint64) error {
	if IsOverlayID(chunkID) {
		if s.tmp != nil {
			s.tmp.RemoveChunk(chunkID)
		}
		return nil
	}
	tt := s.tvids.Begin()
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return s.removeChunkIDInTxn(txn, tt, chunkID)
	})
	if err != nil {
		tt.Abort()
		return err
	}
	tt.Commit()
	return nil
}

// RemoveFileTagValues clears every tag entry for a fileid. Overlay
// fileids dispatch to TmpTagStore.RemoveFile. Persistent fileids
// are a no-op at this entry point — their per-chunk cleanup runs
// through microfts2's RemovedChunkCallback during the LMDB removal.
// CRC: crc-Store.md | R1944, R1947
func (s *Store) RemoveFileTagValues(fileID uint64) {
	if IsOverlayID(fileID) && s.tmp != nil {
		s.tmp.RemoveFile(fileID)
	}
}

// RemoveTagValuesInTxn drops a chunk's V/F/T contributions using a
// caller-supplied TvidTxn. The caller is responsible for committing
// the TvidTxn after microfts2's surrounding env.Update returns success,
// or aborting it on error — this keeps the in-memory tvid map
// consistent with LMDB even if microfts2's commit fails.
// CRC: crc-Store.md | R1899, R1962, R1963
func (s *Store) RemoveTagValuesInTxn(txn *lmdb.Txn, tt *TvidTxn, chunkID uint64) error {
	return s.removeChunkIDInTxn(txn, tt, chunkID)
}

// WithTvidTxn runs fn under a fresh write-txn-scoped tvid overlay,
// committing on nil error and aborting otherwise. Used to wrap
// microfts2 callback-bearing operations whose own commit/abort governs
// whether the in-memory tvid map should track their work.
// CRC: crc-Store.md | R1959, R1962
func (s *Store) WithTvidTxn(fn func(*TvidTxn) error) error {
	tt := s.tvids.Begin()
	if err := fn(tt); err != nil {
		tt.Abort()
		return err
	}
	tt.Commit()
	return nil
}

// writeChunkTagValuesInTxn writes F+V records for one chunk's values.
// Groups values by tag, allocates/reuses tvids, updates V records,
// writes F[chunkID][tag] = count + tvids, increments T for new pairs.
// Tvid registrations are recorded in the supplied TvidTxn; the caller
// commits or aborts to publish them to the live TvidMap.
// CRC: crc-Store.md | R1874, R1875, R1876, R1959, R1963, R1991
func (s *Store) writeChunkTagValuesInTxn(txn *lmdb.Txn, tt *TvidTxn, chunkID uint64, values []TagValue) error {
	// Group values by tag
	perTag := make(map[string][]TagValue)
	for _, tv := range values {
		if tv.Tag == "" {
			continue
		}
		perTag[tv.Tag] = append(perTag[tv.Tag], tv)
	}

	for tag, vals := range perTag {
		fk := tagFileKey(chunkID, tag)
		_, err := txn.Get(s.dbi, fk)
		isNew := lmdb.IsNotFound(err)
		if err != nil && !isNew {
			return err
		}

		// Add chunkID to V records, collect tvids
		var tvids []uint64
		count := uint32(0)
		for _, tv := range vals {
			if tv.Value == "" {
				count++
				continue
			}
			// Cap key length: V key = prefix(1) + tag + sep(1) + value + sep(1) + varint(tvid)
			if 1+len(tv.Tag)+1+len(tv.Value)+1+10 > maxVKeyLen {
				count++
				continue
			}
			tvid, err := s.addChunkIDToVRecord(txn, tt, tv.Tag, tv.Value, chunkID)
			if err != nil {
				return err
			}
			tvids = append(tvids, tvid)
			count++
		}

		// Write F[chunkID][tag] = count + tvids
		val := make([]byte, 4, 4+len(tvids)*10)
		binary.BigEndian.PutUint32(val, count)
		for _, tv := range tvids {
			val = encodeVarint(val, tv)
		}
		if err := txn.Put(s.dbi, fk, val, 0); err != nil {
			return err
		}

		// T adjustment: +1 per new (chunkID, tag) pair
		if isNew {
			if err := s.adjustTagTotal(txn, tag, 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// addChunkIDToVRecord adds chunkID to V[tag][value] (allocating tvid for
// new (tag, value) pairs). Uses TvidTxn.Lookup to find an existing tvid
// without scanning the V prefix; falls back to allocation when none
// exists. Multi-set append — no dedup check. Each contribution (inline
// or ext-routed) writes its own varint entry; cleanup uses
// removeOneVarint via removeOneChunkIDFromVRecord (ext) or removeVarint
// via removeChunkIDInTxn (inline orphan path). Returns the tvid.
// CRC: crc-Store.md | R1873, R1281, R1955, R1963, R1988
func (s *Store) addChunkIDToVRecord(txn *lmdb.Txn, tt *TvidTxn, tag, value string, chunkID uint64) (uint64, error) {
	if tvid, ok := tt.Lookup(tag, value); ok {
		fullKey := tagValueFullKey(tag, value, tvid)
		existing, err := txn.Get(s.dbi, fullKey)
		if err != nil && !lmdb.IsNotFound(err) {
			return 0, err
		}
		blob := encodeVarint(bytes.Clone(existing), chunkID)
		if err := txn.Put(s.dbi, fullKey, blob, 0); err != nil {
			return 0, err
		}
		return tvid, nil
	}
	tvid, err := s.allocIDInTxn(txn, IFieldNextTvid)
	if err != nil {
		return 0, fmt.Errorf("alloc tvid: %w", err)
	}
	fullKey := tagValueFullKey(tag, value, tvid)
	blob := encodeVarint(nil, chunkID)
	if err := txn.Put(s.dbi, fullKey, blob, 0); err != nil {
		return 0, err
	}
	tt.Add(tvid, tag, value, OriginPersistent)
	return tvid, nil
}

// removeOneVarint removes the first occurrence of target from a
// varint-encoded blob. Returns the new blob and whether the value
// was found. Used for multi-set V cleanup of ext routings —
// each routing's contribution is one entry and cleanup strikes
// one occurrence so other contributors survive.
func removeOneVarint(data []byte, target uint64) ([]byte, bool) {
	i := 0
	for i < len(data) {
		start := i
		var v uint64
		var shift uint
		for i < len(data) {
			b := data[i]
			i++
			v |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
			shift += 7
		}
		if v == target {
			return append(append([]byte(nil), data[:start]...), data[i:]...), true
		}
	}
	return data, false
}

// removeOneChunkIDFromVRecord strikes one occurrence of chunkID from
// V[tag][value][tvid]. If the record empties, deletes the V key and
// records tvid removal in the TvidTxn. Returns whether anything was
// removed.
// CRC: crc-Store.md | R1988, R2005, R2008
func (s *Store) removeOneChunkIDFromVRecord(txn *lmdb.Txn, tt *TvidTxn, tag, value string, tvid, chunkID uint64) (bool, error) {
	fullKey := tagValueFullKey(tag, value, tvid)
	existing, err := txn.Get(s.dbi, fullKey)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	newV, found := removeOneVarint(existing, chunkID)
	if !found {
		return false, nil
	}
	if len(newV) == 0 {
		if err := txn.Del(s.dbi, fullKey, nil); err != nil {
			return false, err
		}
		tt.Remove(tvid)
		return true, nil
	}
	return true, txn.Put(s.dbi, fullKey, newV, 0)
}

// extRoutingKey builds an X record key: X + varint(tvid_ext) + varint(target_chunkid).
// CRC: crc-Store.md | R1989
func extRoutingKey(tvidExt, targetChunk uint64) []byte {
	key := []byte{byte(prefixExtRouting)}
	key = encodeVarint(key, tvidExt)
	return encodeVarint(key, targetChunk)
}

// extRoutingPrefix returns the scan prefix for one tvid_ext.
// CRC: crc-Store.md | R1989
func extRoutingPrefix(tvidExt uint64) []byte {
	key := []byte{byte(prefixExtRouting)}
	return encodeVarint(key, tvidExt)
}

// parseExtRoutingKey extracts tvid_ext and target_chunkid from a key.
// Returns ok=false if the key shape doesn't match.
// CRC: crc-Store.md | R1989
func parseExtRoutingKey(k []byte) (tvidExt, targetChunk uint64, ok bool) {
	if len(k) < 3 || k[0] != byte(prefixExtRouting) {
		return 0, 0, false
	}
	rest := k[1:]
	tvidExt, n := binary.Uvarint(rest)
	if n <= 0 {
		return 0, 0, false
	}
	rest = rest[n:]
	targetChunk, n = binary.Uvarint(rest)
	if n <= 0 || n != len(rest) {
		return 0, 0, false
	}
	return tvidExt, targetChunk, true
}

// ExtRouting is one X record's payload: a target chunkid and the
// routed tag tvids that this @ext routing contributed.
// CRC: crc-Store.md | R1989
type ExtRouting struct {
	TargetChunkID uint64
	RoutedTvids   []uint64
}

// WriteExtRecord writes X[tvid_ext][target_chunkid] = packed routed_tvid varints.
// CRC: crc-Store.md | R1989
func (s *Store) WriteExtRecord(txn *lmdb.Txn, tvidExt, targetChunk uint64, routedTvids []uint64) error {
	key := extRoutingKey(tvidExt, targetChunk)
	var blob []byte
	for _, t := range routedTvids {
		blob = encodeVarint(blob, t)
	}
	return txn.Put(s.dbi, key, blob, 0)
}

// ReadExtRecord returns the routed tvids for one (tvid_ext, target_chunkid).
// Returns (nil, nil) when the record is absent.
// CRC: crc-Store.md | R1989
func (s *Store) ReadExtRecord(txn *lmdb.Txn, tvidExt, targetChunk uint64) ([]uint64, error) {
	key := extRoutingKey(tvidExt, targetChunk)
	blob, err := txn.Get(s.dbi, key)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return decodeVarints(blob), nil
}

// ScanExtRecords prefix-scans X[tvid_ext], decoding each routing.
// CRC: crc-Store.md | R1989
func (s *Store) ScanExtRecords(txn *lmdb.Txn, tvidExt uint64) ([]ExtRouting, error) {
	prefix := extRoutingPrefix(tvidExt)
	var out []ExtRouting
	err := scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
		te, tc, ok := parseExtRoutingKey(k)
		if !ok || te != tvidExt {
			return nil
		}
		out = append(out, ExtRouting{TargetChunkID: tc, RoutedTvids: decodeVarints(v)})
		return nil
	})
	return out, err
}

// DeleteExtRecord removes one X record.
// CRC: crc-Store.md | R1989
func (s *Store) DeleteExtRecord(txn *lmdb.Txn, tvidExt, targetChunk uint64) error {
	key := extRoutingKey(tvidExt, targetChunk)
	if err := txn.Del(s.dbi, key, nil); err != nil {
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// ScanAllExtRecords iterates every X record in the store. Used by
// ExtMap.Rebuild on startup to repopulate the in-memory maps.
// CRC: crc-Store.md | R1990, R1993
func (s *Store) ScanAllExtRecords(txn *lmdb.Txn, fn func(tvidExt, targetChunk uint64, routedTvids []uint64) error) error {
	prefix := []byte{byte(prefixExtRouting)}
	return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
		te, tc, ok := parseExtRoutingKey(k)
		if !ok {
			return nil
		}
		return fn(te, tc, decodeVarints(v))
	})
}

// ReadExtTvidsForChunk returns the @ext tag tvids registered against
// chunkID's F record. Used by orphan callbacks to capture source-side
// ext routings before the F records are dropped.
// CRC: crc-Store.md | R2008
func (s *Store) ReadExtTvidsForChunk(txn *lmdb.Txn, chunkID uint64) ([]uint64, error) {
	key := tagFileKey(chunkID, tagExt)
	v, err := txn.Get(s.dbi, key)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(v) <= 4 {
		return nil, nil
	}
	return decodeVarints(v[4:]), nil
}

// removeChunkIDInTxn drops F records for a chunkid, decrements T totals
// for each (chunkID, tag) pair, and removes the chunkid from V records
// identified by the F-record tvid trail. When a V record is fully
// emptied, its tvid is recorded in the TvidTxn for removal from the
// live map on commit. CRC: crc-Store.md | R1900, R1963
func (s *Store) removeChunkIDInTxn(txn *lmdb.Txn, tt *TvidTxn, chunkID uint64) error {
	fPrefix := []byte{byte(prefixTagFile)}
	fPrefix = encodeVarint(fPrefix, chunkID)

	// First pass: collect all (tag, tvids) for this chunkid
	type fEntry struct {
		tag   string
		tvids []uint64
	}
	var entries []fEntry
	if err := scanPrefix(txn, s.dbi, fPrefix, func(_ *lmdb.Cursor, k, v []byte) error {
		_, tag, ok := parseFKey(k)
		if !ok {
			return nil
		}
		var tvids []uint64
		if len(v) > 4 {
			tvids = decodeVarints(v[4:])
		}
		entries = append(entries, fEntry{tag: tag, tvids: tvids})
		return nil
	}); err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	// Collect all tvids touched by this chunkid for the V scan
	tvidSet := make(map[uint64]bool)
	for _, e := range entries {
		for _, tv := range e.tvids {
			tvidSet[tv] = true
		}
	}

	// Decrement T totals (-1 per chunk-tag pair)
	for _, e := range entries {
		if err := s.adjustTagTotal(txn, e.tag, -1); err != nil {
			return err
		}
	}

	// Drop the F records for this chunkid
	if err := scanPrefix(txn, s.dbi, fPrefix, func(cur *lmdb.Cursor, _, _ []byte) error {
		return cur.Del(0)
	}); err != nil {
		return err
	}

	// Walk V records, remove chunkid from values where tvid matches
	if len(tvidSet) > 0 {
		vPrefix := []byte{byte(prefixTagValue)}
		if err := scanPrefix(txn, s.dbi, vPrefix, func(cur *lmdb.Cursor, k, v []byte) error {
			_, _, tvid, ok := parseVKey(k)
			if !ok || !tvidSet[tvid] {
				return nil
			}
			newV, found := removeVarint(v, chunkID)
			if !found {
				return nil
			}
			if len(newV) == 0 {
				if err := cur.Del(0); err != nil {
					return err
				}
				tt.Remove(tvid)
				return nil
			}
			return txn.Put(s.dbi, k, newV, 0)
		}); err != nil {
			return err
		}
	}
	return nil
}

// QueryTagValues returns values for a tag, optionally filtered by prefix.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1108, R1109
// QueryTagValues returns values for the given tag whose text starts
// with prefix, with counts. Unions inline V records with ExtMap
// virtual values and TmpTagStore overlay values; counts are summed
// per (tag, value) across sources. Honors tag source parity.
// CRC: crc-Store.md | R1108, R1109, R2344, R2347
func (s *Store) QueryTagValues(tag, prefix string) ([]TagValueCount, error) {
	counts := make(map[string]int)
	scanKey := tagValuePrefix(tag, prefix)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, scanKey, func(_ *lmdb.Cursor, k, v []byte) error {
			// Key format: V[tag]\x00[value]\x00[tvid] — parse with two null separators
			_, value, _, ok := parseVKey(k)
			if !ok {
				return nil
			}
			c := len(decodeVarints(v))
			if c > 0 {
				counts[value] += c
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if s.extmap != nil {
		for _, value := range s.extmap.VirtualTagValues(tag) {
			if prefix != "" && !strings.HasPrefix(value, prefix) {
				continue
			}
			counts[value] += len(s.extmap.ExtTagValueFiles(tag, value))
		}
	}
	if s.tmp != nil {
		for _, value := range s.tmp.TagValuesForTag(tag) {
			if prefix != "" && !strings.HasPrefix(value, prefix) {
				continue
			}
			counts[value] += len(s.tmp.TagValueFiles(tag, value))
		}
	}
	results := make([]TagValueCount, 0, len(counts))
	for value, count := range counts {
		results = append(results, TagValueCount{Value: value, Count: count})
	}
	// R1129: sort by count descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Count > results[j].Count
	})
	return results, nil
}

// TagValueFiles returns chunkids for a specific (tag, value) pair.
// Resolves the tvid via TvidMap.Lookup and reads the V record by exact
// key — no prefix scan.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1110, R1309, R1955
func (s *Store) TagValueFiles(tag, value string) ([]uint64, error) {
	var ids []uint64
	if tvid, ok := s.tvids.Lookup(tag, value); ok {
		fullKey := tagValueFullKey(tag, value, tvid)
		err := s.env.View(func(txn *lmdb.Txn) error {
			v, err := txn.Get(s.dbi, fullKey)
			if lmdb.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			ids = decodeVarints(v)
			return nil
		})
		if err != nil {
			return ids, err
		}
	}
	if s.tmp != nil {
		ids = append(ids, s.tmp.TagValueFiles(tag, value)...)
	}
	if s.extmap != nil {
		ids = append(ids, s.extmap.ExtTagValueFiles(tag, value)...)
	}
	return ids, nil
}

// FileTagValues returns the first value found per tag for a given
// fileid. Resolves fileid → chunkids via chunksForFile (or
// TmpTagStore.ChunksForFile for overlay fileids), then unions:
// inline V records intersecting the file's chunks, ExtMap-routed
// virtual values targeting those chunks, and TmpTagStore overlay
// values. "First" means the first match seen across the union,
// preserving inline → ext → overlay precedence per tag.
// Honors tag source parity.
// CRC: crc-Store.md | R1142, R1143, R1889, R2344, R2348, R2354
func (s *Store) FileTagValues(fileid uint64, tags []string) (map[string]string, error) {
	result := make(map[string]string, len(tags))
	if len(tags) == 0 {
		return result, nil
	}

	var chunks []uint64
	if IsOverlayID(fileid) {
		if s.tmp == nil {
			return result, nil
		}
		chunks = s.tmp.ChunksForFile(fileid)
	} else if s.chunksForFile != nil {
		chunks = s.chunksForFile(fileid)
	}
	chunkSet := make(map[uint64]bool, len(chunks))
	for _, c := range chunks {
		chunkSet[c] = true
	}

	// Inline path: persistent V records.
	if !IsOverlayID(fileid) && len(chunkSet) > 0 {
		err := s.env.View(func(txn *lmdb.Txn) error {
			for _, tag := range tags {
				if _, found := result[tag]; found {
					continue
				}
				prefix := tagValuePrefix(tag, "")
				err := scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
					_, value, _, ok := parseVKey(k)
					if !ok {
						return nil
					}
					for _, id := range decodeVarints(v) {
						if chunkSet[id] {
							result[tag] = value
							return errStopScan
						}
					}
					return nil
				})
				if err != nil && err != errStopScan {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}

	// Ext-routed virtual values targeting any of the file's chunks.
	if s.extmap != nil && len(chunkSet) > 0 {
		for _, tag := range tags {
			if _, found := result[tag]; found {
				continue
			}
			for _, value := range s.extmap.VirtualTagValues(tag) {
				hit := false
				for _, cid := range s.extmap.ExtTagValueFiles(tag, value) {
					if chunkSet[cid] {
						hit = true
						break
					}
				}
				if hit {
					result[tag] = value
					break
				}
			}
		}
	}

	// Overlay (tmp://) values — direct dispatch for overlay fileids.
	if s.tmp != nil && IsOverlayID(fileid) {
		var pending []string
		for _, tag := range tags {
			if _, found := result[tag]; !found {
				pending = append(pending, tag)
			}
		}
		for tag, value := range s.tmp.FileTagValues(fileid, pending) {
			result[tag] = value
		}
	}

	return result, nil
}

// TagsForChunk returns inline (tag, value) pairs present at chunkID.
// Reads F records for the chunk and resolves each tvid via TvidMap.
// Multi-set semantics — if a (tag, value) pair appears multiple times
// in the chunk's F record it appears multiple times in the result.
// Inline only — does NOT consult ExtMap routings; for ext-routed
// tags use ExtMap.ExtRoutingsForTargetChunk. Routes overlay chunkids
// to TmpTagStore.
// CRC: crc-Store.md | R2080
func (s *Store) TagsForChunk(chunkID uint64) ([]TagValue, error) {
	if IsOverlayID(chunkID) {
		if s.tmp == nil {
			return nil, nil
		}
		return s.tmp.TagsForChunk(chunkID), nil
	}
	prefix := []byte{byte(prefixTagFile)}
	prefix = encodeVarint(prefix, chunkID)

	var result []TagValue
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			_, tag, ok := parseFKey(k)
			if !ok || len(v) < 4 {
				return nil
			}
			rest := v[4:]
			for len(rest) > 0 {
				tvid, n := binary.Uvarint(rest)
				if n <= 0 {
					break
				}
				rest = rest[n:]
				_, value, ok := s.tvids.Resolve(tvid)
				if !ok {
					continue
				}
				result = append(result, TagValue{Tag: tag, Value: value})
			}
			return nil
		})
	})
	return result, err
}

// AllTagsForChunk returns the canonical union of (tag, value) pairs
// at chunkID: inline TagsForChunk plus ExtMap-routed virtual tags
// targeting the chunk. Honors tag source parity — use this for any
// "all tags here" question; reserve TagsForChunk for the strict
// inline view (e.g. write-side code that must see only source text
// tags).
// CRC: crc-Store.md | R2344, R2351, R2354
func (s *Store) AllTagsForChunk(chunkID uint64) ([]TagValue, error) {
	result, err := s.TagsForChunk(chunkID)
	if err != nil {
		return result, err
	}
	if s.extmap != nil {
		result = append(result, s.extmap.RoutedTagsForChunk(chunkID)...)
	}
	return result, nil
}

// TagValueMatch is a tag value with its chunkID list, returned by
// MatchTagValues. The chunkIDs come straight from the V record value blob
// (post chunkid migration); callers that need fileIDs resolve via filesForChunk.
// CRC: crc-Store.md | R1468
type TagValueMatch struct {
	Value    string   `json:"value"`
	ChunkIDs []uint64 `json:"chunk_ids"`
}

// MatchTagNames returns tag names where every token appears as a
// case-insensitive substring of the name. Linear scan across all
// three sources: inline T records, ExtMap virtual names, and
// TmpTagStore overlay names. Deduplicated. Honors tag source parity.
// CRC: crc-Store.md | R1467, R2344, R2349
func (s *Store) MatchTagNames(tokens []string) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	lower := make([]string, len(tokens))
	for i, t := range tokens {
		lower[i] = strings.ToLower(t)
	}
	seen := make(map[string]struct{})
	consider := func(name string) {
		ln := strings.ToLower(name)
		for _, tok := range lower {
			if !strings.Contains(ln, tok) {
				return
			}
		}
		seen[name] = struct{}{}
	}
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 2 || len(v) < 4 {
				return nil
			}
			consider(string(k[1:]))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if s.extmap != nil {
		for _, name := range s.extmap.VirtualTagNames() {
			consider(name)
		}
	}
	if s.tmp != nil {
		for _, name := range s.tmp.TagNames() {
			consider(name)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out, nil
}

// MatchTagValues returns values for the given tag where every token
// appears as a case-insensitive substring. Unions inline V records,
// ExtMap virtual values, and TmpTagStore overlay values; chunkIDs
// are merged per value across sources. Honors tag source parity.
// CRC: crc-Store.md | R1468, R2344, R2350
func (s *Store) MatchTagValues(tag string, tokens []string) ([]TagValueMatch, error) {
	lower := make([]string, len(tokens))
	for i, t := range tokens {
		lower[i] = strings.ToLower(t)
	}
	matches := make(map[string][]uint64)
	consider := func(value string, chunkIDs []uint64) {
		if len(tokens) > 0 {
			lv := strings.ToLower(value)
			for _, tok := range lower {
				if !strings.Contains(lv, tok) {
					return
				}
			}
		}
		matches[value] = append(matches[value], chunkIDs...)
	}
	prefix := tagValuePrefix(tag, "")
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			_, value, _, ok := parseVKey(k)
			if !ok {
				return nil
			}
			consider(value, decodeVarints(v))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if s.extmap != nil {
		for _, value := range s.extmap.VirtualTagValues(tag) {
			consider(value, s.extmap.ExtTagValueFiles(tag, value))
		}
	}
	if s.tmp != nil {
		for _, value := range s.tmp.TagValuesForTag(tag) {
			consider(value, s.tmp.TagValueFiles(tag, value))
		}
	}
	results := make([]TagValueMatch, 0, len(matches))
	for value, chunkIDs := range matches {
		results = append(results, TagValueMatch{
			Value:    value,
			ChunkIDs: dedupUint64(chunkIDs),
		})
	}
	return results, nil
}

// MatchNamesRegex returns every tag name accepted by re across
// inline T records, ExtMap virtual names, and TmpTagStore overlay.
// Case sensitivity is the regex's own concern — callers that want
// case-insensitive matching compile with `(?i)`.
// CRC: crc-Store.md | R2443
func (s *Store) MatchNamesRegex(re *regexp.Regexp) []string {
	if re == nil {
		return nil
	}
	seen := make(map[string]struct{})
	consider := func(name string) {
		if re.MatchString(name) {
			seen[name] = struct{}{}
		}
	}
	_ = s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 2 {
				return nil
			}
			consider(string(k[1:]))
			return nil
		})
	})
	if s.extmap != nil {
		for _, name := range s.extmap.VirtualTagNames() {
			consider(name)
		}
	}
	if s.tmp != nil {
		for _, name := range s.tmp.TagNames() {
			consider(name)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out
}

// FileMatchesPredicate reports whether fileID currently has at
// least one (tag, value) pair accepted by p. Walks the file's
// chunks (via the chunk resolver and tmp overlay) and tests each
// chunk's full tag set through AllTagsForChunk so inline + ExtMap
// virtual tags both count. Returns false on first error or when no
// chunks resolve.
// CRC: crc-Store.md | R2463, R2464
func (s *Store) FileMatchesPredicate(fileID uint64, p MatchPredicate) bool {
	var chunks []uint64
	if IsOverlayID(fileID) {
		if s.tmp == nil {
			return false
		}
		chunks = s.tmp.ChunksForFile(fileID)
	} else if s.chunksForFile != nil {
		chunks = s.chunksForFile(fileID)
	}
	for _, cid := range chunks {
		tags, err := s.AllTagsForChunk(cid)
		if err != nil {
			continue
		}
		for _, tv := range tags {
			if p.Match(tv) {
				return true
			}
		}
	}
	return false
}

// FilesForChunks returns the fileID set that owns any of the given
// chunkIDs. Uses the chunk-resolver set via SetChunkResolver so
// both inline and overlay chunkIDs are handled the same way.
// CRC: crc-Store.md | R2453
func (s *Store) FilesForChunks(chunkIDs map[uint64]bool) map[uint64]bool {
	fileIDs := make(map[uint64]bool)
	if s.filesForChunk == nil || len(chunkIDs) == 0 {
		return fileIDs
	}
	_ = s.env.View(func(txn *lmdb.Txn) error {
		for cid := range chunkIDs {
			for _, fid := range s.filesForChunk(txn, cid) {
				fileIDs[fid] = true
			}
		}
		return nil
	})
	return fileIDs
}

// dedupUint64 returns the input slice with duplicates removed,
// preserving first-occurrence order.
func dedupUint64(xs []uint64) []uint64 {
	if len(xs) <= 1 {
		return xs
	}
	seen := make(map[uint64]struct{}, len(xs))
	out := xs[:0]
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

// errStopScan is a sentinel to break out of scanPrefix early.
var errStopScan = fmt.Errorf("stop scan")

// maxVKeyLen is the maximum V record key length. LMDB default max key is 511 bytes.
// Values that would push the key past this limit are skipped — long values
// aren't useful for completion.
const maxVKeyLen = 511

// --- Day-bucket operations (scheduling) ---

// AckEntry represents a parsed @ack: tag.
// CRC: crc-Store.md | R883, R884, R885, R886
type AckEntry struct {
	Start time.Time // zero for open-start (..DATE)
	End   time.Time
	Text  string
}

// ParseAcks extracts @ack: tags from content that are in the same chunk
// as the given schedule tag. Returns parsed date entries.
// CRC: crc-Store.md | R883, R884, R885, R886, R887, R888, R936
func ParseAcks(content []byte, tag string) []AckEntry {
	lines := strings.Split(string(content), "\n")
	var acks []AckEntry
	inChunk := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			inChunk = false
			continue
		}
		if strings.Contains(trimmed, "@"+tag+":") {
			inChunk = true
		}
		if inChunk && strings.HasPrefix(trimmed, "@ack:") {
			value := strings.TrimSpace(trimmed[len("@ack:"):])
			entry := parseAckValue(value)
			if !entry.End.IsZero() {
				acks = append(acks, entry)
			}
		}
	}
	return acks
}

// parseAckValue parses a single @ack: value into an AckEntry.
func parseAckValue(value string) AckEntry {
	// ..DATE [text] — open start
	if strings.HasPrefix(value, "..") {
		rest := strings.TrimSpace(value[2:])
		end, text := parseDateAndText(rest)
		return AckEntry{End: end, Text: text}
	}
	// DATE..DATE [text] — closed range
	if idx := strings.Index(value, ".."); idx > 0 {
		left := strings.TrimSpace(value[:idx])
		right := strings.TrimSpace(value[idx+2:])
		start, _ := parseDateAndText(left)
		end, text := parseDateAndText(right)
		return AckEntry{Start: start, End: end, Text: text}
	}
	// DATE [text] — single date
	t, text := parseDateAndText(value)
	return AckEntry{Start: t, End: t, Text: text}
}

// parseDateAndText splits a string into a date and trailing text.
// Delegates to parseDateTrimming (scheduler.go) for consistent parsing.
func parseDateAndText(s string) (time.Time, string) {
	t, text, err := parseDateTrimming(strings.TrimSpace(s), time.Now().Location())
	if err != nil {
		return time.Time{}, s
	}
	return t, text
}

// AckCoversDate checks if any ack entry covers the given date.
// Returns the matching ack text if found.
// CRC: crc-Store.md | R934
func AckCoversDate(acks []AckEntry, date time.Time) (bool, string) {
	d := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	for _, a := range acks {
		aEnd := time.Date(a.End.Year(), a.End.Month(), a.End.Day(), 0, 0, 0, 0, a.End.Location())
		if a.Start.IsZero() {
			// Open start: covers everything up to End
			if !d.After(aEnd) {
				return true, a.Text
			}
		} else {
			aStart := time.Date(a.Start.Year(), a.Start.Month(), a.Start.Day(), 0, 0, 0, 0, a.Start.Location())
			if !d.Before(aStart) && !d.After(aEnd) {
				return true, a.Text
			}
		}
	}
	return false, ""
}

// GetScheduleConfig reads the stored [schedule] config string from I records.
// CRC: crc-Store.md | R927, R928, R1572
func (s *Store) GetScheduleConfig() (string, error) {
	return s.IGet(IFieldScheduleConfig)
}

// PutScheduleConfig writes the [schedule] config string to I records.
// CRC: crc-Store.md | R927, R932, R1572
func (s *Store) PutScheduleConfig(serialized string) error {
	return s.IPut(IFieldScheduleConfig, serialized)
}

// recordPrefixOf returns the full prefix string for an ark-subdatabase
// key. Known multi-byte prefixes (E:, EV, EC, EF, ED, PC) are matched
// first; otherwise the single-byte prefix is returned.
// CRC: crc-Store.md | R2479, R2481, R2162
func recordPrefixOf(k []byte) string {
	if len(k) >= 2 {
		switch {
		case k[0] == byte(prefixError) && k[1] == ':':
			return "E:"
		case k[0] == 'E' && k[1] == 'V':
			return "EV"
		case k[0] == 'E' && k[1] == 'C':
			return "EC"
		case k[0] == 'E' && k[1] == 'F':
			return "EF"
		case k[0] == 'E' && k[1] == 'D':
			return "ED"
		case k[0] == 'P' && k[1] == 'C':
			return "PC"
		case k[0] == 'R' && k[1] == 'M':
			return "RM" // R2882 — RM surfaces in status -db
		}
	}
	if len(k) == 0 {
		return ""
	}
	return string(k[0:1])
}

// RecordCounts scans all keys in the ark subdatabase and returns
// stats grouped by full prefix string.
// CRC: crc-Store.md | R2479, R2481
func (s *Store) RecordCounts() (map[string]RecordStats, error) {
	counts := make(map[string]RecordStats)
	err := s.env.View(func(txn *lmdb.Txn) error {
		cur, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cur.Close()
		k, v, err := cur.Get(nil, nil, lmdb.First)
		for err == nil {
			if len(k) > 0 {
				p := recordPrefixOf(k)
				st := counts[p]
				st.Count++
				st.KeyBytes += int64(len(k))
				st.ValueBytes += int64(len(v))
				counts[p] = st
			}
			k, v, err = cur.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
	return counts, err
}

// --- Tag Value ID allocation (R1280-R1284) ---

// allocIDInTxn increments and returns the next ID within an existing write txn.
func (s *Store) allocIDInTxn(txn *lmdb.Txn, iFieldName string) (uint64, error) {
	key := makeIKey(iFieldName)
	var id uint64
	val, err := txn.Get(s.dbi, key)
	if err != nil && !lmdb.IsNotFound(err) {
		return 0, err
	}
	if val != nil {
		id, _ = strconv.ParseUint(string(val), 10, 64)
	}
	id++
	if err := txn.Put(s.dbi, key, []byte(strconv.FormatUint(id, 10)), 0); err != nil {
		return 0, err
	}
	return id, nil
}

// --- Vector Freshness Substrate (S records, R2174-R2193) ---

// serialKey builds an S-side-index key: S + original-prefix + original-key-tail.
// CRC: crc-Store.md | R2174
func serialKey(prefix, key []byte) []byte {
	out := make([]byte, 1+len(prefix)+len(key))
	out[0] = byte(prefixSerial)
	copy(out[1:], prefix)
	copy(out[1+len(prefix):], key)
	return out
}

// allocSerial reads the I:serial counter, advances it by 1, writes back, and
// returns the new value. The counter never resets — preserved across
// rebuild, DropEmbeddings, and mdb_env_copy(MDB_CP_COMPACT) because the
// I-record lives in the active B-tree. Records stamped within one write
// txn share one serial; serials are strictly monotonic across txns. The
// substrate does not backfill pre-existing records.
// CRC: crc-Store.md | R2176, R2177, R2184, R2192
func (s *Store) allocSerial(txn *lmdb.Txn) (uint64, error) {
	return s.allocIDInTxn(txn, "serial")
}

// stampWriteWith writes the S-side-index entry for (prefix + key) with a
// caller-supplied serial. Caller is responsible for the original record's
// txn.Put. Used by batch writers that allocate one serial and stamp many
// records with it.
// CRC: crc-Store.md | R2174, R2175
func stampWriteWith(txn *lmdb.Txn, dbi lmdb.DBI, prefix, key []byte, serial uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], serial)
	return txn.Put(dbi, serialKey(prefix, key), buf[:n], 0)
}

// stampWrite is the convenience wrapper for single-record callers: alloc a
// serial and stamp in one call.
// CRC: crc-Store.md | R2174-R2176
func (s *Store) stampWrite(txn *lmdb.Txn, prefix, key []byte) error {
	serial, err := s.allocSerial(txn)
	if err != nil {
		return err
	}
	return stampWriteWith(txn, s.dbi, prefix, key, serial)
}

// deleteStamp removes the S-side-index entry for (prefix + key). No-op if
// absent. Used by embedding-record delete paths to keep the side index in
// sync. No tombstone serials are introduced.
// CRC: crc-Store.md | R2185, R2186, R2191
func deleteStamp(txn *lmdb.Txn, dbi lmdb.DBI, prefix, key []byte) error {
	err := txn.Del(dbi, serialKey(prefix, key), nil)
	if lmdb.IsNotFound(err) {
		return nil
	}
	return err
}

// RecordSerial returns the stamped serial of the record at (prefix + key).
// found is false iff no S-entry exists for that (prefix, key).
// CRC: crc-Store.md | R2188
func (s *Store) RecordSerial(prefix, key []byte) (serial uint64, found bool, err error) {
	err = s.env.View(func(txn *lmdb.Txn) error {
		v, gerr := txn.Get(s.dbi, serialKey(prefix, key))
		if lmdb.IsNotFound(gerr) {
			return nil
		}
		if gerr != nil {
			return gerr
		}
		serial, _ = binary.Uvarint(v)
		found = true
		return nil
	})
	return serial, found, err
}

// WalkRecordsSinceSerial walks the S<prefix> side index in key order and
// calls fn for each entry whose stamped serial is strictly greater than
// `since`. fn receives the original record's full key (with the leading
// 'S' byte stripped, so the original prefix bytes lead) and the decoded
// serial. A non-nil error from fn stops iteration and is returned.
// CRC: crc-Store.md | R2189, R2190
func (s *Store) WalkRecordsSinceSerial(prefix []byte, since uint64, fn func(originalKey []byte, serial uint64) error) error {
	sPrefix := serialKey(prefix, nil) // 'S' + prefix
	return s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, sPrefix, func(_ *lmdb.Cursor, k, v []byte) error {
			serial, _ := binary.Uvarint(v)
			if serial <= since {
				return nil
			}
			return fn(k[1:], serial) // strip leading 'S'
		})
	})
}

// --- Embedding records (R1289-R1294) ---

func embedValueKey(tvid uint64) []byte {
	key := []byte(prefixEmbedValue)
	return encodeVarint(key, tvid)
}

// embedDefKey builds an ED prefix key: ED[tagname][fileid:8]. R2151
func embedDefKey(tag string, fileid uint64) []byte {
	key := make([]byte, len(prefixEmbedDef)+len(tag)+8)
	copy(key, prefixEmbedDef)
	copy(key[len(prefixEmbedDef):], tag)
	binary.BigEndian.PutUint64(key[len(prefixEmbedDef)+len(tag):], fileid)
	return key
}

// TagDefRef identifies a single (tag, fileid) tag-definition pair.
// Description is populated by MissingTagDefEmbeddings so the Librarian
// can embed without a second LMDB read. R2151, R2157
type TagDefRef struct {
	Tag         string
	FileID      uint64
	Description string
}

// WriteTagNameEmbedding appends an embedding vector to a T record.
// T record value layout (count:4bytes + float32 vector) is unchanged
// by stamping. Stamps ST<tag> in the same txn.
// CRC: crc-Store.md | R1289, R2178, R2179
func (s *Store) WriteTagNameEmbedding(tag string, vec []float32) error {
	tk := tagTotalKey(tag)
	return s.env.Update(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, tk)
		if err != nil && !lmdb.IsNotFound(err) {
			return err
		}
		val := make([]byte, 4)
		if v != nil {
			copy(val, v[:4]) // preserve count
		}
		val = append(val, float32ToBytes(vec)...)
		if err := txn.Put(s.dbi, tk, val, 0); err != nil {
			return err
		}
		return s.stampWrite(txn, []byte{byte(prefixTagTotal)}, []byte(tag))
	})
}

// WriteTagValueEmbedding writes an EV record. EV value layout is unchanged
// by stamping. Stamps SEV<tvid-varint> in the same txn.
// CRC: crc-Store.md | R1290, R2178, R2180
func (s *Store) WriteTagValueEmbedding(tvid uint64, vec []float32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		key := embedValueKey(tvid)
		if err := txn.Put(s.dbi, key, float32ToBytes(vec), 0); err != nil {
			return err
		}
		return s.stampWrite(txn, []byte(prefixEmbedValue), key[len(prefixEmbedValue):])
	})
}

// ReadTagNameEmbedding reads the embedding vector from a T record.
// Returns nil if no embedding is present.
func (s *Store) ReadTagNameEmbedding(tag string) ([]float32, error) {
	tk := tagTotalKey(tag)
	var vec []float32
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, tk)
		if lmdb.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(v) > 4 {
			vec = bytesToFloat32(v[4:])
		}
		return nil
	})
	return vec, err
}

// ReadTagValueEmbedding reads an EV record.
func (s *Store) ReadTagValueEmbedding(tvid uint64) ([]float32, error) {
	var vec []float32
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, embedValueKey(tvid))
		if lmdb.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		vec = bytesToFloat32(v)
		return nil
	})
	return vec, err
}

// ScanTagNameEmbeddings returns all T records that have embeddings as tag → vector.
func (s *Store) ScanTagNameEmbeddings() (map[string][]float32, error) {
	result := make(map[string][]float32)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) >= 2 && len(v) > 4 {
				result[string(k[1:])] = bytesToFloat32(v[4:])
			}
			return nil
		})
	})
	return result, err
}

// TagDefEmbedding pairs a (tag, fileid) ED key with its decoded vector.
// CRC: crc-Store.md | R2151, R2164
type TagDefEmbedding struct {
	Tag    string
	FileID uint64
	Vec    []float32
}

// ScanTagDefEmbeddings returns every ED record as a flat slice. Used
// by Librarian.SuggestTagNames to cosine-score each definition vector
// against a chunk's EC vector. Order is undefined.
// CRC: crc-Store.md | R2164
func (s *Store) ScanTagDefEmbeddings() ([]TagDefEmbedding, error) {
	var out []TagDefEmbedding
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedDef), func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < len(prefixEmbedDef)+8 {
				return nil
			}
			tag := string(k[len(prefixEmbedDef) : len(k)-8])
			fid := binary.BigEndian.Uint64(k[len(k)-8:])
			out = append(out, TagDefEmbedding{Tag: tag, FileID: fid, Vec: bytesToFloat32(v)})
			return nil
		})
	})
	return out, err
}

// ScanTagValueEmbeddings returns all EV records as tvid → vector.
func (s *Store) ScanTagValueEmbeddings() (map[uint64][]float32, error) {
	result := make(map[uint64][]float32)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedValue), func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) <= len(prefixEmbedValue) {
				return nil
			}
			id, _ := binary.Uvarint(k[len(prefixEmbedValue):])
			if id > 0 {
				result[id] = bytesToFloat32(v)
			}
			return nil
		})
	})
	return result, err
}

// MissingTagNameEmbeddings returns tag names from T records that lack embeddings.
func (s *Store) MissingTagNameEmbeddings() ([]string, error) {
	var missing []string
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) >= 2 && len(v) >= 4 && len(v) == 4 {
				// Has count but no embedding
				missing = append(missing, string(k[1:]))
			}
			return nil
		})
	})
	return missing, err
}

// MissingTagValueEmbeddings returns tvids from V records that lack EV records.
// CRC: crc-Store.md | R1292
func (s *Store) MissingTagValueEmbeddings() ([]uint64, error) {
	var missing []uint64
	err := s.env.View(func(txn *lmdb.Txn) error {
		// Collect all tvids from V records
		tvids := make(map[uint64]bool)
		if err := scanPrefix(txn, s.dbi, []byte{byte(prefixTagValue)}, func(_ *lmdb.Cursor, k, _ []byte) error {
			_, _, tvid, ok := parseVKey(k)
			if ok && tvid > 0 {
				tvids[tvid] = true
			}
			return nil
		}); err != nil {
			return err
		}
		// Remove tvids that already have EV records
		if err := scanPrefix(txn, s.dbi, []byte(prefixEmbedValue), func(_ *lmdb.Cursor, k, _ []byte) error {
			if len(k) > len(prefixEmbedValue) {
				id, _ := binary.Uvarint(k[len(prefixEmbedValue):])
				if id > 0 {
					delete(tvids, id)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		// What remains is missing
		for tvid := range tvids {
			missing = append(missing, tvid)
		}
		return nil
	})
	return missing, err
}

// WriteTagDefEmbedding writes an ED record keyed by (tag, fileid). ED
// value layout is unchanged by stamping. Stamps SED<tag><fileid:8> in
// the same txn.
// CRC: crc-Store.md | R2151, R2153, R2159, R2178, R2181
func (s *Store) WriteTagDefEmbedding(tag string, fileid uint64, vec []float32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		key := embedDefKey(tag, fileid)
		if err := txn.Put(s.dbi, key, float32ToBytes(vec), 0); err != nil {
			return err
		}
		return s.stampWrite(txn, []byte(prefixEmbedDef), key[len(prefixEmbedDef):])
	})
}

// ReadTagDefEmbedding reads an ED record. Returns (nil, nil) if absent.
// CRC: crc-Store.md | R2159
func (s *Store) ReadTagDefEmbedding(tag string, fileid uint64) ([]float32, error) {
	var vec []float32
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, embedDefKey(tag, fileid))
		if lmdb.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		vec = bytesToFloat32(v)
		return nil
	})
	return vec, err
}

// MissingTagDefEmbeddings returns (tag, fileid, description) tuples that
// have a D record but no matching ED record. Pure DB scan: enumerates
// every D record's description, then strips pairs already covered by
// ED. Crash-safe and self-recovering — the next post-reconcile pass
// picks up anything left unwritten.
// CRC: crc-Store.md | R2157
func (s *Store) MissingTagDefEmbeddings() ([]TagDefRef, error) {
	type tdKey struct {
		tag    string
		fileid uint64
	}
	missing := make(map[tdKey]string)
	err := s.env.View(func(txn *lmdb.Txn) error {
		// Collect every D record as a candidate.
		if err := scanPrefix(txn, s.dbi, []byte{byte(prefixTagDef)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 9 {
				return nil
			}
			tag := string(k[1 : len(k)-8])
			fid := binary.BigEndian.Uint64(k[len(k)-8:])
			missing[tdKey{tag, fid}] = string(v)
			return nil
		}); err != nil {
			return err
		}
		// Strip pairs that already have an ED record.
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedDef), func(_ *lmdb.Cursor, k, _ []byte) error {
			if len(k) < len(prefixEmbedDef)+8 {
				return nil
			}
			tag := string(k[len(prefixEmbedDef) : len(k)-8])
			fid := binary.BigEndian.Uint64(k[len(k)-8:])
			delete(missing, tdKey{tag, fid})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]TagDefRef, 0, len(missing))
	for k, desc := range missing {
		out = append(out, TagDefRef{Tag: k.tag, FileID: k.fileid, Description: desc})
	}
	return out, nil
}

// ScanVRecordTvids returns the live tvid → (tag, value) mapping. Reads
// the in-memory TvidMap; the V-record scan only runs once during
// LoadTvidMap. CRC: crc-Store.md | R1310, R1956
func (s *Store) ScanVRecordTvids() (map[uint64]TagAlt, error) {
	return s.tvids.Snapshot(), nil
}

// scanVRecordTvidsRaw is the persistent V-prefix scan. Called once by
// TvidMap.LoadFromStore at startup; not used afterwards.
// CRC: crc-Store.md | R1958
func (s *Store) scanVRecordTvidsRaw() (map[uint64]TagAlt, error) {
	result := make(map[uint64]TagAlt)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagValue)}, func(_ *lmdb.Cursor, k, _ []byte) error {
			tag, value, tvid, ok := parseVKey(k)
			if ok && tvid > 0 {
				result[tvid] = TagAlt{Tag: tag, Value: value}
			}
			return nil
		})
	})
	return result, err
}

// DropEmbeddings strips embedding vectors from T records and deletes
// all EV and ED records. Triggered by tag_model mismatch — every
// vector tied to the old model goes together. ST*/SEV*/SED* side-index
// entries are dropped in the same txn; SEC* is preserved (EC is not
// part of DropEmbeddings).
// CRC: crc-Store.md | R1294, R2160, R2187
func (s *Store) DropEmbeddings() error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Strip vectors from T records (keep count)
		if err := scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(v) > 4 {
				val := make([]byte, 4)
				copy(val, v[:4])
				return txn.Put(s.dbi, k, val, 0)
			}
			return nil
		}); err != nil {
			return err
		}
		// Delete all EV records
		if err := scanPrefix(txn, s.dbi, []byte(prefixEmbedValue), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		// Delete all ED records. R2160
		if err := scanPrefix(txn, s.dbi, []byte(prefixEmbedDef), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		// Delete every ST*, SEV*, SED* side-index entry. SEC* is preserved
		// (DropEmbeddings does not touch EC). R2187
		dropSerial := func(prefix []byte) error {
			return scanPrefix(txn, s.dbi, serialKey(prefix, nil), func(cur *lmdb.Cursor, _, _ []byte) error {
				return cur.Del(0)
			})
		}
		if err := dropSerial([]byte{byte(prefixTagTotal)}); err != nil {
			return err
		}
		if err := dropSerial([]byte(prefixEmbedValue)); err != nil {
			return err
		}
		if err := dropSerial([]byte(prefixEmbedDef)); err != nil {
			return err
		}
		// Delete all HC records and their SHC stamps. R2231
		if err := scanPrefix(txn, s.dbi, []byte(prefixHotCorrelation), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		return dropSerial([]byte(prefixHotCorrelation))
	})
}

// --- Hot Correlation Records (HC) ---
// CRC: crc-Store.md | R2226-R2231

// HotCorrelation pairs a chunkID with its cosine score against a tag's
// definition vectors. Stored under HC[tag][chunkID:8].
// CRC: crc-Store.md | R2226, R2227
type HotCorrelation struct {
	ChunkID uint64
	Score   float64
}

// hotCorrKey builds an HC prefix key: HC[tagname][chunkid:8].
// Same encoding as embedDefKey, with chunkid replacing fileid.
// CRC: crc-Store.md | R2226
func hotCorrKey(tag string, chunkID uint64) []byte {
	key := make([]byte, len(prefixHotCorrelation)+len(tag)+8)
	copy(key, prefixHotCorrelation)
	copy(key[len(prefixHotCorrelation):], tag)
	binary.BigEndian.PutUint64(key[len(prefixHotCorrelation)+len(tag):], chunkID)
	return key
}

// hotCorrValue packs a cosine score into 8 big-endian bytes.
// CRC: crc-Store.md | R2227
func hotCorrValue(score float64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], math.Float64bits(score))
	return buf[:]
}

// hotCorrParse decodes an HC value into its float64 score. Returns 0 if
// the value is shorter than 8 bytes (defensive — should never happen).
func hotCorrParse(v []byte) float64 {
	if len(v) < 8 {
		return 0
	}
	return math.Float64frombits(binary.BigEndian.Uint64(v))
}

// WriteHotCorrelation writes one HC entry and stamps SHC<tag><chunkid>
// in the same LMDB transaction. The stamp is the entry's alibi at
// freshness check time (R2229).
// CRC: crc-Store.md | R2226, R2227, R2229
func (s *Store) WriteHotCorrelation(tag string, chunkID uint64, score float64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		key := hotCorrKey(tag, chunkID)
		if err := txn.Put(s.dbi, key, hotCorrValue(score), 0); err != nil {
			return err
		}
		return s.stampWrite(txn, []byte(prefixHotCorrelation), key[len(prefixHotCorrelation):])
	})
}

// ReadHotCorrelations returns every HC entry for a tag. Order is
// undefined — caller sorts.
// CRC: crc-Store.md | R2226
func (s *Store) ReadHotCorrelations(tag string) ([]HotCorrelation, error) {
	prefix := make([]byte, len(prefixHotCorrelation)+len(tag))
	copy(prefix, prefixHotCorrelation)
	copy(prefix[len(prefixHotCorrelation):], tag)
	suffixOffset := len(prefix)
	tagLen := len(tag)
	var out []HotCorrelation
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			// Reject keys whose tag portion is longer than ours — scanPrefix
			// matches by byte prefix, so HC<tag>foo<chunkid> would also match
			// when querying HC<tag>. Re-check the tag boundary.
			if len(k) != suffixOffset+8 {
				return nil
			}
			// Extract tag portion to be sure prefix matches exactly.
			if string(k[len(prefixHotCorrelation):len(prefixHotCorrelation)+tagLen]) != tag {
				return nil
			}
			chunkID := binary.BigEndian.Uint64(k[suffixOffset:])
			out = append(out, HotCorrelation{ChunkID: chunkID, Score: hotCorrParse(v)})
			return nil
		})
	})
	return out, err
}

// DeleteHotCorrelation removes one HC entry and its matching SHC stamp
// in the same txn. No-op if the entry is absent.
// CRC: crc-Store.md | R2229
func (s *Store) DeleteHotCorrelation(tag string, chunkID uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		key := hotCorrKey(tag, chunkID)
		if err := txn.Del(s.dbi, key, nil); err != nil && !lmdb.IsNotFound(err) {
			return err
		}
		return deleteStamp(txn, s.dbi, []byte(prefixHotCorrelation), key[len(prefixHotCorrelation):])
	})
}

// ReplaceHotCorrelations atomically replaces a tag's HC entries with
// the supplied slice. All deletes and writes happen in one LMDB txn;
// every new entry is stamped with that txn's serial. Used by the
// sweep's phase-3 tag rebuild.
// CRC: crc-Store.md | R2229, R2238
func (s *Store) ReplaceHotCorrelations(tag string, entries []HotCorrelation) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Delete every existing HC entry for this tag, plus its stamp.
		prefix := make([]byte, len(prefixHotCorrelation)+len(tag))
		copy(prefix, prefixHotCorrelation)
		copy(prefix[len(prefixHotCorrelation):], tag)
		tagLen := len(tag)
		if err := scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, k, _ []byte) error {
			if len(k) != len(prefixHotCorrelation)+tagLen+8 {
				return nil
			}
			if string(k[len(prefixHotCorrelation):len(prefixHotCorrelation)+tagLen]) != tag {
				return nil
			}
			origKey := append([]byte(nil), k...)
			if err := cur.Del(0); err != nil {
				return err
			}
			return deleteStamp(txn, s.dbi, []byte(prefixHotCorrelation), origKey[len(prefixHotCorrelation):])
		}); err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		// One serial per replace call — all new entries in this batch share it.
		serial, err := s.allocSerial(txn)
		if err != nil {
			return err
		}
		for _, e := range entries {
			key := hotCorrKey(tag, e.ChunkID)
			if err := txn.Put(s.dbi, key, hotCorrValue(e.Score), 0); err != nil {
				return err
			}
			if err := stampWriteWith(txn, s.dbi, []byte(prefixHotCorrelation), key[len(prefixHotCorrelation):], serial); err != nil {
				return err
			}
		}
		return nil
	})
}

// MaxTagDefSerial returns the maximum recorded serial across the tag's
// SED side-index entries. Zero if the tag has no defs. Used by HC's
// alibi-stamp freshness check — a tag's HC entry is stale if any of
// its def serials has advanced past the entry's own stamp.
// CRC: crc-Store.md | R2229, R2249
func (s *Store) MaxTagDefSerial(tag string) (uint64, error) {
	prefix := serialKey([]byte(prefixEmbedDef), []byte(tag))
	wantKeyLen := len(prefix) + 8
	var maxSerial uint64
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) != wantKeyLen {
				return nil
			}
			serial, _ := binary.Uvarint(v)
			if serial > maxSerial {
				maxSerial = serial
			}
			return nil
		})
	})
	return maxSerial, err
}

// DropHotCorrelations deletes every HC record and every SHC side-index
// entry. Called by DropEmbeddings on model swap.
// CRC: crc-Store.md | R2231
func (s *Store) DropHotCorrelations() error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		if err := scanPrefix(txn, s.dbi, []byte(prefixHotCorrelation), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		return scanPrefix(txn, s.dbi, serialKey([]byte(prefixHotCorrelation), nil), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		})
	})
}

// --- Chunk Embedding Records (EC/EF) ---
// CRC: crc-Store.md | R1833-R1845

func chunkEmbedKey(chunkID uint64) []byte {
	key := []byte(prefixEmbedChunk)
	return encodeVarint(key, chunkID)
}

func fileCentroidKey(fileID uint64) []byte {
	key := []byte(prefixEmbedFileCent)
	return encodeVarint(key, fileID)
}

// ChunkVec pairs a chunkID with its embedding vector. R1837
type ChunkVec struct {
	ChunkID uint64
	Vec     []float32
}

// WriteChunkEmbedding writes one EC record keyed by chunkID. EC value
// layout is unchanged by stamping. Stamps SEC<chunkID-varint> in the
// same txn.
// CRC: crc-Store.md | R1836, R2178, R2182
func (s *Store) WriteChunkEmbedding(chunkID uint64, vec []float32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		key := chunkEmbedKey(chunkID)
		if err := txn.Put(s.dbi, key, float32ToBytes(vec), 0); err != nil {
			return err
		}
		return s.stampWrite(txn, []byte(prefixEmbedChunk), key[len(prefixEmbedChunk):])
	})
}

// WriteChunkEmbeddingBatch writes multiple EC records in a single
// transaction. One serial is allocated for the batch; every SEC entry is
// stamped with that one serial.
// CRC: crc-Store.md | R1837, R2183
func (s *Store) WriteChunkEmbeddingBatch(chunks []ChunkVec) error {
	if len(chunks) == 0 {
		return nil
	}
	return s.env.Update(func(txn *lmdb.Txn) error {
		serial, err := s.allocSerial(txn)
		if err != nil {
			return err
		}
		for _, c := range chunks {
			key := chunkEmbedKey(c.ChunkID)
			if err := txn.Put(s.dbi, key, float32ToBytes(c.Vec), 0); err != nil {
				return err
			}
			if err := stampWriteWith(txn, s.dbi, []byte(prefixEmbedChunk), key[len(prefixEmbedChunk):], serial); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReadChunkEmbedding reads one EC record by chunkID. R1838
func (s *Store) ReadChunkEmbedding(chunkID uint64) ([]float32, error) {
	var vec []float32
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, chunkEmbedKey(chunkID))
		if err != nil {
			return err
		}
		data := make([]byte, len(v))
		copy(data, v)
		vec = bytesToFloat32(data)
		return nil
	})
	if lmdb.IsNotFound(err) {
		return nil, nil
	}
	return vec, err
}

// ReadChunkEmbeddings batch reads EC records for centroid computation. R1842
func (s *Store) ReadChunkEmbeddings(chunkIDs []uint64) [][]float32 {
	result := make([][]float32, len(chunkIDs))
	s.env.View(func(txn *lmdb.Txn) error {
		for i, id := range chunkIDs {
			v, err := txn.Get(s.dbi, chunkEmbedKey(id))
			if err != nil {
				continue
			}
			data := make([]byte, len(v))
			copy(data, v)
			result[i] = bytesToFloat32(data)
		}
		return nil
	})
	return result
}

// DeleteChunkEmbedding deletes one EC record by chunkID and its matching
// SEC side-index entry.
// CRC: crc-Store.md | R1839, R2185
func (s *Store) DeleteChunkEmbedding(chunkID uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return s.DeleteChunkEmbeddingInTxn(txn, chunkID)
	})
}

// DeleteChunkEmbeddingInTxn deletes one EC record using an existing
// transaction; also drops the matching SEC side-index entry.
// CRC: crc-Store.md | R1840, R2185
func (s *Store) DeleteChunkEmbeddingInTxn(txn *lmdb.Txn, chunkID uint64) error {
	key := chunkEmbedKey(chunkID)
	err := txn.Del(s.dbi, key, nil)
	if err != nil && !lmdb.IsNotFound(err) {
		return err
	}
	return deleteStamp(txn, s.dbi, []byte(prefixEmbedChunk), key[len(prefixEmbedChunk):])
}

// WriteFileCentroid writes one EF record (running sum + count). R1835
func (s *Store) WriteFileCentroid(fileID uint64, sum []float32, count uint32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		if count == 0 {
			err := txn.Del(s.dbi, fileCentroidKey(fileID), nil)
			if lmdb.IsNotFound(err) {
				return nil
			}
			return err
		}
		buf := float32ToBytes(sum)
		countBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(countBuf, count)
		buf = append(buf, countBuf...)
		return txn.Put(s.dbi, fileCentroidKey(fileID), buf, 0)
	})
}

// ReadFileCentroid reads one EF record. Returns sum, count.
func (s *Store) ReadFileCentroid(fileID uint64) ([]float32, uint32, error) {
	var sum []float32
	var count uint32
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, fileCentroidKey(fileID))
		if err != nil {
			return err
		}
		data := make([]byte, len(v))
		copy(data, v)
		if len(data) < 4 {
			return fmt.Errorf("EF record too short")
		}
		count = binary.LittleEndian.Uint32(data[len(data)-4:])
		sum = bytesToFloat32(data[:len(data)-4])
		return nil
	})
	if lmdb.IsNotFound(err) {
		return nil, 0, nil
	}
	return sum, count, err
}

// DeleteFileCentroid deletes one EF record.
func (s *Store) DeleteFileCentroid(fileID uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		err := txn.Del(s.dbi, fileCentroidKey(fileID), nil)
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// DeleteFileCentroidInTxn deletes one EF record using an existing transaction. R1841
func (s *Store) DeleteFileCentroidInTxn(txn *lmdb.Txn, fileID uint64) error {
	err := txn.Del(s.dbi, fileCentroidKey(fileID), nil)
	if lmdb.IsNotFound(err) {
		return nil
	}
	return err
}

// ScanFileCentroids returns all EF records as fileID → centroid (sum/count). R1605
func (s *Store) ScanFileCentroids() (map[uint64][]float32, error) {
	result := make(map[uint64][]float32)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedFileCent), func(_ *lmdb.Cursor, k, v []byte) error {
			rest := k[len(prefixEmbedFileCent):]
			fileID, _ := binary.Uvarint(rest)
			data := make([]byte, len(v))
			copy(data, v)
			if len(data) < 4 {
				return nil
			}
			count := binary.LittleEndian.Uint32(data[len(data)-4:])
			sum := bytesToFloat32(data[:len(data)-4])
			if count == 0 || len(sum) < 2 {
				return nil
			}
			centroid := make([]float32, len(sum))
			for i, s := range sum {
				centroid[i] = s / float32(count)
			}
			result[fileID] = centroid
			return nil
		})
	})
	return result, err
}

// ScanFileCentroidCounts scans all EF records, returning fileID → stored count.
func (s *Store) ScanFileCentroidCounts() (map[uint64]uint32, error) {
	result := make(map[uint64]uint32)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedFileCent), func(_ *lmdb.Cursor, k, v []byte) error {
			rest := k[len(prefixEmbedFileCent):]
			fileID, _ := binary.Uvarint(rest)
			data := make([]byte, len(v))
			copy(data, v)
			if len(data) < 4 {
				return nil
			}
			count := binary.LittleEndian.Uint32(data[len(data)-4:])
			result[fileID] = count
			return nil
		})
	})
	return result, err
}

// ViewChunkEmbeddings cursor-walks the EC prefix inside a single
// LMDB View. fn receives the open txn, the chunkID, and the raw
// vector bytes (length is len(vec)/4 float32s). Reading further
// records inside the same txn (e.g. fts.ReadCRecord) is safe.
// Returning false stops the scan; returning an error aborts.
// CRC: crc-Store.md | R1915
func (s *Store) ViewChunkEmbeddings(fn func(txn *lmdb.Txn, chunkID uint64, vec []byte) (cont bool, err error)) error {
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedChunk), func(_ *lmdb.Cursor, k, v []byte) error {
			rest := k[len(prefixEmbedChunk):]
			chunkID, _ := binary.Uvarint(rest)
			cont, err := fn(txn, chunkID, v)
			if err != nil {
				return err
			}
			if !cont {
				return errStopScan
			}
			return nil
		})
	})
	if err == errStopScan {
		return nil
	}
	return err
}

// ScanChunkEmbeddingKeys scans all EC records, returning chunkID → vector dimension. R1845
func (s *Store) ScanChunkEmbeddingKeys() (map[uint64]int, error) {
	result := make(map[uint64]int)
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedChunk), func(_ *lmdb.Cursor, k, v []byte) error {
			rest := k[len(prefixEmbedChunk):]
			chunkID, _ := binary.Uvarint(rest)
			result[chunkID] = len(v) / 4
			return nil
		})
	})
	return result, err
}

// DropChunkEmbeddings deletes all EC and EF records, and drops every SEC*
// side-index entry alongside the EC sweep. EF is not stamped.
// CRC: crc-Store.md | R1844, R2193
func (s *Store) DropChunkEmbeddings() error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		if err := scanPrefix(txn, s.dbi, []byte(prefixEmbedChunk), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		if err := scanPrefix(txn, s.dbi, []byte(prefixEmbedFileCent), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		// Drop SEC* side-index entries. R2193
		return scanPrefix(txn, s.dbi, serialKey([]byte(prefixEmbedChunk), nil), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		})
	})
}

// pageContentKey builds the PC[fileID][page] key. R1720
func pageContentKey(fileID uint64, page uint32) []byte {
	key := []byte(prefixPageContent)
	key = encodeVarint(key, fileID)
	return encodeVarint(key, uint64(page))
}

// WritePageContent stores a per-page compressed chunk-text blob. R1720, R1721, R1722
func (s *Store) WritePageContent(fileID uint64, page uint32, blob []byte) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, pageContentKey(fileID, page), blob, 0)
	})
}

// ReadPageContent fetches a stored page blob. Returns (nil, nil) when absent.
func (s *Store) ReadPageContent(fileID uint64, page uint32) ([]byte, error) {
	var out []byte
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, pageContentKey(fileID, page))
		if lmdb.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		out = append(out[:0], v...)
		return nil
	})
	return out, err
}

// RemovePageContents deletes every PC record for a file. R1724, R1725
func (s *Store) RemovePageContents(fileID uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		prefix := []byte(prefixPageContent)
		prefix = encodeVarint(prefix, fileID)
		return scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		})
	})
}

// --- Recall Discussed-tag (RD) records ---

// Discussed is one (tag, value, timestamp) triple from the RD range.
// CRC: crc-Store.md | R2650, R2651
type Discussed struct {
	Tag       string    `json:"tag"`
	Value     string    `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// discussedKey builds an RD prefix key: "RD" + session + \x00 + tag + \x00 + value.
// A bare-tag entry (value == "") encodes with an empty trailing value
// segment — the key ends `... + \x00 + tag + \x00`. The `RD` prefix
// is the first occupant of the `R` recall-feature namespace.
// CRC: crc-Store.md | R2648, R2649
func discussedKey(session, tag, value string) []byte {
	key := make([]byte, 0, len(prefixDiscussed)+len(session)+1+len(tag)+1+len(value))
	key = append(key, prefixDiscussed...)
	key = append(key, session...)
	key = append(key, 0)
	key = append(key, tag...)
	key = append(key, 0)
	key = append(key, value...)
	return key
}

// discussedSessionPrefix returns the range-scan prefix for one session.
func discussedSessionPrefix(session string) []byte {
	prefix := make([]byte, 0, len(prefixDiscussed)+len(session)+1)
	prefix = append(prefix, prefixDiscussed...)
	prefix = append(prefix, session...)
	prefix = append(prefix, 0)
	return prefix
}

// parseDiscussedKey extracts the (session, tag, value) triple from an
// RD key. Returns false if the key isn't a well-formed RD record. R2648
func parseDiscussedKey(k []byte) (session, tag, value string, ok bool) {
	if !bytes.HasPrefix(k, []byte(prefixDiscussed)) {
		return "", "", "", false
	}
	rest := k[len(prefixDiscussed):]
	sep1 := bytes.IndexByte(rest, 0)
	if sep1 < 0 {
		return "", "", "", false
	}
	session = string(rest[:sep1])
	rest = rest[sep1+1:]
	sep2 := bytes.IndexByte(rest, 0)
	if sep2 < 0 {
		return "", "", "", false
	}
	tag = string(rest[:sep2])
	value = string(rest[sep2+1:])
	return session, tag, value, true
}

// AddDiscussed writes one RD record stamped with NOW. Re-adding an
// existing (session, tag, value) overwrites the timestamp. The value
// is exactly 8 bytes (big-endian uint64 unix-nanos), matching the RD
// record layout.
// CRC: crc-Store.md | R2648, R2650
func (s *Store) AddDiscussed(session, tag, value string) error {
	key := discussedKey(session, tag, value)
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, uint64(time.Now().UnixNano()))
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, key, val, 0)
	})
}

// ListDiscussed range-scans `RD + session + \x00` and returns the
// unexpired entries. `ttl == 0` disables expiry. `since > 0` keeps
// only entries newer than `NOW - since`. Malformed values (not 8
// bytes) are silently skipped — see R2663.
// CRC: crc-Store.md | R2651, R2659, R2663
func (s *Store) ListDiscussed(session string, since, ttl time.Duration) ([]Discussed, error) {
	if session == "" {
		return nil, nil
	}
	prefix := discussedSessionPrefix(session)
	now := time.Now()
	var entries []Discussed
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(v) != 8 {
				return nil // R2663
			}
			_, tag, value, ok := parseDiscussedKey(k)
			if !ok {
				return nil
			}
			ts := time.Unix(0, int64(binary.BigEndian.Uint64(v)))
			if ttl > 0 && ts.Add(ttl).Before(now) {
				return nil
			}
			if since > 0 && ts.Before(now.Add(-since)) {
				return nil
			}
			entries = append(entries, Discussed{Tag: tag, Value: value, Timestamp: ts})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Tag != entries[j].Tag {
			return entries[i].Tag < entries[j].Tag
		}
		return entries[i].Value < entries[j].Value
	})
	return entries, nil
}

// ClearDiscussed deletes every RD record under one session.
// Returns the deleted count.
// CRC: crc-Store.md | R2652
func (s *Store) ClearDiscussed(session string) (int, error) {
	if session == "" {
		return 0, nil
	}
	prefix := discussedSessionPrefix(session)
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, _, _ []byte) error {
			if err := cur.Del(0); err != nil {
				return err
			}
			deleted++
			return nil
		})
	})
	return deleted, err
}

// ClearAllDiscussed deletes every RD record across every session.
// Returns the deleted count. Intended for testing/reset; routine
// expiry uses PruneDiscussed.
// CRC: crc-Store.md | R2744
func (s *Store) ClearAllDiscussed() (int, error) {
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixDiscussed), func(cur *lmdb.Cursor, _, _ []byte) error {
			if err := cur.Del(0); err != nil {
				return err
			}
			deleted++
			return nil
		})
	})
	return deleted, err
}

// PruneDiscussed sweeps RD records across all sessions, deleting
// entries older than `ttl` (or, when `ttl == 0`, deletes nothing —
// "0" means never expire, matching the [recall].discussed_ttl
// convention). Returns the deleted count.
// CRC: crc-Store.md | R2653, R2659
func (s *Store) PruneDiscussed(ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-ttl)
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixDiscussed), func(cur *lmdb.Cursor, _, v []byte) error {
			if len(v) != 8 {
				// Treat malformed as expired too — same outcome as lazy
				// read, but here we actually clean up. R2663
				if err := cur.Del(0); err != nil {
					return err
				}
				deleted++
				return nil
			}
			ts := time.Unix(0, int64(binary.BigEndian.Uint64(v)))
			if ts.Before(cutoff) {
				if err := cur.Del(0); err != nil {
					return err
				}
				deleted++
			}
			return nil
		})
	})
	return deleted, err
}

// surfaceCooldownKey builds an RM key: "RM" + session + \x00 + chunkid varint.
// CRC: crc-Store.md | R2882
func surfaceCooldownKey(session string, chunkID uint64) []byte {
	key := make([]byte, 0, len(prefixSurfaceCooldown)+len(session)+1+binary.MaxVarintLen64)
	key = append(key, prefixSurfaceCooldown...)
	key = append(key, session...)
	key = append(key, 0)
	key = encodeVarint(key, chunkID)
	return key
}

// surfaceCooldownSessionPrefix returns the range-scan prefix for one session.
// CRC: crc-Store.md | R2882
func surfaceCooldownSessionPrefix(session string) []byte {
	prefix := make([]byte, 0, len(prefixSurfaceCooldown)+len(session)+1)
	prefix = append(prefix, prefixSurfaceCooldown...)
	prefix = append(prefix, session...)
	prefix = append(prefix, 0)
	return prefix
}

// MarkSurfaced writes/overwrites the RM record for (session, chunkID)
// with NOW unix-nanos, recording that the chunk was surfaced to the
// session (the secretary's surface-cooldown signal). Mirrors AddDiscussed.
// CRC: crc-Store.md | R2882, R2883
func (s *Store) MarkSurfaced(session string, chunkID uint64) error {
	if session == "" {
		return nil
	}
	key := surfaceCooldownKey(session, chunkID)
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, uint64(time.Now().UnixNano()))
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, key, val, 0)
	})
}

// LastSurfaced reads the RM timestamp for (session, chunkID). Absent
// returns (0, false, nil); a value not exactly 8 bytes is treated as
// absent (read robustness, mirroring RD).
// CRC: crc-Store.md | R2882, R2884
func (s *Store) LastSurfaced(session string, chunkID uint64) (int64, bool, error) {
	if session == "" {
		return 0, false, nil
	}
	key := surfaceCooldownKey(session, chunkID)
	var nanos int64
	var present bool
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, key)
		if err != nil {
			if lmdb.IsNotFound(err) {
				return nil
			}
			return err
		}
		if len(v) != 8 {
			return nil // malformed -> absent (R2884)
		}
		nanos = int64(binary.BigEndian.Uint64(v))
		present = true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return nanos, present, nil
}

// PruneSurfaceCooldown sweeps RM records across all sessions, deleting
// entries older than ttl (ttl <= 0 deletes nothing). Malformed values
// are dropped too. Returns the deleted count. Mirrors PruneDiscussed.
// CRC: crc-Store.md | R2885
func (s *Store) PruneSurfaceCooldown(ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-ttl)
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixSurfaceCooldown), func(cur *lmdb.Cursor, _, v []byte) error {
			if len(v) != 8 {
				if err := cur.Del(0); err != nil {
					return err
				}
				deleted++
				return nil
			}
			ts := time.Unix(0, int64(binary.BigEndian.Uint64(v)))
			if ts.Before(cutoff) {
				if err := cur.Del(0); err != nil {
					return err
				}
				deleted++
			}
			return nil
		})
	})
	return deleted, err
}

// ClearSurfaceCooldown deletes every RM record under one session.
// Returns the deleted count. Mirrors ClearDiscussed.
// CRC: crc-Store.md | R2887
func (s *Store) ClearSurfaceCooldown(session string) (int, error) {
	if session == "" {
		return 0, nil
	}
	prefix := surfaceCooldownSessionPrefix(session)
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, _, _ []byte) error {
			if err := cur.Del(0); err != nil {
				return err
			}
			deleted++
			return nil
		})
	})
	return deleted, err
}

// ClearAllSurfaceCooldown deletes every RM record across all sessions.
// Returns the deleted count. Mirrors ClearAllDiscussed.
// CRC: crc-Store.md | R2887
func (s *Store) ClearAllSurfaceCooldown() (int, error) {
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte(prefixSurfaceCooldown), func(cur *lmdb.Cursor, _, _ []byte) error {
			if err := cur.Del(0); err != nil {
				return err
			}
			deleted++
			return nil
		})
	})
	return deleted, err
}

// DerivedProposal is one RC record decoded for callers.
// CRC: crc-Store.md | R2678
type DerivedProposal struct {
	ChunkID uint64
	Tagname string
	Tally   uint64
}

// derivedKey builds an RC- or RJ-prefixed key. Both record classes
// share the same key shape: prefix + chunkid varint + tagname.
// CRC: crc-Store.md | R2664, R2665
func derivedKey(prefix string, chunkID uint64, tagname string) []byte {
	key := make([]byte, 0, len(prefix)+binary.MaxVarintLen64+len(tagname))
	key = append(key, prefix...)
	key = encodeVarint(key, chunkID)
	key = append(key, tagname...)
	return key
}

// derivedChunkPrefix returns the range-scan prefix for one chunk's
// RC or RJ records (prefix + chunkid varint, no tagname).
// CRC: crc-Store.md | R2664, R2665
func derivedChunkPrefix(prefix string, chunkID uint64) []byte {
	buf := make([]byte, 0, len(prefix)+binary.MaxVarintLen64)
	buf = append(buf, prefix...)
	buf = encodeVarint(buf, chunkID)
	return buf
}

// parseDerivedKey decodes a key produced by derivedKey for the
// given prefix. Returns (chunkID, tagname, ok). Used by
// DerivedProposals when iterating the RC range.
// CRC: crc-Store.md | R2664
func parseDerivedKey(prefix string, k []byte) (chunkID uint64, tagname string, ok bool) {
	if !bytes.HasPrefix(k, []byte(prefix)) {
		return 0, "", false
	}
	rest := k[len(prefix):]
	cid, n := binary.Uvarint(rest)
	if n <= 0 {
		return 0, "", false
	}
	return cid, string(rest[n:]), true
}

// derivedFreshnessKey returns the RF key for a chunk.
// CRC: crc-Store.md | R2666
func derivedFreshnessKey(chunkID uint64) []byte {
	buf := make([]byte, 0, len(prefixDerivedFreshness)+binary.MaxVarintLen64)
	buf = append(buf, prefixDerivedFreshness...)
	buf = encodeVarint(buf, chunkID)
	return buf
}

// WriteDerivedProposal writes or increments an RC record's tally
// inside the caller's write txn. New records start at tally=1; an
// existing record's 8-byte big-endian tally is incremented by 1.
// Malformed existing values (not exactly 8 bytes) are overwritten
// as if starting fresh.
// CRC: crc-Store.md | Seq: seq-derived-tags.md#1.7 | R2664, R2674, R2675
func (s *Store) WriteDerivedProposal(txn *lmdb.Txn, chunkID uint64, tagname string) error {
	key := derivedKey(prefixDerivedCandidate, chunkID, tagname)
	var tally uint64 = 1
	existing, err := txn.Get(s.dbi, key)
	if err == nil && len(existing) == 8 {
		tally = binary.BigEndian.Uint64(existing) + 1
	} else if err != nil && !lmdb.IsNotFound(err) {
		return err
	}
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, tally)
	return txn.Put(s.dbi, key, val, 0)
}

// WriteDerivedFreshness stamps a chunk's RF record with the given
// serial inside the caller's write txn.
// CRC: crc-Store.md | Seq: seq-derived-tags.md#1.7 | R2666, R2669, R2675
func (s *Store) WriteDerivedFreshness(txn *lmdb.Txn, chunkID, serial uint64) error {
	key := derivedFreshnessKey(chunkID)
	val := make([]byte, 0, binary.MaxVarintLen64)
	val = encodeVarint(val, serial)
	return txn.Put(s.dbi, key, val, 0)
}

// ReadDerivedFreshness reads a chunk's RF stamp. Missing or
// malformed varint returns (0, false, nil) — caller treats both as
// "stale, force re-process."
// CRC: crc-Store.md | R2666, R2669, R2681, R2682
func (s *Store) ReadDerivedFreshness(txn *lmdb.Txn, chunkID uint64) (uint64, bool, error) {
	key := derivedFreshnessKey(chunkID)
	v, err := txn.Get(s.dbi, key)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	serial, n := binary.Uvarint(v)
	if n <= 0 {
		return 0, false, nil // malformed varint — treat as stale (R2681)
	}
	return serial, true, nil
}

// AdjustJudgment applies a signed delta to the Recall Judgment edge
// RJ[chunkid+tagname] inside the caller's write txn: read the current
// score (absent = 0), add delta, stamp NOW, write the v3 value
// signed-varint(score) + 8-byte BE unix nanos. A score that returns to
// 0 deletes the record (absent equals 0). Positive delta reinforces;
// negative decays/rejects. Returns the new score.
// CRC: crc-Store.md | R2874, R2875, R2879, R2881
func (s *Store) AdjustJudgment(txn *lmdb.Txn, chunkID uint64, tagname string, delta int64) (int64, error) {
	key := derivedKey(prefixDerivedRejection, chunkID, tagname)
	var score int64
	if v, err := txn.Get(s.dbi, key); err == nil {
		if cur, _, ok := decodeJudgmentValue(v); ok {
			score = cur
		}
	} else if !lmdb.IsNotFound(err) {
		return 0, err
	}
	score += delta
	if score == 0 {
		if err := txn.Del(s.dbi, key, nil); err != nil && !lmdb.IsNotFound(err) {
			return 0, err
		}
		return 0, nil
	}
	if err := txn.Put(s.dbi, key, encodeJudgmentValue(score, time.Now().UnixNano()), 0); err != nil {
		return 0, err
	}
	return score, nil
}

// ReadJudgment reads the signed score of the Recall Judgment edge
// RJ[chunkid+tagname]. Absent gives (0, false, nil). A value that does
// not decode as signed-varint + 8 bytes is treated conservatively as
// rejected (a negative score with present=true) so a
// reject_propose_ceiling==0 caller never re-proposes a corrupt edge.
// CRC: crc-Store.md | R2874, R2876
func (s *Store) ReadJudgment(txn *lmdb.Txn, chunkID uint64, tagname string) (int64, bool, error) {
	key := derivedKey(prefixDerivedRejection, chunkID, tagname)
	v, err := txn.Get(s.dbi, key)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	score, _, ok := decodeJudgmentValue(v)
	if !ok {
		return -1, true, nil // corrupt edge reads as rejected (conservative)
	}
	return score, true, nil
}

// HasDerivedRejection reports whether the (chunk, tagname) edge is
// net-rejected (judgment score < 0) and the rejection magnitude
// (-score). Thin wrapper over ReadJudgment; the propose pass and the
// assistant's mention path consume magnitude exactly as they consumed
// the v2 counter.
// CRC: crc-Store.md | R2665, R2673, R2765, R2766, R2876, R2878
func (s *Store) HasDerivedRejection(txn *lmdb.Txn, chunkID uint64, tagname string) (bool, uint64, error) {
	score, present, err := s.ReadJudgment(txn, chunkID, tagname)
	if err != nil {
		return false, 0, err
	}
	if !present || score >= 0 {
		return false, 0, nil
	}
	return true, uint64(-score), nil
}

// decodeJudgmentValue parses a v3 Recall Judgment value:
// signed-varint(score) + 8-byte BE unix nanos. Returns (score, nanos,
// ok); ok=false signals a value that is not a signed varint followed
// by exactly 8 bytes.
// CRC: crc-Store.md | R2874, R2876
func decodeJudgmentValue(v []byte) (score int64, nanos int64, ok bool) {
	val, n := binary.Varint(v)
	if n <= 0 || len(v)-n != 8 {
		return 0, 0, false
	}
	return val, int64(binary.BigEndian.Uint64(v[n:])), true
}

// encodeJudgmentValue writes the v3 Recall Judgment value:
// signed-varint(score) + 8-byte BE unix nanos.
// CRC: crc-Store.md | R2874
func encodeJudgmentValue(score int64, nanos int64) []byte {
	buf := make([]byte, binary.MaxVarintLen64+8)
	n := binary.PutVarint(buf, score)
	binary.BigEndian.PutUint64(buf[n:n+8], uint64(nanos))
	return buf[:n+8]
}

// MaxEDSerial returns max(RecordSerial(ED, *)) across the ED prefix,
// or 0 if no ED records exist. Cheap with the S substrate.
// CRC: crc-Store.md | R2669
func (s *Store) MaxEDSerial() (uint64, error) {
	var maxS uint64
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, serialKey([]byte(prefixEmbedDef), nil), func(_ *lmdb.Cursor, _, v []byte) error {
			serial, n := binary.Uvarint(v)
			if n <= 0 {
				return nil
			}
			if serial > maxS {
				maxS = serial
			}
			return nil
		})
	})
	return maxS, err
}

// DerivedProposals returns all RC records for one chunk sorted by
// tally descending. RC entries shadowed by a matching RJ record are
// filtered as defense-in-depth — the derivation pass already skips
// them, but a forge view might surface pre-rejection RC records.
// Malformed RC values surface as tally=0; the next WriteDerivedProposal
// self-corrects.
// CRC: crc-Store.md | R2664, R2678, R2681
func (s *Store) DerivedProposals(chunkID uint64) ([]DerivedProposal, error) {
	prefix := derivedChunkPrefix(prefixDerivedCandidate, chunkID)
	var out []DerivedProposal
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			_, tagname, ok := parseDerivedKey(prefixDerivedCandidate, k)
			if !ok {
				return nil
			}
			rejected, _, err := s.HasDerivedRejection(txn, chunkID, tagname)
			if err != nil {
				return err
			}
			if rejected {
				return nil
			}
			var tally uint64
			if len(v) == 8 {
				tally = binary.BigEndian.Uint64(v)
			}
			out = append(out, DerivedProposal{ChunkID: chunkID, Tagname: tagname, Tally: tally})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tally != out[j].Tally {
			return out[i].Tally > out[j].Tally
		}
		return out[i].Tagname < out[j].Tagname
	})
	return out, nil
}

// ClearAllDerivedProposals deletes every RC record across the
// corpus. Returns the deleted count. Intended for testing/reset;
// the propose pass would otherwise rewrite RC records as chunks
// become stale.
// CRC: crc-Store.md | R2744
func (s *Store) ClearAllDerivedProposals() (int, error) {
	return s.clearAllByPrefix([]byte(prefixDerivedCandidate))
}

// ClearAllDerivedFreshness deletes every RF record across the
// corpus. Pairs with ClearAllDerivedProposals — without clearing
// RF, the propose pass treats existing chunks as fresh and skips
// the derivation step. Returns the deleted count.
// CRC: crc-Store.md | R2744
func (s *Store) ClearAllDerivedFreshness() (int, error) {
	return s.clearAllByPrefix([]byte(prefixDerivedFreshness))
}

// ClearAllDerivedRejections deletes every RJ record across the
// corpus. Curator-authored judgments are normally durable; this
// helper exists for testing reset and as the v2->v3 migration
// mechanism (`ark connections clean -all -checkpoint` wipes RJ; the
// next reject/reinforce cycle rewrites in v3 signed shape). Returns
// the deleted count.
// CRC: crc-Store.md | R2744, R2880
func (s *Store) ClearAllDerivedRejections() (int, error) {
	return s.clearAllByPrefix([]byte(prefixDerivedRejection))
}

// clearAllByPrefix is the shared scan-and-delete used by the
// recall-substrate Clear* helpers.
func (s *Store) clearAllByPrefix(prefix []byte) (int, error) {
	var deleted int
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, _, _ []byte) error {
			if err := cur.Del(0); err != nil {
				return err
			}
			deleted++
			return nil
		})
	})
	return deleted, err
}

// AcceptDerived promotes a derived proposal to an attached tag:
// delete RC[chunkid+tagname], then attach (tagname, value) via the
// existing F/V path (AppendTagValues). Returns the resolved tvid.
// Empty value produces a bare-tag attach. No-op safe if the RC record
// is already gone (e.g. concurrent accept). Two writes — the RC
// deletion isn't part of AppendTagValues' txn — but both run inside
// Store.env, so a crash between them leaves the chunk with the tag
// attached and the RC still present; the next derivation pass will
// drop the stale RC because alreadyOn now contains tagname (R2671).
// CRC: crc-Store.md | Seq: seq-derived-tags.md#2.2 | R2679
func (s *Store) AcceptDerived(chunkID uint64, tagname, value string) (uint64, error) {
	rcKey := derivedKey(prefixDerivedCandidate, chunkID, tagname)
	if err := s.env.Update(func(txn *lmdb.Txn) error {
		if err := txn.Del(s.dbi, rcKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return err
		}
		return nil
	}); err != nil {
		return 0, err
	}
	if err := s.AppendTagValues([]ChunkTagValues{{
		ChunkID: chunkID,
		Values:  []TagValue{{Tag: tagname, Value: value}},
	}}); err != nil {
		return 0, err
	}
	// Resolve the tvid the attach produced. The append-path reuses an
	// existing tvid if the (tag, value) pair already exists; either way
	// the tvids map now carries the canonical id.
	tvid, _ := s.tvids.Lookup(tagname, value)
	return tvid, nil
}

// RejectDerived deletes RC[chunkid+tagname] and applies a -1 judgment
// delta in one txn, returning the rejection magnitude (max(0,
// -newScore)). With no reinforcement producer present, a
// rejection-only sequence yields scores -1, -2, -3 ... bit-for-bit
// identical to the v2 monotonic counter. No-op safe if the RC record
// is already gone.
// CRC: crc-Store.md | Seq: seq-derived-tags.md#3.2 | R2680, R2877
func (s *Store) RejectDerived(chunkID uint64, tagname string) (uint64, error) {
	rcKey := derivedKey(prefixDerivedCandidate, chunkID, tagname)
	var magnitude uint64
	err := s.env.Update(func(txn *lmdb.Txn) error {
		if err := txn.Del(s.dbi, rcKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return err
		}
		newScore, err := s.AdjustJudgment(txn, chunkID, tagname, -1)
		if err != nil {
			return err
		}
		if newScore < 0 {
			magnitude = uint64(-newScore)
		}
		return nil
	})
	return magnitude, err
}

// --- float32 ↔ bytes conversion ---

func float32ToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func bytesToFloat32(b []byte) []float32 {
	n := len(b) / 4
	vec := make([]float32, n)
	for i := range n {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return vec
}
