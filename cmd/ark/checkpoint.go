package main

// CRC: crc-Checkpoint.md
// Fossil-backed app checkpoint machinery behind `ark ui checkpoint`.
// The 2026-07-23 incidents (walk-up into an enclosing checkout, working-tree
// clobber via `fossil update trunk`, swallowed fossil errors) are the reason
// for the shape of this file: every fossil step is checked, the checkout
// binding is verified at every step, and branch switches use
// `checkout --keep`, which never touches the working tree.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const checkpointBaselineMsg = "=== BASELINE ==="

// CRC: crc-Checkpoint.md | R287
func cmdUICheckpoint(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: ark ui checkpoint <cmd> <app> [msg]

Commands:
  save <app> [msg]       save a checkpoint
  list <app>             list checkpoints
  rollback <app> [n]     rollback to nth checkpoint (default: undo last)
  diff <app> [n]         diff against nth checkpoint (default: 1)
  clear <app>            reset to baseline
  baseline <app>         set current state as baseline
  count <app>            count checkpoints
  update <app> [msg]     save to updates branch
  local <app> [msg]      save to local branch`)
		os.Exit(1)
	}

	cmd := args[0]
	app := args[1]
	var msg string
	if len(args) > 2 {
		msg = args[2]
	}

	fossil := fossilBin()
	if fossil == "" {
		// Crank-handle: output a prompt for the agent to install fossil
		fmt.Printf(`Fossil is not installed. To set up checkpoints, run these commands:

mkdir -p ~/.claude/bin
# Download fossil for your platform:
#   Linux x86_64:
curl -sL "https://fossil-scm.org/home/uv/fossil-linux-x64-2.27.tar.gz" | tar -xzf - -C ~/.claude/bin fossil
#   macOS ARM:
# curl -sL "https://fossil-scm.org/home/uv/fossil-mac-arm-2.27.tar.gz" | tar -xzf - -C ~/.claude/bin fossil
#   macOS x86_64:
# curl -sL "https://fossil-scm.org/home/uv/fossil-mac-x64-2.27.tar.gz" | tar -xzf - -C ~/.claude/bin fossil
chmod +x ~/.claude/bin/fossil

Then re-run: ark ui checkpoint %s %s
`, cmd, app)
		os.Exit(1)
	}

	appDir := filepath.Join(arkDir, "apps", app)
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "App not found: %s\n", app)
		os.Exit(1)
	}

	repo := filepath.Join(appDir, "checkpoint.fossil")

	var err error
	switch cmd {
	case "save":
		err = checkpointSave(fossil, appDir, repo, msg)
	case "list":
		err = checkpointList(fossil, appDir, repo)
	case "rollback":
		err = checkpointRollback(fossil, appDir, repo, msg) // msg is used as "n"
	case "diff":
		err = checkpointDiff(fossil, appDir, repo, msg) // msg is used as "n"
	case "clear", "baseline":
		err = checkpointBaseline(fossil, appDir, repo)
	case "count":
		err = checkpointCount(fossil, appDir, repo)
	case "update":
		err = checkpointUpdate(fossil, appDir, repo, msg)
	case "local":
		err = checkpointLocal(fossil, appDir, repo, msg)
	default:
		fmt.Fprintf(os.Stderr, "unknown checkpoint command: %s\n", cmd)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkpoint %s failed: %v\n", cmd, err)
		os.Exit(1)
	}
}

// fossilBin returns the path to the fossil binary, or "" if not found.
func fossilBin() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	bin := filepath.Join(home, ".claude", "bin", "fossil")
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	return bin
}

// fossilRun executes a fossil command in the given directory, returning combined output.
func fossilRun(fossil, dir string, args ...string) (string, error) {
	cmd := exec.Command(fossil, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fossilStep runs one fossil command as a checked step: on failure the error
// names the command and carries fossil's own output.
// CRC: crc-Checkpoint.md | R3239
func fossilStep(fossil, dir string, args ...string) (string, error) {
	out, err := fossilRun(fossil, dir, args...)
	if err != nil {
		return out, fmt.Errorf("fossil %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return out, nil
}

// resolvePath returns the symlink-resolved, cleaned form of p for comparison.
// Fossil reports resolved paths; app dirs are routinely reached via symlinks.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

// verifyCheckout asks fossil which repository and checkout root it would act
// on from appDir and errors unless both match the expected app repo. This is
// the abort gate that prevents checkpoint verbs from resolving to an
// enclosing checkout (the 2026-07-23 walk-up incident).
// CRC: crc-Checkpoint.md | R3234
func verifyCheckout(fossil, appDir, repo string) error {
	out, err := fossilRun(fossil, appDir, "info")
	if err != nil {
		return fmt.Errorf("no open checkout in %s: %v\n%s", appDir, err, strings.TrimSpace(out))
	}
	var gotRepo, gotRoot string
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "repository:"); ok {
			gotRepo = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "local-root:"); ok {
			gotRoot = strings.TrimSpace(v)
		}
	}
	if gotRepo == "" || gotRoot == "" {
		return fmt.Errorf("no open checkout in %s (fossil info reports no repository/local-root)", appDir)
	}
	if resolvePath(gotRepo) != resolvePath(repo) || resolvePath(gotRoot) != resolvePath(appDir) {
		return fmt.Errorf("checkout binding mismatch in %s:\n  expected repository %s at root %s\n  fossil reports repository %s at root %s",
			appDir, repo, appDir, gotRepo, gotRoot)
	}
	return nil
}

// ensureCheckout establishes the invariant at verb entry: the app directory
// has its own checkout bound to the app's checkpoint.fossil. If no checkout
// is open here, it opens one safely (--nested: bind inside an enclosing
// checkout instead of failing into the walk-up; --keep: never extract over
// the working tree). A checkout bound to the wrong repository is never
// "fixed" silently — it aborts.
// CRC: crc-Checkpoint.md | R3234, R3236
func ensureCheckout(fossil, appDir, repo string) error {
	if _, err := os.Stat(filepath.Join(appDir, ".fslckout")); err != nil {
		if _, err := fossilStep(fossil, appDir, "open", repo, "--force", "--nested", "--keep"); err != nil {
			return err
		}
	}
	if err := verifyCheckout(fossil, appDir, repo); err != nil {
		return err
	}
	return applyCheckoutSettings(fossil, appDir)
}

// applyCheckoutSettings configures the checkout after every open — not only
// at repository creation, so pre-contract repositories pick the settings up
// on next use. mtime-changes off makes commits hash content instead of
// trusting the stat cache, which misses a same-size edit within the same
// mtime second — the normal cadence of an agent editing and checkpointing
// immediately.
// CRC: crc-Checkpoint.md | R3241
func applyCheckoutSettings(fossil, appDir string) error {
	if _, err := fossilStep(fossil, appDir, "settings", "mtime-changes", "off"); err != nil {
		return err
	}
	_, err := fossilStep(fossil, appDir, "settings", "ignore-glob", ".#*,.*~,*~,checkpoint.fossil,.fslckout")
	return err
}

// fossilInit initializes a new checkpoint repo for an app and opens it.
// CRC: crc-Checkpoint.md | Seq: seq-checkpoint.md#3 | R3236
func fossilInit(fossil, appDir, repo string) error {
	if _, err := fossilStep(fossil, appDir, "init", repo, "--project-name", filepath.Base(appDir)); err != nil {
		return err
	}
	if _, err := fossilStep(fossil, appDir, "open", repo, "--force", "--nested", "--keep"); err != nil {
		return err
	}
	// R3235: binding verified after every open
	if err := verifyCheckout(fossil, appDir, repo); err != nil {
		return err
	}
	if err := applyCheckoutSettings(fossil, appDir); err != nil {
		return err
	}
	_, err := fossilStep(fossil, appDir, "add", ".")
	return err
}

// CRC: crc-Checkpoint.md | R3239
func checkpointSave(fossil, appDir, repo, msg string) error {
	if msg == "" {
		msg = "checkpoint"
	}

	if _, err := os.Stat(repo); os.IsNotExist(err) {
		if err := fossilInit(fossil, appDir, repo); err != nil {
			return err
		}
		if msg == "checkpoint" {
			msg = "initial state"
		}
		if _, err := fossilStep(fossil, appDir, "commit", "-m", msg, "--no-warnings"); err != nil {
			return err
		}
		fmt.Printf("Created checkpoint: %s\n", msg)
	} else {
		if err := ensureCheckout(fossil, appDir, repo); err != nil {
			return err
		}
		if _, err := fossilStep(fossil, appDir, "addremove"); err != nil {
			return err
		}
		changes, err := fossilStep(fossil, appDir, "changes", "--quiet")
		if err != nil {
			return err
		}
		if strings.TrimSpace(changes) == "" {
			fmt.Println("No changes to checkpoint")
			return nil
		}
		if _, err := fossilStep(fossil, appDir, "commit", "-m", msg, "--no-warnings"); err != nil {
			return err
		}
		fmt.Printf("Saved checkpoint: %s\n", msg)
	}
	// Notify UI of checkpoint change
	notifyCheckpointChange()
	return nil
}

// CRC: crc-Checkpoint.md | R3239
func checkpointList(fossil, appDir, repo string) error {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fmt.Printf("No checkpoints for %s\n", filepath.Base(appDir))
		return nil
	}
	if err := ensureCheckout(fossil, appDir, repo); err != nil {
		return err
	}
	out, err := fossilStep(fossil, appDir, "timeline", "-t", "ci", "--oneline")
	if err != nil {
		return err
	}
	// Remove last 3 lines (fossil footer)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 3 {
		lines = lines[:len(lines)-3]
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

// CRC: crc-Checkpoint.md | R3239
func checkpointRollback(fossil, appDir, repo, n string) error {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		return fmt.Errorf("no checkpoints for %s", filepath.Base(appDir))
	}
	if err := ensureCheckout(fossil, appDir, repo); err != nil {
		return err
	}
	if n == "" {
		out, err := fossilStep(fossil, appDir, "undo")
		if err != nil {
			return err
		}
		fmt.Print(out)
	} else {
		out, err := fossilStep(fossil, appDir, "timeline", "-n", "100", "-t", "ci", "--oneline")
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		// Filter out footer/baseline lines
		var commits []string
		for _, l := range lines {
			if strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") || strings.Contains(l, checkpointBaselineMsg) {
				continue
			}
			commits = append(commits, l)
		}
		idx, err := strconv.Atoi(n)
		if err != nil || idx < 1 || idx > len(commits) {
			return fmt.Errorf("checkpoint %s not found", n)
		}
		hash := strings.Fields(commits[idx-1])[0]
		// R3235: the forced checkout is destructive by design (it restores
		// the tree) — re-verify the binding immediately before it
		if err := verifyCheckout(fossil, appDir, repo); err != nil {
			return err
		}
		if _, err := fossilStep(fossil, appDir, "checkout", hash, "--force"); err != nil {
			return err
		}
	}
	fmt.Println("Rolled back to checkpoint")
	return nil
}

// CRC: crc-Checkpoint.md | R3239
func checkpointDiff(fossil, appDir, repo, n string) error {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		return fmt.Errorf("no checkpoints for %s", filepath.Base(appDir))
	}
	if err := ensureCheckout(fossil, appDir, repo); err != nil {
		return err
	}
	if n == "" {
		n = "1"
	}
	out, err := fossilStep(fossil, appDir, "timeline", "-t", "ci", "--oneline")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Remove last 3 lines (footer)
	if len(lines) > 3 {
		lines = lines[:len(lines)-3]
	}
	idx, err := strconv.Atoi(n)
	if err != nil || idx < 1 || idx > len(lines) {
		return fmt.Errorf("checkpoint %s not found", n)
	}
	hash := strings.Fields(lines[idx-1])[0]
	// diff exits nonzero when differences exist — run unchecked
	out, _ = fossilRun(fossil, appDir, "diff", "--from", hash)
	fmt.Print(out)
	return nil
}

// CRC: crc-Checkpoint.md | Seq: seq-checkpoint.md#2 | R3240
func checkpointBaseline(fossil, appDir, repo string) error {
	app := filepath.Base(appDir)
	var bundles map[string]string

	if _, err := os.Stat(repo); err == nil {
		if err := ensureCheckout(fossil, appDir, repo); err != nil {
			return err
		}
		// Export side branches, each to its own bundle file — a shared
		// path would let the second export overwrite the first. Bundles
		// live outside appDir so the re-init's `add .` can't commit them.
		branchList, err := fossilStep(fossil, appDir, "branch", "list")
		if err != nil {
			return err
		}
		bundleDir, err := os.MkdirTemp("", "ark-checkpoint-bundles-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(bundleDir)
		bundles = make(map[string]string)
		for _, branch := range []string{"updates", "local"} {
			if !strings.Contains(branchList, branch) {
				continue
			}
			bundle := filepath.Join(bundleDir, branch+".bundle")
			if _, err := fossilStep(fossil, appDir, "bundle", "export", bundle, "--branch", branch, "--standalone"); err != nil {
				return err
			}
			bundles[branch] = bundle
		}
		// R3235: binding re-verified immediately before destruction
		if err := verifyCheckout(fossil, appDir, repo); err != nil {
			return err
		}
		if _, err := fossilStep(fossil, appDir, "close", "--force"); err != nil {
			return err
		}
		if err := os.Remove(repo); err != nil {
			return fmt.Errorf("removing %s: %v", repo, err)
		}
		if err := os.Remove(filepath.Join(appDir, ".fslckout")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing checkout db: %v", err)
		}
	}

	// Create fresh repo with current state as baseline. fossilInit verifies
	// the new binding after its open (R3235), before the first commit.
	if err := fossilInit(fossil, appDir, repo); err != nil {
		return err
	}
	if _, err := fossilStep(fossil, appDir, "commit", "-m", checkpointBaselineMsg, "--no-warnings"); err != nil {
		return err
	}

	// Restore preserved branches
	for branch, bundle := range bundles {
		if _, err := fossilStep(fossil, appDir, "bundle", "import", bundle, "--force", "--publish"); err != nil {
			return fmt.Errorf("restoring branch %s: %w", branch, err)
		}
	}
	if len(bundles) > 0 {
		fmt.Printf("Baseline set for %s (preserved branches restored)\n", app)
	} else {
		fmt.Printf("Baseline set for %s\n", app)
	}
	notifyCheckpointChange()
	return nil
}

// CRC: crc-Checkpoint.md | R3239
func checkpointCount(fossil, appDir, repo string) error {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		fmt.Println("0")
		return nil
	}
	if err := ensureCheckout(fossil, appDir, repo); err != nil {
		return err
	}
	count, err := trunkCheckpointCount(fossil, appDir)
	if err != nil {
		return err
	}
	fmt.Println(count)
	return nil
}

// trunkCheckpointCount counts trunk commits that are real checkpoints
// (excluding the baseline and fossil's own scaffolding lines).
func trunkCheckpointCount(fossil, appDir string) (int, error) {
	out, err := fossilStep(fossil, appDir, "timeline", "-t", "ci", "--oneline", "-b", "trunk")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.Contains(line, checkpointBaselineMsg) ||
			strings.Contains(line, "initial empty check-in") ||
			strings.HasPrefix(line, "+++ ") {
			continue
		}
		count++
	}
	return count, nil
}

// CRC: crc-Checkpoint.md | Seq: seq-checkpoint.md#1 | R3237
func checkpointUpdate(fossil, appDir, repo, msg string) error {
	if msg == "" {
		msg = "update"
	}
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		if err := firstBranchSave(fossil, appDir, repo, "updates", msg); err != nil {
			return err
		}
	} else {
		if err := ensureCheckout(fossil, appDir, repo); err != nil {
			return err
		}
		// Refuse if unconsolidated trunk checkpoints exist
		count, err := trunkCheckpointCount(fossil, appDir)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("%d checkpoint(s) exist. Consolidate before updating", count)
		}
		if err := branchSave(fossil, appDir, "updates", msg); err != nil {
			return err
		}
	}
	fmt.Printf("Update checkpoint: %s\n", msg)
	return nil
}

// CRC: crc-Checkpoint.md | Seq: seq-checkpoint.md#1 | R3237
func checkpointLocal(fossil, appDir, repo, msg string) error {
	if msg == "" {
		msg = "local"
	}
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		if err := firstBranchSave(fossil, appDir, repo, "local", msg); err != nil {
			return err
		}
	} else {
		if err := ensureCheckout(fossil, appDir, repo); err != nil {
			return err
		}
		if err := branchSave(fossil, appDir, "local", msg); err != nil {
			return err
		}
	}
	fmt.Printf("Local checkpoint: %s\n", msg)
	return nil
}

// firstBranchSave handles a branch save when no repo exists yet: initialize,
// commit the disk state as the trunk baseline, then seed the branch with an
// empty commit. The checkout ends on trunk; since the branch tip and trunk
// hold identical content, no working-tree risk exists on this path.
func firstBranchSave(fossil, appDir, repo, branch, msg string) error {
	if err := fossilInit(fossil, appDir, repo); err != nil {
		return err
	}
	if _, err := fossilStep(fossil, appDir, "commit", "-m", checkpointBaselineMsg, "--no-warnings"); err != nil {
		return err
	}
	if _, err := fossilStep(fossil, appDir, "commit", "--branch", branch, "-m", msg, "--allow-empty", "--no-warnings"); err != nil {
		return err
	}
	_, err := fossilStep(fossil, appDir, "checkout", "trunk", "--keep")
	return err
}

// branchSave commits the current disk state onto a side branch without ever
// modifying the working tree: `checkout --keep` retargets the checkout and
// leaves every file alone (the retired `fossil update` dance extracted trunk
// content over the tree — the 2026-07-23 clobber).
// CRC: crc-Checkpoint.md | Seq: seq-checkpoint.md#1 | R3237, R3238
func branchSave(fossil, appDir, branch, msg string) error {
	out, err := fossilRun(fossil, appDir, "checkout", branch, "--keep")
	if err != nil {
		if !strings.Contains(out, "cannot resolve name") {
			return fmt.Errorf("fossil checkout %s --keep: %v\n%s", branch, err, strings.TrimSpace(out))
		}
		// Branch doesn't exist yet — create it from the current commit
		if _, err := fossilStep(fossil, appDir, "addremove"); err != nil {
			return err
		}
		if _, err := fossilStep(fossil, appDir, "commit", "--branch", branch, "-m", msg, "--allow-empty", "--no-warnings"); err != nil {
			return err
		}
		return returnToTrunk(fossil, appDir, nil)
	}
	// On the branch (files untouched); commit the disk snapshot
	if _, err := fossilStep(fossil, appDir, "addremove"); err != nil {
		return returnToTrunk(fossil, appDir, err)
	}
	out, err = fossilRun(fossil, appDir, "commit", "-m", msg, "--allow-empty", "--no-warnings")
	if err != nil && strings.Contains(out, "closed leaf") {
		// The branch leaf is closed (the first-incident skeleton). Reopen and
		// retry once. --raw is load-bearing: the non-raw form cancels a
		// sym-closed tag and leaves the leaf closed. (R3238)
		if _, err := fossilStep(fossil, appDir, "tag", "cancel", "--raw", "closed", branch); err != nil {
			return returnToTrunk(fossil, appDir, err)
		}
		out, err = fossilRun(fossil, appDir, "commit", "-m", msg, "--allow-empty", "--no-warnings")
	}
	if err != nil {
		return returnToTrunk(fossil, appDir, fmt.Errorf("fossil commit on %s: %v\n%s", branch, err, strings.TrimSpace(out)))
	}
	return returnToTrunk(fossil, appDir, nil)
}

// returnToTrunk retargets the checkout back to trunk without touching files.
// It composes with an in-flight error: the original failure stays primary,
// and a failure to return is reported alongside it.
func returnToTrunk(fossil, appDir string, prior error) error {
	if _, err := fossilStep(fossil, appDir, "checkout", "trunk", "--keep"); err != nil {
		if prior != nil {
			return fmt.Errorf("%v\nadditionally, returning to trunk failed: %v", prior, err)
		}
		return err
	}
	return prior
}

// notifyCheckpointChange tells the UI to refresh checkpoint state.
func notifyCheckpointChange() {
	// Best-effort via unix socket — ignore errors
	client := serverClient(arkDir)
	if client == nil {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"code": "if appConsole then appConsole._checkpointsTime = 0 end",
	})
	req, err := http.NewRequest("POST", "http://ark/api/ui_run", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
