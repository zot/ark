package ark

// CRC: crc-Store.md

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
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

// TagFileRecord is a per-file tag count returned by TagFiles.
type TagFileRecord struct {
	FileID uint64
	Tag    string
	Count  uint32
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
	return &Store{env: env, dbi: dbi}, nil
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

// UpdateTags replaces all tag records for a file and recomputes totals.
// tags maps tagname → count-in-file. All within one LMDB transaction.
func (s *Store) UpdateTags(fileid uint64, tags map[string]uint32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Collect old tags for this file
		oldTags, err := s.fileTagsInTxn(txn, fileid)
		if err != nil {
			return err
		}

		// Delete old F records and decrement T totals
		for tag, oldCount := range oldTags {
			fk := tagFileKey(fileid, tag)
			txn.Del(s.dbi, fk, nil)
			if err := s.adjustTagTotal(txn, tag, -int64(oldCount)); err != nil {
				return err
			}
		}

		// Write new F records and increment T totals
		for tag, count := range tags {
			fk := tagFileKey(fileid, tag)
			val := make([]byte, 4)
			binary.BigEndian.PutUint32(val, count)
			if err := txn.Put(s.dbi, fk, val, 0); err != nil {
				return err
			}
			if err := s.adjustTagTotal(txn, tag, int64(count)); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveTags deletes all tag records for a file and decrements totals.
func (s *Store) RemoveTags(fileid uint64) error {
	return s.UpdateTags(fileid, nil)
}

// AppendTags adds to existing F record counts and T totals for a file
// without replacing. Used by the append-only indexing path where only
// new content is scanned for tags.
func (s *Store) AppendTags(fileid uint64, tags map[string]uint32) error {
	if len(tags) == 0 {
		return nil
	}
	return s.env.Update(func(txn *lmdb.Txn) error {
		for tag, count := range tags {
			fk := tagFileKey(fileid, tag)
			var existing uint32
			var existingTvids []byte
			v, err := txn.Get(s.dbi, fk)
			if err == nil && len(v) >= 4 {
				existing = binary.BigEndian.Uint32(v[:4])
				if len(v) > 4 {
					existingTvids = bytes.Clone(v[4:])
				}
			} else if !lmdb.IsNotFound(err) && err != nil {
				return err
			}
			val := make([]byte, 4, 4+len(existingTvids))
			binary.BigEndian.PutUint32(val, existing+count)
			val = append(val, existingTvids...)
			if err := txn.Put(s.dbi, fk, val, 0); err != nil {
				return err
			}
			if err := s.adjustTagTotal(txn, tag, int64(count)); err != nil {
				return err
			}
		}
		return nil
	})
}

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

// TagCounts returns counts for specific tags.
func (s *Store) TagCounts(tags []string) ([]TagCount, error) {
	var results []TagCount
	err := s.env.View(func(txn *lmdb.Txn) error {
		for _, tag := range tags {
			tk := tagTotalKey(tag)
			v, err := txn.Get(s.dbi, tk)
			if lmdb.IsNotFound(err) {
				results = append(results, TagCount{Tag: tag, Count: 0})
				continue
			}
			if err != nil {
				return err
			}
			if len(v) >= 4 {
				results = append(results, TagCount{
					Tag:   tag,
					Count: binary.BigEndian.Uint32(v[:4]),
				})
			}
		}
		return nil
	})
	return results, err
}

// TagFiles returns per-file records for the given tags.
func (s *Store) TagFiles(tags []string) ([]TagFileRecord, error) {
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	var records []TagFileRecord
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, []byte{byte(prefixTagFile)}, func(_ *lmdb.Cursor, k, v []byte) error {
			if len(k) >= 10 && len(v) >= 4 {
				tag := string(k[9:])
				if tagSet[tag] {
					records = append(records, TagFileRecord{
						FileID: binary.BigEndian.Uint64(k[1:9]),
						Tag:    tag,
						Count:  binary.BigEndian.Uint32(v[:4]),
					})
				}
			}
			return nil
		})
	})
	return records, err
}

// fileTagsInTxn reads all tag counts for a file within an existing transaction.
func (s *Store) fileTagsInTxn(txn *lmdb.Txn, fileid uint64) (map[string]uint32, error) {
	tags := make(map[string]uint32)
	prefix := make([]byte, 9)
	prefix[0] = byte(prefixTagFile)
	binary.BigEndian.PutUint64(prefix[1:], fileid)

	err := scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
		if len(k) >= 10 && len(v) >= 4 {
			tags[string(k[9:])] = binary.BigEndian.Uint32(v[:4])
		}
		return nil
	})
	return tags, err
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

func tagFileKey(fileid uint64, tag string) []byte {
	key := make([]byte, 9+len(tag))
	key[0] = byte(prefixTagFile)
	binary.BigEndian.PutUint64(key[1:], fileid)
	copy(key[9:], tag)
	return key
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

// tagValueScanKey builds a V record scan prefix: V[tagname]\x00[value]\x00
// Used for prefix scan to find the one record with tvid appended.
// CRC: crc-Store.md | R1309
func tagValueScanKey(tag, value string) []byte {
	key := make([]byte, 1+len(tag)+1+len(value)+1)
	key[0] = byte(prefixTagValue)
	copy(key[1:], tag)
	key[1+len(tag)] = 0
	copy(key[2+len(tag):], value)
	key[2+len(tag)+len(value)] = 0
	return key
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

// UpdateTagValues replaces all V records for a fileid with new values.
// Also updates F records with tvids for targeted cleanup.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1099, R1100, R1101, R1103, R1281, R1311, R1312, R1313
func (s *Store) UpdateTagValues(fileid uint64, values []TagValue) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Remove fileid from existing V records using targeted cleanup
		if err := s.removeFileidByTvids(txn, fileid); err != nil {
			return err
		}
		// Add new V records, get tvids per tag
		tagTvids, err := s.addFileidToV(txn, fileid, values)
		if err != nil {
			return err
		}
		// Update F records with tvids (replace old tvids)
		return s.updateFRecordTvids(txn, fileid, tagTvids, true)
	})
}

// AppendTagValues adds V records without removing — append path.
// Also appends tvids to F records.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1104, R1281, R1311
func (s *Store) AppendTagValues(fileid uint64, values []TagValue) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		tagTvids, err := s.addFileidToV(txn, fileid, values)
		if err != nil {
			return err
		}
		return s.updateFRecordTvids(txn, fileid, tagTvids, false)
	})
}

// RemoveTagValues removes a fileid from V records identified by F-record tvids.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1105, R1312, R1313, R1314
func (s *Store) RemoveTagValues(fileid uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return s.removeFileidByTvids(txn, fileid)
	})
}

// updateFRecordTvids writes tvids to F records for a fileid.
// If replace is true, existing tvids are replaced (keeps count only).
// If replace is false, new tvids are appended to existing ones.
// CRC: crc-Store.md | R1311
func (s *Store) updateFRecordTvids(txn *lmdb.Txn, fileid uint64, tagTvids map[string][]uint64, replace bool) error {
	for tag, tvids := range tagTvids {
		fk := tagFileKey(fileid, tag)
		existing, err := txn.Get(s.dbi, fk)
		if lmdb.IsNotFound(err) || len(existing) < 4 {
			existing = nil
		} else if err != nil {
			return err
		}
		var val []byte
		if existing == nil {
			val = make([]byte, 4)
		} else if replace {
			val = make([]byte, 4)
			copy(val, existing[:4])
		} else {
			val = bytes.Clone(existing)
		}
		for _, tvid := range tvids {
			val = encodeVarint(val, tvid)
		}
		if err := txn.Put(s.dbi, fk, val, 0); err != nil {
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

// TagValueFiles returns fileids for a specific (tag, value) pair.
// Uses prefix scan since V key includes tvid suffix.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1110, R1309
func (s *Store) TagValueFiles(tag, value string) ([]uint64, error) {
	scanKey := tagValueScanKey(tag, value)
	var ids []uint64
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, scanKey, func(_ *lmdb.Cursor, _, v []byte) error {
			ids = decodeVarints(v)
			return errStopScan
		})
	})
	if err == errStopScan {
		err = nil
	}
	return ids, err
}

// FileTagValues returns the first value found per tag for a given fileid by scanning V records.
// CRC: crc-Store.md | R1142, R1143
func (s *Store) FileTagValues(fileid uint64, tags []string) (map[string]string, error) {
	result := make(map[string]string, len(tags))
	err := s.env.View(func(txn *lmdb.Txn) error {
		for _, tag := range tags {
			prefix := tagValuePrefix(tag, "")
			err := scanPrefix(txn, s.dbi, prefix, func(_ *lmdb.Cursor, k, v []byte) error {
				// Key format: V[tag]\x00[value]\x00[tvid]
				_, value, _, ok := parseVKey(k)
				if !ok {
					return nil
				}
				ids := decodeVarints(v)
				if slices.Contains(ids, fileid) {
					result[tag] = value
					return errStopScan
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

// removeFileidByTvids removes fileid from specific V records identified by tvids.
// Reads F records for the fileid to get tvids, then uses ScanVRecordTvids-style
// scan to find and update/delete those V records.
// CRC: crc-Store.md | R1312, R1313, R1314
func (s *Store) removeFileidByTvids(txn *lmdb.Txn, fileid uint64) error {
	// Read all F records for this fileid to collect tvids
	tvids := make(map[uint64]bool)
	fPrefix := make([]byte, 9)
	fPrefix[0] = byte(prefixTagFile)
	binary.BigEndian.PutUint64(fPrefix[1:], fileid)
	if err := scanPrefix(txn, s.dbi, fPrefix, func(_ *lmdb.Cursor, _, v []byte) error {
		// F value: count:4bytes + packed tvid varints
		if len(v) > 4 {
			for _, id := range decodeVarints(v[4:]) {
				tvids[id] = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if len(tvids) == 0 {
		// No tvids recorded — fall back to full scan for old-format records
		return s.removeFileidFromAllV(txn, fileid)
	}
	// Scan V prefix, find records with matching tvids, remove fileid
	prefix := []byte{byte(prefixTagValue)}
	return scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, k, v []byte) error {
		_, _, tvid, ok := parseVKey(k)
		if !ok || !tvids[tvid] {
			return nil
		}
		newV, found := removeVarint(v, fileid)
		if !found {
			return nil
		}
		if len(newV) == 0 {
			return cur.Del(0)
		}
		return txn.Put(s.dbi, k, newV, 0)
	})
}

// removeFileidFromAllV scans all V keys and removes the fileid from value blobs.
// Fallback for old-format V records that lack tvids in F records.
func (s *Store) removeFileidFromAllV(txn *lmdb.Txn, fileid uint64) error {
	prefix := []byte{byte(prefixTagValue)}
	return scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, k, v []byte) error {
		newV, found := removeVarint(v, fileid)
		if !found {
			return nil
		}
		if len(newV) == 0 {
			return cur.Del(0)
		}
		return txn.Put(s.dbi, k, newV, 0)
	})
}

// maxVKeyLen is the maximum V record key length. LMDB default max key is 511 bytes.
// Values that would push the key past this limit are skipped — long values
// aren't useful for completion.
const maxVKeyLen = 511

// findVRecord looks up an existing V record for (tag, value) by prefix scan.
// Returns the full key, existing value blob, and tvid. If not found, returns nil key and tvid 0.
func (s *Store) findVRecord(txn *lmdb.Txn, tag, value string) (fullKey, existing []byte, tvid uint64, err error) {
	scanKey := tagValueScanKey(tag, value)
	err = scanPrefix(txn, s.dbi, scanKey, func(_ *lmdb.Cursor, k, v []byte) error {
		fullKey = bytes.Clone(k)
		existing = bytes.Clone(v)
		_, _, tvid, _ = parseVKey(k)
		return errStopScan
	})
	if err == errStopScan {
		err = nil
	}
	return
}

// addFileidToV appends the fileid to V records for each (tag, value).
// Returns a map of tag → []tvid for F record storage.
// CRC: crc-Store.md | R1281, R1309, R1311
func (s *Store) addFileidToV(txn *lmdb.Txn, fileid uint64, values []TagValue) (map[string][]uint64, error) {
	tagTvids := make(map[string][]uint64)
	for _, tv := range values {
		if tv.Value == "" {
			continue
		}
		// Check key length (estimate without tvid)
		if 1+len(tv.Tag)+1+len(tv.Value)+1+10 > maxVKeyLen {
			continue
		}
		fullKey, existing, tvid, err := s.findVRecord(txn, tv.Tag, tv.Value)
		if err != nil {
			return nil, err
		}
		if fullKey == nil {
			// New (tag, value) pair — allocate tvid
			tvid, err = s.allocIDInTxn(txn, IFieldNextTvid)
			if err != nil {
				return nil, fmt.Errorf("alloc tvid: %w", err)
			}
			fullKey = tagValueFullKey(tv.Tag, tv.Value, tvid)
			blob := encodeVarint(nil, fileid)
			if err := txn.Put(s.dbi, fullKey, blob, 0); err != nil {
				return nil, err
			}
		} else {
			// Existing record — check if fileid already present
			if !slices.Contains(decodeVarints(existing), fileid) {
				blob := encodeVarint(bytes.Clone(existing), fileid)
				if err := txn.Put(s.dbi, fullKey, blob, 0); err != nil {
					return nil, err
				}
			}
		}
		tagTvids[tv.Tag] = append(tagTvids[tv.Tag], tvid)
	}
	return tagTvids, nil
}

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

// RecordCounts scans all keys in the ark subdatabase and returns
// stats grouped by prefix byte. CRC: crc-Store.md | R907
func (s *Store) RecordCounts() (map[byte]RecordStats, error) {
	counts := make(map[byte]RecordStats)
	err := s.env.View(func(txn *lmdb.Txn) error {
		cur, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cur.Close()
		k, v, err := cur.Get(nil, nil, lmdb.First)
		for err == nil {
			if len(k) > 0 {
				s := counts[k[0]]
				s.Count++
				s.KeyBytes += int64(len(k))
				s.ValueBytes += int64(len(v))
				counts[k[0]] = s
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

// AllocTagValueID atomically increments and returns the next tag-value-id.
// CRC: crc-Store.md | R1280, R1282, R1536, R1572
func (s *Store) AllocTagValueID() (uint64, error) {
	return s.allocID(IFieldNextTvid)
}

func (s *Store) allocID(iFieldName string) (uint64, error) {
	var id uint64
	err := s.env.Update(func(txn *lmdb.Txn) error {
		var err error
		id, err = s.allocIDInTxn(txn, iFieldName)
		return err
	})
	return id, err
}

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

// ScanVRecordTvids scans all V records and returns tvid → {tag, value} mapping.
// CRC: crc-Store.md | R1310
func (s *Store) ScanVRecordTvids() (map[uint64]TagAlt, error) {
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
// CRC: crc-Store.md | R1598-R1608, R1618, R1619

// ChunkEmbedRef identifies a chunk that needs embedding.
type ChunkEmbedRef struct {
	FileID   uint64
	ChunkIdx int
	Path     string
}

func chunkEmbedKey(fileID uint64, chunkIdx int) []byte {
	key := []byte(prefixEmbedChunk)
	key = encodeVarint(key, fileID)
	key = encodeVarint(key, uint64(chunkIdx))
	return key
}

func fileCentroidKey(fileID uint64) []byte {
	key := []byte(prefixEmbedFileCent)
	key = encodeVarint(key, fileID)
	return key
}

// WriteChunkEmbedding writes one EC record. R1600
func (s *Store) WriteChunkEmbedding(fileID uint64, chunkIdx int, vec []float32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, chunkEmbedKey(fileID, chunkIdx), float32ToBytes(vec), 0)
	})
}

// ChunkVec pairs a chunk index with its embedding vector for batch writes.
type ChunkVec struct {
	FileID   uint64
	ChunkIdx int
	Vec      []float32
}

// WriteChunkEmbeddingBatch writes multiple EC records in a single transaction. R1600
func (s *Store) WriteChunkEmbeddingBatch(chunks []ChunkVec) error {
	if len(chunks) == 0 {
		return nil
	}
	return s.env.Update(func(txn *lmdb.Txn) error {
		for _, c := range chunks {
			if err := txn.Put(s.dbi, chunkEmbedKey(c.FileID, c.ChunkIdx), float32ToBytes(c.Vec), 0); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReadChunkEmbedding reads one EC record. R1601
func (s *Store) ReadChunkEmbedding(fileID uint64, chunkIdx int) ([]float32, error) {
	var vec []float32
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, chunkEmbedKey(fileID, chunkIdx))
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

// WriteFileCentroid writes one EF record (running sum + count). R1602
func (s *Store) WriteFileCentroid(fileID uint64, sum []float32, count uint32) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		if count == 0 {
			// Invalidate: delete the EF record
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

// ReadFileCentroid reads one EF record. Returns sum, count. R1603
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
				return nil // skip invalid or "all-skipped" markers
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

// RemoveFileChunkEmbeddings deletes all EC records for a file and its EF centroid. R1607
func (s *Store) RemoveFileChunkEmbeddings(fileID uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// EC records keyed by fileID prefix
		prefix := []byte(prefixEmbedChunk)
		prefix = encodeVarint(prefix, fileID)
		if err := scanPrefix(txn, s.dbi, prefix, func(cur *lmdb.Cursor, _, _ []byte) error {
			return cur.Del(0)
		}); err != nil {
			return err
		}
		// EF centroid — not-found is fine (may not exist yet)
		err := txn.Del(s.dbi, fileCentroidKey(fileID), nil)
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// DropChunkEmbeddings deletes all EC and EF records. R1606
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
