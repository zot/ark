package ark

// CRC: crc-Indexer.md

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"microfts2"

	"github.com/anthropics/microvec"
)

var tagRegex = regexp.MustCompile(`(?:^|\n)@([a-zA-Z][\w.-]*):`)

// Indexer coordinates adding, removing, and refreshing files across
// both microfts2 and microvec. Extracts tags from file content.
type Indexer struct {
	fts   *microfts2.DB
	vec   *microvec.DB
	store *Store
}

// AddFile adds a file to both engines and extracts tags. microfts2
// first (gets fileid and chunk offsets), then reads chunks and adds
// to microvec, then extracts and stores tags.
func (idx *Indexer) AddFile(path, strategy string) (uint64, error) {
	fileid, content, err := idx.fts.AddFileWithContent(path, strategy)
	if err != nil {
		return 0, fmt.Errorf("fts add %s: %w", path, err)
	}

	data, chunks, err := splitChunks(content, fileid, idx.fts)
	if err != nil {
		return fileid, fmt.Errorf("read chunks %s: %w", path, err)
	}

	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fileid, fmt.Errorf("vec add %s: %w", path, err)
	}

	if idx.store != nil {
		tags := ExtractTags(data)
		if err := idx.store.UpdateTags(fileid, tags); err != nil {
			return fileid, fmt.Errorf("update tags %s: %w", path, err)
		}
		defs := ExtractTagDefs(data)
		if err := idx.store.UpdateTagDefs(fileid, defs); err != nil {
			return fileid, fmt.Errorf("update tag defs %s: %w", path, err)
		}
	}

	return fileid, nil
}

// RemoveFile removes a file from both engines and tags by path.
func (idx *Indexer) RemoveFile(path string) error {
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}
	fileid := status.FileID

	if err := idx.fts.RemoveFile(path); err != nil {
		return fmt.Errorf("fts remove %s: %w", path, err)
	}
	// Vec removal is best-effort: file may never have been vectorized
	idx.vec.RemoveFile(fileid)
	if idx.store != nil {
		if err := idx.store.RemoveTags(fileid); err != nil {
			return fmt.Errorf("remove tags %s: %w", path, err)
		}
		idx.store.RemoveTagDefs(fileid)
	}
	return nil
}

// RemoveByID removes a file from both engines and tags by fileid.
func (idx *Indexer) RemoveByID(fileid uint64) error {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return fmt.Errorf("file info %d: %w", fileid, err)
	}
	if err := idx.fts.RemoveFile(info.Filename); err != nil {
		return fmt.Errorf("fts remove %d: %w", fileid, err)
	}
	// Vec removal is best-effort: file may never have been vectorized
	idx.vec.RemoveFile(fileid)
	if idx.store != nil {
		if err := idx.store.RemoveTags(fileid); err != nil {
			return fmt.Errorf("remove tags %d: %w", fileid, err)
		}
		idx.store.RemoveTagDefs(fileid)
	}
	return nil
}

// RefreshFile re-indexes a single file. Tries append detection first;
// falls back to full reindex if the change isn't append-only.
func (idx *Indexer) RefreshFile(path, strategy string) error {
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}

	// Try append-only path
	if ok, aErr := idx.DetectAppend(path, status.FileID); aErr == nil && ok {
		if aErr := idx.AppendFile(path, status.FileID, strategy); aErr == nil {
			return nil
		}
		// Append failed — fall through to full reindex
	}

	return idx.fullRefresh(path, strategy, status.FileID)
}

// fullRefresh does a complete reindex: microfts2 first, then vectors and tags.
func (idx *Indexer) fullRefresh(path, strategy string, oldID uint64) error {
	fileid, content, err := idx.fts.ReindexWithContent(path, strategy)
	if err != nil {
		return fmt.Errorf("fts reindex %s: %w", path, err)
	}

	idx.vec.RemoveFile(oldID)

	data, chunks, err := splitChunks(content, fileid, idx.fts)
	if err != nil {
		return fmt.Errorf("read chunks %s: %w", path, err)
	}
	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fmt.Errorf("vec add %s: %w", path, err)
	}

	if idx.store != nil {
		if fileid != oldID {
			idx.store.RemoveTags(oldID)
			idx.store.RemoveTagDefs(oldID)
		}
		tags := ExtractTags(data)
		if err := idx.store.UpdateTags(fileid, tags); err != nil {
			return fmt.Errorf("update tags %s: %w", path, err)
		}
		defs := ExtractTagDefs(data)
		if err := idx.store.UpdateTagDefs(fileid, defs); err != nil {
			return fmt.Errorf("update tag defs %s: %w", path, err)
		}
	}

	return nil
}

// DetectAppend checks whether a file change is append-only by hashing
// the first FileLength bytes and comparing to the stored ContentHash.
// Returns true if the prefix is unchanged and the file grew.
func (idx *Indexer) DetectAppend(path string, fileid uint64) (bool, error) {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return false, err
	}
	if info.FileLength <= 0 || info.ContentHash == "" {
		return false, nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if fi.Size() <= info.FileLength {
		return false, nil // didn't grow
	}

	// Hash the first FileLength bytes
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.CopyN(h, f, info.FileLength); err != nil {
		return false, err
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))

	return hash == info.ContentHash, nil
}

// AppendFile indexes only the new content appended to a file.
// FTS uses AppendChunks; vectors get a full refresh; tags are incremental.
func (idx *Indexer) AppendFile(path string, fileid uint64, strategy string) error {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return fmt.Errorf("file info %d: %w", fileid, err)
	}

	// Read only the new bytes
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	newBytes := data[info.FileLength:]

	// Parse last chunk range for base line
	baseLine := 0
	if n := len(info.ChunkRanges); n > 0 {
		_, endLine := parseRange(info.ChunkRanges[n-1])
		baseLine = endLine
	}

	// Compute new file metadata
	fullHash := sha256.Sum256(data)
	fi, _ := os.Stat(path)

	// Append to FTS index
	err = idx.fts.AppendChunks(fileid, newBytes, strategy,
		microfts2.WithBaseLine(baseLine),
		microfts2.WithContentHash(fmt.Sprintf("%x", fullHash)),
		microfts2.WithModTime(fi.ModTime().UnixNano()),
		microfts2.WithFileLength(fi.Size()),
	)
	if err != nil {
		return fmt.Errorf("fts append %s: %w", path, err)
	}

	// Vectors: full refresh (remove old, re-add all chunks)
	idx.vec.RemoveFile(fileid)
	_, allChunks, err := splitChunks(data, fileid, idx.fts)
	if err != nil {
		return fmt.Errorf("read chunks %s: %w", path, err)
	}
	if err := idx.vec.AddFile(fileid, allChunks); err != nil {
		return fmt.Errorf("vec add %s: %w", path, err)
	}

	// Tags and defs: incremental — only scan new content
	if idx.store != nil {
		tags := ExtractTags(newBytes)
		if err := idx.store.AppendTags(fileid, tags); err != nil {
			return fmt.Errorf("append tags %s: %w", path, err)
		}
		defs := ExtractTagDefs(newBytes)
		if err := idx.store.AppendTagDefs(fileid, defs); err != nil {
			return fmt.Errorf("append tag defs %s: %w", path, err)
		}
	}

	return nil
}

// RefreshStale re-indexes all stale files, optionally filtered by patterns.
// Returns the list of missing files found during the check.
func (idx *Indexer) RefreshStale(patterns []string, matcher *Matcher) ([]microfts2.FileStatus, error) {
	statuses, err := idx.fts.StaleFiles()
	if err != nil {
		return nil, fmt.Errorf("stale files: %w", err)
	}

	var missing []microfts2.FileStatus
	for _, s := range statuses {
		if s.Status == "missing" {
			missing = append(missing, s)
			continue
		}
		if s.Status != "stale" {
			continue
		}
		// If patterns given, filter
		if len(patterns) > 0 {
			matched := false
			for _, p := range patterns {
				if matcher.Match(p, s.Path, false) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if err := idx.RefreshFile(s.Path, s.Strategy); err != nil {
			return missing, fmt.Errorf("refresh %s: %w", s.Path, err)
		}
	}
	return missing, nil
}

// ExtractTags scans content for @tag: patterns and returns tag counts.
// Tag names are stored lowercase. The colon is required (disambiguates
// from emails and mentions).
func ExtractTags(content []byte) map[string]uint32 {
	matches := tagRegex.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	tags := make(map[string]uint32)
	for _, m := range matches {
		name := strings.ToLower(string(m[1]))
		tags[name]++
	}
	return tags
}

// tagDefRegex matches @tag: definitions at line start. First word after
// "@tag:" is the tag name, rest is description.
var tagDefRegex = regexp.MustCompile(`(?:^|\n)@tag:\s+(\S+)\s+(.+)`)

// ExtractTagDefs scans content for @tag: name description lines.
// Returns map of tagname → description.
func ExtractTagDefs(content []byte) map[string]string {
	matches := tagDefRegex.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	defs := make(map[string]string)
	for _, m := range matches {
		name := strings.ToLower(string(m[1]))
		desc := strings.TrimSpace(string(m[2]))
		defs[name] = desc
	}
	return defs
}

// splitChunks reads chunk text from a file using ranges from microfts2.
// Returns the raw file data alongside sliced chunks (avoids bytes.Join
// when callers need the full content for tag extraction).
func splitChunks(data []byte, fileid uint64, fts *microfts2.DB) ([]byte, [][]byte, error) {
	info, err := fts.FileInfoByID(fileid)
	if err != nil {
		return nil, nil, err
	}

	lines := strings.Split(string(data), "\n")
	chunks := make([][]byte, len(info.ChunkRanges))
	for i, r := range info.ChunkRanges {
		chunks[i] = []byte(extractByRange(lines, r))
	}
	return data, chunks, nil
}
