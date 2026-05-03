package ark

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// CRC: crc-DB.md | R2086, R2087, R2088, R2090
//
// CompactDB rewrites the LMDB environment at dbPath via mdb_env_copy2
// with MDB_CP_COMPACT, replacing the live data.mdb on success. Must
// run before the env is opened by microfts2/store. Failure is logged
// and ignored — startup continues with the uncompacted DB.
func CompactDB(dbPath string) error {
	compactDir := filepath.Join(dbPath, ".compact-tmp")
	if err := os.RemoveAll(compactDir); err != nil {
		return fmt.Errorf("clear compact dir: %w", err)
	}
	if err := os.MkdirAll(compactDir, 0755); err != nil {
		return fmt.Errorf("create compact dir: %w", err)
	}
	defer os.RemoveAll(compactDir)

	env, err := lmdb.NewEnv()
	if err != nil {
		return fmt.Errorf("new env: %w", err)
	}
	if err := env.SetMapSize(2 << 30); err != nil {
		env.Close()
		return fmt.Errorf("set map size: %w", err)
	}
	if err := env.SetMaxDBs(8); err != nil {
		env.Close()
		return fmt.Errorf("set max dbs: %w", err)
	}
	if err := env.Open(dbPath, lmdb.Readonly|lmdb.NoSubdir, 0644); err != nil {
		// Try with subdir form (default LMDB layout)
		env2, err2 := lmdb.NewEnv()
		if err2 != nil {
			env.Close()
			return fmt.Errorf("new env retry: %w", err2)
		}
		if err2 := env2.SetMapSize(2 << 30); err2 != nil {
			env.Close()
			env2.Close()
			return fmt.Errorf("set map size: %w", err2)
		}
		if err2 := env2.SetMaxDBs(8); err2 != nil {
			env.Close()
			env2.Close()
			return fmt.Errorf("set max dbs: %w", err2)
		}
		if err2 := env2.Open(dbPath, lmdb.Readonly, 0644); err2 != nil {
			env.Close()
			env2.Close()
			return fmt.Errorf("open env: %w (subdir retry: %w)", err, err2)
		}
		env.Close()
		env = env2
	}
	defer env.Close()

	if err := env.CopyFlag(compactDir, lmdb.CopyCompact); err != nil {
		return fmt.Errorf("copy compact: %w", err)
	}

	oldData := filepath.Join(dbPath, "data.mdb")
	newData := filepath.Join(compactDir, "data.mdb")
	oldSize, err := fileSize(oldData)
	if err != nil {
		return fmt.Errorf("stat live data.mdb: %w", err)
	}
	newSize, err := fileSize(newData)
	if err != nil {
		return fmt.Errorf("stat compact data.mdb: %w", err)
	}

	log.Printf("compacting ark: %s → %s", formatBytesInternal(oldSize), formatBytesInternal(newSize))

	// R2090: skip rename if size reduction under 5%
	if newSize >= oldSize-(oldSize/20) {
		log.Printf("compact: already compact (%.1f%% of original), skipping rename", float64(newSize)/float64(oldSize)*100)
		return nil
	}

	// Close env before swapping data.mdb
	env.Close()

	if err := os.Rename(newData, oldData); err != nil {
		return fmt.Errorf("rename compacted data.mdb: %w", err)
	}
	// lock.mdb is rebuilt on next open; remove the old one to avoid stale state
	_ = os.Remove(filepath.Join(dbPath, "lock.mdb"))
	return nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// formatBytesInternal mirrors cmd/ark/main.go's formatBytes for log
// output without depending on the cmd package.
func formatBytesInternal(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
