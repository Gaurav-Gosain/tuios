package app

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A session larger than this client.
//
// Under the daemon's window_size policy (largest or latest; see
// internal/session/window_size.go) a session can be bigger than the terminal
// a client runs in. Every client lays the panes out in the session's size,
// because the panes' PTYs have one size each, and a client smaller than that
// draws a view of the layout: the part around the focused pane's cursor, the
// way tmux draws a window larger than its client.
//
// Two frames of reference follow from that, and the code keeps them apart:
//
//   - The layout frame is the session's. GetLayoutWidth, GetLeftMargin,
//     GetContentWidth, GetUsableHeight and the rest place the panes in it,
//     and window X and Y are in it. Tiling, snapping, clamping and the
//     daemon's state all use it.
//   - The view frame is this client's terminal. The rail, the dock, the
//     overlays and every other piece of chrome are drawn in it, around this
//     client's own reserve (viewReserve below), so they stay whole and
//     at the edges of the screen whatever the session's size.
//
// sessionView maps the one onto the other: the pane layers are composed shifted
// by (dx, dy) and clipped to the view's pane area, a pointer over the pane
// area is shifted back before it is tested against a pane, and the cursor is
// shifted forward. Nothing is cached per offset, so the style cache and the
// per-pane cell layers are reused as the view moves.

// windowSizeAware reports whether the daemon may hand this client a session
// larger than its terminal. Against a daemon that predates the policy the
// session is never larger, and this keeps the client drawing as it did.
func (m *OS) windowSizeAware() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.DaemonClient.WindowSizeAware()
}

// cropsWidth reports whether the session is wider than this client.
func (m *OS) cropsWidth() bool {
	return m.Width > 0 && m.EffectiveWidth > m.Width && m.windowSizeAware()
}

// cropsHeight reports whether the session is taller than this client.
func (m *OS) cropsHeight() bool {
	return m.Height > 0 && m.EffectiveHeight > m.Height && m.windowSizeAware()
}

// ViewCropped reports whether this client shows only part of the session.
func (m *OS) ViewCropped() bool {
	return m.cropsWidth() || m.cropsHeight()
}

// GetLayoutWidth is the width the panes are laid out in: the session's when
// it is wider than this client, otherwise the render width.
func (m *OS) GetLayoutWidth() int {
	if m.cropsWidth() {
		return m.EffectiveWidth
	}
	return m.GetRenderWidth()
}

// GetLayoutHeight is the height the panes are laid out in. See GetLayoutWidth.
func (m *OS) GetLayoutHeight() int {
	if m.cropsHeight() {
		return m.EffectiveHeight
	}
	return m.GetRenderHeight()
}

// viewReserve is the chrome this client takes around its own screen. Along
// an axis the session fits, it is the layout's reserve. Along an axis the
// session overflows, only this client's own chrome is kept: the session's
// agreed reserve is a layout quantity, and the view has no blank band to leave.
func (m *OS) viewReserve() session.LayoutReserve {
	r := m.paneReserve()
	own := m.OwnLayoutReserve()
	if m.cropsWidth() {
		r.Left, r.Right = own.Left, own.Right
	}
	if m.cropsHeight() {
		r.Top, r.Bottom = own.Top, own.Bottom
	}
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	return session.LayoutReserve{
		Left:   m.clampReserve(r.Left, w),
		Right:  m.clampReserve(r.Right, w),
		Top:    m.clampReserve(r.Top, h),
		Bottom: m.clampReserve(r.Bottom, h),
	}
}

// ViewContentWidth is the width of this client's pane area on its own screen.
func (m *OS) ViewContentWidth() int {
	if !m.cropsWidth() {
		return m.GetContentWidth()
	}
	r := m.viewReserve()
	return max(m.GetRenderWidth()-r.Left-r.Right, 0)
}

// ViewUsableHeight is the height of this client's pane area on its own screen.
func (m *OS) ViewUsableHeight() int {
	if !m.cropsHeight() {
		return m.GetUsableHeight()
	}
	r := m.viewReserve()
	return max(m.GetRenderHeight()-r.Top-r.Bottom, 0)
}

// sessionView is one frame's mapping from the layout frame to the view frame.
type sessionView struct {
	on bool
	// dx and dy are added to a layout position to place it on the screen.
	dx, dy int
	// clip is the view's pane area, on the screen.
	clip image.Rectangle
	// box is the session's pane area, in the layout frame, and off is how far
	// into it the view starts.
	box  image.Rectangle
	offX int
	offY int
}

// toScreen maps a layout position to the screen.
func (v sessionView) toScreen(x, y int) (int, int) { return x + v.dx, y + v.dy }

// toLayout maps a screen position to the layout frame.
func (v sessionView) toLayout(x, y int) (int, int) { return x - v.dx, y - v.dy }

// visible is the part of the layout frame the view shows.
func (v sessionView) visible() image.Rectangle {
	return v.clip.Sub(image.Pt(v.dx, v.dy))
}

// computeSessionView works out where the view sits for this frame.
//
// It follows the focused pane's cursor with tmux's rule (tty_window_offset1
// in tty.c): along each axis, a cursor within the first screenful shows the
// start, one within the last shows the end, and anywhere between puts the
// cursor in the middle across and on the bottom row down.
//
// Two departures from tmux, both on purpose. tmux shows the window's top left
// corner (offset 0, 0) when the pane's cursor is hidden (no MODE_CURSOR);
// tuios follows the hidden cursor's position, see paneCursor. And in copy
// mode tuios follows the copy-mode cursor. Only a pane with no position to
// follow shows its own top left corner.
func (m *OS) computeSessionView() sessionView {
	if !m.ViewCropped() {
		return sessionView{}
	}
	box := image.Rect(m.GetLeftMargin(), m.GetTopMargin(),
		m.GetLeftMargin()+m.GetContentWidth(), m.GetTopMargin()+m.GetUsableHeight())
	view := m.viewReserve()
	clip := image.Rect(view.Left, view.Top,
		view.Left+m.ViewContentWidth(), view.Top+m.ViewUsableHeight())
	v := sessionView{on: true, box: box, clip: clip}

	cx, cy, cursor := m.viewTarget()
	v.offX = followOffset(cx-box.Min.X, box.Dx(), clip.Dx(), cursor, false)
	v.offY = followOffset(cy-box.Min.Y, box.Dy(), clip.Dy(), cursor, true)
	v.dx = clip.Min.X - box.Min.X - v.offX
	v.dy = clip.Min.Y - box.Min.Y - v.offY
	return v
}

// followOffset is the view's start along one axis. pos is the target inside
// the box, boxLen the box's length and viewLen the view's.
func followOffset(pos, boxLen, viewLen int, cursor, vertical bool) int {
	if viewLen >= boxLen || viewLen <= 0 {
		return 0
	}
	last := boxLen - viewLen
	if !cursor {
		return min(max(pos, 0), last)
	}
	switch {
	case pos < viewLen:
		return 0
	case pos > last:
		return last
	case vertical:
		return pos - viewLen + 1
	default:
		return pos - viewLen/2
	}
}

// viewTarget is the layout position the view follows: the focused pane's
// cursor, or its top left corner when the cursor is not there to follow.
func (m *OS) viewTarget() (x, y int, cursor bool) {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return m.GetLeftMargin(), m.GetTopMargin(), false
	}
	w := m.Windows[m.FocusedWindow]
	if w == nil {
		return m.GetLeftMargin(), m.GetTopMargin(), false
	}
	if px, py, ok := paneCursor(w); ok {
		return px, py, true
	}
	return w.X, w.Y, false
}

// paneCursor is the position the view follows in a window, in the layout
// frame: the copy-mode cursor while copy mode is shown, otherwise the
// terminal's cursor, shown or hidden, read as getRealCursor reads it.
//
// A hidden cursor is followed on purpose. An agent CLI hides the terminal's
// cursor and draws its own, but it still moves the real one to its input box,
// so the hidden position is where the person is typing. Falling back to the
// pane's top left corner showed such a program's header and never its input.
func paneCursor(w *terminal.Window) (int, int, bool) {
	if w.Terminal == nil {
		return 0, 0, false
	}
	border := w.BorderOffset()
	inside := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < w.ContentWidth() && y < w.ContentHeight()
	}
	if w.CopyModeVisible() && w.CopyMode != nil {
		x, y := w.CopyMode.CursorX, w.CopyMode.CursorY
		if !inside(x, y) {
			return 0, 0, false
		}
		return w.X + border + x, w.Y + border + y, true
	}
	pos, _, ok := w.GuestCursor()
	if !ok || !inside(pos.X, pos.Y) {
		return 0, 0, false
	}
	return w.X + border + pos.X, w.Y + border + pos.Y, true
}

// PointerToLayout maps a pointer on the screen to the layout frame when it is
// over the view's pane area, and reports whether it did. The rail, the dock
// and anything else outside the pane area keep screen positions.
func (m *OS) PointerToLayout(x, y int) (int, int, bool) {
	v := m.sessionView
	if !v.on || !image.Pt(x, y).In(v.clip) {
		return x, y, false
	}
	lx, ly := v.toLayout(x, y)
	return lx, ly, true
}

// SetPointerInLayout records that the mouse event being handled was mapped to
// the layout frame. The chrome hit tests read it to map the position back,
// since the rail, the dock and the picture-in-picture view are drawn on the
// screen. internal/input sets it around each mouse event.
func (m *OS) SetPointerInLayout(on bool) { m.pointerInLayout = on }

// screenPoint maps a pointer position back to the screen when the event
// being handled was mapped to the layout frame, so a chrome hit test is
// always asked in screen positions.
func (m *OS) ScreenPoint(x, y int) (int, int) {
	if !m.pointerInLayout {
		return x, y
	}
	return m.sessionView.toScreen(x, y)
}

// MapPointer maps a mouse event to the layout frame when it belongs to the
// panes of a cropped view, and returns the event to handle. A press decides
// for itself and for the drag and the release that follow it, so a pane
// dragged over the rail stays a pane drag. internal/input calls it first for
// every mouse event and clears the mark with SetPointerInLayout(false) after.
func (m *OS) MapPointer(msg tea.Msg) tea.Msg {
	m.pointerInLayout = false
	if !m.sessionView.on {
		if _, ok := msg.(tea.MouseMsg); ok {
			m.pressInLayout = false
		}
		return msg
	}
	switch e := msg.(type) {
	case tea.MouseClickMsg:
		m.pressInLayout = m.pointerOverPanes(e.X, e.Y)
		if m.pressInLayout {
			return tea.MouseClickMsg(m.mouseToLayout(tea.Mouse(e)))
		}
	case tea.MouseReleaseMsg:
		held := m.pressInLayout
		m.pressInLayout = false
		if held {
			return tea.MouseReleaseMsg(m.mouseToLayout(tea.Mouse(e)))
		}
	case tea.MouseMotionMsg:
		mapIt := m.pressInLayout
		if e.Button == tea.MouseNone {
			mapIt = m.pointerOverPanes(e.X, e.Y)
		}
		if mapIt {
			return tea.MouseMotionMsg(m.mouseToLayout(tea.Mouse(e)))
		}
	case tea.MouseWheelMsg:
		if m.pointerOverPanes(e.X, e.Y) {
			return tea.MouseWheelMsg(m.mouseToLayout(tea.Mouse(e)))
		}
	}
	return msg
}

// mouseToLayout maps a mouse event's position to the layout frame and marks
// the event being handled as mapped.
func (m *OS) mouseToLayout(mouse tea.Mouse) tea.Mouse {
	mouse.X, mouse.Y = m.sessionView.toLayout(mouse.X, mouse.Y)
	m.pointerInLayout = true
	return mouse
}

// pointerOverPanes reports whether a pointer on the screen is over the
// view's pane area with nothing drawn on the screen above the panes there.
func (m *OS) pointerOverPanes(x, y int) bool {
	if !image.Pt(x, y).In(m.sessionView.clip) {
		return false
	}
	if m.ContextMenuActive() || m.AnyOverlayOpen() || m.OverlayActive() ||
		m.Renaming() || m.CaptureActive() || m.ReviewOpen() || m.ShowScrollbackBrowser {
		return false
	}
	return !m.PiPAt(x, y)
}

// viewMarkLayerID names the mark layer.
const viewMarkLayerID = "view-mark"

// renderViewMark is the mark that this client shows only part of the
// session: arrows toward the parts out of view, and the session's size.
//
// It sits at the right end of the dock's rule, the line between the panes and
// the dock. That row is chrome that carries nothing else, so the mark covers
// no pane, no title bar and no dock control, and it is always in the same
// place. With the dock hidden there is no such row, and the mark goes in the
// bottom right corner of the view's pane area, over pane content, which is the
// one place left. Nil when the whole session is on the screen.
func (m *OS) renderViewMark() *lipgloss.Layer {
	v := m.sessionView
	if !v.on || v.clip.Empty() {
		return nil
	}
	var arrows uint8
	if v.offX > 0 {
		arrows |= 1
	}
	if v.offX+v.clip.Dx() < v.box.Dx() {
		arrows |= 2
	}
	if v.offY > 0 {
		arrows |= 4
	}
	if v.offY+v.clip.Dy() < v.box.Dy() {
		arrows |= 8
	}
	pal := theme.UI()
	key := viewMarkKey{
		arrows: arrows, ascii: m.Settings.UseASCIIOnly,
		w: m.EffectiveWidth, h: m.EffectiveHeight, room: m.GetRenderWidth(),
		surface: pal.Surface, fg: pal.Fg,
	}
	c := &m.viewMark
	if !c.valid || c.key != key {
		c.key, c.valid = key, true
		c.label = viewMarkLabel(key, pal)
		c.width = lipgloss.Width(c.label)
	}
	label, width := c.label, c.width
	x, y := m.GetRenderWidth()-width-1, v.clip.Max.Y-1
	switch m.Settings.DockbarPosition {
	case "hidden":
		x = v.clip.Max.X - width
	case "top":
		y = m.viewReserve().Top - 1
	default:
		y = m.viewReserve().Top + m.ViewUsableHeight()
	}
	return lipgloss.NewLayer(label).X(max(x, 0)).Y(max(y, 0)).Z(config.ZIndexDock + 1).ID(viewMarkLayerID)
}

// viewMarkKey is everything the mark's label is built from: which arrows it
// shows, the session's size, the room on the screen and the two theme colours.
type viewMarkKey struct {
	arrows  uint8
	ascii   bool
	w, h    int
	room    int
	surface color.Color
	fg      color.Color
}

// viewMarkCache keeps the mark's label across frames. The view and the
// session's size change rarely, and the label is a Sprintf and a lipgloss
// render that would otherwise run on every frame.
type viewMarkCache struct {
	valid bool
	key   viewMarkKey
	label string
	width int
}

// viewMarkLabel builds the mark's label for key.
func viewMarkLabel(key viewMarkKey, pal overlay.Palette) string {
	glyphs := [4]string{"←", "→", "↑", "↓"}
	if key.ascii {
		glyphs = [4]string{"<", ">", "^", "v"}
	}
	var arrows strings.Builder
	for i, g := range glyphs {
		if key.arrows&(1<<i) != 0 {
			arrows.WriteString(g)
		}
	}
	text := fmt.Sprintf("Part of session %dx%d", key.w, key.h)
	if arrows.Len() > 0 {
		text = arrows.String() + " " + text
	}
	return tooltipLabel(text, key.room, pal)
}

// sendWindowSizeToDaemon sets the window_size policy of the session this
// client shows, on its daemon, off the Update goroutine. A client attached
// to a session on another machine sets it there.
func (m *OS) sendWindowSizeToDaemon(value string) {
	if !m.IsDaemonSession || m.DaemonClient == nil || m.SessionName == "" {
		return
	}
	dial, name := m.verbDialer(), m.SessionName
	go func() {
		client, err := dial()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_, _ = client.Call("set-option", map[string]any{"session": name, "key": "daemon.window_size", "value": value})
	}()
}

// paneChromeAt places chrome a pane anchors at a layout position (the
// copy-mode search prompt, the multi copy "Save to" prompt) on the screen. It
// is the position itself unless the client shows a view of a larger session,
// when it is shifted by the view and kept inside the view's pane area, so a
// prompt at the bottom of a pane taller than the view sits on the view's last
// row. w and h are the size of what is placed.
func (m *OS) paneChromeAt(x, y, w, h int) (int, int) {
	v := m.sessionView
	if !v.on {
		return x, y
	}
	x, y = v.toScreen(x, y)
	x = max(min(x, v.clip.Max.X-w), v.clip.Min.X)
	y = max(min(y, v.clip.Max.Y-h), v.clip.Min.Y)
	return x, y
}
