package ark

// CRC: crc-Store.md | Test: test-TagSourceParity.md | R2344, R2345, R2346,
// R2347, R2348, R2349, R2350, R2351, R2352, R2353, R2354

import (
	"sort"
	"testing"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// paritySetup wires a Store with an ExtMap and TmpTagStore so that
// each tag source (inline T/F/V, ext-routed virtual, tmp:// overlay)
// can be populated independently. Returns the wired triple and a
// resolver-stub setup that lets FileTagValues / TagFiles iterate
// chunk/file relationships.
type parityFixture struct {
	store *Store
	ext   *ExtMap
	tmp   *TmpTagStore
}

func setupParity(t *testing.T) *parityFixture {
	t.Helper()
	s := testStore(t)
	ext := NewExtMap()
	tmp := NewTmpTagStore(s.TvidMap())
	s.SetExtMap(ext)
	s.SetTmpTagStore(tmp)

	// Single-file mapping for the inline side: persistent fileID 1
	// owns persistent chunkID 100. Overlay fileID 0xFFFE owns overlay
	// chunkID 0xFFF0. TmpTagStore handles the overlay mapping internally;
	// the inline side gets a minimal resolver stub.
	s.SetChunkResolver(
		func(_ *lmdb.Txn, _ uint64) []uint64 { return nil },
		func(fileID uint64) []uint64 {
			if fileID == 1 {
				return []uint64{100}
			}
			return nil
		},
	)
	return &parityFixture{store: s, ext: ext, tmp: tmp}
}

// addInline writes T/F/V records for chunkID 100 belonging to fileID 1.
func (f *parityFixture) addInline(t *testing.T, tag, value string) {
	t.Helper()
	err := f.store.UpdateTagValues([]ChunkTagValues{{
		ChunkID: 100,
		FileID:  1,
		Values:  []TagValue{{Tag: tag, Value: value}},
	}})
	if err != nil {
		t.Fatalf("addInline %s=%s: %v", tag, value, err)
	}
}

// addExt records an ext-routed (tag, value) pair targeting chunkID
// `target` with `count` virtual contributions. Wires the minimum ExtMap
// state required by VirtualTagNames/VirtualTagValues/RoutedTagsForChunk
// /ExtTagFiles/ExtTagValueChunks.
func (f *parityFixture) addExt(t *testing.T, tvidExt, target uint64, tag, value string, count int) {
	t.Helper()
	f.ext.mu.Lock()
	defer f.ext.mu.Unlock()
	f.ext.routedTagsByTvidExt[tvidExt] = append(
		f.ext.routedTagsByTvidExt[tvidExt],
		TagValue{Tag: tag, Value: value},
	)
	f.ext.targetToChunk[tvidExt] = append(f.ext.targetToChunk[tvidExt], target)
	f.ext.chunkToTargets[target] = append(f.ext.chunkToTargets[target], tvidExt)
	f.ext.virtualTagCount[tag] += count
}

// addTmp writes overlay T/F/V via TmpTagStore for overlay fileID/chunkID.
func (f *parityFixture) addTmp(t *testing.T, tag, value string) {
	t.Helper()
	const overlayFID = uint64(0xFFFFFFFFFFFFFFFE)
	const overlayCID = uint64(0xFFFFFFFFFFFFFFF0)
	f.tmp.UpdateTagValues(overlayFID, []ChunkTagValues{{
		ChunkID: overlayCID,
		FileID:  overlayFID,
		Values:  []TagValue{{Tag: tag, Value: value}},
	}})
}

// --- Tests ---

// MatchTagNames must find names from all three sources. (R2349)
func TestMatchTagNamesParity(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "shared-inline", "x")
	f.addExt(t, 1000, 100, "shared-ext", "y", 1)
	f.addTmp(t, "shared-tmp", "z")

	got, err := f.store.MatchTagNames([]string{"shared"})
	if err != nil {
		t.Fatalf("MatchTagNames: %v", err)
	}
	want := []string{"shared-ext", "shared-inline", "shared-tmp"}
	sort.Strings(got)
	if !equalStrings(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

// ListTags must include names from all three sources. (R2345)
func TestListTagsParity(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "list-inline", "x")
	f.addExt(t, 1001, 100, "list-ext", "y", 1)
	f.addTmp(t, "list-tmp", "z")

	got, err := f.store.ListTags()
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	names := make(map[string]bool)
	for _, tc := range got {
		names[tc.Tag] = true
	}
	for _, want := range []string{"list-inline", "list-ext", "list-tmp"} {
		if !names[want] {
			t.Errorf("ListTags missing %q (got %+v)", want, got)
		}
	}
}

// TagCounts must sum across all three sources. (R2346)
func TestTagCountsParity(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "count-tag", "v1")
	f.addExt(t, 1002, 100, "count-tag", "v2", 3) // count=3
	f.addTmp(t, "count-tag", "v3")

	got, err := f.store.TagCounts([]string{"count-tag"})
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 count, got %d", len(got))
	}
	// Inline V records produce one T total (1); ext virtual adds 3;
	// tmp overlay adds 1 (one chunk in TmpTagStore.tagCounts).
	if got[0].Count != 5 {
		t.Errorf("count-tag: want 5 (1 inline + 3 ext + 1 tmp), got %d", got[0].Count)
	}
}

// QueryTagValues must include values from all three sources. (R2347)
func TestQueryTagValuesParity(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "qv-tag", "inline-val")
	f.addExt(t, 1003, 100, "qv-tag", "ext-val", 1)
	f.addTmp(t, "qv-tag", "tmp-val")

	got, err := f.store.QueryTagValues("qv-tag", "")
	if err != nil {
		t.Fatalf("QueryTagValues: %v", err)
	}
	values := make(map[string]bool)
	for _, tvc := range got {
		values[tvc.Value] = true
	}
	for _, want := range []string{"inline-val", "ext-val", "tmp-val"} {
		if !values[want] {
			t.Errorf("QueryTagValues missing %q (got %+v)", want, got)
		}
	}
}

// MatchTagValues must include values from all three sources. (R2350)
func TestMatchTagValuesParity(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "mv-tag", "shared-inline")
	f.addExt(t, 1004, 100, "mv-tag", "shared-ext", 1)
	f.addTmp(t, "mv-tag", "shared-tmp")

	got, err := f.store.MatchTagValues("mv-tag", []string{"shared"})
	if err != nil {
		t.Fatalf("MatchTagValues: %v", err)
	}
	values := make(map[string]bool)
	for _, m := range got {
		values[m.Value] = true
	}
	for _, want := range []string{"shared-inline", "shared-ext", "shared-tmp"} {
		if !values[want] {
			t.Errorf("MatchTagValues missing %q (got %+v)", want, got)
		}
	}
}

// AllTagsForChunk unions inline tags with ext-routed virtual tags
// targeting the same chunk. (R2351)
func TestAllTagsForChunkParity(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "atc-inline", "ix")
	f.addExt(t, 1005, 100, "atc-ext", "ex", 1)

	got, err := f.store.AllTagsForChunk(100)
	if err != nil {
		t.Fatalf("AllTagsForChunk: %v", err)
	}
	pairs := make(map[string]string)
	for _, tv := range got {
		pairs[tv.Tag] = tv.Value
	}
	if pairs["atc-inline"] != "ix" {
		t.Errorf("AllTagsForChunk missing atc-inline=ix (got %+v)", got)
	}
	if pairs["atc-ext"] != "ex" {
		t.Errorf("AllTagsForChunk missing atc-ext=ex (got %+v)", got)
	}
}

// TagsForChunk stays strictly inline. Routings onto the chunk should
// NOT appear via TagsForChunk; only AllTagsForChunk surfaces them.
// (R2344 exception clause, R2351)
func TestTagsForChunkInlineOnly(t *testing.T) {
	f := setupParity(t)
	f.addInline(t, "tfc-inline", "v")
	f.addExt(t, 1006, 100, "tfc-ext", "v", 1)

	got, err := f.store.TagsForChunk(100)
	if err != nil {
		t.Fatalf("TagsForChunk: %v", err)
	}
	tags := make(map[string]bool)
	for _, tv := range got {
		tags[tv.Tag] = true
	}
	if !tags["tfc-inline"] {
		t.Errorf("TagsForChunk should return inline tag (got %+v)", got)
	}
	if tags["tfc-ext"] {
		t.Errorf("TagsForChunk should NOT return ext-routed tag (got %+v)", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
