package main

// CRC: crc-CLI.md, crc-WingetInstall.md, crc-Uninstaller.md | Test: test-InstallLifecycle.md | R3683, R3684, R3686, R3687, R3688, R3689, R3690, R3692, R3693, R3694, R3695, R3696, R3697, R3698, R3699
//
// The install's three states and the verbs that move between them, against a
// temp ~/.ark and ~/.claude. The registry and the Windows self-delete are
// verified live; here they are injected.

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zot/ark"
)

func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestInstallStateIsMarkerPlusExe: marker and exe → installed; marker alone →
// reinstall; no marker → fresh, the exe alone being the README install.
// R3683, R3684
func TestInstallStateIsMarkerPlusExe(t *testing.T) {
	for _, tc := range []struct {
		name        string
		marker, exe bool
		want        installStateKind
	}{
		{"marker and exe", true, true, stateInstalled},
		{"marker alone", true, false, stateReinstall},
		{"exe alone", false, true, stateFresh},
		{"neither", false, false, stateFresh},
	} {
		dir := t.TempDir()
		if tc.marker {
			putFile(t, filepath.Join(dir, ark.IndexFileName), "db")
		}
		if tc.exe {
			putFile(t, filepath.Join(dir, arkExeName()), "exe")
		}
		if got := installState(dir); got != tc.want {
			t.Errorf("%s: installState = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReinstallKeepsEditedFiles: reinstalling over kept data writes the missing
// bundle files, keeps the one the user edited, and activates every app. R3684
func TestReinstallKeepsEditedFiles(t *testing.T) {
	dir := t.TempDir()
	edited := filepath.Join(dir, "skills", "ark", "SKILL.md")
	putFile(t, edited, "my edits")
	src := fakeAssets{files: map[string][]byte{
		"skills/ark/SKILL.md": []byte("bundled ark skill"),
		"skills/ui/SKILL.md":  []byte("bundled ui skill"),
		"apps/demo/app.lua":   []byte("-- demo"),
	}}
	if err := reinstallOverData(dir, src); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(edited); string(got) != "my edits" {
		t.Errorf("the edited skill was overwritten with %q; uninstall kept it, so reinstall must too", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "skills", "ui", "SKILL.md")); string(got) != "bundled ui skill" {
		t.Errorf("the missing skill was not restored: %q", got)
	}
	if !ark.IsOurLink(filepath.Join(dir, "lua", "demo"), filepath.Join(dir, "apps", "demo")) {
		t.Error("the bundled app was not activated")
	}
}

// TestWingetInstallNeverDowngrades: each case of the winget verb, its effects
// recorded. R3686–R3689
func TestWingetInstallNeverDowngrades(t *testing.T) {
	const running = "0.6.0"
	for _, tc := range []struct {
		name      string
		goos      string
		marker    bool
		exe       string // "", "file" or "symlink"
		installed string // "" = unreadable
		want      []string
		fails     bool
	}{
		{name: "off Windows", goos: "linux", marker: true, exe: "file", fails: true},
		{name: "developer install", goos: "windows", marker: true, exe: "symlink", fails: true},
		{name: "fresh", goos: "windows", want: []string{"setup"}},
		{name: "data without program", goos: "windows", marker: true, want: []string{"setup"}},
		{name: "older", goos: "windows", marker: true, exe: "file", installed: "0.5.0", want: []string{"stop", "swap", "refresh", "register"}},
		{name: "unreadable", goos: "windows", marker: true, exe: "file", want: []string{"stop", "swap", "refresh", "register"}},
		{name: "dev build installed", goos: "windows", marker: true, exe: "file", installed: "dev", want: []string{"stop", "swap", "refresh", "register"}},
		{name: "same", goos: "windows", marker: true, exe: "file", installed: running, want: []string{"register"}},
		{name: "newer installed", goos: "windows", marker: true, exe: "file", installed: "0.7.0", want: []string{"register"}},
	} {
		dir := t.TempDir()
		if tc.marker {
			putFile(t, filepath.Join(dir, ark.IndexFileName), "db")
		}
		exe := filepath.Join(dir, arkExeName())
		switch tc.exe {
		case "file":
			putFile(t, exe, "exe")
		case "symlink":
			putFile(t, exe+".real", "exe")
			if err := os.Symlink(exe+".real", exe); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
		}
		var got []string
		step := func(name string) func() error { return func() error { got = append(got, name); return nil } }
		w := &WingetInstall{
			ArkDir: dir, Version: running, GOOS: tc.goos,
			VersionOf: func(string) (string, error) {
				if tc.installed == "" {
					return "", errors.New("unreadable")
				}
				return tc.installed, nil
			},
			Setup: step("setup"), Stop: step("stop"), Swap: step("swap"), Refresh: step("refresh"), Register: step("register"),
		}
		err := w.Run()
		if (err != nil) != tc.fails {
			t.Errorf("%s: err = %v, want failure %v", tc.name, err, tc.fails)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: steps = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReleaseVersions: only dotted numbers are release versions. R3688
func TestReleaseVersions(t *testing.T) {
	for v, want := range map[string]bool{"0.5.1": true, "v1.2": true, "dev": false, "0.5.1-rc1": false, "": false} {
		if got := isReleaseVersion(v); got != want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

// seededUninstall is an install with one of everything uninstall must sort:
// unchanged and edited installed files, our links, a foreign link, a real
// directory where a link would go, a downloaded model, the provisioned libs,
// bookkeeping, and the user's data.
type seededUninstall struct {
	ark, claude                       string
	unchanged, edited, foreignLink    string
	realDir, appLink, ourSkill, model string
	keepers                           []string
}

func seedUninstall(t *testing.T) seededUninstall {
	t.Helper()
	s := seededUninstall{ark: t.TempDir(), claude: t.TempDir()}
	bundle := map[string]string{
		"skills/ark/SKILL.md": "ark skill",
		"skills/ui/SKILL.md":  "ui skill",
		"agents/hermes.md":    "agent",
		"apps/demo/app.lua":   "-- demo",
		"patterns/lists.md":   "pattern",
		"README.md":           "readme",
	}
	m, err := ark.LoadManifest(filepath.Join(s.ark, ark.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range bundle {
		putFile(t, filepath.Join(s.ark, filepath.FromSlash(rel)), content)
		m.Record(rel, []byte(content))
	}
	if err := m.Save(filepath.Join(s.ark, ark.ManifestFileName)); err != nil {
		t.Fatal(err)
	}
	s.unchanged = filepath.Join(s.ark, "skills", "ark", "SKILL.md")
	s.edited = filepath.Join(s.ark, "skills", "ui", "SKILL.md")
	putFile(t, s.edited, "ui skill, with my notes")

	s.appLink = filepath.Join(s.ark, "lua", "demo")
	mustLink(t, s.appLink, filepath.Join(s.ark, "apps", "demo"))
	s.ourSkill = filepath.Join(s.claude, "skills", "ark")
	mustLink(t, s.ourSkill, filepath.Join(s.ark, "skills", "ark"))
	mustLink(t, filepath.Join(s.claude, "agents", "ark"), filepath.Join(s.ark, "agents"))
	// skills/foreign is a bundled skill dir whose ~/.claude entry links elsewhere.
	putFile(t, filepath.Join(s.ark, "skills", "foreign", "SKILL.md"), "unrecorded")
	s.foreignLink = filepath.Join(s.claude, "skills", "foreign")
	mustLink(t, s.foreignLink, t.TempDir())
	// skills/real's ~/.claude entry is a real directory: the user's.
	putFile(t, filepath.Join(s.ark, "skills", "real", "SKILL.md"), "unrecorded")
	s.realDir = filepath.Join(s.claude, "skills", "real")
	putFile(t, filepath.Join(s.realDir, "mine.md"), "mine")

	putFile(t, filepath.Join(s.ark, arkConfigFile), "[embedding]\nmodel = \"m.gguf\"\n")
	s.model = filepath.Join(s.ark, "m.gguf")
	putFile(t, s.model, "weights")
	putFile(t, filepath.Join(s.ark, "lib", "libllama.so"), "lib")
	_, staged, _ := stagedExePaths(s.ark)
	putFile(t, staged, "staged")
	putFile(t, filepath.Join(s.ark, ark.UpdateRequestFile), "{}")
	putFile(t, filepath.Join(s.ark, arkExeName()), "exe")
	putFile(t, filepath.Join(s.ark, ark.IndexFileName), "db")
	putFile(t, filepath.Join(s.ark, arkStorageDir, "app", "data.json"), "{}")
	putFile(t, filepath.Join(s.ark, "logs", "ark.log"), "log")
	putFile(t, filepath.Join(s.ark, "log", "lua.log"), "log")
	putFile(t, filepath.Join(s.ark, ark.PidFileName), "123")
	for _, data := range []string{"tags.md", "chimes.md", filepath.Join("searching", "CLAUDE.md")} {
		putFile(t, filepath.Join(s.ark, data), "made with the database")
	}
	s.keepers = []string{
		filepath.Join(s.ark, ark.IndexFileName),
		filepath.Join(s.ark, arkConfigFile),
		filepath.Join(s.ark, arkStorageDir, "app", "data.json"),
		s.edited,
		filepath.Join(s.realDir, "mine.md"),
		filepath.Join(s.ark, "tags.md"),
		filepath.Join(s.ark, "chimes.md"),
		filepath.Join(s.ark, "searching", "CLAUDE.md"),
	}
	return s
}

func mustLink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ark.MakeDirLink(link, target); err != nil {
		t.Fatal(err)
	}
}

func (s seededUninstall) uninstaller(removed *[]string) *Uninstaller {
	return &Uninstaller{
		ArkDir: s.ark, ClaudeDir: s.claude, Out: &strings.Builder{},
		Stop:       func() error { return nil },
		Unregister: func(string) error { *removed = append(*removed, "registration"); return nil },
		RemoveExe:  func(exe string) error { *removed = append(*removed, "exe"); return os.Remove(exe) },
	}
}

// TestUninstallPlanKeepsWhatTheUserMade: the plan removes unchanged installed
// files and our links, and keeps edited files, the data, someone else's link
// and a real directory. R3692–R3694, R3697
func TestUninstallPlanKeepsWhatTheUserMade(t *testing.T) {
	s := seedUninstall(t)
	var removed []string
	p, err := s.uninstaller(&removed).Plan()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Files, s.unchanged) {
		t.Errorf("the unchanged skill is not planned for removal: %v", p.Files)
	}
	if slices.Contains(p.Files, s.edited) || !slices.Contains(p.Kept, s.edited) {
		t.Errorf("the edited skill must be kept, not removed: files %v, kept %v", p.Files, p.Kept)
	}
	for _, l := range []string{s.ourSkill, s.appLink} {
		if !slices.Contains(p.Links, l) {
			t.Errorf("our link %s is not planned: %v", l, p.Links)
		}
	}
	for _, l := range []string{s.foreignLink, s.realDir} {
		if slices.Contains(p.Links, l) {
			t.Errorf("%s is not ours and must stay: %v", l, p.Links)
		}
	}
	if p.Model != s.model || p.LibDir != filepath.Join(s.ark, "lib") || p.Exe == "" {
		t.Errorf("model %q, lib %q, exe %q: the downloads and the exe should all be planned", p.Model, p.LibDir, p.Exe)
	}
}

// TestUninstallRunLeavesTheData: after Run the planned entries are gone and the
// kept ones are untouched. R3692, R3693, R3695, R3696, R3697
func TestUninstallRunLeavesTheData(t *testing.T) {
	s := seedUninstall(t)
	var removed []string
	u := s.uninstaller(&removed)
	p, err := u.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Run(p); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		s.unchanged, filepath.Join(s.ark, "skills", "ark"), filepath.Join(s.ark, "apps"), s.appLink, s.ourSkill,
		filepath.Join(s.claude, "agents", "ark"), s.model, filepath.Join(s.ark, "lib"),
		filepath.Join(s.ark, ark.ManifestFileName), filepath.Join(s.ark, ark.UpdateRequestFile), filepath.Join(s.ark, arkExeName()),
		filepath.Join(s.ark, "patterns"), filepath.Join(s.ark, "README.md"),
		filepath.Join(s.ark, "logs"), filepath.Join(s.ark, "log"), filepath.Join(s.ark, ark.PidFileName),
	} {
		if exists(gone) {
			t.Errorf("%s survived the uninstall", gone)
		}
	}
	for _, k := range append(s.keepers, s.foreignLink) {
		if !exists(k) {
			t.Errorf("%s was removed; uninstall keeps it", k)
		}
	}
	if !slices.Equal(removed, []string{"registration", "exe"}) {
		t.Errorf("effects = %v, want the registration removed, then the exe last", removed)
	}
	if !strings.Contains(u.Out.(*strings.Builder).String(), s.edited) {
		t.Error("the report does not name the edited file it kept")
	}
}

// TestManifestRecordsEveryBundledFile: setup's manifest seed records every
// root the bundle ships, so update refreshes them and uninstall can sort them.
// R3698
func TestManifestRecordsEveryBundledFile(t *testing.T) {
	dir := t.TempDir()
	src := fakeAssets{files: map[string][]byte{}}
	for _, name := range []string{"html/a.html", "skills/x/SKILL.md", "patterns/lists.md", "resources/intro.md", "README.md", "mcp", "linkapp"} {
		src.files[name] = []byte(name)
	}
	if err := SeedManifest(dir, src); err != nil {
		t.Fatal(err)
	}
	m, err := ark.LoadManifest(filepath.Join(dir, ark.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for name := range src.files {
		if _, ok := m.Files[name]; !ok {
			t.Errorf("the manifest does not record bundled %s, so update never refreshes it and uninstall leaves it behind", name)
		}
	}
}

// TestUninstallAsksFirst: only y or yes proceeds; n or no answer changes
// nothing. R3690
func TestUninstallAsksFirst(t *testing.T) {
	for in, want := range map[string]bool{"n\n": false, "": false, "yes\n": true, "Y\n": true, "sure\n": false} {
		if got := confirmed(strings.NewReader(in)); got != want {
			t.Errorf("answer %q: confirmed = %v, want %v", in, got, want)
		}
	}
}
