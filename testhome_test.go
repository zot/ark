package ark

import "testing"

// setTestHome points the user's home directory at dir for the length of the
// test. Go's os.UserHomeDir reads HOME on Unix and USERPROFILE on Windows, so
// a test that sets only HOME writes into the real Windows home: measured
// 2026-10-26, mirror writes from one test landed in the VM user's ~/.ark and
// failed another test that scanned it.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
