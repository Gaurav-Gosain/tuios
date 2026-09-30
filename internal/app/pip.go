package app

import (
	"fmt"
	"image"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The picture-in-picture view: a small live copy of one pane, drawn in a
// corner of the screen while another pane has the focus, so a person working
// in one pane can watch another (typically a coding agent) without giving it
// room in the layout.
//
// The view is not a pane. The source pane stays where it is in the layout, or
// keeps running where it is when it sits on another workspace, is minimised
// or is a hidden scratch terminal. The view only reads the cells that pane's
// emulator already holds; there is no second emulator and no second stream.
// It never takes the focus or a key. A click on it jumps to the source pane,
// and the view is not drawn while the source pane has the focus, because the
// pane itself is then on the screen.
//
// It is this client's, like the spotlight. Nothing about it is in session
// state, so each attached client pins its own pane (or none), and a detach
// forgets it. One view per client: a second pin replaces the first. More than
// one would need a stacking rule for the corners and the cursor avoidance
// below, and nobody has asked for it yet.
//
// What it costs. The view's body is read from the emulator only when the
// source pane had output since the last read (MarkTerminalsWithNewContent
// sets pip.dirty), and the finished box is a layer with an id, so on any
// other frame the compositor copies cells it already parsed. It adds no tick
// and no timer, so an idle session stays idle. It does take the fullscreen
// fast path away while it is drawn, because that path composes no layers.
//
// It never covers the focused pane's cursor, nor the text to the cursor's left
// on its row, which is the line being typed. The box stays in its corner until
// that line reaches it, then moves to the first other corner clear of it. It
// stays there until the line reaches it again, rather than going back, so a
// cursor that moves about near an edge does not make the box jump to and fro.

// pipLayerID is the view's layer id. The compositor keys its parsed cells on
// it, and layerFill paints its grounds by it.
const pipLayerID = "pip"

// pipMargin is the gap in cells between the box and the edges of the pane
// region, so the box never sits on a pane's border row or column.
const pipMargin = 1

// pipCorner is one of the four places the box can sit.
type pipCorner int

const (
	pipBottomRight pipCorner = iota
	pipBottomLeft
	pipTopRight
	pipTopLeft
)

// parsePiPCorner reads a [pip] corner value. An unknown one is bottom-right.
func parsePiPCorner(s string) pipCorner {
	switch s {
	case config.PiPCornerBottomLeft:
		return pipBottomLeft
	case config.PiPCornerTopRight:
		return pipTopRight
	case config.PiPCornerTopLeft:
		return pipTopLeft
	}
	return pipBottomRight
}

// pipCornerOrder is the order the corners are tried in, starting from the
// preferred one: the preferred corner, then the other end of the same edge
// (the box keeps to the same edge of the screen when it can), then the
// opposite edge, same side first.
func pipCornerOrder(preferred pipCorner) [4]pipCorner {
	mirrorX := map[pipCorner]pipCorner{pipBottomRight: pipBottomLeft, pipBottomLeft: pipBottomRight, pipTopRight: pipTopLeft, pipTopLeft: pipTopRight}
	mirrorY := map[pipCorner]pipCorner{pipBottomRight: pipTopRight, pipBottomLeft: pipTopLeft, pipTopRight: pipBottomRight, pipTopLeft: pipBottomLeft}
	return [4]pipCorner{preferred, mirrorX[preferred], mirrorY[preferred], mirrorY[mirrorX[preferred]]}
}

// pipBox is the box of a w by h view in corner c of region, kept pipMargin
// cells from its edges. ok is false when the region cannot hold even the
// smallest box, which is when the view is not drawn at all. A region smaller
// than the asked size shrinks the box rather than hiding it.
func pipBox(region image.Rectangle, w, h int, c pipCorner) (image.Rectangle, bool) {
	w = min(w, region.Dx()-2*pipMargin)
	h = min(h, region.Dy()-2*pipMargin)
	if w < config.PiPMinWidth || h < config.PiPMinHeight {
		return image.Rectangle{}, false
	}
	x := region.Max.X - pipMargin - w
	y := region.Max.Y - pipMargin - h
	if c == pipBottomLeft || c == pipTopLeft {
		x = region.Min.X + pipMargin
	}
	if c == pipTopRight || c == pipTopLeft {
		y = region.Min.Y + pipMargin
	}
	return image.Rect(x, y, x+w, y+h), true
}

// pipChooseCorner is the corner the box is drawn in this frame. clear is the
// part of the screen the box must not cover: the cursor's cell and the text
// left of it on its row, empty when no cursor is drawn. current is where the
// box was last frame and is kept while it misses clear. When it does not, the
// corners are tried in pipCornerOrder(preferred) and the first whose box
// misses clear wins. ok is false when every corner's box meets clear, which
// only a region barely bigger than the box can do; the view is then not drawn.
func pipChooseCorner(region image.Rectangle, w, h int, current, preferred pipCorner, clear image.Rectangle) (pipCorner, bool) {
	holds := func(c pipCorner) bool {
		box, ok := pipBox(region, w, h, c)
		return !ok || box.Overlaps(clear)
	}
	if !holds(current) {
		return current, true
	}
	for _, c := range pipCornerOrder(preferred) {
		if c != current && !holds(c) {
			return c, true
		}
	}
	return current, false
}

// pipState is this client's picture-in-picture view.
type pipState struct {
	// windowID is the source pane, empty when nothing is pinned.
	windowID string
	// corner is where the box sits now, and from is the configured corner it
	// was worked out from: a change of pip.corner puts the box back there.
	corner pipCorner
	from   string
	// dirty says the source pane had output since body was read.
	dirty bool
	// body is the source's cells as the view last read them, cols by rows.
	body       string
	cols, rows int
	// box is the finished frame, and boxKey what it was built from, so a
	// frame that changes none of it reuses the string and the compositor
	// reuses the cells it parsed from it.
	box    string
	boxKey string
	// rect is where the box was drawn on the last composed frame, empty when
	// it was not drawn. Clicks are tested against it.
	rect image.Rectangle
	// occluder is the scratch slice the graphics pass hands the box in.
	occluder []cellRect
	// closedNote is the dock note for a pinned pane that closed while a
	// state sync was being applied. The sync's own "Window closed" note is
	// shown after the sync, and the newest note is the one the dock draws, so
	// this one waits and goes up after it. See flushPiPNote.
	closedNote string
}

// PiPWindowID is the pane pinned as the picture-in-picture view, or "".
func (m *OS) PiPWindowID() string { return m.pip.windowID }

// pipSource is the pinned pane, or nil.
func (m *OS) pipSource() *terminal.Window {
	if m.pip.windowID == "" {
		return nil
	}
	if i := m.windowIndexByID(m.pip.windowID); i >= 0 {
		return m.Windows[i]
	}
	return nil
}

// pipWanted reports whether the view is to be drawn: a pane is pinned, it
// still exists, and it does not have the focus.
func (m *OS) pipWanted() bool {
	src := m.pipSource()
	if src == nil || src.Terminal == nil {
		return false
	}
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) && m.Windows[m.FocusedWindow] == src {
		return false
	}
	return true
}

// pipConfig is the [pip] table in force.
func (m *OS) pipConfig() config.PiPConfig {
	if m.UserConfig == nil {
		return config.PiPConfig{}
	}
	return m.UserConfig.PiP
}

// pipRegion is the rectangle the panes are laid out in, which the box is
// placed inside. It leaves out the rail and the dock.
func (m *OS) pipRegion() image.Rectangle {
	x, y := m.GetLeftMargin(), m.GetTopMargin()
	return image.Rect(x, y, x+m.GetContentWidth(), y+m.GetUsableHeight())
}

// pipKeepClear is the part of the screen the view must not cover: the focused
// pane's cursor and the text left of it on the same row, from the pane's left
// edge. It is empty when no cursor is drawn, which is the case in window mode
// and under an open panel.
func (m *OS) pipKeepClear() image.Rectangle {
	c := m.getRealCursor()
	if c == nil {
		return image.Rectangle{}
	}
	left := c.X
	if w := m.GetFocusedWindow(); w != nil {
		// The same offset getRealCursor puts the cursor at.
		left = w.X
		if !w.Tiled {
			left++
		}
		left = min(left, c.X)
	}
	return image.Rect(left, c.Y, c.X+1, c.Y+1)
}

// PinPiP pins the pane with this id as the picture-in-picture view, in place
// of any pane pinned before.
func (m *OS) PinPiP(windowID string) error {
	i := m.windowIndexByID(windowID)
	if i < 0 {
		return fmt.Errorf("no pane has the id %s", windowID)
	}
	if prev := m.pipSource(); prev != nil && prev.ID != windowID {
		m.UnpinPiP()
	}
	w := m.Windows[i]
	m.pip.windowID = w.ID
	m.pip.dirty = true
	m.pip.box, m.pip.boxKey = "", ""
	// In a daemon session a pane on another workspace is not streamed to
	// this client, so its emulator would stop at whatever it held when the
	// workspace was left. Priming it fetches the daemon's screen and starts
	// the stream, and unsubscribeFromPTY keeps the stream while it is pinned.
	if m.IsDaemonSession && w.DaemonMode && w.PTYID != "" && !m.SubscribedPTYs[w.PTYID] {
		m.primePaneFromDaemon(w)
	}
	m.cachedViewContent = ""
	return nil
}

// UnpinPiP takes the view away. It is a no-op when nothing is pinned.
func (m *OS) UnpinPiP() {
	src := m.pipSource()
	m.pip = pipState{occluder: m.pip.occluder[:0]}
	m.cachedViewContent = ""
	// A pane that is streamed only because it was pinned goes back to not
	// being streamed, as the rest of its workspace is not.
	if src != nil && m.IsDaemonSession && src.DaemonMode && src.Workspace != m.CurrentWorkspace {
		m.unsubscribeFromPTY(src)
	}
}

// pipKeepsStream reports whether w's output stream has to stay open because
// it is the pinned pane. See unsubscribeFromPTY.
func (m *OS) pipKeepsStream(w *terminal.Window) bool {
	return w != nil && m.pip.windowID != "" && w.ID == m.pip.windowID
}

// TogglePiP is the toggle_pip key and the palette row. With nothing pinned it
// pins the focused pane. With a pane pinned it unpins it, whichever pane has
// the focus, so the key that put the view up takes it down from anywhere.
func (m *OS) TogglePiP() {
	if src := m.pipSource(); src != nil {
		name := pipPaneName(src)
		m.UnpinPiP()
		m.ShowNotification(fmt.Sprintf("Unpinned %s.", name), "info", m.Settings.NotificationDuration)
		return
	}
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		m.ShowNotification("No pane has the focus. Focus a pane to pin it.", "info", m.Settings.NotificationDuration)
		return
	}
	w := m.Windows[m.FocusedWindow]
	if err := m.PinPiP(w.ID); err != nil {
		m.ShowNotification(err.Error(), "error", m.Settings.NotificationDuration)
		return
	}
	m.ShowNotification(fmt.Sprintf("Pinned %s. Focus another pane to see it in the corner.", pipPaneName(w)),
		"info", m.Settings.NotificationDuration)
}

// SetPiP is the pip verb on this client. window is the pane to pin, "" for
// the focused one. A pane that is already pinned is unpinned, so the verb
// toggles the way the key does. off unpins whatever is pinned.
func (m *OS) SetPiP(window string, off bool) (pinned bool, windowID string, err error) {
	if off {
		m.UnpinPiP()
		return false, "", nil
	}
	if window == "" {
		if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
			return false, "", fmt.Errorf("no pane has the focus, so name the pane to pin")
		}
		window = m.Windows[m.FocusedWindow].ID
	}
	if window == m.pip.windowID {
		m.UnpinPiP()
		return false, "", nil
	}
	if err := m.PinPiP(window); err != nil {
		return false, "", err
	}
	return true, window, nil
}

// pipSourceClosed is called as a pane is torn down. When it is the pinned
// pane the view goes with it, and the dock says so, because a view that just
// vanishes reads as a bug.
func (m *OS) pipSourceClosed(w *terminal.Window) {
	if w == nil || m.pip.windowID == "" || w.ID != m.pip.windowID {
		return
	}
	note := fmt.Sprintf("%s closed. The picture-in-picture view is off.", pipPaneName(w))
	m.pip = pipState{occluder: m.pip.occluder[:0]}
	m.cachedViewContent = ""
	if m.applyingPeerSync {
		m.pip.closedNote = note
		return
	}
	m.ShowNotification(note, "info", m.Settings.NotificationDuration)
}

// flushPiPNote shows the note pipSourceClosed held back during a state sync.
func (m *OS) flushPiPNote() {
	if note := m.pip.closedNote; note != "" {
		m.pip.closedNote = ""
		m.ShowNotification(note, "info", m.Settings.NotificationDuration)
	}
}

// PiPAt reports whether the cell at x, y is on the view as it was last drawn.
func (m *OS) PiPAt(x, y int) bool {
	return m.pip.windowID != "" && image.Pt(x, y).In(m.pip.rect)
}

// JumpToPiP focuses the pinned pane: it switches to the pane's workspace,
// restores it when it is minimised and shows it when it is a hidden scratch
// terminal (FocusWindow does the last two). The view then is not drawn,
// because its pane has the focus. It reports whether there was a pane to go
// to.
func (m *OS) JumpToPiP() bool {
	i := m.windowIndexByID(m.pip.windowID)
	if i < 0 {
		return false
	}
	w := m.Windows[i]
	if w.Minimized && !w.HiddenScratch() {
		if w.Workspace != m.CurrentWorkspace {
			m.FocusWindow(i)
		}
		m.RestoreWindow(i)
		if i = m.windowIndexByID(w.ID); i < 0 {
			return false
		}
	}
	m.FocusWindow(i)
	if i = m.windowIndexByID(w.ID); i >= 0 {
		m.Windows[i].MinimizeHighlightUntil = time.Now().Add(time.Second)
	}
	m.MarkAllDirty()
	return true
}

// pipPaneName is how the view and its messages name a pane: the name the
// user gave it, else the title its program set, else its short id.
func pipPaneName(w *terminal.Window) string {
	if w.CustomName != "" {
		return printableTitle(w.CustomName)
	}
	if t := w.Title(); t != "" && !isDefaultTitle(t, w.ID) {
		return printableTitle(t)
	}
	return "pane " + shortID(w.ID)
}

// renderPiP is the view's layer for this frame, or nil when it is not drawn.
// It records where the box went, for clicks and for the graphics pass.
func (m *OS) renderPiP() *lipgloss.Layer {
	m.pip.rect = image.Rectangle{}
	if !m.pipWanted() {
		return nil
	}
	src := m.pipSource()
	cfg := m.pipConfig()
	w, h := cfg.Size()
	preferred := cfg.CornerName()
	if m.pip.from != preferred {
		m.pip.from = preferred
		m.pip.corner = parsePiPCorner(preferred)
	}
	region := m.pipRegion()
	corner, ok := pipChooseCorner(region, w, h, m.pip.corner, parsePiPCorner(preferred), m.pipKeepClear())
	if !ok {
		return nil
	}
	m.pip.corner = corner
	box, _ := pipBox(region, w, h, corner)

	cols, rows := box.Dx()-2, box.Dy()-2
	m.pipReadBody(src, cols, rows)

	pal := theme.UI()
	state, seen := m.railAgentState(src.ID, src.AgentState, src.AgentCompletionSeq)
	name := pipPaneName(src)
	key := fmt.Sprintf("%d:%d:%s:%s:%t:%t", box.Dx(), box.Dy(), name, state, seen, overlay.UseASCII())
	if m.pip.box == "" || m.pip.boxKey != key {
		m.pip.box = pipFrame(m.pip.body, cols, rows, name, state, seen, pal)
		m.pip.boxKey = key
	}
	m.pip.rect = box
	return lipgloss.NewLayer(m.pip.box).X(box.Min.X).Y(box.Min.Y).Z(config.ZIndexPiP).ID(pipLayerID)
}

// pipReadBody reads the source's cells into pip.body when the source had
// output since the last read or the view changed size. It never waits for
// the source's lock: a pane flooding output holds it almost all the time, and
// the frame that would wait is the one carrying the user's keystrokes. The
// last body is kept and the read is tried again next frame.
func (m *OS) pipReadBody(src *terminal.Window, cols, rows int) {
	if !m.pip.dirty && m.pip.body != "" && m.pip.cols == cols && m.pip.rows == rows {
		return
	}
	if !src.TryRLockIO() {
		return
	}
	body := ""
	if src.Terminal != nil {
		body = pipCells(src.Terminal, cols, rows)
	}
	src.RUnlockIO()
	if body != m.pip.body {
		// A new string is a new frame for the compositor; the same string
		// is a memcmp and a copy of cells it already parsed.
		m.pip.boxKey = ""
	}
	m.pip.body, m.pip.cols, m.pip.rows = body, cols, rows
	m.pip.dirty = false
}

// pipScreen is the part of an emulator the view reads.
type pipScreen interface {
	Width() int
	Height() int
	CellAt(x, y int) *uv.Cell
	CursorPosition() uv.Position
}

// pipCells renders the part of the screen the view shows: the last rows rows
// that hold text, from the left edge, cols wide, at one cell per cell.
//
// The rows end at the lower of the last row with text on it and the cursor's
// row, rather than at the bottom of the screen. A shell that has printed ten
// lines on a fifty-row pane has nothing but blanks at the bottom, and a view of
// the bottom would show nothing until the screen had filled. The columns start
// at the left edge because that is where text starts; the right-hand part of a
// line is most often blank.
//
// Every row is exactly cols cells wide. A wide glyph cut by the right edge
// becomes a blank, and a kitty image placeholder cell becomes a blank, because
// the view is text only. Links are dropped: the view is for looking at, and a
// click on it goes to the pane.
func pipCells(scr pipScreen, cols, rows int) string {
	sw, sh := scr.Width(), scr.Height()
	if cols <= 0 || rows <= 0 || sw <= 0 || sh <= 0 {
		return ""
	}
	end := min(scr.CursorPosition().Y, sh-1)
	for y := sh - 1; y > end; y-- {
		if pipRowHasText(scr, y, sw) {
			end = y
			break
		}
	}
	end = max(end, min(rows, sh)-1)
	start := max(0, end-rows+1)

	var b strings.Builder
	line := make(uv.Line, cols)
	for i := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		y := start + i
		for x := range cols {
			line[x] = uv.EmptyCell
			if y > end || x >= sw {
				continue
			}
			c := scr.CellAt(x, y)
			if c == nil {
				continue
			}
			cell := *c
			cell.Link = uv.Link{}
			switch {
			case cell.Width > 1 && x+cell.Width > cols:
				cell = uv.EmptyCell
			case vt.IsKittyPlaceholder(cell.Content):
				cell = uv.EmptyCell
			}
			line[x] = cell
		}
		// A wide glyph's tail is a zero cell, which Render skips, so the
		// row stays cols cells wide however many wide glyphs it holds.
		b.WriteString(line.Render())
	}
	return b.String()
}

// pipRowHasText reports whether row y has anything on it but default blanks.
func pipRowHasText(scr pipScreen, y, w int) bool {
	for x := range w {
		c := scr.CellAt(x, y)
		if c != nil && !c.IsZero() && !c.Equal(&uv.EmptyCell) && c.Content != " " && c.Content != "" {
			return true
		}
		if c != nil && !c.Style.IsZero() && c.Style.Bg != nil {
			return true
		}
	}
	return false
}

// pipFrame draws the box: a border in the accent, the source's agent mark and
// name on the top edge, and the body inside.
func pipFrame(body string, cols, rows int, name, state string, seen bool, pal overlay.Palette) string {
	tl, tr, bl, br, hz, vl := "╭", "╮", "╰", "╯", "─", "│"
	if overlay.UseASCII() {
		tl, tr, bl, br, hz, vl = "+", "+", "+", "+", "-", "|"
	}
	edge := lipgloss.NewStyle().Foreground(pal.Accent)

	// The title: the state's mark in its own colour, then the name, cut to
	// fit between the corner and a cell of rule on the right.
	glyph, glyphColor := agentMark(state, seen, pal)
	mark := ""
	if glyph != "" {
		mark = lipgloss.NewStyle().Foreground(glyphColor).Bold(sidebarAttention(state)).Render(" " + glyph)
	}
	tail := "…"
	if overlay.UseASCII() {
		tail = "."
	}
	title := ""
	if room := cols - 1 - lipgloss.Width(mark) - 2; room >= 1 {
		title = mark + lipgloss.NewStyle().Foreground(pal.Fg).Bold(true).Render(" "+ansi.Truncate(name, room, tail)+" ")
	}
	tw := lipgloss.Width(title)

	var b strings.Builder
	b.WriteString(edge.Render(tl + hz))
	b.WriteString(title)
	b.WriteString(edge.Render(strings.Repeat(hz, max(cols-1-tw, 0)) + tr))
	lines := strings.Split(body, "\n")
	left, right := edge.Render(vl), edge.Render(vl)
	for i := range rows {
		b.WriteByte('\n')
		b.WriteString(left)
		row := ""
		if i < len(lines) {
			row = lines[i]
		}
		b.WriteString(row)
		if pad := cols - ansi.StringWidth(row); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(right)
	}
	b.WriteByte('\n')
	b.WriteString(edge.Render(bl + strings.Repeat(hz, cols) + br))
	return b.String()
}

// pipOccluders adds the view's box to the rectangles an image must not be
// drawn over. A kitty image is painted by the host over the finished frame, so
// without this an image in a pane under the view would draw on top of it.
func (m *OS) pipOccluders(rects []cellRect) []cellRect {
	if m.pip.rect.Empty() {
		return rects
	}
	r := m.pip.rect
	if rects == nil {
		m.pip.occluder = append(m.pip.occluder[:0], cellRect{r.Min.X, r.Min.Y, r.Dx(), r.Dy()})
		return m.pip.occluder
	}
	return append(rects, cellRect{r.Min.X, r.Min.Y, r.Dx(), r.Dy()})
}
