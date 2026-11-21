package app

import (
	"image/color"
	"slices"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
)

type copyFlash struct {
	Block    bool
	WindowID string
	Start    terminal.Position
	End      terminal.Position
	At       time.Time
}

// NoteCopyFlash records a copy so the next frames can sweep over it.
//
// It takes the region from the pane's live selection, which is still there at
// the moment of the copy and usually gone immediately after: the sweep outlives
// the selection it describes, which is the whole point.
func (m *OS) NoteCopyFlash(window *terminal.Window) {
	if window == nil || !window.HasSelection() || window.CopyMode == nil {
		return
	}
	if window.CopyMode.State == terminal.CopyModeVisualBlock {
		m.NoteCopyFlashBlock(window, window.CopyMode.VisualStart, window.CopyMode.VisualEnd)
		return
	}
	m.NoteCopyFlashRegion(window, window.CopyMode.VisualStart, window.CopyMode.VisualEnd)
}

// NoteCopyFlashBlock is NoteCopyFlashRegion for a rectangular selection.
func (m *OS) NoteCopyFlashBlock(window *terminal.Window, a, b terminal.Position) {
	topLeft, bottomRight := terminal.BlockCorners(a, b)
	m.NoteCopyFlashRegion(window, topLeft, bottomRight)
	if m.copyFlash != nil && window != nil && m.copyFlash.WindowID == window.ID {
		m.copyFlash.Block = true
	}
}

// NoteCopyFlashRegion records a copy of an explicit region of the pane, in
// the pane's absolute coordinates, so the next frames can sweep over it.
//
// It is for a copy whose selection is gone by the time the copy is applied.
// Copy mode's y ends the visual selection while it still holds the pane's
// lock, and the copy is applied after the lock is dropped, so the region is
// taken at the key and handed over here.
func (m *OS) NoteCopyFlashRegion(window *terminal.Window, start, end terminal.Position) {
	if window == nil || !m.copyFlashAllowed() {
		return
	}
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}
	m.copyFlash = &copyFlash{WindowID: window.ID, Start: start, End: end, At: time.Now()}
	m.copyFlashMore = nil
	window.ContentDirty = true
}

func (m *OS) copyFlashAllowed() bool {
	if !m.Settings.CopyFlash || m.copyFlashDuration() <= 0 {
		return false
	}
	return m.Settings.MotionOn(config.AspectCopy)
}

// NoteCopyFlashMany records one copy taken from several panes at once, the
// multi copy mode yank, so the same sweep crosses each pane's selection. Panes
// without a selection are skipped. The sweeps share one clock and end
// together.
func (m *OS) NoteCopyFlashMany(windows []*terminal.Window) {
	var noted []copyFlash
	for _, w := range windows {
		m.NoteCopyFlash(w)
		if m.copyFlash != nil && m.copyFlash.WindowID == w.ID {
			noted = append(noted, *m.copyFlash)
		}
	}
	if len(noted) == 0 {
		return
	}
	at := noted[len(noted)-1].At
	for i := range noted {
		noted[i].At = at
	}
	m.copyFlash = &noted[0]
	m.copyFlashMore = noted[1:]
}

func (m *OS) copyFlashFor(id string) *copyFlash {
	if m.copyFlash == nil {
		return nil
	}
	if m.copyFlash.WindowID == id {
		return m.copyFlash
	}
	for i := range m.copyFlashMore {
		if m.copyFlashMore[i].WindowID == id {
			return &m.copyFlashMore[i]
		}
	}
	return nil
}

func (m *OS) copyFlashIDs() []string {
	if m.copyFlash == nil {
		return nil
	}
	ids := []string{m.copyFlash.WindowID}
	for _, f := range m.copyFlashMore {
		ids = append(ids, f.WindowID)
	}
	return ids
}

func (m *OS) dropCopyFlash(id string) {
	if m.copyFlash == nil {
		return
	}
	if m.copyFlash.WindowID != id {
		m.copyFlashMore = slices.DeleteFunc(m.copyFlashMore, func(f copyFlash) bool { return f.WindowID == id })
		return
	}
	if len(m.copyFlashMore) == 0 {
		m.copyFlash = nil
		return
	}
	next := m.copyFlashMore[0]
	m.copyFlash = &next
	m.copyFlashMore = m.copyFlashMore[1:]
}

func (m *OS) markCopyFlashPane() bool {
	if m.copyFlash == nil {
		return false
	}
	ids := m.copyFlashIDs()
	m.CopyFlashActive()
	for _, id := range ids {
		if w := m.windowByID(id); w != nil {
			w.ContentDirty = true
		}
	}
	return true
}

// CancelCopyFlash drops a sweep that is still running, and asks its pane for
// the frame without it.
//
// Anything the user does supersedes it. The sweep is a 550ms acknowledgement
// of a copy, and once they have pressed a key or clicked somewhere they are
// no longer looking at what was copied: leaving the light running meant it
// carried on painting a region whose text had moved underneath it, which is
// what leaving copy mode mid-sweep looked like.
func (m *OS) CancelCopyFlash() {
	if m.copyFlash == nil {
		return
	}
	ids := m.copyFlashIDs()
	m.copyFlash = nil
	m.copyFlashMore = nil
	for _, id := range ids {
		if w := m.windowByID(id); w != nil {
			w.ContentDirty = true
		}
	}
}

func (m *OS) copyFlashDuration() time.Duration {
	return time.Duration(m.Settings.CopyFlashMs) * time.Millisecond
}

func (m *OS) copyFlashProgress(windowID string) (float64, bool) {
	if m.copyFlash == nil {
		return 0, false
	}
	elapsed := time.Since(m.copyFlash.At)
	total := m.copyFlashDuration()
	if total <= 0 || elapsed >= total {
		m.copyFlash = nil
		return 0, false
	}
	if m.copyFlashFor(windowID) == nil {
		return 0, false
	}
	return float64(elapsed) / float64(total), true
}

// CopyFlashActive reports whether a sweep is still running, so the render loop
// knows to keep asking for frames while it does.
func (m *OS) CopyFlashActive() bool {
	if m.copyFlash == nil {
		return false
	}
	if time.Since(m.copyFlash.At) >= m.copyFlashDuration() {
		m.copyFlash = nil
		return false
	}
	return true
}

type copyFlashBand struct {
	centre   float64
	reach    float64
	amp      float64
	slope    float64
	vertical bool

	tint color.Color
	ink  color.Color
}

const copyFlashSlope = 2

func (b copyFlashBand) position(x, row int) float64 {
	if b.vertical {
		return float64(row)
	}
	return float64(x) - float64(row)*b.slope
}

func (b copyFlashBand) intensity(x, row int) float64 {
	if b.reach <= 0 || b.amp <= 0 {
		return 0
	}
	d := b.position(x, row) - b.centre
	if d < 0 {
		d = -d
	}
	if d >= b.reach {
		return 0
	}
	t := 1 - d/b.reach
	return t * t * (3 - 2*t) * b.amp
}

func (b copyFlashBand) styleFor(x, row int, hasGlyph bool, bg color.Color) (lipgloss.Style, bool) {
	i := b.intensity(x, row)
	if i <= 0.02 {
		return lipgloss.Style{}, false
	}
	_ = hasGlyph
	return lipgloss.NewStyle().Background(overlay.MixColors(bg, b.tint, i)), true
}

func copyFlashEnvelope(progress float64) float64 {
	const (
		rampIn  = 0.15
		rampOut = 0.25
	)
	switch {
	case progress <= 0 || progress >= 1:
		return 0
	case progress < rampIn:
		return progress / rampIn
	case progress > 1-rampOut:
		return (1 - progress) / rampOut
	default:
		return 1
	}
}

func (m *OS) copyFlashBandFor(progress float64, box copyFlashBox) copyFlashBand {
	pal := theme.UI()
	band := copyFlashBand{
		amp:  copyFlashEnvelope(progress),
		tint: m.copyFlashTint(),
		ink:  pal.Fg,
	}
	switch m.Settings.CopyFlashStyle {
	case config.CopyFlashHorizontal:
	case config.CopyFlashDiagonalReverse:
		band.slope = -copyFlashSlope
	case config.CopyFlashVertical:
		band.vertical = true
	default:
		band.slope = copyFlashSlope
	}

	lo, hi := band.axisRange(box)
	span := hi - lo
	band.reach = span * m.copyFlashReach()
	floor := 4.0
	if band.vertical {
		floor = 1.5
	}
	if band.reach < floor {
		band.reach = floor
	}
	band.centre = lo - band.reach + progress*(span+2*band.reach)
	return band
}

func (b copyFlashBand) axisRange(box copyFlashBox) (lo, hi float64) {
	if b.vertical {
		return float64(box.top), float64(box.bottom)
	}
	corners := []float64{
		b.position(box.left, box.top), b.position(box.right, box.top),
		b.position(box.left, box.bottom), b.position(box.right, box.bottom),
	}
	lo, hi = corners[0], corners[0]
	for _, c := range corners[1:] {
		lo, hi = min(lo, c), max(hi, c)
	}
	return lo, hi
}

type copyFlashBox struct {
	left   int
	right  int
	top    int
	bottom int
}

func copyFlashBoxOf(grid *pool.HighlightGrid, maxY, maxX int) (copyFlashBox, bool) {
	box := copyFlashBox{left: maxX, right: -1, top: -1, bottom: -1}
	for y := range maxY {
		for x := range maxX {
			if !grid.Get(y, x) {
				continue
			}
			if x < box.left {
				box.left = x
			}
			if x > box.right {
				box.right = x
			}
			if box.top < 0 {
				box.top = y
			}
			box.bottom = y
		}
	}
	if box.right < 0 {
		return copyFlashBox{}, false
	}
	return box, true
}

func fillPaneRegion(grid *pool.HighlightGrid, start, end terminal.Position, block bool,
	scrollbackLen, scrollbackOffset, maxY, maxX int, textEnd func(y int) int,
) {
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}
	for absY := start.Y; absY <= end.Y; absY++ {
		viewportY, ok := paneViewportRow(absY, scrollbackLen, scrollbackOffset, maxY)
		if !ok {
			continue
		}
		startX, endX := 0, maxX-1
		if block || absY == start.Y {
			startX = start.X
		}
		if block || absY == end.Y {
			endX = end.X
		}
		if textEnd != nil {
			endX = min(endX, textEnd(viewportY))
		}
		for x := startX; x <= endX && x < maxX; x++ {
			grid.Set(viewportY, x)
		}
	}
}

func paneViewportRow(absY, scrollbackLen, scrollbackOffset, maxY int) (int, bool) {
	var viewportY int
	if absY < scrollbackLen {
		if scrollbackOffset <= 0 || absY < scrollbackLen-scrollbackOffset {
			return 0, false
		}
		viewportY = absY - (scrollbackLen - scrollbackOffset)
	} else {
		viewportY = absY - scrollbackLen
		if scrollbackOffset > 0 {
			viewportY += scrollbackOffset
		}
	}
	return viewportY, viewportY >= 0 && viewportY < maxY
}

func fillBlockSelection(grid *pool.HighlightGrid, window *terminal.Window, screen cellGrid, a, b terminal.Position,
	scrollbackLen, maxY, maxX int,
) {
	topLeft, bottomRight := terminal.BlockCorners(a, b)
	for absY := topLeft.Y; absY <= bottomRight.Y; absY++ {
		viewportY, ok := paneViewportRow(absY, scrollbackLen, window.ScrollbackOffset, maxY)
		if !ok {
			continue
		}
		var line []uv.Cell
		if absY < scrollbackLen {
			line = window.ScrollbackLine(absY)
		}
		lo, hi := terminal.BlockRowSpan(topLeft.X, bottomRight.X, func(x int) *uv.Cell {
			switch {
			case x < 0:
				return nil
			case absY >= scrollbackLen:
				return screen.CellAt(x, absY-scrollbackLen)
			case x < len(line):
				return &line[x]
			}
			return nil
		})
		for x := lo; x <= hi && x < maxX; x++ {
			grid.Set(viewportY, x)
		}
	}
}

func (m *OS) copyFlashGrid(window *terminal.Window, screen cellGrid, scrollbackLen, maxY, maxX int) (*pool.HighlightGrid, copyFlashBand) {
	progress, ok := m.copyFlashProgress(window.ID)
	fl := m.copyFlashFor(window.ID)
	if !ok || fl == nil {
		return nil, copyFlashBand{}
	}
	grid := pool.GetHighlightGrid()
	grid.Init(maxY, maxX)
	textEnd := func(y int) int {
		return paneRowTextEnd(window, screen, y, maxX, scrollbackLen)
	}
	fillPaneRegion(grid, fl.Start, fl.End, fl.Block,
		scrollbackLen, window.ScrollbackOffset, maxY, maxX, textEnd)
	box, ok := copyFlashBoxOf(grid, maxY, maxX)
	if !ok {
		pool.PutHighlightGrid(grid)
		m.dropCopyFlash(window.ID)
		return nil, copyFlashBand{}
	}
	return grid, m.copyFlashBandFor(progress, box)
}

func paneRowTextEnd(window *terminal.Window, screen cellGrid, y, maxX, scrollbackLen int) int {
	blank := func(c *uv.Cell) bool {
		return c == nil || c.Width == 0 || c.Content == "" || c.Content == " "
	}
	offset := window.ScrollbackOffset
	if offset > 0 && y < offset {
		idx := scrollbackLen - offset + y
		if idx < 0 || idx >= scrollbackLen {
			return -1
		}
		line := window.ScrollbackLine(idx)
		for i := min(len(line), maxX) - 1; i >= 0; i-- {
			if !blank(&line[i]) {
				return min(i+line[i].Width, maxX) - 1
			}
		}
		return -1
	}
	screenY := y
	if offset > 0 {
		screenY = y - offset
	}
	for i := maxX - 1; i >= 0; i-- {
		if c := screen.CellAt(i, screenY); !blank(c) {
			return min(i+c.Width, maxX) - 1
		}
	}
	return -1
}

func (m *OS) copyFlashReach() float64 { return 0.30 }

func (m *OS) copyFlashTint() color.Color {
	if c := theme.ConfigColor(m.Settings.CopyFlashColor); c != nil {
		return c
	}
	return overlay.Tone(m.terminalBg(), copyFlashLift)
}

const copyFlashLift = 1.6
