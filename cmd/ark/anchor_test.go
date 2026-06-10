package main

import "testing"

// Sleeping Sentry for R2958: relative -files/-exclude-files globs resolve
// cwd-relative; absolute / ~ / tmp:// pass through untouched.
func TestAnchorFilterToCwd(t *testing.T) {
	const cwd = "/home/deck/work/ark"
	cases := []struct{ glob, want string }{
		{"specs/**", "/home/deck/work/ark/specs/**"},             // relative with slash
		{"*.jsonl", "/home/deck/work/ark/*.jsonl"},               // relative, no slash → cwd top-level
		{"**/*.jsonl", "/home/deck/work/ark/**/*.jsonl"},         // recursive stays under cwd
		{"./specs/**", "/home/deck/work/ark/specs/**"},           // ./ cleaned by Join
		{"../sib/specs/**", "/home/deck/work/sib/specs/**"},      // .. escapes cwd, UNIX-faithful
		{"specs/", "/home/deck/work/ark/specs/"},                 // trailing slash preserved
		{"/abs/specs/**", "/abs/specs/**"},                       // absolute untouched
		{"~/notes/**", "~/notes/**"},                             // home untouched (ExpandTilde handles later)
		{"tmp://ARK-RECALL/dm-x.md", "tmp://ARK-RECALL/dm-x.md"}, // virtual overlay untouched
		{"", ""}, // empty untouched
	}
	for _, c := range cases {
		if got := anchorFilterToCwd(c.glob, cwd); got != c.want {
			t.Errorf("anchorFilterToCwd(%q) = %q, want %q", c.glob, got, c.want)
		}
	}
}
