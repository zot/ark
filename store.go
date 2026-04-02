package ark

// CRC: crc-Store.md

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
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

// ArkSettings stores ark-level configuration in LMDB.
type ArkSettings struct {
	Dotfiles bool              `json:"dotfiles"`
	Extra    map[string]string `json:"extra,omitempty"`
}

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
	prefixMissing    = 'M'
	prefixUnresolved = 'U'
	prefixInfo       = 'I'
	prefixTagTotal   = 'T'
	prefixTagFile    = 'F'
	prefixTagDef     = 'D'
	prefixTagValue   = 'V'
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

// GetSettings reads ark-level settings from the subdatabase.
func (s *Store) GetSettings() (ArkSettings, error) {
	var settings ArkSettings
	err := s.env.View(func(txn *lmdb.Txn) error {
		key := []byte{byte(prefixInfo)}
		val, err := txn.Get(s.dbi, key)
		if err != nil {
			return err
		}
		return json.Unmarshal(val, &settings)
	})
	if lmdb.IsNotFound(err) {
		return ArkSettings{Dotfiles: true}, nil
	}
	return settings, err
}

// PutSettings writes ark-level settings to the subdatabase.
func (s *Store) PutSettings(settings ArkSettings) error {
	val, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	key := []byte{byte(prefixInfo)}
	return s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, key, val, 0)
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
			v, err := txn.Get(s.dbi, fk)
			if err == nil && len(v) == 4 {
				existing = binary.BigEndian.Uint32(v)
			} else if !lmdb.IsNotFound(err) && err != nil {
				return err
			}
			val := make([]byte, 4)
			binary.BigEndian.PutUint32(val, existing+count)
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
			if len(k) >= 2 && len(v) == 4 {
				count := binary.BigEndian.Uint32(v)
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
			if len(v) == 4 {
				results = append(results, TagCount{
					Tag:   tag,
					Count: binary.BigEndian.Uint32(v),
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
			if len(k) >= 10 && len(v) == 4 {
				tag := string(k[9:])
				if tagSet[tag] {
					records = append(records, TagFileRecord{
						FileID: binary.BigEndian.Uint64(k[1:9]),
						Tag:    tag,
						Count:  binary.BigEndian.Uint32(v),
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
		if len(k) >= 10 && len(v) == 4 {
			tags[string(k[9:])] = binary.BigEndian.Uint32(v)
		}
		return nil
	})
	return tags, err
}

// adjustTagTotal increments or decrements a T record within an existing transaction.
func (s *Store) adjustTagTotal(txn *lmdb.Txn, tag string, delta int64) error {
	tk := tagTotalKey(tag)
	var current uint32
	v, err := txn.Get(s.dbi, tk)
	if err == nil && len(v) == 4 {
		current = binary.BigEndian.Uint32(v)
	} else if !lmdb.IsNotFound(err) && err != nil {
		return err
	}

	newVal := int64(current) + delta
	if newVal <= 0 {
		// Remove the T record entirely
		txn.Del(s.dbi, tk, nil)
		return nil
	}

	val := make([]byte, 4)
	binary.BigEndian.PutUint32(val, uint32(newVal))
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

// tagValueKey builds a V record key: V[tagname]\x00[value]
func tagValueKey(tag, value string) []byte {
	key := make([]byte, 1+len(tag)+1+len(value))
	key[0] = byte(prefixTagValue)
	copy(key[1:], tag)
	key[1+len(tag)] = 0
	copy(key[2+len(tag):], value)
	return key
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
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1099, R1100, R1101, R1103
func (s *Store) UpdateTagValues(fileid uint64, values []TagValue) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		// Remove fileid from all existing V records
		if err := s.removeFileidFromAllV(txn, fileid); err != nil {
			return err
		}
		// Add new V records
		return s.addFileidToV(txn, fileid, values)
	})
}

// AppendTagValues adds V records without removing — append path.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1104
func (s *Store) AppendTagValues(fileid uint64, values []TagValue) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return s.addFileidToV(txn, fileid, values)
	})
}

// RemoveTagValues removes a fileid from all V records.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1105
func (s *Store) RemoveTagValues(fileid uint64) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		return s.removeFileidFromAllV(txn, fileid)
	})
}

// QueryTagValues returns values for a tag, optionally filtered by prefix.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1108, R1109
func (s *Store) QueryTagValues(tag, prefix string) ([]TagValueCount, error) {
	scanKey := tagValuePrefix(tag, prefix)
	var results []TagValueCount
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, scanKey, func(_ *lmdb.Cursor, k, v []byte) error {
			// Key format: V[tag]\x00[value] — extract value after the null separator
			tagEnd := bytes.IndexByte(k[1:], 0)
			if tagEnd < 0 {
				return nil
			}
			value := string(k[1+tagEnd+1:])
			count := len(decodeVarints(v))
			if count > 0 {
				results = append(results, TagValueCount{Value: value, Count: count})
			}
			return nil
		})
	})
	return results, err
}

// TagValueFiles returns fileids for a specific (tag, value) pair.
// CRC: crc-Store.md | Seq: seq-tag-value-index.md | R1110
func (s *Store) TagValueFiles(tag, value string) ([]uint64, error) {
	key := tagValueKey(tag, value)
	var ids []uint64
	err := s.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(s.dbi, key)
		if lmdb.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		ids = decodeVarints(v)
		return nil
	})
	return ids, err
}

// removeFileidFromAllV scans all V keys and removes the fileid from value blobs.
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

// addFileidToV appends the fileid to V records for each (tag, value).
func (s *Store) addFileidToV(txn *lmdb.Txn, fileid uint64, values []TagValue) error {
	for _, tv := range values {
		if tv.Value == "" {
			continue
		}
		key := tagValueKey(tv.Tag, tv.Value)
		existing, err := txn.Get(s.dbi, key)
		if lmdb.IsNotFound(err) {
			existing = nil
		} else if err != nil {
			return err
		}
		// Check if fileid already present
		for _, id := range decodeVarints(existing) {
			if id == fileid {
				goto next
			}
		}
		{
			blob := encodeVarint(bytes.Clone(existing), fileid)
			if err := txn.Put(s.dbi, key, blob, 0); err != nil {
				return err
			}
		}
	next:
	}
	return nil
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

// scheduleConfigKey is the LMDB key for storing the [schedule] config hash.
const scheduleConfigKey = "schedule_config"

// GetScheduleConfig reads the stored [schedule] config string from settings.
// CRC: crc-Store.md | R927, R928
func (s *Store) GetScheduleConfig() (string, error) {
	settings, err := s.GetSettings()
	if err != nil {
		return "", err
	}
	return settings.Extra[scheduleConfigKey], nil
}

// PutScheduleConfig writes the [schedule] config string to settings.
// CRC: crc-Store.md | R927, R932
func (s *Store) PutScheduleConfig(serialized string) error {
	settings, err := s.GetSettings()
	if err != nil {
		return err
	}
	if settings.Extra == nil {
		settings.Extra = make(map[string]string)
	}
	settings.Extra[scheduleConfigKey] = serialized
	return s.PutSettings(settings)
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
