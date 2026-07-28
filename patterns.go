package ark

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Personal pattern collection — a directory of markdown files whose
// frontmatter names and summarizes a reusable technique.
//
// Filesystem only, deliberately: `ark patterns list` runs at session start
// before a server exists, and the cheapest way to guarantee a verb never
// needs the server is to give its component no way to ask for one (R3270).
//
// CRC: crc-Patterns.md | R3267, R3269, R3271, R3272, R3273, R3277

const (
	patternExt = ".md"
	// frontmatterFence delimits the YAML block. Frontmatter is recognized
	// only when content *begins* with it — see ParseFrontmatter (R3275).
	frontmatterFence = "---"

	fmKeyName = "name"
	// fmKeySummary is this collection's summary key; the Frictionless UI
	// collection uses fmKeyDescription instead (R3277).
	fmKeySummary     = "summary"
	fmKeyTags        = "tags"
	fmKeyDescription = "description"

	blockHeaderFmt = "These patterns are in %s\n" +
		"When you notice a recurring technique worth naming, suggest adding it as a new pattern.\n\n"
)

// Pattern is one parsed pattern file. Slug is the identity; Name is data.
// CRC: crc-Patterns.md | R3267
type Pattern struct {
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	Summary string   `json:"summary"`
	Path    string   `json:"path"`
}

// Patterns reads and writes one collection directory.
// CRC: crc-Patterns.md | R3267
type Patterns struct {
	dir string
}

// NewPatterns binds a collection to a directory. An empty dir yields a
// collection that reads as empty rather than erroring, so an unconfigured
// installation still works.
// CRC: crc-Patterns.md | R3271
func NewPatterns(dir string) *Patterns {
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	return &Patterns{dir: dir}
}

// Dir reports the collection directory.
// CRC: crc-Patterns.md
func (p *Patterns) Dir() string { return p.dir }

// PatternSlug derives a pattern's identity from its path: basename without
// the extension, lowercased.
// CRC: crc-Patterns.md | R3267
func PatternSlug(path string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(path), patternExt))
}

// ParseFrontmatter reads a leading `---`-delimited block into a map.
//
// The leading-fence requirement is load-bearing rather than stylistic: it is
// the whole reason the append path derives nothing (R3275). A mid-file
// fragment, which is what executeAppend passes, cannot begin with the fence,
// so it parses to nil without anyone having to ask "am I appending?".
//
// Shared with `ark ui patterns`, which keys on description where this
// collection keys on summary (R3277).
// CRC: crc-Patterns.md | R3275, R3277
func ParseFrontmatter(content []byte) map[string]string {
	s := string(content)
	if !strings.HasPrefix(s, frontmatterFence+"\n") {
		return nil
	}
	rest := s[len(frontmatterFence)+1:]
	end := strings.Index(rest, "\n"+frontmatterFence)
	if end < 0 {
		return nil
	}
	fields := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		fields[key] = strings.TrimSpace(val)
	}
	return fields
}

// splitTags turns "a, b ,c" into ["a","b","c"], dropping empties. Never
// returns nil, so the JSON surface emits [] rather than null (R3272).
// CRC: crc-Patterns.md | R3272
func splitTags(s string) []string {
	out := []string{}
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// patternFrom builds a Pattern from a file's content, reporting false when
// the file is not a usable pattern (no frontmatter, or no name).
// CRC: crc-Patterns.md | R3271
func patternFrom(path string, content []byte) (Pattern, bool) {
	fields := ParseFrontmatter(content)
	if fields == nil || fields[fmKeyName] == "" {
		return Pattern{}, false
	}
	return Pattern{
		Slug:    PatternSlug(path),
		Name:    fields[fmKeyName],
		Tags:    splitTags(fields[fmKeyTags]),
		Summary: fields[fmKeySummary],
		Path:    path,
	}, true
}

// List returns every usable pattern in the collection, ordered by slug.
//
// Slug order rather than directory order because the block this feeds is
// pasted into prompts and CLAUDE.md files: an unstable order would churn
// whatever file holds it (R3269). An absent or empty directory is not an
// error — a new installation has no patterns yet (R3271).
// CRC: crc-Patterns.md | R3267, R3269, R3271
func (p *Patterns) List() ([]Pattern, error) {
	if p.dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Pattern
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), patternExt) {
			continue
		}
		path := filepath.Join(p.dir, e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if pat, ok := patternFrom(path, content); ok {
			out = append(out, pat)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// WriteBlock renders the injection block. Its bytes are the contract — this
// output is pasted into system prompts — so the shape is fixed here rather
// than left to callers (R3269).
// CRC: crc-Patterns.md | R3269
func (p *Patterns) WriteBlock(w io.Writer) error {
	pats, err := p.List()
	if err != nil {
		return err
	}
	if len(pats) == 0 {
		return nil
	}
	// Rendered whole, then written once: the block is small, and a partial
	// write on error would leave a truncated prompt fragment behind.
	var b strings.Builder
	fmt.Fprintf(&b, blockHeaderFmt, p.dir)
	for _, pat := range pats {
		fmt.Fprintf(&b, "- **%s** (%s%s)\n", pat.Name, pat.Slug, patternExt)
		if len(pat.Tags) > 0 {
			fmt.Fprintf(&b, "  Tags: %s\n", strings.Join(pat.Tags, ", "))
		}
		if pat.Summary != "" {
			fmt.Fprintf(&b, "  %s\n", pat.Summary)
		}
		b.WriteString("\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// WriteJSON renders the machine surface, in the same slug order.
// CRC: crc-Patterns.md | R3272
func (p *Patterns) WriteJSON(w io.Writer) error {
	pats, err := p.List()
	if err != nil {
		return err
	}
	if pats == nil {
		pats = []Pattern{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(pats)
}

// Add writes a new pattern file and returns its path.
//
// Refuses an existing slug rather than overwriting: a pattern is edited in
// place, and a silent clobber would destroy a body nobody asked to delete
// (R3273).
// CRC: crc-Patterns.md | R3273
func (p *Patterns) Add(slug, name, summary, tags string, body []byte) (string, error) {
	if p.dir == "" {
		return "", fmt.Errorf("no patterns directory configured")
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return "", fmt.Errorf("slug is required")
	}
	if name == "" || summary == "" {
		return "", fmt.Errorf("--name and --summary are required")
	}
	path := filepath.Join(p.dir, slug+patternExt)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("pattern %q already exists at %s — edit it in place", slug, path)
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(frontmatterFence + "\n")
	fmt.Fprintf(&b, "%s: %s\n", fmKeyName, name)
	// Split-and-rejoin rather than passing the raw flag through: it
	// normalizes spacing so every file in the collection reads the same,
	// and it is the same splitter List uses, so add/read cannot disagree.
	if norm := splitTags(tags); len(norm) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", fmKeyTags, strings.Join(norm, ", "))
	}
	fmt.Fprintf(&b, "%s: %s\n", fmKeySummary, summary)
	b.WriteString(frontmatterFence + "\n\n")
	fmt.Fprintf(&b, "# %s\n", name)
	if len(body) > 0 {
		b.WriteString("\n")
		b.Write(body)
		if !strings.HasSuffix(string(body), "\n") {
			b.WriteString("\n")
		}
	}
	// O_EXCL so a racing writer loses rather than silently merging.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.WriteString(f, b.String()); err != nil {
		return "", err
	}
	return path, nil
}

// DerivedDef reports the tag definition a pattern file contributes: its slug
// defined as its summary. No @tag: line is written into the file — the
// frontmatter stays the single source of truth and the definition is computed
// from it, so the two cannot disagree (R3274).
//
// Scoped to *.md directly inside the collection, and to content that actually
// carries frontmatter. The second test is what makes the append path derive
// nothing without either path knowing about the other (R3275).
// CRC: crc-Patterns.md | Seq: seq-patterns-derive.md#1.1.3 | R3274, R3275
func (p *Patterns) DerivedDef(path string, content []byte) (name, desc string, ok bool) {
	if p == nil || p.dir == "" {
		return "", "", false
	}
	if !strings.EqualFold(filepath.Ext(path), patternExt) {
		return "", "", false
	}
	if filepath.Dir(filepath.Clean(path)) != p.dir {
		return "", "", false
	}
	fields := ParseFrontmatter(content)
	if fields == nil || fields[fmKeyName] == "" || fields[fmKeySummary] == "" {
		return "", "", false
	}
	return PatternSlug(path), fields[fmKeySummary], true
}

// StripFrontmatter returns the content with a leading YAML frontmatter block
// removed, and reports whether one was found.
//
// Deliberately narrow: it removes the fenced block at the head of the file and
// nothing else. The body's `@tag:` lines look like metadata and are not — they
// carry the pattern's origin and its connections to neighbours, which the
// listing never showed and which are the reason a reader opened the file
// (R3297). A "strip the metadata" implementation that also took those would
// remove exactly what `show` exists to deliver.
// CRC: crc-Patterns.md | R3297
func StripFrontmatter(content []byte) ([]byte, bool) {
	s := string(content)
	if !strings.HasPrefix(s, frontmatterFence+"\n") {
		return content, false
	}
	rest := s[len(frontmatterFence)+1:]
	end := strings.Index(rest, "\n"+frontmatterFence)
	if end < 0 {
		return content, false
	}
	body := rest[end+len("\n"+frontmatterFence):]
	return []byte(strings.TrimLeft(body, "\n")), true
}

// Show writes one pattern's body, without its frontmatter, under a single
// header line naming it (R3297).
//
// The header is what keeps the output attributable: `list` already carried the
// name, but this text gets pasted onward, and a body that has lost its name is
// a technique nobody can trace back or correct.
// CRC: crc-Patterns.md | R3297
func (p *Patterns) Show(w io.Writer, slug string) error {
	if p.dir == "" {
		return fmt.Errorf("no pattern collection configured")
	}
	path := filepath.Join(p.dir, slug+patternExt)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Name the directory: the likeliest cause is a collection that is
			// not where the reader assumed, and the slug alone would not say so.
			return fmt.Errorf("no pattern %q in %s", slug, p.dir)
		}
		return err
	}
	name := slug
	if pat, ok := patternFrom(path, content); ok {
		name = pat.Name
	}
	body, _ := StripFrontmatter(content)
	// `patterns add` writes a `# NAME` heading, so a well-formed pattern already
	// opens with its own name. Emitting the header on top of that would print
	// the name twice — in the one verb whose purpose is spending fewer tokens.
	// Drop the body's copy, but only when it *is* the name: an H1 saying
	// something else is content, and removing it would lose a section.
	body = dropLeadingHeading(body, name)
	if _, err := fmt.Fprintf(w, "# %s (%s%s)\n\n", name, slug, patternExt); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// dropLeadingHeading removes a leading `# <name>` line when it repeats name,
// along with the blank line after it. Comparison is trimmed and
// case-insensitive; anything else is left alone.
// CRC: crc-Patterns.md | R3297
func dropLeadingHeading(body []byte, name string) []byte {
	s := string(body)
	line, rest, found := strings.Cut(s, "\n")
	if !found {
		return body
	}
	h, ok := strings.CutPrefix(strings.TrimSpace(line), "# ")
	if !ok || !strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(name)) {
		return body
	}
	return []byte(strings.TrimLeft(rest, "\n"))
}
