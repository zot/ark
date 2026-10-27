package ark

// CRC: crc-DB.md | Test: test-ConfigErrors.md

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// configParseRecord returns the decoded `config_parse` record, or nil.
func configParseRecord(t *testing.T, db *DB) *configParseCondition {
	t.Helper()
	recs, err := db.store.ReadERecords()
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := recs[ECondConfigParse]
	if !ok {
		return nil
	}
	var cond configParseCondition
	if err := json.Unmarshal(raw, &cond); err != nil {
		t.Fatalf("record %s does not decode: %v", raw, err)
	}
	return &cond
}

// R3651, R3652: a failed reread records config_parse and keeps the old config;
// a good reread deletes it, and so does the next open.
func TestConfigParseCondition(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, InitOpts{ConfigSeed: []byte("dotfiles = true\n")}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	toml := filepath.Join(dir, "ark.toml")
	good, err := os.ReadFile(toml)
	if err != nil {
		t.Fatal(err)
	}
	breakFile := func() {
		if err := os.WriteFile(toml, []byte("dotfiles = = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixFile := func() {
		if err := os.WriteFile(toml, good, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	before := db.Config()
	breakFile()
	if err := db.ReloadConfig(); err == nil {
		t.Fatal("reload of a broken ark.toml succeeded")
	}
	cond := configParseRecord(t, db)
	if cond == nil {
		t.Fatal("no config_parse record; the failure would live only in the log")
	}
	if cond.Error == "" || cond.Time == "" || cond.Server == "" {
		t.Errorf("record lacks a field: %+v", cond)
	}
	if db.Config() != before || !db.Config().Dotfiles {
		t.Error("a failed reread replaced the running config")
	}

	fixFile()
	if err := db.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if configParseRecord(t, db) != nil {
		t.Error("config_parse survived a good reread")
	}

	breakFile()
	db.ReloadConfig()
	if configParseRecord(t, db) == nil {
		t.Fatal("no config_parse record before reopening")
	}
	db.Close()
	fixFile()
	if db, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	if configParseRecord(t, db) != nil {
		t.Error("config_parse survived an open that loaded ark.toml")
	}
}
