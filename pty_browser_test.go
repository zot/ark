package ark

import "testing"

// Unit tests for the browser-transport control-frame parser (test-PtyBrowser.md).
// The websocket wiring itself (upgrade, attach, read loop) is driven live and
// banked with O149/O151; the deterministic parser is pinned here.
//
// CRC: crc-PtyBrowser.md | Test: test-PtyBrowser.md | R3143
func TestParsePtyControl(t *testing.T) {
	t.Run("resize", func(t *testing.T) {
		ctrl, err := parsePtyControl([]byte(`{"t":"resize","cols":120,"rows":40}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctrl.Kind != ptyCtrlResize {
			t.Fatalf("kind = %v, want resize", ctrl.Kind)
		}
		if ctrl.Size != (PtyWinsize{Cols: 120, Rows: 40}) {
			t.Fatalf("size = %+v, want {Cols:120 Rows:40}", ctrl.Size)
		}
	})
	t.Run("repaint", func(t *testing.T) {
		ctrl, err := parsePtyControl([]byte(`{"t":"repaint"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctrl.Kind != ptyCtrlRepaint {
			t.Fatalf("kind = %v, want repaint", ctrl.Kind)
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		if _, err := parsePtyControl([]byte(`not json`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
	t.Run("unknown type", func(t *testing.T) {
		if _, err := parsePtyControl([]byte(`{"t":"frobnicate"}`)); err == nil {
			t.Fatal("expected error for unknown type")
		}
	})
}
