package ark

// CRC: crc-Updater.md | R3463, R3475, R3480, R3483
//
// ProvisionModel downloads the configured embedding model when the model file
// is absent, from the configured per-model URL, verifying against the pinned
// SHA-256. Reused by both setup and update. Model-neutral: the URL and hash
// come from [embedding] model_url / model_sha256, so whatever model the config
// names is fetched.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// modelDownloadTimeout bounds a single model download. Embedding GGUFs run to
// hundreds of MB, so this is generous.
const modelDownloadTimeout = 30 * time.Minute

// ErrModelKeysMissing reports that the configured model file is absent and no
// model_url / model_sha256 keys are set to fetch it. It is not fatal: callers
// print the guidance and continue, since search falls back to trigram-only.
// R3483
type ErrModelKeysMissing struct {
	Model string
}

func (e *ErrModelKeysMissing) Error() string {
	return fmt.Sprintf("embedding model %q is not present and [embedding] model_url / model_sha256 are unset;\n"+
		"  add both keys to ark.toml to have setup/update download it, or place the file at the model path manually", e.Model)
}

// ProvisionModel fetches cfg.Model into arkDir when it is absent. A present
// file or a link is left alone (R3463). With no URL/hash configured it returns
// *ErrModelKeysMissing (R3483). Otherwise it downloads and verifies (R3475).
// CRC: crc-Updater.md | Seq: seq-install.md#3.3.2 | R3463, R3475, R3480, R3483
func ProvisionModel(cfg EmbeddingConfig, arkDir string) error {
	if cfg.Model == "" {
		return nil // no model configured — nothing to provision
	}
	modelPath := filepath.Join(arkDir, cfg.Model)
	// "If needed" = the file is absent. A present file or a link (Lstat sees
	// either) is left alone.
	if _, err := os.Lstat(modelPath); err == nil {
		return nil
	}
	if cfg.ModelURL == "" || cfg.ModelSHA256 == "" {
		return &ErrModelKeysMissing{Model: cfg.Model}
	}
	return downloadFileVerified(cfg.ModelURL, modelPath, cfg.ModelSHA256)
}

// downloadFileVerified downloads url to dest, streaming through a SHA-256 hash,
// and renames it into place only when the hash matches want. A mismatch or a
// transport error leaves dest untouched (the partial file is removed). Reuses
// the GitHub/HTTP download shape of llamalibs.go's downloadAndExtract, adapted
// for a single verified file rather than an archive.
// CRC: crc-Updater.md | R3475, R3476
func downloadFileVerified(url, dest, want string) error {
	client := &http.Client{Timeout: modelDownloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, want)
	}
	return os.Rename(tmpName, dest)
}
