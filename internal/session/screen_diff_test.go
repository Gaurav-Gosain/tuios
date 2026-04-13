package session

import (
	"testing"
)

func TestScreenDiffHasMouseMode(t *testing.T) {
	diff := &ScreenDiff{
		Width: 80, Height: 24,
		CursorX: 5, CursorY: 10,
		IsAltScreen:  true,
		HasMouseMode: true,
		Cells: []DiffCell{
			{Row: 0, Col: 0, Content: "x", Width: 1},
		},
	}

	encoded := EncodeScreenDiff("test-pty-id-00000000000000000000", diff)
	_, decoded, err := DecodeScreenDiff(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if !decoded.HasMouseMode {
		t.Error("HasMouseMode should be true after round-trip")
	}
	if !decoded.IsAltScreen {
		t.Error("IsAltScreen should be true")
	}

	// Test with HasMouseMode=false
	diff.HasMouseMode = false
	encoded2 := EncodeScreenDiff("test-pty-id-00000000000000000000", diff)
	_, decoded2, err := DecodeScreenDiff(encoded2)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded2.HasMouseMode {
		t.Error("HasMouseMode should be false")
	}
}
