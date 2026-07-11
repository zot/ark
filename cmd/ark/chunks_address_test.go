package main

import "testing"

// TestResolveChunksTarget covers the last-colon split: the range is any
// non-empty colon-free Location (line N-M, PDF PAGE/KIND/N), so the split is
// unambiguous even with a colon in the path or a tmp:// scheme. R3078
func TestResolveChunksTarget(t *testing.T) {
	cases := []struct {
		name                            string
		args                            []string
		wantPath, wantRange, wantAnchor string
		wantErr                         bool
	}{
		{"chunkID", []string{"123"}, "", "123", "", false},
		{"line range", []string{"/a/b.md:3-9"}, "/a/b.md", "3-9", "", false},
		{"pdf range", []string{"/x/f.pdf:1/para/1"}, "/x/f.pdf", "1/para/1", "", false},
		{"colon in path", []string{"/weird:name.md:3-9"}, "/weird:name.md", "3-9", "", false},
		{"tmp scheme", []string{"tmp://a/b:3-9"}, "tmp://a/b", "3-9", "", false},
		{"snippet", []string{`/a/b.md:3-9:"hi there"`}, "/a/b.md", "3-9", "hi there", false},
		{"two-arg", []string{"/a/b.md", "3-9"}, "/a/b.md", "3-9", "", false},
		{"no colon, not digits", []string{"hello"}, "", "", "", true},
		{"missing", []string{}, "", "", "", true},
	}
	for _, tc := range cases {
		p, r, a, err := resolveChunksTarget(tc.args)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", tc.name, err, tc.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if p != tc.wantPath || r != tc.wantRange || a != tc.wantAnchor {
			t.Errorf("%s: got (%q,%q,%q) want (%q,%q,%q)", tc.name, p, r, a, tc.wantPath, tc.wantRange, tc.wantAnchor)
		}
	}
}
