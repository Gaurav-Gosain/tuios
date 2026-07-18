package app

import (
	"os"
	"testing"
)

// TestSixelVisibleWhenScrollbackExceedsHeight reproduces the viewport-math bug:
// once a window's scrollback grew past its content height, the sixel refresh
// computed relativeY with an extra -contentHeight term, so hostY fell past the
// window bottom and the bottom-edge guards hid the image. The image must stay
// visible at its true on-screen row, matching the kitty convention.
func TestSixelVisibleWhenScrollbackExceedsHeight(t *testing.T) {
	prevCaps := cachedCapabilities
	cachedCapabilities = &HostCapabilities{CellWidth: 9, CellHeight: 20, Cols: 80, Rows: 40}
	t.Cleanup(func() { cachedCapabilities = prevCaps })

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })
	sp := NewSixelPassthroughWithOptions(SixelPassthroughOptions{ForceEnable: true, Output: devnull})
	sp.enabled = true

	winID := "test-window-id-abcdef12"

	const (
		scrollbackLen = 100
		contentHeight = 24 // window height, borderless
		onScreenRow   = 2
	)
	// Image placed at on-screen row 2 while at the bottom of 100 lines of
	// scrollback: AbsoluteLine = scrollbackLen + cursorY.
	sp.placements[winID] = []*SixelPassthroughPlacement{
		{
			WindowID:     winID,
			AbsoluteLine: scrollbackLen + onScreenRow,
			GuestX:       0,
			Width:        90,
			Height:       100,
			Rows:         5,
			Cols:         10,
			Hidden:       true,
			RawSequence:  []byte("0;1;0;0#0"),
		},
	}

	sp.RefreshAllPlacements(func(windowID string) *WindowPositionInfo {
		if windowID != winID {
			return nil
		}
		return &WindowPositionInfo{
			WindowX: 0, WindowY: 0,
			Width: 80, Height: contentHeight,
			ContentOffsetX: 0, ContentOffsetY: 0,
			Visible:       true,
			ScrollbackLen: scrollbackLen,
			ScrollOffset:  0,
			ScreenWidth:   80,
			ScreenHeight:  40,
		}
	})

	p := sp.placements[winID][0]
	if p.Hidden {
		t.Fatal("BUG: sixel hidden when scrollback exceeds window height; image reserves space but never displays")
	}
	if p.HostY != onScreenRow {
		t.Errorf("expected hostY=%d (on-screen row), got %d", onScreenRow, p.HostY)
	}
}
