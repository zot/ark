# Test Design: PtyBrowser
**Source:** crc-PtyBrowser.md

The deterministic, transport-free logic of the browser transport is the
control-frame parser (`parsePtyControl`). The websocket wiring itself (upgrade,
attach, read loop, the first-frame-must-be-resize precondition) is
raw-connection code driven live, banked as a gap alongside O149/O151 — it needs
a real browser or websocket client. These tests pin the parser.

## Test: resize control frame
**Purpose:** a well-formed resize parses to its cols/rows (R3143).
**Input:** `{"t":"resize","cols":120,"rows":40}`.
**Expected:** kind = resize, size = {Cols: 120, Rows: 40}, no error.

## Test: repaint control frame
**Purpose:** a repaint frame parses to a repaint request (R3143).
**Input:** `{"t":"repaint"}`.
**Expected:** kind = repaint, no error.

## Test: malformed JSON
**Purpose:** non-JSON control text is an error, not a panic (R3143).
**Input:** `not json`.
**Expected:** error returned; no kind.

## Test: unknown type
**Purpose:** an unknown `t` value is an error (R3143).
**Input:** `{"t":"frobnicate"}`.
**Expected:** error returned.

**Refs:** crc-PtyBrowser.md, R3143
