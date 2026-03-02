package ark

// CRC: crc-Store.md

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// Store manages ark's own LMDB subdatabase for missing files,
// unresolved files, and settings.
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

// Key prefixes for the ark subdatabase.
const (
	prefixMissing    = 'M'
	prefixUnresolved = 'U'
	prefixInfo       = 'I'
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
		cur, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cur.Close()

		prefix := []byte{byte(prefixMissing)}
		k, v, err := cur.Get(prefix, nil, lmdb.SetRange)
		for err == nil {
			if len(k) < 9 || k[0] != byte(prefixMissing) {
				break
			}
			var rec MissingRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				rec.FileID = binary.BigEndian.Uint64(k[1:9])
				records = append(records, rec)
			}
			k, v, err = cur.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
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
		cur, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cur.Close()

		prefix := []byte{byte(prefixUnresolved)}
		k, v, err := cur.Get(prefix, nil, lmdb.SetRange)
		for err == nil {
			if len(k) == 0 || k[0] != byte(prefixUnresolved) {
				break
			}
			var rec UnresolvedRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				records = append(records, rec)
			}
			k, v, err = cur.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
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
		cur, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cur.Close()

		prefix := []byte{byte(prefixMissing)}
		k, v, err := cur.Get(prefix, nil, lmdb.SetRange)
		for err == nil {
			if len(k) < 9 || k[0] != byte(prefixMissing) {
				break
			}
			var rec MissingRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				for _, pat := range patterns {
					if matcher.Match(pat, rec.Path, false) {
						rec.FileID = binary.BigEndian.Uint64(k[1:9])
						dismissed = append(dismissed, rec)
						if e := cur.Del(0); e != nil {
							return e
						}
						break
					}
				}
			}
			k, v, err = cur.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})
	return dismissed, err
}

// ResolveByPattern removes unresolved records where the path matches any pattern.
func (s *Store) ResolveByPattern(patterns []string, matcher *Matcher) error {
	return s.env.Update(func(txn *lmdb.Txn) error {
		cur, err := txn.OpenCursor(s.dbi)
		if err != nil {
			return err
		}
		defer cur.Close()

		prefix := []byte{byte(prefixUnresolved)}
		k, v, err := cur.Get(prefix, nil, lmdb.SetRange)
		for err == nil {
			if len(k) == 0 || k[0] != byte(prefixUnresolved) {
				break
			}
			var rec UnresolvedRecord
			if e := json.Unmarshal(v, &rec); e == nil {
				for _, pat := range patterns {
					if matcher.Match(pat, rec.Path, false) {
						if e := cur.Del(0); e != nil {
							return e
						}
						break
					}
				}
			}
			k, v, err = cur.Get(nil, nil, lmdb.Next)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
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
