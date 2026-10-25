# Test Design: PubSub excludes and routed delivery
**Source:** crc-PubSub.md

Covers only #165's delivery rules. The older pubsub tests in `pubsub_test.go`
predate this design and are not catalogued here.

## Test: a subscription never receives a fenced path
**Purpose:** the fence holds with or without explicit file filters (R3586)
**Input:** SetExcludes(fence=`**/fenced/**`, noise=nil); one sub with no filters, one with a positive `-files '**/fenced/**'`; Publish under `fenced/x.md`
**Expected:** neither queue receives the event
**Refs:** crc-PubSub.md
**Code:** pubsub_test.go
**Fire alarm:** check the fence only for subs with InheritNoise; red on the explicit-filter sub
**Inject:** pubsub.go:PubSub.admits
**Pulled:** 2026-10-25 — rang

## Test: inherited noise follows a config reload
**Purpose:** the inherited list is read at delivery, not copied at subscribe (R3595)
**Input:** subscribe with no filters while noise is empty; SetExcludes(noise=`**/noise/**`); Publish under `noise/x.md`
**Expected:** no event delivered
**Refs:** crc-PubSub.md
**Code:** pubsub_test.go
**Fire alarm:** snapshot the noise list into the sub's ExcludeFiles at Subscribe and clear InheritNoise; red, the event arrives
**Inject:** pubsub.go:PubSub.Subscribe
**Pulled:** 2026-10-25 — rang

## Test: explicit file filters replace inherited noise
**Purpose:** R941's rule holds under the new representation
**Input:** noise=`**/noise/**`; a sub with `-files '**/noise/**'`; Publish under `noise/x.md`
**Expected:** delivered
**Refs:** crc-PubSub.md
**Code:** pubsub_test.go
**Fire alarm:** apply the noise list regardless of `sub.InheritNoise`; red, nothing delivered. (The server's one-line computation of InheritNoise is not reached by a pubsub test; the live check covers it.)
**Inject:** pubsub.go:PubSub.admits
**Pulled:** 2026-10-25 — rang

## Test: a routed event passes inherited noise, not the fence
**Purpose:** routed values are exempt from search_exclude only (R3587, R3594)
**Input:** noise=`**/noise/**`, fence=`**/fenced/**`; an inheriting sub on `status`; PublishRouted under `noise/x.md` and under `fenced/y.md`, each with `status: done`
**Expected:** the `noise/` event is delivered, the `fenced/` one is not; Publish (not routed) under `noise/x.md` is not delivered
**Refs:** crc-PubSub.md
**Code:** pubsub_test.go
**Fire alarm:** pass routed=false from PublishRouted; red on the `noise/` event
**Inject:** pubsub.go:PubSub.PublishRouted
**Pulled:** 2026-10-25 — rang

## Test: indexing a mirror publishes its routed tags
**Purpose:** end to end, a mirror's index pass publishes the values its `@ext` routes, under the target's path, past the target's inherited noise; no ExtMap rebuild in between (R3594, R3587)
**Input:** setupFileBackedRecall; a target file; a PubSub on the indexer with noise naming the target; an inheriting sub on `note`; a mirror file (extMirrorSource) with `@ext: <target> @note: hello`
**Expected:** exactly one `note: hello` event, at the target's path
**Refs:** crc-PubSub.md, crc-Indexer.md, crc-ExtMap.md, seq-ext-routing.md
**Code:** pubsub_test.go
**Fire alarm:** drop the `publishRouted` call after the publish in the add path; red, no note event
**Inject:** indexer.go:Indexer.afterAddStore
**Pulled:** 2026-10-25 — rang
