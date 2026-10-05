package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// CRC: crc-WSLBridge.md | Test: test-WSLBridge.md | R3494-R3514

func envFrom(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func parentIs(name string) func() string { return func() string { return name } }

// Test: test-WSLBridge.md | R3494, R3495, R3496 -- detection decision
func TestWSLModeOn(t *testing.T) {
	cases := []struct {
		name   string
		goos   string
		env    map[string]string
		parent string
		want   bool
	}{
		{"parent wsl.exe", "windows", nil, "wsl.exe", true},
		{"parent WSL.EXE", "windows", nil, "WSL.EXE", true},
		{"parent full path", "windows", nil, `C:\Windows\System32\wsl.exe`, true},
		{"parent explorer.exe", "windows", nil, "explorer.exe", false},
		{"no parent", "windows", nil, "", false},
		{"force on", "windows", map[string]string{envWSL: "1"}, "explorer.exe", true},
		{"force off", "windows", map[string]string{envWSL: "0"}, "wsl.exe", false},
		{"child marker wins", "windows", map[string]string{envWSL: "1", envWSLChild: "1"}, "wsl.exe", false},
		{"linux never", "linux", map[string]string{envWSL: "1"}, "wsl.exe", false},
	}
	for _, c := range cases {
		if got := wslModeOn(c.goos, envFrom(c.env), parentIs(c.parent)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Test: test-WSLBridge.md | R3498 -- mount map parsing
func TestParseWSLMounts(t *testing.T) {
	m, err := parseWSLMounts("C:=/mnt/c;d:=/mnt/d/")
	if err != nil {
		t.Fatal(err)
	}
	want := wslMounts{{'C', "/mnt/c"}, {'D', "/mnt/d"}}
	if len(m) != len(want) || m[0] != want[0] || m[1] != want[1] {
		t.Fatalf("got %v, want %v", m, want)
	}
	if m, err := parseWSLMounts(""); err != nil || m != nil {
		t.Errorf("empty: got %v, %v; want nil default", m, err)
	}
	if m, err := parseWSLMounts("c:=/c;"); err != nil || len(m) != 1 || m[0].point != "/c" {
		t.Errorf("trailing separator: got %v, %v", m, err)
	}
	for _, bad := range []string{"C=/mnt/c", "C:=mnt/c", "CC:=/x", "1:=/x"} {
		_, err := parseWSLMounts(bad)
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: want an error naming the entry, got %v", bad, err)
		}
	}
}

// Test: test-WSLBridge.md | R3499, R3500 -- argument translation
func TestTranslateWSLArgs(t *testing.T) {
	ok := []struct {
		in, want string
		m        wslMounts
	}{
		{"/mnt/c/x", `C:\x`, nil},
		{"/mnt/c/Users/zot/proj", `C:\Users\zot\proj`, nil},
		{"--files=/mnt/d/a b", `--files=D:\a b`, nil},
		{"/mnt/c", `C:\`, nil},
		{"/mnt/c/", `C:\`, nil},
		{"search", "search", nil},
		{"/api/v1", "/api/v1", nil},
		{"/foo/", "/foo/", nil},
		{"https://x", "https://x", nil},
		{"/home", "/home", nil},
		{"-v", "-v", nil},
		{"/mntc/x", "/mntc/x", nil},
		{"/c/u", `C:\u`, wslMounts{{'C', "/c"}}},
		{"/data/c/x", `C:\x`, wslMounts{{'C', "/data/c"}, {'D', "/data"}}},
	}
	for _, c := range ok {
		got, err := translateWSLArgs([]string{c.in}, c.m)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.in, err)
			continue
		}
		if got[0] != c.want {
			t.Errorf("%s: got %q, want %q", c.in, got[0], c.want)
		}
	}
	for _, native := range []string{"/home/zot/p", "--dir=/tmp/x", "/mnt/wsl/x"} {
		_, err := translateWSLArgs([]string{"files", native}, nil)
		if err == nil {
			t.Errorf("%s: no error", native)
			continue
		}
		if !strings.Contains(err.Error(), "/mnt/c") {
			t.Errorf("%s: error does not point at /mnt/c: %v", native, err)
		}
	}
	// With a custom map, /mnt/c is no longer a drive mount.
	if _, err := translateWSLArgs([]string{"/mnt/c/x"}, wslMounts{{'C', "/c"}}); err == nil {
		t.Error("/mnt/c/x under a custom map: want the native-path error")
	}
}

// Test: test-WSLBridge.md | R3501, R3502, R3503 -- output filter rewrites
func TestTranslateWinPaths(t *testing.T) {
	cases := []struct {
		name, in, want string
		m              wslMounts
	}{
		{"plain", `C:\Users\zot\a.md:10-20` + "\n", "/mnt/c/Users/zot/a.md:10-20\n", nil},
		{"json", `{"path":"C:\\Users\\zot"}`, `{"path":"/mnt/c/Users/zot"}`, nil},
		{"program files", `see C:\Program Files\ark\x.md now`, "see /mnt/c/Program Files/ark/x.md now", nil},
		{"json program files", `"C:\\Program Files\\ark"`, `"/mnt/c/Program Files/ark"`, nil},
		{"last component with a space", `C:\Users\zot\my notes.md`, "/mnt/c/Users/zot/my notes.md", nil},
		{"two paths and prose", `C:\a and D:\b`, "/mnt/c/a and /mnt/d/b", nil},
		{"path list", `C:\a;D:\b`, "/mnt/c/a;/mnt/d/b", nil},
		{"quoted", `'C:\a\b' "D:\c"`, `'/mnt/c/a/b' "/mnt/d/c"`, nil},
		{"url", "https://x and http://y:80/z", "https://x and http://y:80/z", nil},
		{"word before", `abc:\x yC:\z`, `abc:\x yC:\z`, nil},
		{"no backslash", "C: drive and C:/x", "C: drive and C:/x", nil},
		{"unmapped drive", `E:\x and C:\y`, `E:\x and /c/y`, wslMounts{{'C', "/c"}}},
		{"json unicode escape", `"C:\\a\u00e9\\b"`, `"/mnt/c/a\u00e9/b"`, nil},
		{"json newline escape ends", `"C:\\a\nb\\c"`, `"/mnt/c/a\nb\\c"`, nil},
		{"tab ends", "C:\\a\tb\\c", "/mnt/c/a\tb\\c", nil},
		{"drive root", `C:\ `, "/mnt/c/ ", nil},
		{"lowercase drive", `d:\x`, "/mnt/d/x", nil},
	}
	for _, c := range cases {
		out, n := translateWinPaths([]byte(c.in), true, -1, c.m)
		if n != len(c.in) {
			t.Errorf("%s: consumed %d of %d", c.name, n, len(c.in))
		}
		if string(out) != c.want {
			t.Errorf("%s: got %q, want %q", c.name, out, c.want)
		}
	}
}

// filterAll writes the pieces to a fresh filter and flushes.
func filterAll(pieces ...string) string {
	var buf bytes.Buffer
	f := newWSLFilter(&buf, nil)
	for _, p := range pieces {
		f.Write([]byte(p))
	}
	f.Flush()
	return buf.String()
}

// Test: test-WSLBridge.md | R3504 -- output filter across write boundaries
func TestWSLFilterSplits(t *testing.T) {
	samples := []string{
		`C:\Users\zot\a.md:3` + "\nnext line\n",
		`{"p":"C:\\Program Files\\ark\\x.md","q":"D:\\y"}` + "\n",
		`ab C:\x D:\y` + "\n",
		`"C:\\a\u00e9\\b"`,
		`xC:\no and C:\yes`,
	}
	for _, s := range samples {
		whole := filterAll(s)
		for i := 0; i <= len(s); i++ {
			if got := filterAll(s[:i], s[i:]); got != whole {
				t.Errorf("%q: split mismatch at offset %d: got %q, want %q", s, i, got, whole)
				break
			}
		}
	}
	// A prompt with no path is not held back.
	var buf bytes.Buffer
	f := newWSLFilter(&buf, nil)
	f.Write([]byte("Continue? "))
	if buf.String() != "Continue? " {
		t.Errorf("pathless output was held: %q", buf.String())
	}
	// An unfinished path is held until more arrives.
	buf.Reset()
	f = newWSLFilter(&buf, nil)
	f.Write([]byte(`path: C:\Us`))
	if buf.String() != "path: " {
		t.Errorf("unfinished path not held: %q", buf.String())
	}
	f.Write([]byte("ers\n"))
	if buf.String() != "path: /mnt/c/Users\n" {
		t.Errorf("after the rest: %q", buf.String())
	}
	// The hold is bounded.
	buf.Reset()
	f = newWSLFilter(&buf, nil)
	long := `C:\` + strings.Repeat("a", maxWSLFilterHold+10)
	f.Write([]byte(long))
	if buf.Len() == 0 {
		t.Error("a hold past maxWSLFilterHold was not flushed")
	}
}

// Test: test-WSLBridge.md | R3505 -- re-exec wiring
func TestRunWSLFiltered(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	script := `printf 'C:\\Users\\x\n'; cat; printf '"D:\\\\a\\\\b"\n' >&2; exit 3`
	var out, errOut bytes.Buffer
	code := runWSLFiltered(sh, []string{"-c", script}, strings.NewReader("from stdin C:\\in\n"), &out, &errOut, nil)
	if code != 3 {
		t.Errorf("exit code %d, want 3", code)
	}
	if want := "/mnt/c/Users/x\nfrom stdin /mnt/c/in\n"; out.String() != want {
		t.Errorf("stdout %q, want %q", out.String(), want)
	}
	if want := "\"/mnt/d/a/b\"\n"; errOut.String() != want {
		t.Errorf("stderr %q, want %q", errOut.String(), want)
	}
	if code := runWSLFiltered(sh, []string{"-c", "exit 0"}, strings.NewReader(""), &out, &errOut, nil); code != 0 {
		t.Errorf("success exit code %d", code)
	}
}

// Test: test-WSLBridge.md | R3506 -- exempt commands
func TestWSLExempt(t *testing.T) {
	cases := map[string]bool{
		"luhmann attach":                     true,
		`--dir C:\x luhmann attach`:          true,
		"--force-session abc luhmann attach": true,
		"-v nano -m m":                       true,
		"serve --no-scan":                    true,
		"wsl-init":                           true,
		"luhmann status":                     false,
		"search x":                           false,
		"--dir serve status":                 false,
	}
	for in, want := range cases {
		if got := wslExempt(strings.Fields(in)); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

// fakeArkInstall makes an ark dir with two skills and an agents dir, and a
// few top-level entries.
func fakeArkInstall(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"skills/a", "skills/b", "agents", "html"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "skills", "not-a-dir.md"), nil, 0o644)
	return dir
}

// winStyle maps a link set from a fake Linux install onto C:\Users\zot\.ark.
func winStyle(links []installLink, dir string) []installLink {
	out := make([]installLink, len(links))
	for i, l := range links {
		rel := strings.TrimPrefix(l.Target, dir)
		out[i] = installLink{Rel: l.Rel, Target: `C:\Users\zot\.ark` + strings.ReplaceAll(rel, "/", `\`)}
	}
	return out
}

const testWinArkDir = `C:\Users\zot\.ark`

var testEntries = []string{"ark.exe", "ARK", "ark.toml", "html", "it's.md", "skills", "agents"}

func testScript(t *testing.T) (string, []installLink) {
	t.Helper()
	dir := fakeArkInstall(t)
	links := winStyle(installLinkSet(dir), dir)
	s, err := wslInitScript(testWinArkDir, testWinArkDir+`\ark.exe`, testEntries, links, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, links
}

var claudeLinkRe = regexp.MustCompile(`(?m)^ark_link '([^']*)' "\$HOME"/'\.claude/([^']*)'$`)

// Test: test-WSLBridge.md | R3509, R3511, R3512, R3514 -- wsl-init script content and link set
func TestWSLInitScriptLinkSet(t *testing.T) {
	dir := fakeArkInstall(t)
	// What installLinks really makes, read back off the filesystem.
	prev := arkDir
	arkDir = dir
	t.Cleanup(func() { arkDir = prev })
	claude := t.TempDir()
	if err := installLinks(claude); err != nil {
		t.Fatal(err)
	}
	var made []string
	for _, sub := range []string{"skills", "agents"} {
		ents, _ := os.ReadDir(filepath.Join(claude, sub))
		for _, e := range ents {
			made = append(made, sub+"/"+e.Name())
		}
	}
	sort.Strings(made)

	links := winStyle(installLinkSet(dir), dir)
	s, err := wslInitScript(testWinArkDir, testWinArkDir+`\ark.exe`, testEntries, links, nil)
	if err != nil {
		t.Fatal(err)
	}
	var scripted []string
	for _, m := range claudeLinkRe.FindAllStringSubmatch(s, -1) {
		scripted = append(scripted, m[2])
		if !strings.HasPrefix(m[1], "/mnt/c/Users/zot/.ark/") {
			t.Errorf("link %s targets %s, not the Windows install under /mnt/c", m[2], m[1])
		}
	}
	sort.Strings(scripted)
	if strings.Join(scripted, " ") != strings.Join(made, " ") {
		t.Errorf("script links %v, installLinks made %v", scripted, made)
	}
	if !strings.Contains(s, "exec '/mnt/c/Users/zot/.ark/ark.exe' \"$@\"") {
		t.Error("shim does not exec the Windows ark.exe by its /mnt path")
	}
	if !strings.Contains(s, wslShimMarker+"\n") {
		t.Error("shim lacks its marker line")
	}
	if !strings.Contains(s, "WSLENV=\"ARK_WSL/u:ARK_WSL_MOUNTS/u${WSLENV:+:$WSLENV}\"") {
		t.Error("shim does not list ARK_WSL and ARK_WSL_MOUNTS in WSLENV with /u")
	}
	for _, skipped := range []string{"ark.exe", "ARK"} {
		if strings.Contains(s, "'.ark/"+skipped+"'") {
			t.Errorf("script links %s into ~/.ark", skipped)
		}
	}
	if !strings.Contains(s, `ark_link '/mnt/c/Users/zot/.ark/ark.toml' "$HOME"/'.ark/ark.toml'`) {
		t.Error("script does not link ark.toml")
	}
	// A custom map moves every path.
	s2, err := wslInitScript(testWinArkDir, testWinArkDir+`\ark.exe`, testEntries, links, wslMounts{{'C', "/c"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s2, "/mnt/") || !strings.Contains(s2, "exec '/c/Users/zot/.ark/ark.exe'") {
		t.Error("a custom mount map did not move the script's paths")
	}
	if _, err := wslInitScript(`\\server\share\.ark`, `C:\ark.exe`, nil, nil, nil); err == nil {
		t.Error("a non-drive ark dir: want an error")
	}
}

// Test: test-WSLBridge.md | R3508, R3509, R3511, R3513 -- wsl-init script runs under sh
func TestWSLInitScriptRuns(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	s, _ := testScript(t)
	if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
	home := t.TempDir()
	// A real file where one entry link would go: must survive.
	os.MkdirAll(filepath.Join(home, ".ark"), 0o755)
	realFile := filepath.Join(home, ".ark", "html")
	os.WriteFile(realFile, []byte("mine"), 0o644)
	run := func() string {
		cmd := exec.Command(sh, "-c", s)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("script: %v\n%s", err, out)
		}
		return string(out)
	}
	for pass := 1; pass <= 2; pass++ {
		out := run()
		if !strings.Contains(out, "skipped "+realFile) {
			t.Errorf("pass %d: no warning for the real file: %q", pass, out)
		}
		if b, _ := os.ReadFile(realFile); string(b) != "mine" {
			t.Fatalf("pass %d: real file at %s was replaced", pass, realFile)
		}
		shim := filepath.Join(home, ".ark", "ark")
		fi, err := os.Lstat(shim)
		if err != nil || fi.Mode()&0o111 == 0 || !fi.Mode().IsRegular() {
			t.Fatalf("pass %d: shim missing or not executable: %v", pass, err)
		}
		if b, _ := os.ReadFile(shim); !strings.Contains(string(b), wslShimMarker) {
			t.Errorf("pass %d: shim lacks the marker", pass)
		}
		for link, target := range map[string]string{
			".ark/ark.toml":      "/mnt/c/Users/zot/.ark/ark.toml",
			".ark/it's.md":       "/mnt/c/Users/zot/.ark/it's.md",
			".claude/skills/a":   "/mnt/c/Users/zot/.ark/skills/a",
			".claude/agents/ark": "/mnt/c/Users/zot/.ark/agents",
		} {
			if got, err := os.Readlink(filepath.Join(home, link)); err != nil || got != target {
				t.Errorf("pass %d: %s -> %q (%v), want %s", pass, link, got, err, target)
			}
		}
	}
	// A stale shim (ours, by its marker) is rewritten.
	shim := filepath.Join(home, ".ark", "ark")
	os.WriteFile(shim, []byte("#!/bin/sh\n"+wslShimMarker+"\nexec '/old/ark.exe' \"$@\"\n"), 0o755)
	run()
	if b, _ := os.ReadFile(shim); !strings.Contains(string(b), "exec '/mnt/c/Users/zot/.ark/ark.exe'") {
		t.Errorf("a stale shim carrying the marker was not rewritten: %q", b)
	}
	// A foreign file at ~/.ark/ark is left alone.
	os.WriteFile(shim, []byte("#!/bin/sh\necho mine\n"), 0o755)
	if out := run(); !strings.Contains(out, "skipped "+shim) {
		t.Errorf("no warning for a foreign ~/.ark/ark: %q", out)
	}
	if b, _ := os.ReadFile(shim); string(b) != "#!/bin/sh\necho mine\n" {
		t.Error("a foreign ~/.ark/ark was overwritten")
	}
	// ~/.ark as a symlink stops the script.
	home2 := t.TempDir()
	os.Symlink(t.TempDir(), filepath.Join(home2, ".ark"))
	cmd := exec.Command(sh, "-c", s)
	cmd.Env = append(os.Environ(), "HOME="+home2)
	if out, _ := cmd.CombinedOutput(); !strings.Contains(string(out), "symlink or not a directory") {
		t.Errorf("symlinked ~/.ark not refused: %q", out)
	}
}

// Test: test-WSLBridge.md | R3510, R3498 -- shim mount parser under sh
func TestShimMountParser(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	fixture := filepath.Join(t.TempDir(), "mounts")
	os.WriteFile(fixture, []byte(
		"none /usr/lib/wsl/lib overlay rw,relatime 0 0\n"+
			`C:\134 /mnt/c 9p rw,noatime,aname=drvfs;path=C:\;uid=1000;gid=1000;symlinkroot=/mnt/,cache=0x5,access=client,msize=65536,trans=fd,rfd=6,wfd=6 0 0`+"\n"+
			`drvfs /mnt/x 9p rw,noatime,aname=other;path=X:\;uid=1000 0 0`+"\n"+
			`D:\134 /mnt/d 9p rw,noatime,aname=drvfs;path=D:\;uid=1000 0 0`+"\n"), 0o644)
	if out, err := exec.Command(sh, "-n", "-c", wslShimText("/mnt/c/ark.exe")).CombinedOutput(); err != nil {
		t.Fatalf("shim sh -n: %v\n%s", err, out)
	}
	out, err := exec.Command(sh, "-c", wslMountsShFunc+"ark_mounts '"+fixture+"'").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "C:=/mnt/c;D:=/mnt/d" {
		t.Fatalf("got %q", out)
	}
	m, err := parseWSLMounts(string(out))
	if err != nil || len(m) != 2 {
		t.Fatalf("parseWSLMounts(%q): %v, %v", out, m, err)
	}
	if w, ok := m.toWindows("/mnt/d/p"); !ok || w != `D:\p` {
		t.Errorf("round trip: %q, %v", w, ok)
	}
}
