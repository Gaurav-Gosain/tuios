package theme

import (
	"image/color"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// RailGround is what the rail's rows are drawn on.
//
// The rail paints no fill of its own, so a row sits on whatever the terminal's
// background is, which is the theme's when a theme is on. Without a theme the
// ground is the host terminal's own background, which only a client knows (a
// server process has several, one per client), so a client that has asked
// passes it to GroundUIOn and RailRuleOn itself. This package on its own falls
// back to the chrome ramp's canvas, which is what every constant ink in the
// rail was picked against.
func RailGround() color.Color {
	if t := Current(); t != nil {
		return AsColor(t.Bg)
	}
	return uiCanvas
}

var fileIconMemo struct {
	sync.Mutex
	ground [4][4]uint32
	ink    [4]map[string]color.Color
	next   int
}

// FileIconInkOn is the ink a file icon of the given hex ("#RRGGBB") burns on
// bg, a ground the caller paints itself, like the rail or the band under the
// pointer.
func FileIconInkOn(hex string, bg color.Color) color.Color {
	r, g, b, a := bg.RGBA()
	key := [4]uint32{r, g, b, a}

	fileIconMemo.Lock()
	defer fileIconMemo.Unlock()

	slot := -1
	for i, held := range fileIconMemo.ink {
		if held != nil && fileIconMemo.ground[i] == key {
			slot = i
			break
		}
	}
	if slot < 0 {
		slot = fileIconMemo.next
		fileIconMemo.next = (fileIconMemo.next + 1) % len(fileIconMemo.ink)
		fileIconMemo.ground[slot] = key
		fileIconMemo.ink[slot] = make(map[string]color.Color, 64)
	}
	if ink, ok := fileIconMemo.ink[slot][hex]; ok {
		return ink
	}
	ink := overlay.ReadableAt(lipgloss.Color(hex), bg, overlay.MarkFloor)
	fileIconMemo.ink[slot][hex] = ink
	return ink
}
