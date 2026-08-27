package theme

import (
	"image/color"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The ink a per-type file icon burns on the rail.
//
// The hex a file icon arrives with is absolute. It comes from
// nvim-web-devicons, which picked it against an editor's own dark ground and
// knows nothing about the theme the user is running. Drawn as given, a ruby red
// (#701516) measures 1.3:1 on a dark background and a dotenv yellow (#FAF743)
// measures 1.1:1 on a light one, so one end of the palette or the other is
// simply gone.
//
// So the hue is kept and the luminance is spent. ReadableAt carries the colour
// toward the ground's own text end until it clears MarkFloor, which is the 3:1
// WCAG 2.1 holds a non-text graphic to. An icon is a shape as well as a colour,
// which is why it is held to the mark floor rather than to the 4.5:1 the name
// beside it clears: lifting every colour to text contrast is what turns the
// ruby red into pink and loses the thing the colour was for.

// RailGround is what the rail's rows are drawn on.
//
// The rail paints no fill of its own, so a row sits on whatever the terminal's
// background is, which is the theme's when a theme is on. Without a theme tuios
// paints nothing and cannot ask, so the ground is the chrome ramp's own canvas,
// which is what every constant ink in the rail was picked against.
func RailGround() color.Color {
	if t := Current(); t != nil {
		return t.Bg
	}
	return uiCanvas
}

// fileIconMemo holds the answers per ground. ReadableAt walks up to sixteen
// blends measuring a contrast ratio at each, and the icons are read once per
// drawn row on every rail rebuild, so the walk is done once per colour and kept.
//
// Four grounds rather than one because a rail draws on more than one at a time:
// the resting rows sit on the terminal's background and the row under the
// pointer sits on a band of its own, and a single slot would be cleared and
// refilled on every one of those transitions.
var fileIconMemo struct {
	sync.Mutex
	ground [4][4]uint32
	ink    [4]map[string]color.Color
	next   int
}

// FileIconInk is the ink a file icon of the given hex burns on the rail's own
// ground. hex is "#RRGGBB".
func FileIconInk(hex string) color.Color { return FileIconInkOn(hex, RailGround()) }

// FileIconInkOn is FileIconInk against a ground the caller paints itself, like
// the band under the pointer.
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
