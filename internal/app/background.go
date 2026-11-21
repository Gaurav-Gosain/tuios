package app

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type surface uint8

const (
	surfacePane surface = iota
	surfaceDesktop
	surfaceChrome
	surfaceDock
	surfaceSidebar
	surfaceCount
)

func surfaceSetting(s *config.Settings, which surface) string {
	switch which {
	case surfacePane:
		return s.PaneBackgroundResolved()
	case surfaceDesktop:
		return s.DesktopBackgroundResolved()
	case surfaceChrome:
		return s.WindowChromeBackgroundResolved()
	case surfaceDock:
		return themeGround(s.DockBackground, s.Background, s.DockBackgroundResolved(), theme.RoleDockGround)
	case surfaceSidebar:
		return themeGround(s.SidebarBackground, s.Background, s.SidebarBackgroundResolved(), theme.RoleRailGround)
	}
	return config.BackgroundOff
}

func themeGround(own, all, resolved string, role theme.Role) string {
	if own != "" || (all != "" && all != config.BackgroundOff) || !theme.ChromeNames(role) {
		return resolved
	}
	return role.String()
}

type ground struct {
	bg, fg       color.Color
	key          string
	bgSGR, fgSGR string
}

func (g ground) on() bool { return g.bg != nil }

type groundMemo struct {
	setting, themeID string
	themeGen         uint64
	depth            overlay.Depth
	valid            bool
	ground           ground
}

func (m *OS) surfaceGround(which surface) ground {
	setting := surfaceSetting(&m.Settings, which)
	if setting == config.BackgroundOff {
		return ground{}
	}
	gen, depth := theme.Generation(), theme.Depth()
	memo := &m.groundCache[which]
	if memo.valid && memo.setting == setting && memo.themeGen == gen && memo.depth == depth {
		return memo.ground
	}
	themeID := theme.CurrentThemeID()
	memo.setting, memo.themeID, memo.themeGen, memo.depth, memo.valid = setting, themeID, gen, depth, true
	memo.ground = resolveGround(setting, themeID)
	return memo.ground
}

func (m *OS) paneGround() ground { return m.surfaceGround(surfacePane) }

type frameGrounds [surfaceCount]ground

func (f *frameGrounds) any() bool {
	for i := range f {
		if f[i].on() {
			return true
		}
	}
	return false
}

func (m *OS) frameGrounds() frameGrounds {
	var f frameGrounds
	for i := range f {
		f[i] = m.surfaceGround(surface(i))
	}
	return f
}

func resolveGround(setting, themeID string) ground {
	var g ground
	switch setting {
	case config.BackgroundOff:
		return g
	case config.BackgroundTheme:
		if themeID == "" || theme.Depth() == overlay.Depth16 {
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
		bg := theme.ConfigColor(setting)
		if isNilColor(bg) || overlay.IsNoColor(bg) {
			return g
		}
		g.bg = solidColor(bg)
		if themeID != "" {
			if fg := theme.TerminalFg(); !isNilColor(fg) {
				g.fg = solidColor(theme.ReadableAt(fg, g.bg, theme.ContrastFloor))
			}
		}
	}
	if theme.Depth() == overlay.Depth256 {
		g.bg = overlay.To256(g.bg)
		if g.fg != nil {
			g.fg = overlay.To256(g.fg)
		}
	}
	g.key = setting + "\x00" + themeID + "\x00" + strconv.FormatUint(theme.Generation(), 10) + "\x00" + theme.Depth().String()
	g.bgSGR = sgrColor(48, g.bg)
	if g.fg != nil {
		g.fgSGR = sgrColor(38, g.fg)
	}
	return g
}

func sgrColor(which int, c color.Color) string {
	if i, ok := c.(ansi.IndexedColor); ok {
		return fmt.Sprintf("\x1b[%d;5;%dm", which, i)
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", which, r>>8, g>>8, b>>8)
}

func solidColor(c color.Color) color.Color {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

func paneContentRect(w *terminal.Window) image.Rectangle {
	b := w.BorderOffset()
	x, y := w.X+b, w.Y+b
	return image.Rect(x, y, x+w.ContentWidth(), y+w.ContentHeight())
}

type layerFill struct {
	rect         image.Rectangle
	inner, outer ground
}

func (f *layerFill) on() bool { return f.inner.on() || f.outer.on() }

func (m *OS) layerFill(id string, bounds image.Rectangle, w, h int, grounds *frameGrounds) layerFill {
	switch id {
	case "sidebar":
		return layerFill{outer: grounds[surfaceSidebar]}
	case "dock":
		return layerFill{outer: grounds[surfaceDock]}
	case "scrollback-browser":
		return layerFill{outer: grounds[surfacePane]}
	case pipLayerID:
		return layerFill{
			rect:  image.Rect(1, 1, w-1, h-1),
			inner: grounds[surfacePane],
			outer: grounds[surfaceChrome],
		}
	}
	if r, ok := m.paneContentRects[id]; ok {
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

const scrollbarLayerSuffix = "-sb"

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
	paintGround(lines, image.Rect(0, 0, w, r.Min.Y), f.outer)
	paintGround(lines, image.Rect(0, r.Max.Y, w, h), f.outer)
	paintGround(lines, image.Rect(0, r.Min.Y, r.Min.X, r.Max.Y), f.outer)
	paintGround(lines, image.Rect(r.Max.X, r.Min.Y, w, r.Max.Y), f.outer)
}

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

func desktopBlank(g ground) uv.Cell {
	c := uv.EmptyCell
	c.Style.Bg, c.Style.Fg = g.bg, g.fg
	return c
}

func (m *OS) syncReportColors() {
	r := m.reportedToPanes()
	for _, w := range m.Windows {
		if w != nil {
			w.SetReportColors(r.fg, r.bg, r.pal, r.key)
		}
	}
}

func (m *OS) paneReportHex() (bg, fg, pal string) {
	r := m.reportedToPanes()
	return r.bgHex, r.fgHex, r.palHex
}

func (m *OS) themePreviewing() bool {
	return m.ShowThemePicker || m.themeEditor.open
}

func (m *OS) reportedToPanes() *paneReport {
	if m.themePreviewing() && m.paneReportSentSet {
		return &m.paneReportSent
	}
	r := m.paneReportNow()
	m.paneReportSent, m.paneReportSentSet = *r, true
	return r
}

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

func (m *OS) backgroundsChanged() {
	m.MarkAllDirty()
	m.syncReportColors()
	m.SyncStateToDaemon()
}

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
