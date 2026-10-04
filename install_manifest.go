package ark

// CRC: crc-Updater.md | R3479
//
// The install manifest at ~/.ark/install-manifest.json records, per installed
// file, the hash the bundle wrote and the per-file collision policy — so a
// refresh can tell a user edit from a stale bundle copy. It reuses
// Frictionless's ManifestEntry shape ({hash, onCollision, group}), but lives in
// its own file rather than inside a settings blob (the location differs; the
// shape does not).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

// Collision policies for a manifest entry's OnCollision field.
const (
	CollisionOverwrite    = "overwrite" // always take the bundle copy
	CollisionSkip         = "skip"      // never overwrite once installed
	CollisionReport       = "report"    // leave a user-modified file, report it
	ManifestFileName      = "install-manifest.json"
	defaultCollisionGroup = "assets"
)

// ManifestEntry is the per-file record: the hash last written, the collision
// policy, and a grouping label. Shape shared with Frictionless's ManifestEntry.
type ManifestEntry struct {
	Hash        string `json:"hash"`
	OnCollision string `json:"onCollision"`
	Group       string `json:"group,omitempty"`
}

// InstallManifest is the whole ~/.ark/install-manifest.json document.
type InstallManifest struct {
	Version string                   `json:"version"`
	Files   map[string]ManifestEntry `json:"files"`
}

// LoadManifest reads the manifest at path. A missing file yields an empty
// manifest (not an error), so a first refresh starts from a clean slate.
// CRC: crc-Updater.md | R3479
func LoadManifest(path string) (*InstallManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &InstallManifest{Files: map[string]ManifestEntry{}}, nil
		}
		return nil, err
	}
	var m InstallManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Files == nil {
		m.Files = map[string]ManifestEntry{}
	}
	return &m, nil
}

// Save writes the manifest to path (pretty-printed JSON).
func (m *InstallManifest) Save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// HashContent returns the lowercase hex SHA-256 of content.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// ShouldWrite decides whether a refresh may write newContent to the file
// currently on disk (diskContent, diskExists). The rule (R3474): a brand-new
// file installs; a file whose on-disk bytes still match what the manifest last
// wrote is a safe overwrite; a file whose on-disk bytes already equal the new
// bundle content needs no write; anything else is a user edit and is preserved
// unless its policy is overwrite. Returns whether to write and, when not,
// whether the divergence is a preserved user modification worth reporting.
// CRC: crc-Updater.md | R3474, R3479
func (m *InstallManifest) ShouldWrite(relPath string, diskContent []byte, diskExists bool, newContent []byte) (write, userModified bool) {
	if !diskExists {
		return true, false
	}
	newHash := HashContent(newContent)
	diskHash := HashContent(diskContent)
	if diskHash == newHash {
		return false, false // already current — nothing to do
	}
	entry, tracked := m.Files[relPath]
	if tracked && entry.OnCollision == CollisionOverwrite {
		return true, false
	}
	// A manifest miss with matching content, or an on-disk hash equal to what
	// we last wrote, is a safe overwrite. Otherwise the user changed it.
	if tracked && entry.Hash == diskHash {
		return true, false
	}
	if !tracked {
		// Untracked and different from the bundle: treat as pre-existing user
		// content and preserve it.
		return false, true
	}
	return false, true
}

// Record writes back the entry for a file the refresh just installed. R3479
func (m *InstallManifest) Record(relPath string, content []byte) {
	entry := m.Files[relPath]
	entry.Hash = HashContent(content)
	if entry.OnCollision == "" {
		entry.OnCollision = CollisionReport
	}
	if entry.Group == "" {
		entry.Group = defaultCollisionGroup
	}
	m.Files[relPath] = entry
}
