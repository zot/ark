package ark

// CRC: crc-PubSub.md | Test: test-TmpSubscription.md

import (
	"testing"
	"time"
)

// mustParseSub builds a TagSub from a sigil-form query; panics on
// parse error so test failures are loud at construction time.
func mustParseSub(t testing.TB, q string) MatchPredicate {
	t.Helper()
	p, err := ParseMatchSyntax(q)
	if err != nil {
		t.Fatalf("ParseMatchSyntax(%q): %v", q, err)
	}
	return p
}

// TestCompressBatch dedupes (path, tag) keeping the latest. R2295, R2310.
func TestCompressBatch(t *testing.T) {
	events := []Event{
		{Path: "p", Tag: "x", Value: "1", Time: time.Unix(0, 1)},
		{Path: "p", Tag: "x", Value: "2", Time: time.Unix(0, 2)},
		{Path: "p", Tag: "y", Value: "a", Time: time.Unix(0, 3)},
		{Path: "p", Tag: "x", Value: "3", Time: time.Unix(0, 4)},
		{Path: "q", Tag: "x", Value: "1", Time: time.Unix(0, 5)},
	}
	got := CompressBatch(events)
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d (%+v)", len(got), got)
	}
	// First survivor: (p, x) = "3"
	// Second survivor: (p, y) = "a"
	// Third survivor: (q, x) = "1"
	for _, e := range got {
		switch {
		case e.Path == "p" && e.Tag == "x":
			if e.Value != "3" {
				t.Errorf("(p,x) want value 3, got %q", e.Value)
			}
		case e.Path == "p" && e.Tag == "y":
			if e.Value != "a" {
				t.Errorf("(p,y) want value a, got %q", e.Value)
			}
		case e.Path == "q" && e.Tag == "x":
			if e.Value != "1" {
				t.Errorf("(q,x) want value 1, got %q", e.Value)
			}
		default:
			t.Errorf("unexpected event: %+v", e)
		}
	}
}

func TestCompressBatchEmptyAndSingle(t *testing.T) {
	if got := CompressBatch(nil); got != nil {
		t.Errorf("nil input: want nil, got %v", got)
	}
	one := []Event{{Path: "p", Tag: "x", Value: "v"}}
	if got := CompressBatch(one); len(got) != 1 || got[0].Value != "v" {
		t.Errorf("single event passthrough: got %+v", got)
	}
}

// TestPubSubTmpFilterFirstClass — a literal tmp:// path in FilterFiles
// matches a Publish on that exact path. R2278.
func TestPubSubTmpFilterFirstClass(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	ps.Subscribe("test", []*TagSub{{
		Predicate:   mustParseSub(t, "connections-status"),
		FilterFiles: []string{"tmp://connections/req1.md"},
	}})
	ps.Publish("", "tmp://connections/req1.md", []TagValue{
		{Tag: "connections-status", Value: "completed"},
	})
	events := ps.Listen("test", 100*time.Millisecond)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Value != "completed" || events[0].Path != "tmp://connections/req1.md" {
		t.Errorf("unexpected event: %+v", events[0])
	}
}

// TestPubSubTmpGlobMatching — *.md glob matches tmp:// paths. R2278.
func TestPubSubTmpGlobMatching(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	ps.Subscribe("g", []*TagSub{{
		Predicate:   mustParseSub(t, "status"),
		FilterFiles: []string{"tmp://prospector/*.md"},
	}})
	ps.Publish("", "tmp://prospector/a.md", []TagValue{{Tag: "status", Value: "ok"}})
	ps.Publish("", "tmp://prospector/b.md", []TagValue{{Tag: "status", Value: "ok"}})
	ps.Publish("", "tmp://other/c.md", []TagValue{{Tag: "status", Value: "ok"}})
	events := ps.Listen("g", 100*time.Millisecond)
	if len(events) != 2 {
		t.Fatalf("want 2 events (prospector/*), got %d (%+v)", len(events), events)
	}
}

// TestPublishTmpDiffOnlyOnChange — repeated publishes with identical
// content do not refire; changed values fire. R2284.
func TestPublishTmpDiffOnlyOnChange(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	ps.Subscribe("c", []*TagSub{{
		Predicate:   mustParseSub(t, "status"),
		FilterFiles: []string{"tmp://x.md"},
	}})
	body := []byte("@status: idle\n@kind: report\n")

	// First publish — empty cache, status fires.
	ps.PublishTmpDiff("", "tmp://x.md", body, "lines")
	evts := ps.Listen("c", 100*time.Millisecond)
	if len(evts) != 1 || evts[0].Value != "idle" {
		t.Fatalf("first publish: want one status=idle event, got %+v", evts)
	}

	// Second publish, identical body — nothing fires.
	ps.PublishTmpDiff("", "tmp://x.md", body, "lines")
	evts = ps.Listen("c", 100*time.Millisecond)
	if len(evts) != 0 {
		t.Errorf("identical re-publish: want zero events, got %+v", evts)
	}

	// Third publish, status changed — status fires; kind unchanged stays quiet.
	body2 := []byte("@status: running\n@kind: report\n")
	ps.PublishTmpDiff("", "tmp://x.md", body2, "lines")
	evts = ps.Listen("c", 100*time.Millisecond)
	if len(evts) != 1 {
		t.Fatalf("changed publish: want one event, got %+v", evts)
	}
	if evts[0].Tag != "status" || evts[0].Value != "running" {
		t.Errorf("want status=running, got %+v", evts[0])
	}
}

// TestPublishTmpAppend — appending content where new bytes carry a tag
// already present in prior content doesn't re-publish. R2286.
func TestPublishTmpAppend(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	ps.Subscribe("ap", []*TagSub{{
		Predicate:   mustParseSub(t, "topic"),
		FilterFiles: []string{"tmp://a.md"},
	}})
	// Seed cache as if AddTmpFile had run.
	ps.PublishTmpDiff("", "tmp://a.md", []byte("@topic: ark\n"), "lines")
	_ = ps.Listen("ap", 100*time.Millisecond) // drain initial event

	// Append content with same topic — should NOT fire.
	ps.PublishTmpAppend("", "tmp://a.md", []byte("@topic: ark\nmore body\n"), "lines")
	if evts := ps.Listen("ap", 100*time.Millisecond); len(evts) != 0 {
		t.Errorf("append of same tag: want zero events, got %+v", evts)
	}

	// Append content with NEW topic value — should fire.
	ps.PublishTmpAppend("", "tmp://a.md", []byte("@topic: leisure\n"), "lines")
	evts := ps.Listen("ap", 100*time.Millisecond)
	if len(evts) != 1 || evts[0].Value != "leisure" {
		t.Errorf("append of new tag: want topic=leisure, got %+v", evts)
	}
}

// TestClearTagSetCache — after clearing, the next PublishTmpDiff fires
// every tag again (cache treated as empty). R2287.
func TestClearTagSetCache(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	ps.Subscribe("r", []*TagSub{{
		Predicate:   mustParseSub(t, "status"),
		FilterFiles: []string{"tmp://r.md"},
	}})
	ps.PublishTmpDiff("", "tmp://r.md", []byte("@status: done\n"), "lines")
	_ = ps.Listen("r", 100*time.Millisecond) // drain

	// Without clear: identical publish doesn't fire.
	ps.PublishTmpDiff("", "tmp://r.md", []byte("@status: done\n"), "lines")
	if evts := ps.Listen("r", 100*time.Millisecond); len(evts) != 0 {
		t.Fatalf("without clear: want zero, got %+v", evts)
	}

	// After clear: same content fires again.
	ps.ClearTagSetCache("tmp://r.md")
	ps.PublishTmpDiff("", "tmp://r.md", []byte("@status: done\n"), "lines")
	if evts := ps.Listen("r", 100*time.Millisecond); len(evts) != 1 {
		t.Errorf("after clear: want one event, got %+v", evts)
	}
}

// TestSubCount — reports the number of active subscriptions per session. R2300.
func TestSubCount(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	if got := ps.SubCount("none"); got != 0 {
		t.Errorf("empty session: want 0, got %d", got)
	}
	ps.Subscribe("s", []*TagSub{
		{Predicate: mustParseSub(t, "a")},
		{Predicate: mustParseSub(t, "b")},
	})
	if got := ps.SubCount("s"); got != 2 {
		t.Errorf("after two subs: want 2, got %d", got)
	}
	ps.Cancel("s", "a", "")
	if got := ps.SubCount("s"); got != 1 {
		t.Errorf("after cancel a: want 1, got %d", got)
	}
	ps.Cancel("s", "", "")
	if got := ps.SubCount("s"); got != 0 {
		t.Errorf("after cancel-all: want 0, got %d", got)
	}
}

// TestQueueDepthAndLastListenAt — monitor read APIs report correctly. R2303, R2304.
func TestQueueDepthAndLastListenAt(t *testing.T) {
	ps := NewPubSub(time.Minute, 8)
	ps.Subscribe("m", []*TagSub{{Predicate: mustParseSub(t, "x")}})
	before := time.Now()

	// Three publishes; nothing drained yet.
	ps.Publish("", "p", []TagValue{{Tag: "x", Value: "1"}})
	ps.Publish("", "p", []TagValue{{Tag: "x", Value: "2"}})
	ps.Publish("", "p", []TagValue{{Tag: "x", Value: "3"}})

	if got := ps.QueueDepth("m"); got != 3 {
		t.Errorf("QueueDepth pre-drain: want 3, got %d", got)
	}
	if at := ps.LastListenAt("m"); !at.After(before.Add(-time.Second)) {
		t.Errorf("LastListenAt initial: want recent, got %v", at)
	}

	// Drain.
	events := ps.Listen("m", 100*time.Millisecond)
	if len(events) != 3 {
		t.Errorf("Listen drain: want 3, got %d", len(events))
	}
	if got := ps.QueueDepth("m"); got != 0 {
		t.Errorf("QueueDepth post-drain: want 0, got %d", got)
	}
}

// TestPublishBackpressureIncrementsDrops — queue overflow drops, does
// not block the publisher. R2302.
func TestPublishBackpressureIncrementsDrops(t *testing.T) {
	ps := NewPubSub(time.Minute, 4) // small queue
	sub := &TagSub{Predicate: mustParseSub(t, "x")}
	ps.Subscribe("b", []*TagSub{sub})

	for range 20 {
		ps.Publish("", "p", []TagValue{{Tag: "x", Value: "v"}})
	}
	hits := sub.Hits.Load()
	drops := sub.Drops.Load()
	if hits+drops != 20 {
		t.Errorf("hits+drops should equal 20 (got %d+%d=%d)", hits, drops, hits+drops)
	}
	if drops == 0 {
		t.Errorf("expected some drops with queue=4 and 20 publishes, got %d", drops)
	}
}

// TestReSubscribeReplaceByTag — re-subscribing on the same (session,
// tag) replaces the prior sub. R2290.
//
// Note: replace semantics live in the Lua bridge (cancel-then-subscribe),
// not in PubSub.Subscribe itself. This test exercises the underlying
// PubSub Cancel + Subscribe sequence that the Lua bridge uses.
func TestReSubscribeReplaceByTag(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	// Subscribe with filterFiles=[tmp://a.md]
	ps.Subscribe("r", []*TagSub{{
		Predicate:   mustParseSub(t, "x"),
		FilterFiles: []string{"tmp://a.md"},
	}})
	// Bridge would now run Cancel + Subscribe for replacement.
	ps.Cancel("r", "x", "")
	ps.Subscribe("r", []*TagSub{{
		Predicate:   mustParseSub(t, "x"),
		FilterFiles: []string{"tmp://b.md"},
	}})

	// A publish on the OLD path should not fire (prior sub was dropped).
	ps.Publish("", "tmp://a.md", []TagValue{{Tag: "x", Value: "v"}})
	if evts := ps.Listen("r", 100*time.Millisecond); len(evts) != 0 {
		t.Errorf("publish on old filterFiles: want zero, got %+v", evts)
	}

	// A publish on the NEW path should fire.
	ps.Publish("", "tmp://b.md", []TagValue{{Tag: "x", Value: "v"}})
	if evts := ps.Listen("r", 100*time.Millisecond); len(evts) != 1 {
		t.Errorf("publish on new filterFiles: want one, got %+v", evts)
	}
}

// TestPublishWithValueRE — value-regex filter applies via the
// new sigil syntax (R2446). Regression across the new substrate.
func TestPublishWithValueRE(t *testing.T) {
	ps := NewPubSub(time.Minute, 16)
	ps.Subscribe("re", []*TagSub{{
		Predicate: mustParseSub(t, "status~^(completed|errored)$"),
	}})
	ps.Publish("", "p", []TagValue{{Tag: "status", Value: "pending"}})
	ps.Publish("", "p", []TagValue{{Tag: "status", Value: "completed"}})
	ps.Publish("", "p", []TagValue{{Tag: "status", Value: "errored"}})
	evts := ps.Listen("re", 100*time.Millisecond)
	if len(evts) != 2 {
		t.Fatalf("want 2 events (completed+errored), got %d (%+v)", len(evts), evts)
	}
}
