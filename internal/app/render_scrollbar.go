package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func scrollbarViewOffset(window *terminal.Window) int {
	if !window.InCopyMode() {
		return 0
	}
	return max(window.CopyMode.ScrollOffset, 0)
}

func scrollbarThumbHeight(contentH, scrollbackLen int) int {
	total := scrollbackLen + contentH
	h := (contentH*contentH + total - 1) / total
	return max(min(h, contentH-1), 1)
}

func windowNeedsScrollbar(window *terminal.Window, s *config.Settings) bool {
	if s.HideScrollbar {
		return false
	}
	if window.Terminal == nil || window.IsAltScreen() {
		return false
	}
	if scrollbarViewOffset(window) <= 0 {
		return false
	}
	if window.ScrollbackLenSync() <= 0 {
		return false
	}
	return window.ContentHeight() > 2
}

func scrollbarColumn(window *terminal.Window) int {
	return window.X + window.Width - 1 - window.BorderOffset()
}

// ScrollbarRect is where a pane's bar was drawn on the last frame: the column,
// the rows a press may grab, and the thumb's rows inside them. Recorded by the
// renderer as it draws, because only it knows what survived the clip and which
// cells took ink. A press resolved any other way would grab cells the guest
// still owns: at the live tail, and on a style with no track, that column is
// ordinary content.
type ScrollbarRect struct {
	X              int
	TrackY, TrackH int
	ThumbY, ThumbH int
}

// Contains reports whether a press at (x, y) landed on the drawn bar.
func (r ScrollbarRect) Contains(x, y int) bool {
	return x == r.X && y >= r.TrackY && y < r.TrackY+r.TrackH
}

// OnThumb reports whether row y landed on the thumb rather than the bare track.
func (r ScrollbarRect) OnThumb(y int) bool {
	return y >= r.ThumbY && y < r.ThumbY+r.ThumbH
}

// ScrollbarHit returns the bar drawn for window on the last frame, and whether
// there was one to grab.
func (m *OS) ScrollbarHit(window *terminal.Window) (ScrollbarRect, bool) {
	if window == nil {
		return ScrollbarRect{}, false
	}
	rect, ok := m.scrollbarRects[window.ID]
	return rect, ok
}

func (m *OS) resetScrollbarRects() {
	clear(m.scrollbarRects)
}

func scrollbarTravel(contentH, scrollbackLen int, s *config.Settings) (unitsPerRow, thumb, travel int) {
	if s.ScrollbarStyle != config.ScrollbarStyleTrack {
		thumb = scrollbarThumbHeight(contentH, scrollbackLen)
		return 1, thumb, contentH - thumb
	}
	const halves = 2
	trackH := contentH * halves
	total := scrollbackLen + contentH
	thumb = max(min((trackH*contentH+total-1)/total, trackH-1), 1)
	return halves, thumb, trackH - thumb
}

func roundDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	return (2*a + b) / (2 * b)
}

func scrollbarThumbStart(contentH, scrollbackLen, offset int, s *config.Settings) int {
	_, _, travel := scrollbarTravel(contentH, scrollbackLen, s)
	if travel <= 0 || scrollbackLen <= 0 {
		return 0
	}
	return max(min(travel-roundDiv(offset*travel, scrollbackLen), travel), 0)
}

func scrollbarOffsetForStart(contentH, scrollbackLen, start int, s *config.Settings) int {
	_, _, travel := scrollbarTravel(contentH, scrollbackLen, s)
	if travel <= 0 {
		return scrollbackLen
	}
	start = max(min(start, travel), 0)
	return max(min(roundDiv((travel-start)*scrollbackLen, travel), scrollbackLen), 0)
}

func scrollbarHasTrack(s *config.Settings) bool {
	return s.GetScrollbarTrackChar() != "" ||
		s.ScrollbarStyle == config.ScrollbarStyleTrack
}

const scrollbarBlockThumb = "█"

func scrollbarRows(contentH, scrollbackLen, offset int, s *config.Settings) (rows []string, thumbTop, thumbRows int) {
	perRow, thumbUnits, _ := scrollbarTravel(contentH, scrollbackLen, s)
	start := scrollbarThumbStart(contentH, scrollbackLen, offset, s)
	end := start + thumbUnits

	full := s.GetScrollbarThumbChar()
	upper, lower := "▀", "▄"
	if full != scrollbarBlockThumb {
		upper, lower = full, full
	}
	blank := s.GetScrollbarTrackChar()
	if blank == "" && s.ScrollbarStyle == config.ScrollbarStyleTrack {
		blank = " "
	}

	rows = make([]string, contentH)
	thumbTop = -1
	for i := range rows {
		covered := min(end, (i+1)*perRow) - max(start, i*perRow)
		switch {
		case covered <= 0:
			rows[i] = blank
			continue
		case covered >= perRow:
			rows[i] = full
		case start <= i*perRow:
			rows[i] = upper
		default:
			rows[i] = lower
		}
		if thumbTop < 0 {
			thumbTop = i
		}
		thumbRows++
	}
	if thumbTop < 0 {
		thumbTop = 0
	}
	return rows, thumbTop, thumbRows
}

const scrollbarMinContrast = 2.5

const (
	scrollbarQuietThumbContrast = 4.5
	scrollbarQuietTrackContrast = 2.0
)

func blendColors(a, b color.Color, t float64) color.Color { return overlay.MixColors(a, b, t) }

func scrollbarQuietInk(ground color.Color, want float64) color.Color {
	ink := theme.ContrastText(ground)
	if theme.ContrastRatio(ink, ground) <= want {
		return ink
	}
	const steps = 32
	for i := 1; i <= steps; i++ {
		mixed := blendColors(ink, ground, float64(i)/steps)
		if theme.ContrastRatio(mixed, ground) <= want {
			return mixed
		}
	}
	return ink
}

func (m *OS) scrollbarInk(window *terminal.Window, focused bool, ground color.Color) (thumb, track color.Color) {
	track = theme.UI().FgMute
	switch m.Settings.ScrollbarTintResolved() {
	case config.ScrollbarTintQuiet:
		return scrollbarQuietInk(ground, scrollbarQuietThumbContrast),
			scrollbarQuietInk(ground, scrollbarQuietTrackContrast)
	case config.ScrollbarTintMuted:
		return theme.BorderUnfocusedOn(m.host.bg), track
	}
	if v, ok := m.Settings.ScrollbarTintColor(); ok {
		if c := theme.ConfigColor(v); c != nil {
			return c, track
		}
	}
	if !focused {
		return theme.BorderUnfocusedOn(m.host.bg), track
	}
	focus := theme.BorderFocusedWindowOn(m.host.bg)
	if m.Mode == TerminalMode {
		focus = theme.BorderFocusedTerminalOn(m.host.bg)
	}
	if c := m.focusedStateBorder(window); c != nil {
		focus = c
	}
	acc, ok := m.WindowAccent(window.ID)
	if !ok {
		return focus, track
	}
	accent := acc.Color()
	if theme.ContrastRatio(accent, ground) < scrollbarMinContrast {
		return focus, track
	}
	return accent, track
}

func (m *OS) renderScrollbarLayer(window *terminal.Window, rightClip, zIndex int, focused bool) *lipgloss.Layer {
	if window.IsBeingManipulated || !windowNeedsScrollbar(window, &m.Settings) {
		return nil
	}

	x := scrollbarColumn(window)
	if x < 0 || x >= rightClip {
		return nil
	}

	scrollbackLen := window.ScrollbackLenSync()
	contentH := window.ContentHeight()
	top := window.Y + window.BorderOffset()
	rows, thumbTop, thumbRows := scrollbarRows(contentH, scrollbackLen, scrollbarViewOffset(window), &m.Settings)

	pal := theme.UI()
	ground := m.terminalBg()
	base := lipgloss.NewStyle()
	if m.Settings.ScrollbarStyle == config.ScrollbarStyleTrack {
		ground = pal.Surface
		base = base.Background(pal.Surface)
	} else if g := m.paneGround(); g.on() {
		ground = g.bg
		base = base.Background(g.bg)
	}
	thumbColor, trackColor := m.scrollbarInk(window, focused, ground)
	trackInk := base.Foreground(trackColor)
	thumbInk := base.Foreground(thumbColor)

	trackTop, trackRows := top, contentH
	if !scrollbarHasTrack(&m.Settings) {
		rows = rows[thumbTop : thumbTop+thumbRows]
		top += thumbTop
		trackTop, trackRows = top, thumbRows
		thumbTop = 0
	}

	thumbEnd := thumbTop + thumbRows
	parts := make([]string, 0, 3)
	if thumbTop > 0 {
		parts = append(parts, trackInk.Render(strings.Join(rows[:thumbTop], "\n")))
	}
	parts = append(parts, thumbInk.Render(strings.Join(rows[thumbTop:thumbEnd], "\n")))
	if thumbEnd < len(rows) {
		parts = append(parts, trackInk.Render(strings.Join(rows[thumbEnd:], "\n")))
	}

	if m.scrollbarRects == nil {
		m.scrollbarRects = make(map[string]ScrollbarRect, len(m.Windows))
	}
	m.scrollbarRects[window.ID] = ScrollbarRect{
		X:      x,
		TrackY: trackTop, TrackH: trackRows,
		ThumbY: top + thumbTop, ThumbH: thumbRows,
	}

	return lipgloss.NewLayer(strings.Join(parts, "\n")).X(x).Y(top).Z(zIndex).ID(window.ID + scrollbarLayerSuffix)
}

// ScrollbarThumbRow returns the screen row the thumb's first cell sits on at the
// pane's current scroll position. Input recomputes it after a track click so the
// drag that follows holds the offset the thumb actually landed with.
func ScrollbarThumbRow(window *terminal.Window, s *config.Settings) int {
	_, thumbTop, _ := scrollbarRows(window.ContentHeight(), window.ScrollbackLenSync(),
		scrollbarViewOffset(window), s)
	return window.Y + window.BorderOffset() + thumbTop
}

// ScrollbarOffsetForThumbRow is the inverse: the scroll offset that draws the
// thumb's first cell on the given screen row.
func ScrollbarOffsetForThumbRow(window *terminal.Window, row int, s *config.Settings) int {
	contentH := window.ContentHeight()
	scrollbackLen := window.ScrollbackLenSync()
	perRow, _, _ := scrollbarTravel(contentH, scrollbackLen, s)
	rel := clampInt(row-window.Y-window.BorderOffset(), 0, max(contentH-1, 0))
	return scrollbarOffsetForStart(contentH, scrollbackLen, rel*perRow, s)
}
