package main

// CRC: crc-Uninstaller.md | Seq: seq-install.md#8 | R3690, R3691, R3692, R3693, R3694, R3695, R3696, R3697, R3699
//
// `ark uninstall`: remove the program, keep the data. A plan is built first,
// so what the user is shown and asked about is exactly what then runs, and so
// the plan is tested without deleting anything.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

const (
	arkConfigFile  = "ark.toml"
	arkStorageDir  = "storage"
	appLinkRootDir = "lua"
	appSourceDir   = "apps"
)

// serverRuntimeDirs are the directories a running server writes its logs to
// (ark's own and the Lua runtime's). With the pid file they are the program's
// runtime files, removed by uninstall. R3699
var serverRuntimeDirs = []string{"logs", "log"}

// UninstallPlan is what uninstall removes and what it keeps, as paths.
type UninstallPlan struct {
	Files       []string // installed files whose content is still the install's
	Links       []string // setup's directory links
	LibDir      string   // the provisioned llama.cpp libraries, when inside ~/.ark
	Model       string   // the downloaded model, when a regular file inside ~/.ark
	Bookkeeping []string // the install manifest and staged-update leftovers
	Runtime     []string // the server's logs and pid file
	Exe         string
	Kept        []string // what stays: the user's data and edited files
}

// Uninstaller holds the install's places and the effects Run needs.
// CRC: crc-Uninstaller.md
type Uninstaller struct {
	ArkDir     string
	ClaudeDir  string
	Out        io.Writer
	Stop       func() error
	Unregister func(arkDir string) error
	RemoveExe  func(exe string) error
}

// Plan lists what uninstall will remove and keep. R3692–R3697
func (u *Uninstaller) Plan() (*UninstallPlan, error) {
	p := &UninstallPlan{}
	if err := u.planFiles(p); err != nil {
		return nil, err
	}
	p.Links = u.planLinks()
	u.planDownloads(p)
	_, staged, aside := stagedExePaths(u.ArkDir)
	for _, f := range []string{filepath.Join(u.ArkDir, ark.ManifestFileName), staged, aside, filepath.Join(u.ArkDir, ark.UpdateRequestFile)} {
		if exists(f) {
			p.Bookkeeping = append(p.Bookkeeping, f)
		}
	}
	// R3699: the server's runtime files.
	for _, f := range append(slices.Clone(serverRuntimeDirs), ark.PidFileName) {
		if path := filepath.Join(u.ArkDir, f); exists(path) {
			p.Runtime = append(p.Runtime, path)
		}
	}
	if exe := filepath.Join(u.ArkDir, arkExeName()); exists(exe) {
		p.Exe = exe
	}
	// R3697: the user's data stays; edited files were added by planFiles.
	for _, name := range []string{ark.IndexFileName, arkConfigFile, arkStorageDir} {
		if f := filepath.Join(u.ArkDir, name); exists(f) {
			p.Kept = append(p.Kept, f)
		}
	}
	sort.Strings(p.Kept)
	return p, nil
}

// planFiles sorts each manifest file into Files (content still what the
// install wrote) or Kept (edited). A file reached through a directory link is
// skipped: its real path is listed under its own name. R3692
func (u *Uninstaller) planFiles(p *UninstallPlan) error {
	m, err := ark.LoadManifest(filepath.Join(u.ArkDir, ark.ManifestFileName))
	if err != nil {
		return err
	}
	rels := make([]string, 0, len(m.Files))
	for rel := range m.Files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		path := filepath.Join(u.ArkDir, filepath.FromSlash(rel))
		if ancestorIsLink(u.ArkDir, path) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue // already gone
		}
		if m.Unchanged(rel, data) {
			p.Files = append(p.Files, path)
		} else {
			p.Kept = append(p.Kept, path)
		}
	}
	return nil
}

// planLinks lists setup's links that are still ours: each installLinkSet link
// under ~/.claude, and each ~/.ark/lua/<app> activation of ~/.ark/apps/<app>.
// A link pointing elsewhere is someone else's and stays. R3693
func (u *Uninstaller) planLinks() []string {
	var links []string
	for _, l := range installLinkSet(u.ArkDir) {
		if link := filepath.Join(u.ClaudeDir, filepath.FromSlash(l.Rel)); ark.IsOurLink(link, l.Target) {
			links = append(links, link)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(u.ArkDir, appLinkRootDir))
	for _, e := range entries {
		link := filepath.Join(u.ArkDir, appLinkRootDir, e.Name())
		if ark.IsOurLink(link, filepath.Join(u.ArkDir, appSourceDir, e.Name())) {
			links = append(links, link)
		}
	}
	return links
}

// planDownloads adds the provisioned libraries when they live inside ~/.ark
// and the model when it is a regular file there; a linked model, or a lib_dir
// the user pointed elsewhere, stays. R3694
func (u *Uninstaller) planDownloads(p *UninstallPlan) {
	cfg, err := ark.LoadConfig(filepath.Join(u.ArkDir, arkConfigFile))
	if err != nil {
		return
	}
	if lib := ark.NewLlamaLibs(cfg.Embedding, u.ArkDir).LibDir(); inside(u.ArkDir, lib) && exists(lib) {
		p.LibDir = lib
	}
	if cfg.Embedding.Model == "" {
		return
	}
	model := filepath.Join(u.ArkDir, cfg.Embedding.Model)
	if fi, err := os.Lstat(model); err == nil {
		if fi.Mode().IsRegular() {
			p.Model = model
		} else {
			p.Kept = append(p.Kept, model)
		}
	}
}

// Run carries out a plan: stop the server, then links, files and the
// directories they leave empty, downloads, bookkeeping and registration, and
// the exe last. One entry failing is reported and the rest proceed, so a
// locked file does not strand a half-removed install without a word.
// R3691–R3697
func (u *Uninstaller) Run(p *UninstallPlan) error {
	if err := u.Stop(); err != nil {
		return err // R3691: a live server holds files uninstall must remove
	}
	var errs []error
	note := func(err error) {
		if err != nil {
			fmt.Fprintf(u.out(), "could not remove: %v\n", err)
			errs = append(errs, err)
		}
	}
	for _, l := range p.Links {
		note(ark.RemoveDirLink(l)) // R3693: links only, never a real directory
	}
	for _, f := range p.Files {
		note(removeIfPresent(f))
	}
	u.removeEmptyDirs() // R3692
	if p.LibDir != "" {
		note(os.RemoveAll(p.LibDir))
	}
	if p.Model != "" {
		note(removeIfPresent(p.Model))
	}
	for _, f := range p.Bookkeeping {
		note(removeIfPresent(f))
	}
	for _, f := range p.Runtime {
		note(os.RemoveAll(f)) // R3699
	}
	note(u.Unregister(u.ArkDir)) // R3695
	if p.Exe != "" {
		note(u.RemoveExe(p.Exe)) // R3696
	}
	u.report(p)
	return errors.Join(errs...)
}

// removeEmptyDirs removes, deepest first, every directory under the
// refreshable roots that the removals left empty. A link is never descended
// into, and os.Remove refuses a directory that still holds anything. R3692
func (u *Uninstaller) removeEmptyDirs() {
	for _, root := range refreshableAssetRoots {
		var dirs []string
		_ = filepath.WalkDir(filepath.Join(u.ArkDir, root), func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				dirs = append(dirs, path)
			}
			return nil
		})
		sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
		for _, d := range dirs {
			_ = os.Remove(d)
		}
	}
}

// report says what stayed and how to remove the rest. R3697
func (u *Uninstaller) report(p *UninstallPlan) {
	fmt.Fprintln(u.out(), "ark is uninstalled.")
	if len(p.Kept) == 0 {
		return
	}
	fmt.Fprintf(u.out(), "Kept in %s (a later `ark setup` picks up from here):\n", u.ArkDir)
	for _, k := range p.Kept {
		fmt.Fprintf(u.out(), "- %s\n", k)
	}
	fmt.Fprintf(u.out(), "Delete %s to remove these too.\n", u.ArkDir)
}

func (u *Uninstaller) out() io.Writer {
	if u.Out != nil {
		return u.Out
	}
	return os.Stdout
}

// describe prints the plan for confirmation. R3690
func (p *UninstallPlan) describe(w io.Writer) {
	fmt.Fprintln(w, "ark uninstall will remove:")
	fmt.Fprintf(w, "- %d installed files\n", len(p.Files))
	fmt.Fprintf(w, "- %d links ark made\n", len(p.Links))
	for _, f := range []string{p.LibDir, p.Model, p.Exe} {
		if f != "" {
			fmt.Fprintf(w, "- %s\n", f)
		}
	}
	fmt.Fprintln(w, "- its bookkeeping, and the server's logs and pid file")
	if runtime.GOOS == "windows" {
		fmt.Fprintln(w, "- its PATH entry and its installed-apps entry")
	}
	fmt.Fprintln(w, "and keep:")
	for _, k := range p.Kept {
		fmt.Fprintf(w, "- %s\n", k)
	}
}

// confirmed reads one answer; only y or yes proceeds, so no answer at all
// (EOF) changes nothing. R3690
func confirmed(r io.Reader) bool {
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// uninstallCommand is the verb's node. CRC: crc-CLI.md | R3690
func uninstallCommand() *ucli.Command {
	return &ucli.Command{
		Name:  uninstallVerb,
		Usage: "remove the program and keep your data (index, ark.toml, storage, edited files)",
		Description: "Removes the executable, the installed files you have not edited, the\n" +
			"   links setup made, the llama.cpp libraries and a downloaded model, and on\n" +
			"   Windows the PATH and installed-apps entries. Lists both and asks first.",
		Flags:  []ucli.Flag{&ucli.BoolFlag{Name: uninstallYesFlag, Usage: "do not ask for confirmation"}},
		Action: uninstallAction,
	}
}

// uninstallAction refuses a developer install, shows the plan, asks unless
// --yes, and runs it. R3690
func uninstallAction(_ context.Context, c *ucli.Command) error {
	if exe := filepath.Join(arkDir, arkExeName()); isSymlink(exe) {
		return fmt.Errorf("%s is a symlink (developer install); refusing to uninstall it", exe)
	}
	claudeDir, err := userClaudeDir()
	if err != nil {
		return err
	}
	u := &Uninstaller{
		ArkDir:     arkDir,
		ClaudeDir:  claudeDir,
		Stop:       func() error { return stopServerForInstall(arkDir) },
		Unregister: unregisterInstall,
		RemoveExe:  removeExe,
	}
	plan, err := u.Plan()
	if err != nil {
		return err
	}
	plan.describe(os.Stdout)
	if !c.Bool(uninstallYesFlag) {
		fmt.Print("Proceed? [y/N] ")
		if !confirmed(os.Stdin) {
			fmt.Println("nothing removed")
			return nil
		}
	}
	return u.Run(plan)
}

// inside reports whether path lies within dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
