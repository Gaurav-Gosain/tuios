package app

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The backgrounds: appearance.background and one option per surface.
//
// A cell left on the default background carries no colour, and that is
// deliberate: the host terminal draws its own background through it, so tuios
// looks like the terminal it runs in. The options paint a ground there
// instead, either the theme's background or a colour of the user's, one
// surface at a time:
//
//   - the pane: the rectangle a pane's program draws in, together with the
//     marks drawn on it (the thin scrollbar) and the scrollback browser, which
//     shows a pane's own output;
//   - the desktop: every cell no layer covers, which is the gap between tiled
//     panes, the space around floating ones and an empty workspace, and every
//     floating layer with no ground of its own (the welcome splash, the gaps
//     in the keycast, the screen saver);
//   - the window chrome: the rest of a pane's layer, which is its border and
//     title bar, plus the lines between shared-border panes and the capture
//     marquee that is drawn over a pane's edge;
//   - the dock, and the rail.
//
// The overlay panels (the palette, settings, which-key, the Inbox, every
// picker, the badges and tooltips) are not on the list because they have no
// cell to paint: each fills its whole rectangle with its own surface colour.
// A panel that grew a transparent cell would take the desktop's ground, which
// is the one every layer without a surface of its own falls back to.
//
// It is all painted in the compositor, on the cells each layer parses to, and
// not in the code that draws each surface. Every way a pane reaches the screen
// goes through that one parse: the unfocused fast path that hands back the
// emulator's own Render(), the per-cell path, the cached content string,
// scrollback and copy mode, the zoomed pane, floating panes and popups,
// whichever VT backend the binary was built with, and the SSH and browser
// clients, which draw the same composed frame. The chrome is the same story
// told by several files, and one pass over the parsed cells is one place to
// keep right. The desktop is not a layer at all: it is the value the canvas is
// cleared to before the layers land.
//
// A cell with a background of its own keeps it: a program's colours, every
// mark tuios paints over a pane (the selection, search matches, the copy mode
// cursor), a border's accent, a dock pill. Only the empty ground changes. Text
// left in the default colour on a painted cell takes the theme's foreground,
// lifted to read on a colour of the user's, so it stays legible; text that
// names its own colour keeps it.
//
// The paint is kept with the parsed cells, so a layer that did not change
// since the last frame is copied as before and pays nothing. With every option
// off, the cost is the string comparisons that find them off.
//
// The one frame that is not composed is a lone pane filling the region, which
// the fullscreen fast path hands to the host as a string without parsing it.
// That path paints the same rule into its string instead: see
// background_fast.go.

// surface is one of the places a background can be set on its own.
type surface uint8

const (
	surfacePane surface = iota
	surfaceDesktop
	surfaceChrome
	surfaceDock
	surfaceSidebar
	surfaceCount
)

// surfaceSetting is what a surface's background is behaving as, after the
// precedence between its own option and appearance.background.
func surfaceSetting(s *config.Settings, which surface) string {
	switch which {
	case surfacePane:
		return s.PaneBackgroundResolved()
	case surfaceDesktop:
		return s.DesktopBackgroundResolved()
	case surfaceChrome:
		return s.WindowChromeBackgroundResolved()
	case surfaceDock:
		return s.DockBackgroundResolved()
	case surfaceSidebar:
		return s.SidebarBackgroundResolved()
	}
	return config.BackgroundOff
}

// ground is the colours painted on one surface on this frame.
//
// bg is nil when nothing is painted: the option is off, or it asks for the
// theme's background and no theme is set. fg is the colour a cell left on the
// default foreground is given, or nil to leave it to the host terminal.
type ground struct {
	bg, fg color.Color
	// key names the inputs the colours came from, so a parsed layer can tell
	// whether the paint it holds is the paint this frame wants without
	// comparing colours.
	key string
	// bgSGR and fgSGR are bg and fg as the SGR sequences that set them, empty
	// for a colour that is not painted. The fullscreen fast path paints with
	// these, so they are formatted once here and never per frame. See
	// background_fast.go.
	bgSGR, fgSGR string
}

// on reports whether anything is painted.
func (g ground) on() bool { return g.bg != nil }

// groundMemo is one surface's resolved ground and what it was resolved from.
type groundMemo struct {
	setting, themeID string
	valid            bool
	ground           ground
}

// surfaceGround is this client's ground for one surface, resolved once per
// change of the setting or the theme rather than on every frame. A colour
// literal is parsed here and nowhere else, so the per-frame cost of an option
// is a string comparison.
func (m *OS) surfaceGround(which surface) ground {
	setting := surfaceSetting(&m.Settings, which)
	if setting == config.BackgroundOff {
		return ground{}
	}
	themeID := theme.CurrentThemeID()
	memo := &m.groundCache[which]
	if memo.valid && memo.setting == setting && memo.themeID == themeID {
		return memo.ground
	}
	memo.setting, memo.themeID, memo.valid = setting, themeID, true
	memo.ground = resolveGround(setting, themeID)
	return memo.ground
}

// paneGround is the ground behind pane content.
func (m *OS) paneGround() ground { return m.surfaceGround(surfacePane) }

// frameGrounds is every surface's ground for one frame.
type frameGrounds [surfaceCount]ground

// any reports whether any surface is painted.
func (f *frameGrounds) any() bool {
	for i := range f {
		if f[i].on() {
			return true
		}
	}
	return false
}

// frameGrounds resolves every surface's ground. With every option off it is
// five settings found off and the zero value.
func (m *OS) frameGrounds() frameGrounds {
	var f frameGrounds
	for i := range f {
		f[i] = m.surfaceGround(surface(i))
	}
	return f
}

// resolveGround turns a resolved setting into colours.
//
// theme paints the theme's background and gives text left in the default
// colour the theme's foreground, because that pair is what the theme was
// designed as: leaving the foreground to the host would put a light terminal's
// dark text on a dark theme's ground. A colour literal paints that colour; the
// foreground is the theme's lifted until it reads on it when a theme is set,
// and the host's own when none is, since tuios does not know the host's
// palette and choosing a stranger's is worse than trusting the user's pick.
func resolveGround(setting, themeID string) ground {
	var g ground
	switch setting {
	case config.BackgroundOff:
		return g
	case config.BackgroundTheme:
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
	g.bgSGR = sgrColor(48, g.bg)
	if g.fg != nil {
		g.fgSGR = sgrColor(38, g.fg)
	}
	return g
}

// sgrColor is the SGR sequence that sets a truecolor foreground (38) or
// background (48).
func sgrColor(which int, c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", which, r>>8, g>>8, b>>8)
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

// layerFill is the paint one parsed layer carries. rect, relative to the
// layer, is painted with inner; every other cell of the layer with outer. A
// pane's layer is the one that uses both: its content is the pane surface and
// the rest of it is chrome. The zero value paints nothing.
type layerFill struct {
	rect         image.Rectangle
	inner, outer ground
}

// on reports whether the fill paints anything.
func (f *layerFill) on() bool { return f.inner.on() || f.outer.on() }

// layerFill decides which surface a layer's transparent cells belong to, and
// so what they are painted with. It is only asked while some surface is on.
//
// The layers are told apart by id, which every layer composed from a cell
// cache has: a pane's is the window's own, and GetCanvas recorded its content
// rectangle under it.
func (m *OS) layerFill(id string, bounds image.Rectangle, w, h int, grounds *frameGrounds) layerFill {
	switch id {
	case "sidebar":
		return layerFill{outer: grounds[surfaceSidebar]}
	case "dock":
		return layerFill{outer: grounds[surfaceDock]}
	case "scrollback-browser":
		return layerFill{outer: grounds[surfacePane]}
	}
	if r, ok := m.paneContentRects[id]; ok {
		// Taken relative to where the layer landed, so a pane clipped at the
		// edge of the region paints only the part of it that is drawn.
		return layerFill{
			rect:  r.Sub(bounds.Min).Intersect(image.Rect(0, 0, w, h)),
			inner: grounds[surfacePane],
			outer: grounds[surfaceChrome],
		}
	}
	switch {
	case strings.HasPrefix(id, "sep-"), strings.HasPrefix(id, "capture-marquee"):
		return layerFill{outer: grounds[surfaceChrome]}
	case strings.HasSuffix(id, scrollbarLayerSuffix):
		return layerFill{outer: grounds[surfacePane]}
	}
	return layerFill{outer: grounds[surfaceDesktop]}
}

// scrollbarLayerSuffix ends the id of a pane's scrollbar layer.
const scrollbarLayerSuffix = "-sb"

// paintLayerFill paints a parsed layer w cells wide and h tall.
func paintLayerFill(lines []uv.Line, w, h int, f *layerFill) {
	if f.inner.on() && !f.rect.Empty() {
		paintGround(lines, f.rect, f.inner)
	}
	if !f.outer.on() {
		return
	}
	full := image.Rect(0, 0, w, h)
	r := f.rect.Intersect(full)
	if r.Empty() {
		paintGround(lines, full, f.outer)
		return
	}
	// The rest of the layer: the rows above and below the inner rectangle
	// whole, and the columns either side of it on the rows it spans.
	paintGround(lines, image.Rect(0, 0, w, r.Min.Y), f.outer)
	paintGround(lines, image.Rect(0, r.Max.Y, w, h), f.outer)
	paintGround(lines, image.Rect(0, r.Min.Y, r.Min.X, r.Max.Y), f.outer)
	paintGround(lines, image.Rect(r.Max.X, r.Min.Y, w, r.Max.Y), f.outer)
}

// paintGround gives every cell inside r that has no background of its own the
// ground's background, and, when the ground names one, every cell with no
// foreground of its own the ground's foreground.
//
// It runs when a layer is parsed, not when it is copied, so a layer that did
// not change pays for none of it on the next frame.
func paintGround(lines []uv.Line, r image.Rectangle, g ground) {
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

// desktopBlank is the cell the canvas is cleared to when the desktop is
// painted: a space on the desktop's ground.
func desktopBlank(g ground) uv.Cell {
	c := uv.EmptyCell
	c.Style.Bg, c.Style.Fg = g.bg, g.fg
	return c
}

// A program that asks the terminal what its background is (OSC 11), or its
// default text colour (OSC 10), is asking what it is drawn on. With a pane
// background painted, the honest answer is the painted ground and the ink
// tuios gives default text there, so a program that picks a light or a dark
// palette from the answer picks the one that reads. With the pane background
// off nothing changes, and the emulator answers as it always has.
//
// Two emulators can be asked. A pane this process runs itself answers from its
// own emulator, which syncReportColors keeps told. A pane in a daemon session
// answers from the daemon's emulator, which is told through the state this
// client pushes: see paneReportHex and the session package's
// report_colors.go.

// syncReportColors gives every pane's emulator the pane ground to answer with.
// It runs before every composed frame, so a pane made since the last one, a
// theme switched, or an option changed is caught without each of those paths
// having to remember; a window already told is a string comparison, and with
// the option off both strings are empty.
func (m *OS) syncReportColors() {
	g := m.paneGround()
	for _, w := range m.Windows {
		if w != nil {
			w.SetReportColors(g.fg, g.bg, g.key)
		}
	}
}

// paneReportHex is the pane ground as the #rrggbb pair the daemon is sent,
// empty for a colour that is not painted.
func (m *OS) paneReportHex() (bg, fg string) {
	g := m.paneGround()
	if !g.on() {
		return "", ""
	}
	return colorHex(g.bg), colorHex(g.fg)
}

// colorHex spells a colour as #rrggbb, and nil as empty.
func colorHex(c color.Color) string {
	if isNilColor(c) {
		return ""
	}
	r, g, b, _ := c.RGBA()
	const digits = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out[1+2*i] = digits[v>>4&0xf]
		out[2+2*i] = digits[v&0xf]
	}
	return string(out)
}

// backgroundsChanged lands a change of any background option: every layer is
// repainted, the panes' emulators are told the new ground, and so is the
// daemon.
func (m *OS) backgroundsChanged() {
	m.MarkAllDirty()
	m.syncReportColors()
	m.SyncStateToDaemon()
}

// paneGroundCell fills dst with src given the pane's ground where it had no
// colour of its own, and returns it. It is for the cells renderTerminal styles
// against the ground rather than on it, which is only the fake cursor: the
// cursor block is drawn in the cell's colours swapped, so a cell with no
// colours would draw a white block whatever the ground was.
func paneGroundCell(dst, src *uv.Cell, g ground) *uv.Cell {
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
