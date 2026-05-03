package ark

// CRC: crc-Store.md

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
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
	IFieldGlobalInclude   = "global_include"
	IFieldGlobalExclude   = "global_exclude"
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
	prefixMissing       = 'M'
	prefixUnresolved    = 'U'
	prefixInfo          = 'I'
	prefixTagTotal      = 'T'
	prefixTagFile       = 'F'
	prefixTagDef        = 'D'
	prefixTagValue      = 'V'
	prefixEmbedValue    = "EV" // R1290: tag-value compound embeddings
	prefixEmbedChunk    = "EC" // R1598: chunk-level embeddings
	prefixEmbedFileCent = "EF" // R1599: file centroid (running sum + count)
	prefixError         = 'E'  // R1543: persistent error conditions (E + name → JSON)
	prefixPageContent   = "PC" // R1720: per-page zlib-compressed chunk text blob
	prefixExtRouting    = 'X'  // R1989: @ext provenance (X[tvid_ext][target_chunkid] → routed_tvid varints)
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
					if matcher.Match(pat, rec.Path, false) {
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
					if matcher.Match(pat, rec.Path, false) {
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
		if err := putJSON(IFieldGlobalInclude, cfg.GlobalInclude); err != nil {
			return err
		}
		if err := putJSON(IFieldGlobalExclude, cfg.GlobalExclude); err != nil {
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
		getJSON(IFieldGlobalInclude, &cfg.GlobalInclude)
		getJSON(IFieldGlobalExclude, &cfg.GlobalExclude)
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

// ListTags returns all tags with their total counts.
func (s *Store) ListTags() ([]TagCount, error) {
	var tags []TagCount
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) >= 2 && len(v) >= 4 {
				count := binary.BigEndian.Uint32(v[:4])
				if count > 0 {
					tags = append(tags, TagCount{Tag: string(k[1:]), Count: count})
				}
			}
			return nil
		})
	})
	return tags, err
}

// TagCounts returns counts for specific tags. Augments the LMDB
// F-driven T count with ExtMap.VirtualTagCount so ext-routed
// contributions show up in totals (multi-set V semantics — routed
// tag tvids do not write F records at the target chunkid).
// CRC: crc-Store.md | R2010
func (s *Store) TagCounts(tags []string) ([]TagCount, error) {
	var virtual map[string]int
	if s.extmap != nil {
		virtual = s.extmap.VirtualTagCounts(tags)
	}
	var results []TagCount
	err := s.env.View(func(txn *lmdb.Txn) error {
		for _, tag := range tags {
			tk := tagTotalKey(tag)
			v, err := txn.Get(s.dbi, tk)
			extra := uint32(virtual[tag])
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
func (s *Store) UpdateTagDefs(fileid uint64, defs map[string]string) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Remove old D records for this fileid
		prefix := []byte{byte(prefixTagDef)}
		_ = scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, k, v []byte) error {
			// Key format: D[tagname][fileid:8] — fileid is last 8 bytes
			if len(k) < 9 {
				return nil
			}
			fid := binary.BigEndian.Uint64(k[len(k)-8:])
			if fid == fileid {
				return cur.Del(0)
			}
			return nil
		})

		// Write new D records
		for tag, desc := range defs {
			dk := tagDefKey(tag, fileid)
			if err := txn.Put(s.dbi, dk, []byte(desc), 0); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveTagDefs deletes all D records for a fileid.
func (s *Store) RemoveTagDefs(fileid uint64) error {
	return s.UpdateTagDefs(fileid, nil)
}

// AppendTagDefs adds D records without removing existing ones.
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
func (s *Store) QueryTagValues(tag, prefix string) ([]TagValueCount, error) {
	scanKey := tagValuePrefix(tag, prefix)
	var results []TagValueCount
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, scanKey, func(_ *lmdb.Cursor, k, v []byte) error {
			// Key format: V[tag]\x00[value]\x00[tvid] — parse with two null separators
			_, value, _, ok := parseVKey(k)
			if !ok {
				return nil
			}
			count := len(decodeVarints(v))
			if count > 0 {
				results = append(results, TagValueCount{Value: value, Count: count})
			}
			return nil
		})
	})
	// R1129: sort by count descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Count > results[j].Count
	})
	return results, err
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

// FileTagValues returns the first value found per tag for a given fileid.
// Resolves fileid → chunkids via chunksForFile (opens its own View), then
// scans V records for each tag and returns the value whose chunkid set
// intersects the file's chunkids. "First" means the first V record
// encountered in LMDB key order.
// CRC: crc-Store.md | R1142, R1143, R1889
func (s *Store) FileTagValues(fileid uint64, tags []string) (map[string]string, error) {
	if IsOverlayID(fileid) {
		if s.tmp == nil {
			return map[string]string{}, nil
		}
		return s.tmp.FileTagValues(fileid, tags), nil
	}
	result := make(map[string]string, len(tags))
	if s.chunksForFile == nil {
		return result, nil
	}
	chunks := s.chunksForFile(fileid)
	if len(chunks) == 0 {
		return result, nil
	}
	chunkSet := make(map[uint64]bool, len(chunks))
	for _, c := range chunks {
		chunkSet[c] = true
	}

	err := s.env.View(func(txn *lmdb.Txn) error {
		for _, tag := range tags {
			prefix := tagValuePrefix(tag, "")
			err := scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
				// Key format: V[tag]\x00[value]\x00[tvid]
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
	return result, err
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

// TagValueMatch is a tag value with its file ID list, returned by MatchTagValues.
// CRC: crc-Store.md | R1468
type TagValueMatch struct {
	Value   string   `json:"value"`
	FileIDs []uint64 `json:"file_ids"`
}

// MatchTagNames scans T records and returns tag names where every token
// appears as a case-insensitive substring. Linear scan — the T record
// set is small (hundreds to low thousands).
// CRC: crc-Store.md | R1467
func (s *Store) MatchTagNames(tokens []string) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	lower := make([]string, len(tokens))
	for i, t := range tokens {
		lower[i] = strings.ToLower(t)
	}
	var matched []string
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagTotal)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) < 2 || len(v) < 4 {
				return nil
			}
			name := strings.ToLower(string(k[1:]))
			for _, tok := range lower {
				if !strings.Contains(name, tok) {
					return nil
				}
			}
			matched = append(matched, string(k[1:]))
			return nil
		})
	})
	return matched, err
}

// MatchTagValues scans V records for a given tag name and returns values
// where every token appears as a case-insensitive substring. Each result
// includes the value string and its file ID list.
// CRC: crc-Store.md | R1468
func (s *Store) MatchTagValues(tag string, tokens []string) ([]TagValueMatch, error) {
	lower := make([]string, len(tokens))
	for i, t := range tokens {
		lower[i] = strings.ToLower(t)
	}
	prefix := tagValuePrefix(tag, "")
	var results []TagValueMatch
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
			_, value, _, ok := parseVKey(k)
			if !ok {
				return nil
			}
			if len(tokens) > 0 {
				lv := strings.ToLower(value)
				for _, tok := range lower {
					if !strings.Contains(lv, tok) {
						return nil
					}
				}
			}
			results = append(results, TagValueMatch{
				Value:   value,
				FileIDs: decodeVarints(v),
			})
			return nil
		})
	})
	return results, err
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
// key. Known multi-byte prefixes (E:, EV, EC, EF, PC) are matched
// first; otherwise the single-byte prefix is returned.
// CRC: crc-Store.md | R905, R907
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
		case k[0] == 'P' && k[1] == 'C':
			return "PC"
		}
	}
	if len(k) == 0 {
		return ""
	}
	return string(k[0:1])
}

// RecordCounts scans all keys in the ark subdatabase and returns
// stats grouped by full prefix string.
// CRC: crc-Store.md | R905, R907
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

// --- Embedding records (R1289-R1294) ---

func embedValueKey(tvid uint64) []byte {
	key := []byte(prefixEmbedValue)
	return encodeVarint(key, tvid)
}

// WriteTagNameEmbedding appends an embedding vector to a T record.
// T record value: count:4bytes + float32 vector (3072 bytes).
// CRC: crc-Store.md | R1289
func (s *Store) WriteTagNameEmbedding(tag string, vec []float32) error {
	tk := tagTotalKey(tag)
	return s.env.Update(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, tk)
		if lmdb.IsNotFound(err) {
			// Tag doesn't exist — write count=0 + vector
			val := make([]byte, 4)
			val = append(val, float32ToBytes(vec)...)
			return txn.Put(s.dbi, tk, val, 0)
		}
		if err != nil {
			return err
		}
		// Preserve count, replace/add vector
		val := make([]byte, 4)
		copy(val, v[:4])
		val = append(val, float32ToBytes(vec)...)
		return txn.Put(s.dbi, tk, val, 0)
	})
}

// WriteTagValueEmbedding writes an EV record.
// CRC: crc-Store.md | R1290
func (s *Store) WriteTagValueEmbedding(tvid uint64, vec []float32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, embedValueKey(tvid), float32ToBytes(vec), 0)
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

// DropEmbeddings strips embedding vectors from T records and deletes all EV records.
// CRC: crc-Store.md | R1294
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
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedValue), func(cur *lmdb.Cursor, _, _ []byte) error {
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

// WriteChunkEmbedding writes one EC record keyed by chunkID. R1836
func (s *Store) WriteChunkEmbedding(chunkID uint64, vec []float32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, chunkEmbedKey(chunkID), float32ToBytes(vec), 0)
	})
}

// WriteChunkEmbeddingBatch writes multiple EC records in a single transaction. R1837
func (s *Store) WriteChunkEmbeddingBatch(chunks []ChunkVec) error {
	if len(chunks) == 0 {
		return nil
	}
	return s.env.Update(func(txn *lmdb.Txn) error {
		for _, c := range chunks {
			if err := txn.Put(s.dbi, chunkEmbedKey(c.ChunkID), float32ToBytes(c.Vec), 0); err != nil {
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

// DeleteChunkEmbedding deletes one EC record by chunkID. R1839
func (s *Store) DeleteChunkEmbedding(chunkID uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		err := txn.Del(s.dbi, chunkEmbedKey(chunkID), nil)
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// DeleteChunkEmbeddingInTxn deletes one EC record using an existing transaction. R1840
func (s *Store) DeleteChunkEmbeddingInTxn(txn *lmdb.Txn, chunkID uint64) error {
	err := txn.Del(s.dbi, chunkEmbedKey(chunkID), nil)
	if lmdb.IsNotFound(err) {
		return nil
	}
	return err
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

// DropChunkEmbeddings deletes all EC and EF records. R1844
func (s *Store) DropChunkEmbeddings() error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		if err := scanPrefix(txn, s.dbi, []byte(prefixEmbedChunk), func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		return scanPrefix(txn, s.dbi, []byte(prefixEmbedFileCent), func(cur *lmdb.Cursor, _, _ []byte) error {
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
