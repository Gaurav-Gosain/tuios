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

func (m *OS) windowSizeAware() bool {
	return m.IsDaemonSession && m.DaemonClient != nil
}

func (m *OS) cropsWidth() bool {
	return m.Width > 0 && m.EffectiveWidth > m.Width && m.windowSizeAware()
}

func (m *OS) cropsHeight() bool {
	return m.Height > 0 && m.EffectiveHeight > m.Height && m.windowSizeAware()
}

// ViewCropped reports whether this client shows only part of the session.
func (m *OS) ViewCropped() bool {
	return m.cropsWidth() || m.cropsHeight()
}

func (m *OS) widthDiffers() bool {
	if !m.windowSizeAware() {
		return false
	}
	if m.Width > 0 && m.EffectiveWidth > 0 && m.EffectiveWidth != m.Width {
		return true
	}
	own, agreed, ok := m.reserveDiffers()
	return ok && (own.Left > agreed.Left || own.Right > agreed.Right)
}

func (m *OS) heightDiffers() bool {
	if !m.windowSizeAware() {
		return false
	}
	if m.Height > 0 && m.EffectiveHeight > 0 && m.EffectiveHeight != m.Height {
		return true
	}
	own, agreed, ok := m.reserveDiffers()
	return ok && (own.Top > agreed.Top || own.Bottom > agreed.Bottom)
}

func (m *OS) reserveDiffers() (own, agreed session.LayoutReserve, ok bool) {
	if m.DaemonClient == nil || m.DaemonClient.SessionWindowSize() != config.WindowSizeLatest ||
		!m.DaemonClient.SessionSizedByOther() ||
		m.EffectiveWidth <= 0 || m.EffectiveHeight <= 0 {
		return own, agreed, false
	}
	return m.OwnLayoutReserve(), m.SessionReserve, true
}

func (m *OS) viewShifted() bool {
	return m.widthDiffers() || m.heightDiffers()
}

// GetLayoutWidth is the width the panes are laid out in: the session's when
// its size differs from this client's, otherwise the render width.
func (m *OS) GetLayoutWidth() int {
	if m.widthDiffers() {
		return m.EffectiveWidth
	}
	return m.GetRenderWidth()
}

// GetLayoutHeight is the height the panes are laid out in. See GetLayoutWidth.
func (m *OS) GetLayoutHeight() int {
	if m.heightDiffers() {
		return m.EffectiveHeight
	}
	return m.GetRenderHeight()
}

func (m *OS) viewReserve() session.LayoutReserve {
	r := m.paneReserve()
	own := m.OwnLayoutReserve()
	if m.widthDiffers() {
		r.Left, r.Right = own.Left, own.Right
	}
	if m.heightDiffers() {
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

// ViewContentWidth is the width of this client's pane area on its own
// screen: the room between its own chrome. With a session smaller than this
// client it is more than the session's panes take, and the rest is leftover.
func (m *OS) ViewContentWidth() int {
	if !m.widthDiffers() {
		return m.GetContentWidth()
	}
	r := m.viewReserve()
	return max(m.GetRenderWidth()-r.Left-r.Right, 0)
}

// ViewUsableHeight is the height of this client's pane area on its own screen.
func (m *OS) ViewUsableHeight() int {
	if !m.heightDiffers() {
		return m.GetUsableHeight()
	}
	r := m.viewReserve()
	return max(m.GetRenderHeight()-r.Top-r.Bottom, 0)
}

type sessionView struct {
	on     bool
	dx, dy int
	clip   image.Rectangle
	frame  image.Rectangle
	box    image.Rectangle
	offX   int
	offY   int
}

func (v sessionView) toScreen(x, y int) (int, int) { return x + v.dx, y + v.dy }

func (v sessionView) toLayout(x, y int) (int, int) { return x - v.dx, y - v.dy }

func (v sessionView) visible() image.Rectangle {
	return v.clip.Sub(image.Pt(v.dx, v.dy))
}

func (m *OS) computeSessionView() sessionView {
	if !m.viewShifted() {
		return sessionView{}
	}
	box := image.Rect(m.GetLeftMargin(), m.GetTopMargin(),
		m.GetLeftMargin()+m.GetContentWidth(), m.GetTopMargin()+m.GetUsableHeight())
	view := m.viewReserve()
	frame := image.Rect(view.Left, view.Top,
		view.Left+m.ViewContentWidth(), view.Top+m.ViewUsableHeight())
	clip := image.Rect(frame.Min.X, frame.Min.Y,
		frame.Min.X+min(frame.Dx(), box.Dx()), frame.Min.Y+min(frame.Dy(), box.Dy()))
	v := sessionView{on: true, box: box, clip: clip, frame: frame}

	cx, cy, cursor := m.viewTarget()
	v.offX = followOffset(cx-box.Min.X, box.Dx(), clip.Dx(), cursor, false)
	v.offY = followOffset(cy-box.Min.Y, box.Dy(), clip.Dy(), cursor, true)
	v.dx = clip.Min.X - box.Min.X - v.offX
	v.dy = clip.Min.Y - box.Min.Y - v.offY
	return v
}

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
//
// The leftover (pane_leftover.go) is its own region. A mouse event over it, or
// a drag or release that follows a press on it, is swallowed: see
// PointerSwallowed. Without that it reached the window manager with screen
// positions, which the panes read in the layout frame, so a click on the dots
// focused, moved or resized whichever pane those positions named.
func (m *OS) MapPointer(msg tea.Msg) tea.Msg {
	m.pointerInLayout = false
	m.pointerSwallowed = false
	if !m.sessionView.on {
		if _, ok := msg.(tea.MouseMsg); ok {
			m.pressInLayout = false
			m.pressInLeftover = false
		}
		return msg
	}
	if m.leftoverTakes(msg) {
		m.pointerSwallowed = true
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

func (m *OS) leftoverTakes(msg tea.Msg) bool {
	mouse, ok := msg.(tea.MouseMsg)
	if !ok {
		return false
	}
	ev := mouse.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.pressInLeftover = m.pointerOverLeftover(ev.X, ev.Y)
		return m.pressInLeftover
	case tea.MouseReleaseMsg:
		held := m.pressInLeftover
		m.pressInLeftover = false
		return held
	case tea.MouseMotionMsg:
		if ev.Button != tea.MouseNone {
			return m.pressInLeftover
		}
	}
	return m.pointerOverLeftover(ev.X, ev.Y)
}

func (m *OS) pointerOverLeftover(x, y int) bool {
	v := m.sessionView
	p := image.Pt(x, y)
	if !p.In(v.frame) || p.In(v.clip) {
		return false
	}
	if m.ContextMenuActive() || m.PathPickerOpen() || m.AnyOverlayOpen() || m.OverlayActive() ||
		m.Renaming() || m.CaptureActive() || m.ReviewOpen() || m.ShowScrollbackBrowser {
		return false
	}
	if m.SidebarBandContains(x, y) {
		return false
	}
	return !m.PiPAt(x, y)
}

// PointerSwallowed reports whether the mouse event MapPointer last mapped
// belongs to the leftover. internal/input then does nothing with it.
func (m *OS) PointerSwallowed() bool { return m.pointerSwallowed }

func (m *OS) mouseToLayout(mouse tea.Mouse) tea.Mouse {
	mouse.X, mouse.Y = m.sessionView.toLayout(mouse.X, mouse.Y)
	m.pointerInLayout = true
	return mouse
}

func (m *OS) pointerOverPanes(x, y int) bool {
	if !image.Pt(x, y).In(m.sessionView.clip) {
		return false
	}
	if m.ContextMenuActive() || m.PathPickerOpen() || m.AnyOverlayOpen() || m.OverlayActive() ||
		m.Renaming() || m.CaptureActive() || m.ReviewOpen() || m.ShowScrollbackBrowser {
		return false
	}
	return !m.PiPAt(x, y)
}

const viewMarkLayerID = "view-mark"

func (m *OS) renderViewMark() *lipgloss.Layer {
	v := m.sessionView
	if !v.on || v.clip.Empty() || (v.clip.Dx() >= v.box.Dx() && v.clip.Dy() >= v.box.Dy()) {
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
	switch {
	case m.Settings.DockbarPosition == "hidden" || m.Settings.DockCompact:
		x = v.clip.Max.X - width
	case m.Settings.DockbarPosition == "top":
		y = m.viewReserve().Top - 1
	default:
		y = m.viewReserve().Top + m.ViewUsableHeight()
	}
	return lipgloss.NewLayer(label).X(max(x, 0)).Y(max(y, 0)).Z(config.ZIndexDock + 1).ID(viewMarkLayerID)
}

type viewMarkKey struct {
	arrows  uint8
	ascii   bool
	w, h    int
	room    int
	surface color.Color
	fg      color.Color
}

type viewMarkCache struct {
	valid bool
	key   viewMarkKey
	label string
	width int
}

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

func (m *OS) paneOnScreen(w *terminal.Window) (image.Rectangle, bool) {
	r := image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
	v := m.sessionView
	if !v.on {
		return r, true
	}
	r = r.Add(image.Pt(v.dx, v.dy)).Intersect(v.clip)
	return r, !r.Empty()
}

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
