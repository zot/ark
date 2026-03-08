package ark

// CRC: crc-Store.md

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
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
	Dotfiles bool `json:"dotfiles"`
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
