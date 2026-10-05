package main

// The WSL bridge: the Windows ark.exe serving a Claude Code session that runs
// inside WSL2 (specs/wsl.md). Everything here is client-side; the server never
// learns WSL exists.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// WSL bridge names and limits.
// CRC: crc-WSLBridge.md | R3496, R3498, R3504, R3509, R3510
const (
	envWSL       = "ARK_WSL"        // "1" forces WSL mode on, "0" off
	envWSLMounts = "ARK_WSL_MOUNTS" // drive-to-mount map, written by the shim
	envWSLChild  = "ARK_WSL_CHILD"  // set before re-exec; forces WSL mode off

	wslForceOn  = "1"
	wslForceOff = "0"

	goosWindows   = "windows"
	wslExeName    = "wsl.exe" // the parent of a process launched through interop
	wslWinExeName = "ark.exe" // the Windows binary, never linked into ~/.ark
	wslShimName   = "ark"     // the shim's name in the Linux ~/.ark
	wslMountRoot  = "/mnt"    // WSL's default automount root
	wslEnvFlagU   = "/u"      // WSLENV flag: pass only from WSL to Windows
	wslShimMarker = "# ark-wsl-shim"

	wslMountSep    = ";"
	wslMountAssign = ":="

	// maxWSLFilterHold bounds the bytes the output filter holds back while a
	// candidate path is unfinished; a longer run is translated as if final.
	maxWSLFilterHold = 64 << 10

	// pathTermChars cannot appear in a Windows path, so each ends one.
	pathTermChars = "\"'`<>|*?"

	errWSLNativeFmt = "%s is a WSL-native path, which the Windows ark cannot see; " +
		"keep projects on a Windows drive (under /mnt/c) and use that path"
	errWSLInitNotWindows = "wsl-init is for the Windows ark.exe, run from inside WSL: " +
		`eval "$(/mnt/c/Users/<you>/.ark/ark.exe wsl-init)"`
)

// linuxTopDirs are the standard Linux top-level directories. An argument under
// one of them is unambiguously a WSL-native path (R3500).
var linuxTopDirs = map[string]bool{
	"home": true, "root": true, "tmp": true, "usr": true, "opt": true,
	"var": true, "etc": true, "srv": true, "mnt": true, "media": true,
	"run": true, "proc": true, "sys": true, "dev": true, "bin": true,
	"sbin": true, "lib": true, "lib64": true, "snap": true, "boot": true,
}

// wslExemptPaths are the command paths that need the real console or are not a
// client's output; they run in-process, unfiltered (R3506).
var wslExemptPaths = [][]string{{"luhmann", "attach"}, {"nano"}, {"serve"}, {"wsl-init"}}

// globalValueFlags are the root flags that take a separate value, so the
// command-path scan can step over it.
var globalValueFlags = map[string]bool{"dir": true, forceSessionFlag: true}

// wslActive and wslActiveMounts record WSL mode for in-process commands
// (wsl-init), since wslPrepare removes the variables from the environment.
var (
	wslActive       bool
	wslActiveMounts wslMounts
)

// --- detection ---

// wslModeOn is the pure WSL-mode decision. The child marker wins, then the
// explicit override, then the parent's image name.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.2 | R3494, R3495, R3496
func wslModeOn(goos string, getenv func(string) string, parentName func() string) bool {
	if goos != goosWindows {
		return false
	}
	if getenv(envWSLChild) != "" {
		return false
	}
	switch getenv(envWSL) {
	case wslForceOn:
		return true
	case wslForceOff:
		return false
	}
	name := parentName()
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	return strings.EqualFold(name, wslExeName)
}

// --- the mount map ---

// wslMount maps one Windows drive to its Linux mount point.
type wslMount struct {
	drive byte // upper case
	point string
}

// wslMounts is the drive-to-mount map; nil means the /mnt/<letter> default.
type wslMounts []wslMount

// parseWSLMounts parses ARK_WSL_MOUNTS ("C:=/mnt/c;D:=/mnt/d"). Empty means
// the default map (nil).
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.3 | R3498
func parseWSLMounts(s string) (wslMounts, error) {
	var m wslMounts
	for _, e := range strings.Split(s, wslMountSep) {
		if e == "" {
			continue
		}
		d, point, ok := strings.Cut(e, wslMountAssign)
		if !ok || len(d) != 1 || !isASCIILetter(d[0]) || !strings.HasPrefix(point, "/") {
			return nil, fmt.Errorf("%s: bad entry %q (want X:=/mount/point)", envWSLMounts, e)
		}
		if len(point) > 1 {
			point = strings.TrimRight(point, "/")
		}
		m = append(m, wslMount{drive: upperASCII(d[0]), point: point})
	}
	return m, nil
}

// pointFor returns the mount point of a drive.
func (m wslMounts) pointFor(drive byte) (string, bool) {
	drive = upperASCII(drive)
	if m == nil {
		return wslMountRoot + "/" + string(drive|0x20), true
	}
	for _, e := range m {
		if e.drive == drive {
			return e.point, true
		}
	}
	return "", false
}

// toWindows rewrites a Linux path at or under a drive mount to its Windows
// path, choosing the longest matching mount point.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.5 | R3499
func (m wslMounts) toWindows(p string) (string, bool) {
	if m == nil {
		rest, ok := strings.CutPrefix(p, wslMountRoot+"/")
		if !ok || rest == "" || !isASCIILetter(rest[0]) || (len(rest) > 1 && rest[1] != '/') {
			return "", false
		}
		return winJoin(rest[0], rest[1:]), true
	}
	best := -1
	for i, e := range m {
		if (p == e.point || strings.HasPrefix(p, e.point+"/")) && (best < 0 || len(e.point) > len(m[best].point)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	return winJoin(m[best].drive, p[len(m[best].point):]), true
}

// winJoin builds X:\a\b from a drive and a slash-separated rest ("" or "/a/b").
func winJoin(drive byte, rest string) string {
	return string(upperASCII(drive)) + `:\` + strings.ReplaceAll(strings.TrimPrefix(rest, "/"), "/", `\`)
}

// mountPath joins a drive's mount point and the path's components. It is the
// one Windows-to-Linux translation: the output filter and wsl-init both reach
// it, so their paths cannot disagree.
// CRC: crc-WSLBridge.md | R3501, R3514
func (m wslMounts) mountPath(drive byte, components []string) (string, bool) {
	point, ok := m.pointFor(drive)
	if !ok {
		return "", false
	}
	return strings.TrimSuffix(point, "/") + "/" + strings.Join(components, "/"), true
}

// winPathToLinux translates a Windows drive path (X:\a\b) to its Linux path.
// CRC: crc-WSLBridge.md | R3501, R3514
func winPathToLinux(p string, m wslMounts) (string, bool) {
	if len(p) < 3 || !isASCIILetter(p[0]) || p[1] != ':' || p[2] != '\\' {
		return "", false
	}
	return m.mountPath(p[0], strings.Split(p[3:], `\`))
}

// --- arguments ---

// translateWSLArgs rewrites every path argument, and the value of every
// -flag=value argument, from Linux to Windows, refusing WSL-native paths.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.5 | R3499, R3500
func translateWSLArgs(args []string, m wslMounts) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			name, val, ok := strings.Cut(a, "=")
			if !ok {
				out[i] = a
				continue
			}
			t, err := translateWSLArg(val, m)
			if err != nil {
				return nil, err
			}
			out[i] = name + "=" + t
			continue
		}
		t, err := translateWSLArg(a, m)
		if err != nil {
			return nil, err
		}
		out[i] = t
	}
	return out, nil
}

// translateWSLArg translates one value: a path under a mount becomes Windows,
// a WSL-native path is an error, anything else is untouched.
// CRC: crc-WSLBridge.md | R3499, R3500
func translateWSLArg(a string, m wslMounts) (string, error) {
	if w, ok := m.toWindows(a); ok {
		return w, nil
	}
	if isWSLNativePath(a) {
		return "", fmt.Errorf(errWSLNativeFmt, a)
	}
	return a, nil
}

// isWSLNativePath is the conservative "unambiguously a Linux path" test:
// /<standard top-level dir>/... (R3500).
func isWSLNativePath(a string) bool {
	if !strings.HasPrefix(a, "/") {
		return false
	}
	first, _, ok := strings.Cut(a[1:], "/")
	return ok && linuxTopDirs[first]
}

// --- output ---

func isASCIILetter(c byte) bool { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }
func upperASCII(c byte) byte    { return c &^ 0x20 }
func isWordByte(c byte) bool {
	return isASCIILetter(c) || (c >= '0' && c <= '9') || c == '_'
}
func isPathTerm(c byte) bool {
	return c < 0x20 || c == 0x7f || strings.IndexByte(pathTermChars, c) >= 0
}

// driveAt reports whether a drive path (X:\) starts at b[i]. prev is the byte
// before b (-1 when none). need is true when b ends before the answer is known
// and more input may follow.
// CRC: crc-WSLBridge.md | R3502, R3504
func driveAt(b []byte, i int, prev int, final bool) (ok, need bool) {
	if !isASCIILetter(b[i]) {
		return false, false
	}
	before := prev
	if i > 0 {
		before = int(b[i-1])
	}
	if before >= 0 && isWordByte(byte(before)) {
		return false, false
	}
	for k, want := range []byte{':', '\\'} {
		j := i + 1 + k
		if j >= len(b) {
			return false, !final
		}
		if b[j] != want {
			return false, false
		}
	}
	return true, false
}

// spaceContinues decides whether the path continues across the spaces at
// b[j]: only when the next word holds a backslash and is not a drive path.
// CRC: crc-WSLBridge.md | R3503
func spaceContinues(b []byte, j int, final bool) (cont, need bool) {
	k := j
	for k < len(b) && b[k] == ' ' {
		k++
	}
	if k >= len(b) {
		return false, !final
	}
	if ok, nd := driveAt(b, k, -1, final); ok || nd {
		return false, nd
	}
	for t := k; t < len(b); t++ {
		c := b[t]
		if c == '\\' {
			return true, false
		}
		if c == ' ' || isPathTerm(c) {
			return false, false
		}
	}
	return false, !final
}

// scanWinPath finds the end of the drive path starting at b[i], and whether it
// is the JSON-escaped form. need is true when the end is not yet known.
// CRC: crc-WSLBridge.md | R3501, R3503, R3504
func scanWinPath(b []byte, i int, final bool) (end int, esc, need bool) {
	j := i + 3
	if j >= len(b) {
		return j, false, !final
	}
	if b[j] == '\\' {
		esc = true
		j++
	}
scan:
	for j < len(b) {
		c := b[j]
		switch {
		case c == '\\':
			if !esc {
				j++
				continue
			}
			if j+1 >= len(b) {
				if !final {
					return 0, esc, true
				}
				break scan
			}
			switch b[j+1] {
			case '\\':
				j += 2
				continue
			case 'u':
				if j+6 > len(b) {
					if !final {
						return 0, esc, true
					}
					break scan
				}
				if isHex4(b[j+2 : j+6]) {
					j += 6
					continue
				}
			}
			break scan
		case c == ' ':
			cont, nd := spaceContinues(b, j, final)
			if nd {
				return 0, esc, true
			}
			if !cont {
				break scan
			}
			for j < len(b) && b[j] == ' ' {
				j++
			}
		case isPathTerm(c):
			break scan
		default:
			if ok, _ := driveAt(b, j, -1, final); ok {
				break scan
			}
			j++
		}
	}
	if j >= len(b) && !final {
		return 0, esc, true
	}
	return j, esc, false
}

func isHex4(h []byte) bool {
	for _, c := range h {
		if !(c >= '0' && c <= '9' || (c|0x20) >= 'a' && (c|0x20) <= 'f') {
			return false
		}
	}
	return true
}

// convertWinPath rewrites one scanned drive path. The plain form splits on \;
// the escaped form splits on \\ (a \uXXXX escape stays inside its component).
// CRC: crc-WSLBridge.md | R3501
func convertWinPath(span []byte, esc bool, m wslMounts) []byte {
	if !esc {
		if l, ok := winPathToLinux(string(span), m); ok {
			return []byte(l)
		}
		return span
	}
	if l, ok := m.mountPath(span[0], strings.Split(string(span[4:]), `\\`)); ok {
		return []byte(l)
	}
	return span
}

// translateWinPaths rewrites the drive paths in b. Unless final, it stops
// before a candidate path that might continue past the end of b and returns
// how many bytes of b it consumed; the caller keeps the rest.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.9 | R3501, R3502, R3503, R3504
func translateWinPaths(b []byte, final bool, prev int, m wslMounts) ([]byte, int) {
	out := make([]byte, 0, len(b))
	copied := 0
	for i := 0; i < len(b); {
		ok, need := driveAt(b, i, prev, final)
		if need {
			return append(out, b[copied:i]...), i
		}
		if !ok {
			i++
			continue
		}
		end, esc, need := scanWinPath(b, i, final)
		if need {
			return append(out, b[copied:i]...), i
		}
		out = append(out, b[copied:i]...)
		out = append(out, convertWinPath(b[i:end], esc, m)...)
		i, copied = end, end
	}
	return append(out, b[copied:]...), len(b)
}

// wslFilter is the streaming output filter: an io.Writer that translates
// drive paths and holds back only an unfinished candidate at the end.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.9 | R3504
type wslFilter struct {
	w       io.Writer
	m       wslMounts
	pending []byte
	prev    int // last consumed byte, -1 at the start
}

func newWSLFilter(w io.Writer, m wslMounts) *wslFilter {
	return &wslFilter{w: w, m: m, prev: -1}
}

func (f *wslFilter) Write(p []byte) (int, error) {
	f.pending = append(f.pending, p...)
	if err := f.emit(len(f.pending) > maxWSLFilterHold); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush translates and writes everything held, as at end of output.
func (f *wslFilter) Flush() error { return f.emit(true) }

func (f *wslFilter) emit(final bool) error {
	out, n := translateWinPaths(f.pending, final, f.prev, f.m)
	if n > 0 {
		f.prev = int(f.pending[n-1])
	}
	f.pending = append(f.pending[:0], f.pending[n:]...)
	if len(out) == 0 {
		return nil
	}
	_, err := f.w.Write(out)
	return err
}

// --- the mechanism ---

// wslCommandPath returns up to the first two positional arguments, stepping
// over the root's flags (and the separate value of a value flag).
func wslCommandPath(args []string) []string {
	var p []string
	for i := 0; i < len(args) && len(p) < 2; i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			if len(p) == 0 && !strings.Contains(name, "=") && globalValueFlags[name] {
				i++
			}
			continue
		}
		p = append(p, a)
	}
	return p
}

// wslExempt reports whether the command runs unfiltered in WSL mode.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.6 | R3506
func wslExempt(args []string) bool {
	p := wslCommandPath(args)
	for _, e := range wslExemptPaths {
		if len(p) >= len(e) && slices.Equal(p[:len(e)], e) {
			return true
		}
	}
	return false
}

// wslPrepare is main's first step. Outside WSL mode it returns args as they
// are. In WSL mode it scrubs the environment, translates the arguments, and
// either returns them (exempt commands, run in-process) or re-executes this
// binary through the output filter and exits with the child's code.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md | R3497, R3505, R3506
func wslPrepare(args []string) []string {
	if !wslModeOn(runtime.GOOS, os.Getenv, parentExeName) {
		return args
	}
	mounts, err := parseWSLMounts(os.Getenv(envWSLMounts))
	if err != nil {
		fatal(err)
	}
	wslActive, wslActiveMounts = true, mounts
	// R3497: descendants are ordinary Windows processes.
	os.Unsetenv(envWSL)
	os.Unsetenv(envWSLMounts)
	os.Setenv(envWSLChild, wslForceOn)
	if args, err = translateWSLArgs(args, mounts); err != nil {
		fatal(err)
	}
	if wslExempt(args) {
		return args
	}
	exe, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	os.Exit(runWSLFiltered(exe, args, os.Stdin, os.Stdout, os.Stderr, mounts))
	return nil
}

// runWSLFiltered runs exe as a child with stdin passed straight through and
// both outputs filtered, ignoring interrupts so the child decides how to stop,
// and returns the child's exit code. An in-process filter would lose buffered
// output to the many os.Exit calls; a child's pipes are drained by Wait.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.7, seq-wsl.md#1.10 | R3505
func runWSLFiltered(exe string, args []string, stdin io.Reader, stdout, stderr io.Writer, m wslMounts) int {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = stdin
	outF, errF := newWSLFilter(stdout, m), newWSLFilter(stderr, m)
	cmd.Stdout, cmd.Stderr = outF, errF
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	err := cmd.Run()
	outF.Flush()
	errF.Flush()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "error: run %s: %v\n", exe, err)
	return 1
}

// --- wsl-init ---

// wslMountsShFunc is the shim's /proc/mounts parser: every mount whose options
// carry aname=drvfs, its drive from path=X:, its mount point from field 2.
// Printed into the shim and run by the test against a fixture.
// CRC: crc-WSLBridge.md | R3510
const wslMountsShFunc = `ark_mounts() {
	ark_m=
	while read -r ark_src ark_mp ark_type ark_opts ark_rest; do
		case "$ark_opts" in *aname=drvfs*) ;; *) continue ;; esac
		ark_d=${ark_opts#*path=}
		[ "$ark_d" = "$ark_opts" ] && continue
		ark_d=${ark_d%%:*}
		case "$ark_d" in [A-Za-z]) ;; *) continue ;; esac
		ark_m="$ark_m${ark_m:+` + wslMountSep + `}$ark_d` + wslMountAssign + `$ark_mp"
	done < "$1"
	printf '%s' "$ark_m"
}
`

// wslInitPrelude opens the script's subshell: helpers, the ~/.ark guard, and
// the directories the links go in.
// CRC: crc-WSLBridge.md | R3508, R3513
const wslInitPrelude = `ark_warn() { printf 'ark wsl-init: %s\n' "$*" >&2; }
ark_link() {
	if [ -L "$2" ]; then rm -f "$2" && ln -s "$1" "$2"
	elif [ -e "$2" ]; then ark_warn "skipped $2: exists and is not a symlink"
	else ln -s "$1" "$2"; fi
}
if [ -L "$HOME/.ark" ] || { [ -e "$HOME/.ark" ] && [ ! -d "$HOME/.ark" ]; }; then
	ark_warn "$HOME/.ark is a symlink or not a directory; leaving it alone"
	exit 1
fi
mkdir -p "$HOME/.ark" || exit 1
`

// shQuote single-quotes s for sh.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// wslShimText is the ~/.ark/ark shim that runs the Windows ark.exe.
// CRC: crc-WSLBridge.md | R3509, R3510
func wslShimText(exeLinux string) string {
	return "#!/bin/sh\n" + wslShimMarker + "\n" +
		"# Written by ark wsl-init; re-run that to refresh. Runs the Windows ark.exe\n" +
		"# and tells it the drive mounts so it can translate paths both ways.\n" +
		wslMountsShFunc +
		envWSL + "=" + wslForceOn + "\n" +
		envWSLMounts + "=$(ark_mounts /proc/mounts)\n" +
		"WSLENV=\"" + envWSL + wslEnvFlagU + ":" + envWSLMounts + wslEnvFlagU + "${WSLENV:+:$WSLENV}\"\n" +
		"export " + envWSL + " " + envWSLMounts + " WSLENV\n" +
		"exec " + shQuote(exeLinux) + " \"$@\"\n"
}

// wslInitScript builds the script `ark wsl-init` prints. Every Linux path in
// it comes from winPathToLinux over the Windows path.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#2.4 | R3508, R3509, R3511, R3512, R3513, R3514
func wslInitScript(winArkDir, winExe string, entries []string, links []installLink, m wslMounts) (string, error) {
	toLinux := func(p string) (string, error) {
		if l, ok := winPathToLinux(p, m); ok {
			return l, nil
		}
		return "", fmt.Errorf("%s is not on a drive WSL mounts", p)
	}
	arkLinux, err := toLinux(winArkDir)
	if err != nil {
		return "", err
	}
	exeLinux, err := toLinux(winExe)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# ark wsl-init: sets ark up inside WSL. Use: eval \"$(ark.exe wsl-init)\"; safe to re-run.\n(\n")
	b.WriteString(wslInitPrelude)
	// R3509, R3513: the shim, rewritten only when it is ours (or a link, or absent).
	b.WriteString("ark_shim=\"$HOME/.ark/" + wslShimName + "\"\n")
	b.WriteString("if [ -L \"$ark_shim\" ] || [ ! -e \"$ark_shim\" ] || grep -qx " + shQuote(wslShimMarker) + " \"$ark_shim\" 2>/dev/null; then\n")
	b.WriteString("rm -f \"$ark_shim\"\ncat > \"$ark_shim\" <<'ARK_WSL_SHIM'\n")
	b.WriteString(wslShimText(exeLinux))
	b.WriteString("ARK_WSL_SHIM\nchmod 755 \"$ark_shim\"\nelse\n")
	b.WriteString("ark_warn \"skipped $ark_shim: a file that is not ark's shim\"\nfi\n")
	// R3511: every other entry of the Windows ark directory.
	sorted := append([]string(nil), entries...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if strings.EqualFold(name, wslWinExeName) || strings.EqualFold(name, wslShimName) {
			continue
		}
		b.WriteString("ark_link " + shQuote(arkLinux+"/"+name) + " \"$HOME\"/" + shQuote(".ark/"+name) + "\n")
	}
	// R3512: the .claude links, from installLinkSet's list.
	dirs := map[string]bool{}
	for _, l := range links {
		dirs[path.Dir(l.Rel)] = true
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	for _, d := range dirList {
		b.WriteString("mkdir -p \"$HOME\"/" + shQuote(".claude/"+d) + "\n")
	}
	for _, l := range links {
		target, err := toLinux(l.Target)
		if err != nil {
			return "", err
		}
		b.WriteString("ark_link " + shQuote(target) + " \"$HOME\"/" + shQuote(".claude/"+l.Rel) + "\n")
	}
	b.WriteString(")\n")
	return b.String(), nil
}

// cmdWSLInit prints the wsl-init script. It works whether or not WSL mode was
// detected, and refuses on a non-Windows build.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#2.2, seq-wsl.md#2.3, seq-wsl.md#2.5 | R3507
func cmdWSLInit(_ []string) {
	if runtime.GOOS != goosWindows {
		fatal(errors.New(errWSLInitNotWindows))
	}
	m := wslActiveMounts
	if !wslActive {
		var err error
		if m, err = parseWSLMounts(os.Getenv(envWSLMounts)); err != nil {
			fatal(err)
		}
	}
	dirEntries, err := os.ReadDir(arkDir)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w (run `ark setup` on Windows first)", arkDir, err))
	}
	names := make([]string, 0, len(dirEntries))
	for _, e := range dirEntries {
		names = append(names, e.Name())
	}
	exe := filepath.Join(arkDir, wslWinExeName)
	if _, err := os.Stat(exe); err != nil {
		if exe, err = os.Executable(); err != nil {
			fatal(err)
		}
	}
	script, err := wslInitScript(arkDir, exe, names, installLinkSet(arkDir), m)
	if err != nil {
		fatal(err)
	}
	fmt.Print(script)
}
