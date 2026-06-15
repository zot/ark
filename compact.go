package ark

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"
)

// CRC: crc-DB.md | R2086, R2087, R2088, R2090, R2981
//
// CompactDB rewrites microfts2's bbolt database at IndexPath(dbPath) using
// bolt.Compact, which copies every key/value into a fresh file and so drops
// the free pages bbolt does not reclaim in place. The compacted copy replaces
// the live file on success. Must run before the file is opened by
// microfts2/store. Failure is logged and ignored — startup continues with the
// uncompacted DB. (R2981)
func CompactDB(dbPath string) error {
	live := IndexPath(dbPath)
	if _, err := os.Stat(live); err != nil {
		return nil // nothing to compact yet (fresh install)
	}

	compactDir := filepath.Join(dbPath, ".compact-tmp")
	if err := os.RemoveAll(compactDir); err != nil {
		return fmt.Errorf("clear compact dir: %w", err)
	}
	if err := os.MkdirAll(compactDir, 0755); err != nil {
		return fmt.Errorf("create compact dir: %w", err)
	}
	defer os.RemoveAll(compactDir)

	compacted := filepath.Join(compactDir, IndexFileName)

	src, err := bbolt.Open(live, 0644, &bbolt.Options{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("open live db: %w", err)
	}
	dst, err := bbolt.Open(compacted, 0644, nil)
	if err != nil {
		src.Close()
		return fmt.Errorf("open compact db: %w", err)
	}
	if err := bbolt.Compact(dst, src, 0); err != nil {
		dst.Close()
		src.Close()
		return fmt.Errorf("compact: %w", err)
	}
	dst.Close()
	src.Close()

	oldSize, err := fileSize(live)
	if err != nil {
		return fmt.Errorf("stat live db: %w", err)
	}
	newSize, err := fileSize(compacted)
	if err != nil {
		return fmt.Errorf("stat compacted db: %w", err)
	}

	log.Printf("compacting ark: %s → %s", formatBytesInternal(oldSize), formatBytesInternal(newSize))

	// R2090: skip rename if size reduction under 5%
	if newSize >= oldSize-(oldSize/20) {
		log.Printf("compact: already compact (%.1f%% of original), skipping rename", float64(newSize)/float64(oldSize)*100)
		return nil
	}

	// bbolt is a single file with no lock sidecar; both files live under
	// dbPath, so the rename is atomic on the same filesystem.
	if err := os.Rename(compacted, live); err != nil {
		return fmt.Errorf("rename compacted db: %w", err)
	}
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
