package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// scrollbarViewOffset returns how far back into scrollback the pane is looking,
// 0 at the live tail.
func scrollbarViewOffset(window *terminal.Window) int {
	if !window.InCopyMode() {
		return 0
	}
	return max(window.CopyMode.ScrollOffset, 0)
}

// scrollbarThumbHeight sizes the thumb to the viewport's share of the whole
// buffer, leaving at least one cell of travel so the bar always reads as a
// position rather than a filled track.
func scrollbarThumbHeight(contentH, scrollbackLen int) int {
	total := scrollbackLen + contentH
	h := (contentH*contentH + total - 1) / total
	return max(min(h, contentH-1), 1)
}

// windowNeedsScrollbar reports whether window should show a scrollbar thumb.
// It is the single source of truth shared by every render path (compositor
// cached, sync-hold, redraw, and the fullscreen fast path) so they never
// disagree about whether the thumb is present. It mirrors the eligibility in
// renderScrollbarLayer minus the transient IsBeingManipulated check.
//
// The thumb is a position readout, so it exists only while there is a position
// to read: a bar pinned to the bottom at the live tail is chrome answering a
// question nobody asked, and it is the only thing that used to drop a lone
// scrolled-to-tail pane off the fullscreen fast path.
func windowNeedsScrollbar(window *terminal.Window) bool {
	if config.HideScrollbar {
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

// scrollbarColumn returns the screen column the thumb occupies: the pane's last
// content column, one in from its right border when it has one. A borderless
// pane under shared borders has BorderOffset 0, so the column is its own
// rightmost cell, one in from the separator overlay that lives in the gap
// between rectangles. The bar was never the border's business, which is why one
// formula covers bordered, borderless, hidden-border and zoomed panes alike.
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

// resetScrollbarRects drops the previous frame's bars. Called by every frame
// builder before it draws, so a bar that has scrolled back to the tail, been
// clipped by the rail or lost its pane stops being grabbable with it.
func (m *OS) resetScrollbarRects() {
	clear(m.scrollbarRects)
}

// scrollbarTravel returns the thumb's size and its travel in the units the
// active style measures in, plus how many of those units make a row. The track
// style works at opentui's two units per cell (sst/opentui,
// packages/core/src/renderables/Slider.ts), so a thumb end can land mid-cell as
// ▀ or ▄ and the bar carries twice the resolution a one-column bar would
// otherwise have. The thin style works at one: its glyphs style the cross axis,
// and Unicode has no right-half-plus-partial-height block to smooth the scroll
// axis with, so the two styles do not blend.
func scrollbarTravel(contentH, scrollbackLen int) (unitsPerRow, thumb, travel int) {
	if config.ScrollbarStyle != config.ScrollbarStyleTrack {
		thumb = scrollbarThumbHeight(contentH, scrollbackLen)
		return 1, thumb, contentH - thumb
	}
	const halves = 2
	trackH := contentH * halves
	total := scrollbackLen + contentH
	thumb = max(min((trackH*contentH+total-1)/total, trackH-1), 1)
	return halves, thumb, trackH - thumb
}

// roundDiv divides two non-negative counts, rounding halves up.
func roundDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	return (2*a + b) / (2 * b)
}

// scrollbarThumbStart returns the thumb's leading edge in travel units, 0 at the
// oldest line. The division is rounded rather than truncated, after opentui's
// Slider: truncation biases every position toward the live tail, so a pane a
// hair off its oldest line drew the thumb a whole row shy of the top of its
// track.
func scrollbarThumbStart(contentH, scrollbackLen, offset int) int {
	_, _, travel := scrollbarTravel(contentH, scrollbackLen)
	if travel <= 0 || scrollbackLen <= 0 {
		return 0
	}
	return max(min(travel-roundDiv(offset*travel, scrollbackLen), travel), 0)
}

// scrollbarOffsetForStart inverts scrollbarThumbStart: the scroll offset that
// puts the thumb's leading edge on a given travel unit. A drag reads it, so the
// thumb lands where the pointer left it.
func scrollbarOffsetForStart(contentH, scrollbackLen, start int) int {
	_, _, travel := scrollbarTravel(contentH, scrollbackLen)
	if travel <= 0 {
		return scrollbackLen
	}
	start = max(min(start, travel), 0)
	return max(min(roundDiv((travel-start)*scrollbackLen, travel), scrollbackLen), 0)
}

// scrollbarHasTrack reports whether the bar paints its whole column. The track
// style always does; the thin style does once it has a glyph to draw one with,
// which ASCII has not and `track = "none"` asks it not to.
func scrollbarHasTrack() bool {
	return config.GetScrollbarTrackChar() != "" ||
		config.ScrollbarStyle == config.ScrollbarStyleTrack
}

// scrollbarBlockThumb is the only thumb glyph with half-height siblings, so it
// is the only one the track style can resolve a half cell with. Any other
// glyph, configured or ASCII, rounds a covered half up to a whole cell.
const scrollbarBlockThumb = "█"

// scrollbarRows draws the bar's whole column, one glyph per viewport row, and
// reports the rows the thumb landed on: a press is measured against those, not
// against the arithmetic that produced them.
func scrollbarRows(contentH, scrollbackLen, offset int) (rows []string, thumbTop, thumbRows int) {
	perRow, thumbUnits, _ := scrollbarTravel(contentH, scrollbackLen)
	start := scrollbarThumbStart(contentH, scrollbackLen, offset)
	end := start + thumbUnits

	full := config.GetScrollbarThumbChar()
	upper, lower := "▀", "▄"
	if full != scrollbarBlockThumb {
		upper, lower = full, full
	}
	blank := config.GetScrollbarTrackChar()
	if blank == "" && config.ScrollbarStyle == config.ScrollbarStyleTrack {
		blank = " " // the surface fill is this style's track
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

// scrollbarMinContrast is the floor a derived tint has to clear against the
// ground it is drawn on. Below it the bar is present but unreadable, which is
// worse than a bar that does not match its pane: dark blue measures 1.74:1 on
// the canvas and 1.21:1 on the track's surface.
//
// It sits below theme.ContrastFloor on purpose. A scrollbar is a shape, not a
// label: it has to be seen, not read.
const scrollbarMinContrast = 2.5

// scrollbarInk resolves the colour the thumb is drawn in. The rule is the
// owner's: the bar matches the highlighted terminal, so the focused pane's is
// drawn in its accent, or failing that in the very colour its border is drawn
// in, and every other pane keeps the quiet grey the unfocused frames use.
//
// This is a tint rule only. A pane with scrollback still draws its bar whether
// or not it holds focus, because the bar reports a scroll position and hiding
// it would hide that position.
//
// A derived accent has to clear scrollbarMinContrast against the ground it
// lands on or it falls back to the mode's focus colour, which turns an
// invisible bar into one that still matches the border family. A configured hex
// skips the floor: measurement was overridden on purpose.
func (m *OS) scrollbarInk(window *terminal.Window, focused bool, ground color.Color) color.Color {
	if config.ScrollbarTint == config.ScrollbarTintMuted {
		return theme.BorderUnfocused()
	}
	if hex, ok := config.ScrollbarTintHex(); ok {
		return lipgloss.Color(hex)
	}
	if !focused {
		return theme.BorderUnfocused()
	}
	focus := theme.BorderFocusedWindow()
	if m.Mode == TerminalMode {
		focus = theme.BorderFocusedTerminal()
	}
	acc, ok := m.WindowAccent(window.ID)
	if !ok {
		return focus
	}
	accent := acc.Color()
	if theme.ContrastRatio(accent, ground) < scrollbarMinContrast {
		return focus
	}
	return accent
}

// renderScrollbarLayer creates a 1-column layer floating the bar over the
// pane's last content column, and records where it drew it. rightClip is the
// first column of the sidebar band; a pane mid-drag may straddle it, and the
// bar is composed above the band's own layer.
func (m *OS) renderScrollbarLayer(window *terminal.Window, rightClip, zIndex int, focused bool) *lipgloss.Layer {
	if window.IsBeingManipulated || !windowNeedsScrollbar(window) {
		return nil
	}

	x := scrollbarColumn(window)
	if x < 0 || x >= rightClip {
		return nil
	}

	scrollbackLen := window.ScrollbackLenSync()
	contentH := window.ContentHeight()
	top := window.Y + window.BorderOffset()
	rows, thumbTop, thumbRows := scrollbarRows(contentH, scrollbackLen, scrollbarViewOffset(window))

	pal := theme.UI()
	ground := pal.Canvas
	trackInk := lipgloss.NewStyle().Foreground(pal.FgMute)
	if config.ScrollbarStyle == config.ScrollbarStyleTrack {
		ground = pal.Surface
		trackInk = trackInk.Background(pal.Surface)
	}
	thumbInk := trackInk.Foreground(m.scrollbarInk(window, focused, ground))

	// Without a track the untouched rows stay the guest's: a blank there would
	// paint over a column of content to say nothing.
	trackTop, trackRows := top, contentH
	if !scrollbarHasTrack() {
		rows = rows[thumbTop : thumbTop+thumbRows]
		top += thumbTop
		trackTop, trackRows = top, thumbRows
		thumbTop = 0
	}

	// Three renders rather than one per row: the column is a run of track, a run
	// of thumb and a run of track, and this draws on every frame a scrolled-back
	// pane produces.
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

	return lipgloss.NewLayer(strings.Join(parts, "\n")).X(x).Y(top).Z(zIndex).ID(window.ID + "-sb")
}

// ScrollbarThumbRow returns the screen row the thumb's first cell sits on at the
// pane's current scroll position. Input recomputes it after a track click so the
// drag that follows holds the offset the thumb actually landed with.
func ScrollbarThumbRow(window *terminal.Window) int {
	_, thumbTop, _ := scrollbarRows(window.ContentHeight(), window.ScrollbackLenSync(),
		scrollbarViewOffset(window))
	return window.Y + window.BorderOffset() + thumbTop
}

// ScrollbarOffsetForThumbRow is the inverse: the scroll offset that draws the
// thumb's first cell on the given screen row.
func ScrollbarOffsetForThumbRow(window *terminal.Window, row int) int {
	contentH := window.ContentHeight()
	scrollbackLen := window.ScrollbackLenSync()
	perRow, _, _ := scrollbarTravel(contentH, scrollbackLen)
	rel := clampInt(row-window.Y-window.BorderOffset(), 0, max(contentH-1, 0))
	return scrollbarOffsetForStart(contentH, scrollbackLen, rel*perRow)
}
