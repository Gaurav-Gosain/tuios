package app

import (
	"fmt"
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
//     client's own reserve (the View* margins below), so they stay whole and
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

// ViewLeftMargin is the columns this client's chrome takes on the left of its
// own screen. It is GetLeftMargin unless the session is wider than this
// client, when only this client's own chrome is kept: the session's agreed
// reserve is a layout quantity, and the view has no blank band to leave.
func (m *OS) ViewLeftMargin() int {
	if !m.cropsWidth() {
		return m.GetLeftMargin()
	}
	return m.clampReserve(m.OwnLayoutReserve().Left, m.GetRenderWidth())
}

// ViewRightMargin is ViewLeftMargin for the right edge.
func (m *OS) ViewRightMargin() int {
	if !m.cropsWidth() {
		return m.GetRightMargin()
	}
	return m.clampReserve(m.OwnLayoutReserve().Right, m.GetRenderWidth())
}

// ViewContentWidth is the width of this client's pane area on its own screen.
func (m *OS) ViewContentWidth() int {
	if !m.cropsWidth() {
		return m.GetContentWidth()
	}
	return max(m.GetRenderWidth()-m.ViewLeftMargin()-m.ViewRightMargin(), 0)
}

// ViewTopMargin is ViewLeftMargin for the top edge.
func (m *OS) ViewTopMargin() int {
	if !m.cropsHeight() {
		return m.GetTopMargin()
	}
	return m.clampReserve(m.OwnLayoutReserve().Top, m.GetRenderHeight())
}

// ViewBottomMargin is ViewLeftMargin for the bottom edge.
func (m *OS) ViewBottomMargin() int {
	if !m.cropsHeight() {
		return m.GetBottomMargin()
	}
	return m.clampReserve(m.OwnLayoutReserve().Bottom, m.GetRenderHeight())
}

// ViewUsableHeight is the height of this client's pane area on its own screen.
func (m *OS) ViewUsableHeight() int {
	if !m.cropsHeight() {
		return m.GetUsableHeight()
	}
	return max(m.GetRenderHeight()-m.ViewTopMargin()-m.ViewBottomMargin(), 0)
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
// It follows the focused pane's cursor with tmux's rule (tty_window_offset1):
// along each axis, a cursor within the first screenful shows the start, one
// within the last shows the end, and anywhere between puts the cursor in the
// middle across and on the bottom row down. A pane whose cursor is hidden,
// or not in terminal use, shows its top left corner instead, where tmux shows
// the window's: a full-screen program that hides its cursor still gets its
// own pane on screen.
func (m *OS) computeSessionView() sessionView {
	if !m.ViewCropped() {
		return sessionView{}
	}
	box := image.Rect(m.GetLeftMargin(), m.GetTopMargin(),
		m.GetLeftMargin()+m.GetContentWidth(), m.GetTopMargin()+m.GetUsableHeight())
	clip := image.Rect(m.ViewLeftMargin(), m.ViewTopMargin(),
		m.ViewLeftMargin()+m.ViewContentWidth(), m.ViewTopMargin()+m.ViewUsableHeight())
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

// paneCursor is a window's cursor in the layout frame, when it is shown. It
// takes the window's lock only if it is free and otherwise uses the position
// the last frame read, as getRealCursor does.
func paneCursor(w *terminal.Window) (int, int, bool) {
	if w.Terminal == nil || w.CopyModeVisible() || w.ScrollbackOffset > 0 {
		return 0, 0, false
	}
	hidden, pos := w.CachedCursorHidden, w.CachedCursor
	if w.TryRLockIO() {
		if w.Terminal != nil {
			hidden, pos = w.Terminal.IsCursorHidden(), w.Terminal.CursorPosition()
		}
		w.RUnlockIO()
	}
	if hidden || pos.X < 0 || pos.Y < 0 || pos.X >= w.ContentWidth() || pos.Y >= w.ContentHeight() {
		return 0, 0, false
	}
	border := 1
	if w.Tiled {
		border = 0
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

// PointerInLayout reports whether the mouse event being handled is in the
// layout frame.
func (m *OS) PointerInLayout() bool { return m.pointerInLayout }

// screenPoint maps a pointer position back to the screen when the event
// being handled was mapped to the layout frame, so a chrome hit test is
// always asked in screen positions.
func (m *OS) ScreenPoint(x, y int) (int, int) {
	if !m.pointerInLayout {
		return x, y
	}
	return m.sessionView.toScreen(x, y)
}

// PaneViewOn reports whether the last frame drew a view of a larger session.
func (m *OS) PaneViewOn() bool { return m.sessionView.on }

// MapPointer maps a mouse event to the layout frame when it belongs to the
// panes of a cropped view, and returns the event to handle. A press decides
// for itself and for the drag and the release that follow it, so a pane
// dragged over the rail stays a pane drag. internal/input calls it first for
// every mouse event and clears the mark with SetPointerInLayout(false) after.
func (m *OS) MapPointer(msg tea.Msg) tea.Msg {
	m.pointerInLayout = false
	var mouse tea.Mouse
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		mouse = tea.Mouse(msg)
	case tea.MouseReleaseMsg:
		mouse = tea.Mouse(msg)
	case tea.MouseMotionMsg:
		mouse = tea.Mouse(msg)
	case tea.MouseWheelMsg:
		mouse = tea.Mouse(msg)
	default:
		return msg
	}
	if !m.sessionView.on {
		m.pressInLayout = false
		return msg
	}
	mapIt := false
	switch msg.(type) {
	case tea.MouseClickMsg:
		mapIt = m.pointerOverPanes(mouse.X, mouse.Y)
		m.pressInLayout = mapIt
	case tea.MouseReleaseMsg:
		mapIt = m.pressInLayout
		m.pressInLayout = false
	case tea.MouseMotionMsg:
		if mouse.Button != tea.MouseNone {
			mapIt = m.pressInLayout
		} else {
			mapIt = m.pointerOverPanes(mouse.X, mouse.Y)
		}
	case tea.MouseWheelMsg:
		mapIt = m.pointerOverPanes(mouse.X, mouse.Y)
	}
	if !mapIt {
		return msg
	}
	mouse.X, mouse.Y = m.sessionView.toLayout(mouse.X, mouse.Y)
	m.pointerInLayout = true
	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mouse)
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mouse)
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(mouse)
	default:
		return tea.MouseWheelMsg(mouse)
	}
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
// session: arrows toward the parts out of view, and the session's size. It
// sits in the top right corner of the view's pane area, over the panes and
// under every panel. Nil when the whole session is on the screen.
func (m *OS) renderViewMark() *lipgloss.Layer {
	v := m.sessionView
	if !v.on || v.clip.Empty() {
		return nil
	}
	left, right, up, down := "←", "→", "↑", "↓"
	if m.Settings.UseASCIIOnly {
		left, right, up, down = "<", ">", "^", "v"
	}
	var arrows strings.Builder
	if v.offX > 0 {
		arrows.WriteString(left)
	}
	if v.offX+v.clip.Dx() < v.box.Dx() {
		arrows.WriteString(right)
	}
	if v.offY > 0 {
		arrows.WriteString(up)
	}
	if v.offY+v.clip.Dy() < v.box.Dy() {
		arrows.WriteString(down)
	}
	text := fmt.Sprintf("Part of session %dx%d", m.EffectiveWidth, m.EffectiveHeight)
	if arrows.Len() > 0 {
		text = arrows.String() + " " + text
	}
	label := tooltipLabel(text, v.clip.Dx(), theme.UI())
	x := v.clip.Max.X - lipgloss.Width(label)
	return lipgloss.NewLayer(label).X(max(x, v.clip.Min.X)).Y(v.clip.Min.Y).Z(config.ZIndexDock).ID(viewMarkLayerID)
}

// sendWindowSizeToDaemon sets the window_size policy of the session this
// client shows, on its daemon, off the Update goroutine. A client attached
// to a session on another machine sets it there.
func (m *OS) sendWindowSizeToDaemon(value string) {
	if !m.IsDaemonSession || m.DaemonClient == nil || m.SessionName == "" {
		return
	}
	build, host, name := m.DaemonClient.ClientVersion(), m.AttachedHost, m.SessionName
	go func() {
		var client *session.VerbClient
		var err error
		if host != "" {
			client, _, err = dialVerbThroughHost(host, build)
		} else {
			client, err = session.DialVerbClientAs(build)
		}
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_, _ = client.Call("set-option", map[string]any{"session": name, "key": "daemon.window_size", "value": value})
	}()
}
