package ark

// CRC: crc-Server.md | Seq: seq-link-resolve.md | R3378, R3379
//
// The link-view selector's server door. links_test.go covers the table's own
// views against a bare LinkTable, deliberately with no DB and no server; this
// file covers the half that exists only because a server owns the index —
// POST /files/expand — and it asserts the two doors AGREE rather than testing
// either alone. Design: design/test-LinkTable.md

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Both doors answer `--links` with the same set (R3379).
//
// The cold path expands locally through LinkTable.ExpandMatches. The proxied
// path must ask the server, because bbolt's exclusive lock means the CLI holds
// no link table while a server owns the index — which is the whole reason
// R3378's match-then-expand needed a second door at all (O212).
//
// Asserting the *agreement* rather than either door alone is deliberate. One
// behaviour reachable by two paths with only one tested is what let O208 live
// for as long as both paths existed, and it is the third instance of that
// shape in this repo.
//
// Fire alarm (test-LinkTable.md): make handleFilesExpand echo req.Paths back
// unchanged — the shape this door had while O212 was open — and this must go
// red. The property is invisible when absent: an unexpanded answer under a
// `--links` banner is byte-identical to what the default view returns.
// CRC: crc-Server.md | R3378, R3379
func TestFilesExpandDoorsAgree(t *testing.T) {
	db, canonical, alias := extLinkTestDB(t)
	// The handler rides the DB actor (Sync), and newTestDB leaves db.svc nil —
	// so start one as DB.Open does, the same wiring recall_test.go performs.
	// Hand-wiring in a fixture is the shape that hid the withFTS defect for
	// three months, which is why this is the actor and nothing else: the
	// full-*DB harness that would remove the hand-wiring is PENDING #121.
	db.svc = newChanSvc(chanSvcSlots)
	db.svc.start()
	t.Cleanup(func() {
		if db.svc != nil {
			db.svc.stop()
			db.svc = nil
		}
	})
	srv := &Server{db: db}

	matched := []string{canonical}
	cold := db.LinkTable().ExpandMatches(matched)
	if !hasAddr(cold, alias) {
		t.Fatalf("fixture: the cold door did not expand %q to %q (got %v) — the comparison below would be vacuous", canonical, alias, cold)
	}

	body, err := json.Marshal(map[string]any{"paths": matched})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleFilesExpand(rec, httptest.NewRequest(http.MethodPost, "/files/expand", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /files/expand = %d: %s", rec.Code, rec.Body.String())
	}
	var hot []string
	if err := json.Unmarshal(rec.Body.Bytes(), &hot); err != nil {
		t.Fatalf("decoding the server's answer: %v (body %q)", err, rec.Body.String())
	}

	if !sameAddrSet(cold, hot) {
		t.Errorf("the doors disagree about --links: cold path %v, server %v", cold, hot)
	}
}

// `--status` answers in the canonical namespace whatever spelling the query
// used (R3376). The plain listing is the opposite view by design — it matches
// addresses and keeps the caller's prefix (R3364) — so `--status` is how a
// caller discovers that two listed addresses are one file, and that only works
// if an alias-spelled query reaches the file at all.
//
// Fire alarm (test-LinkTable.md): delete the ResolvePattern loop in
// handleFilesStatus and this must go red. Invisible when absent: an
// alias-spelled query that resolves nothing returns an empty listing, which is
// indistinguishable from a pattern that genuinely matches no file.
// CRC: crc-Server.md | R3376
func TestFilesStatusAnswersCanonicalForAliasQuery(t *testing.T) {
	db, canonical, alias := extLinkTestDB(t)
	db.svc = newChanSvc(chanSvcSlots)
	db.svc.start()
	t.Cleanup(func() {
		if db.svc != nil {
			db.svc.stop()
			db.svc = nil
		}
	})
	srv := &Server{db: db}

	status := func(pattern string) []fileStatusEntry {
		t.Helper()
		body, err := json.Marshal(map[string]any{"patterns": []string{pattern}})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		srv.handleFilesStatus(rec, httptest.NewRequest(http.MethodPost, "/files/status", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /files/status %q = %d: %s", pattern, rec.Code, rec.Body.String())
		}
		var out []fileStatusEntry
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding status for %q: %v (body %q)", pattern, err, rec.Body.String())
		}
		return out
	}

	// Guard the guard: without this the alias assertion below proves nothing,
	// since a corpus that matches nothing answers empty for every spelling.
	if got := status(canonical); len(got) != 1 || got[0].Path != canonical {
		t.Fatalf("control: the canonical spelling answered %+v, want exactly one entry at %q", got, canonical)
	}

	got := status(alias)
	if len(got) != 1 {
		t.Fatalf("the alias spelling %q answered %+v, want exactly one entry", alias, got)
	}
	if got[0].Path != canonical {
		t.Errorf("--status reported %q for an alias-spelled query, want the canonical %q", got[0].Path, canonical)
	}
}

// sameAddrSet compares two address answers as sets. Order is not part of the
// contract — both sides dedupe through a map — but membership is.
func sameAddrSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}
