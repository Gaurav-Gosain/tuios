package app

import (
	"image"
	"image/color"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The pane background (appearance.pane_background).
//
// A cell a program leaves on the default background carries no colour, and
// that is deliberate: the host terminal draws its own background through it,
// so tuios looks like the terminal it runs in. The option paints a ground
// there instead, either the theme's background or a colour of the user's.
//
// It is painted in the compositor, on the cells a pane's layer parsed to, and
// not in renderTerminal. Every way a pane reaches the screen goes through that
// one parse: the unfocused fast path that hands back the emulator's own
// Render(), the per-cell path, the cached content string, scrollback and copy
// mode, the zoomed pane, floating panes and popups, whichever VT backend the
// binary was built with, and the SSH and browser clients, which draw the same
// composed frame. Painting in each of those would be seven places to keep in
// step, and a missed one shows as a pane that loses its ground when it gains
// focus. The blank area lipgloss pads a short body out with is covered by the
// same pass, because it is cells of the layer like any other.
//
// Only the pane's content rectangle is painted. The border, the title bar, the
// gap between panes, the shared-border dividers, the rail and the dock are
// chrome, and they keep drawing on the terminal's own background: the option
// is about the ground the program's output sits on, not a second theme for
// tuios itself.
//
// A cell a program gave a background of its own keeps it. So does every mark
// tuios paints over a pane (the selection, search matches, the copy mode
// cursor), because each of those sets its own background too.
//
// The paint is kept with the parsed cells, so a pane whose layer did not change
// since the last frame is copied as before and pays nothing. With the option
// off, the only cost is a string comparison per layer.

// paneGround is the colours painted behind pane content on this frame.
//
// bg is nil when nothing is painted: the option is off, or it asks for the
// theme's background and no theme is set. fg is the colour a cell left on the
// default foreground is given, or nil to leave it to the host terminal.
type paneGround struct {
	bg, fg color.Color
	// key names the inputs the colours came from, so a parsed layer can tell
	// whether the paint it holds is the paint this frame wants without
	// comparing colours.
	key string
}

// on reports whether anything is painted.
func (g paneGround) on() bool { return g.bg != nil }

// paneGroundMemo is the resolved ground and what it was resolved from.
type paneGroundMemo struct {
	setting, themeID string
	valid            bool
	ground           paneGround
}

// paneGround is this client's pane background, resolved once per change of
// the setting or the theme rather than on every frame. A colour literal is
// parsed here and nowhere else, so the per-frame cost of the option is a
// string comparison.
func (m *OS) paneGround() paneGround {
	setting := m.Settings.PaneBackgroundResolved()
	if setting == config.PaneBackgroundOff {
		return paneGround{}
	}
	themeID := theme.CurrentThemeID()
	memo := &m.paneGroundCache
	if memo.valid && memo.setting == setting && memo.themeID == themeID {
		return memo.ground
	}
	memo.setting, memo.themeID, memo.valid = setting, themeID, true
	memo.ground = resolvePaneGround(setting, themeID)
	return memo.ground
}

// resolvePaneGround turns a resolved setting into colours.
//
// theme paints the theme's background and gives text left in the default
// colour the theme's foreground, because that pair is what the theme was
// designed as: leaving the foreground to the host would put a light terminal's
// dark text on a dark theme's ground. A colour literal paints that colour; the
// foreground is the theme's lifted until it reads on it when a theme is set,
// and the host's own when none is, since tuios does not know the host's
// palette and choosing a stranger's is worse than trusting the user's pick.
func resolvePaneGround(setting, themeID string) paneGround {
	var g paneGround
	switch setting {
	case config.PaneBackgroundOff:
		return g
	case config.PaneBackgroundTheme:
		if themeID == "" {
			return g
		}
		bg := theme.TerminalBg()
		if isNilColor(bg) {
			return g
		}
		g.bg = solidColor(bg)
		if fg := theme.TerminalFg(); !isNilColor(fg) {
			g.fg = solidColor(fg)
		}
	default:
		if !config.IsHexColor(setting) {
			return g
		}
		g.bg = solidColor(lipgloss.Color(setting))
		if themeID != "" {
			if fg := theme.TerminalFg(); !isNilColor(fg) {
				g.fg = solidColor(theme.ReadableAt(fg, g.bg, theme.ContrastFloor))
			}
		}
	}
	g.key = setting + "\x00" + themeID
	return g
}

// solidColor copies a colour into a plain RGBA value, so a cell painted with it
// holds a comparable value with no pointer into a theme behind it.
func solidColor(c color.Color) color.Color {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

// paneContentRect is the rectangle a pane's program draws in, in screen cells:
// the whole window for a borderless pane, and inside the border for one that
// has a border. The title bar is the border's top row, so a bordered pane's
// content starts one row down whichever edge the title is drawn on.
func paneContentRect(w *terminal.Window) image.Rectangle {
	b := w.BorderOffset()
	x, y := w.X+b, w.Y+b
	return image.Rect(x, y, x+w.ContentWidth(), y+w.ContentHeight())
}

// layerFill is the paint one parsed layer carries: the rectangle, relative to
// the layer, and the ground it is painted with. The zero value paints nothing.
type layerFill struct {
	rect   image.Rectangle
	ground paneGround
}

// paintPaneGround gives every cell inside r that has no background of its own
// the ground's background, and, when the ground names one, every cell with no
// foreground of its own the ground's foreground.
//
// It runs when a layer is parsed, not when it is copied, so a pane that did not
// change pays for none of it on the next frame.
func paintPaneGround(lines []uv.Line, r image.Rectangle, g paneGround) {
	for y := max(r.Min.Y, 0); y < r.Max.Y && y < len(lines); y++ {
		line := lines[y]
		for x := max(r.Min.X, 0); x < r.Max.X && x < len(line); x++ {
			c := &line[x]
			if isNilColor(c.Style.Bg) {
				c.Style.Bg = g.bg
			}
			if g.fg != nil && isNilColor(c.Style.Fg) {
				c.Style.Fg = g.fg
			}
		}
	}
}

// paneGroundCell fills dst with src given the pane's ground where it had no
// colour of its own, and returns it. It is for the cells renderTerminal styles
// against the ground rather than on it, which is only the fake cursor: the
// cursor block is drawn in the cell's colours swapped, so a cell with no
// colours would draw a white block whatever the ground was.
func paneGroundCell(dst, src *uv.Cell, g paneGround) *uv.Cell {
	if src == nil || !g.on() {
		return src
	}
	*dst = *src
	if isNilColor(dst.Style.Bg) {
		dst.Style.Bg = g.bg
	}
	if isNilColor(dst.Style.Fg) {
		if g.fg != nil {
			dst.Style.Fg = g.fg
		} else {
			dst.Style.Fg = theme.ContrastText(g.bg)
		}
	}
	return dst
}
