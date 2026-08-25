package app

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Capture mode and the post-capture preview.
//
// The whole surface works on a terminal with no graphics support. The
// selection, the size chip and the preview are cells; kitty graphics add one
// thing and one thing only, which is seeing the pixel frame before leaving the
// terminal. Nothing here needs a tick: capture mode is driven entirely by the
// input events that already wake the loop, and the preview is a static panel,
// so no field added by this file is ever read by tickNeedsWork.

// CaptureTarget is what a capture-mode gesture is aiming at.
type CaptureTarget int

const (
	// CaptureWindow captures one window's pane.
	CaptureWindow CaptureTarget = iota
	// CaptureRegion captures a rectangle of the composed screen.
	CaptureRegion
	// CaptureScreen captures the whole composed screen.
	CaptureScreen
)

// captureState is capture mode. Every field is zero when the mode is off, so
// entering and leaving it is one assignment either way.
type captureState struct {
	Active bool
	// Hover is the window index under the pointer, or -1.
	Hover int
	// Keyboard marks an entry with no mouse, which changes the hint strip and
	// makes tab and enter the way through.
	Keyboard bool
	// Dragging is a region selection in progress.
	Dragging    bool
	AnchorX     int
	AnchorY     int
	CursorX     int
	CursorY     int
	wasTerminal bool
}

// screenshotResultMsg carries a finished render back to the Update goroutine,
// which is the only place a notification or a panel can be opened from.
type screenshotResultMsg struct {
	path     string
	format   shot.Format
	grid     *shot.Grid
	frame    *shot.Frame
	warnings []string
	copied   string
	// png is the picture the preview's pixel tier places. Empty on a host with
	// no kitty graphics, which is every host the text tier already serves.
	png []byte
	// pixelW and pixelH are png's own size, read from its header so the pixel
	// tier can keep the capture's shape instead of filling whatever cell box
	// the panel has.
	pixelW, pixelH int
	err            error
}

// screenshotPreview is the panel that opens after a capture.
type screenshotPreview struct {
	Open bool
	// Path is the file that was written. Escape deletes it.
	Path   string
	Format shot.Format
	// Grid is the captured cells, redrawn in the panel body at the text tier.
	Grid *shot.Grid
	// Scroll is the first grid row shown, for a capture taller than the body.
	Scroll int
	// ScrollX is the first grid column shown, for a capture wider than the body.
	ScrollX int
	// Note is the one quiet line about what the panel cannot show.
	Note string
	// Status carries a reason a control is missing, in plain words.
	Status string
	// CopyLabel is what c will actually do here, empty when nothing will.
	CopyLabel string
	// Copied names the tool that put the capture on the clipboard, empty when
	// nothing did.
	Copied string
	// Bytes is the file size, for the header line.
	Bytes int
	// PNG is the picture the pixel tier places, empty on a host that cannot
	// show one. It is a second render of the same grid when the saved format
	// is not PNG, because what the preview is for is seeing the frame.
	PNG []byte
	// PixelW and PixelH are the picture's own size. The pixel tier draws into a
	// cell box, and a box chosen without them stretches the capture to fill it.
	PixelW, PixelH int
}

// CaptureActive reports whether capture mode has the input.
func (m *OS) CaptureActive() bool { return m.Capture.Active }

// ScreenshotPreviewOpen reports whether the preview panel is up.
func (m *OS) ScreenshotPreviewOpen() bool { return m.ShotPreview.Open }

// BeginCapture enters capture mode. mouse says whether a pointer drove the
// entry, which decides whether the hint strip offers the drag or the keyboard
// path; on a mouse-less entry every window is still reachable with tab.
func (m *OS) BeginCapture(mouse bool) {
	if m.Capture.Active {
		return
	}
	m.CloseScreenshotPreview(false)
	wasTerminal := m.Mode == TerminalMode
	m.BeginPointerGesture()
	m.Capture = captureState{
		Active: true, Hover: -1, Keyboard: !mouse, wasTerminal: wasTerminal,
	}
	if !mouse {
		m.Capture.Hover = m.FocusedWindow
	}
}

// EndCapture leaves capture mode and restores the previous input mode.
func (m *OS) EndCapture() {
	if !m.Capture.Active {
		return
	}
	m.Capture = captureState{}
	m.EndPointerGesture()
}

// CaptureHoverNext moves the keyboard selection to the next visible window, so
// a capture never needs a mouse.
func (m *OS) CaptureHoverNext(delta int) {
	visible := m.captureVisibleWindows()
	if len(visible) == 0 {
		return
	}
	at := -1
	for i, idx := range visible {
		if idx == m.Capture.Hover {
			at = i
			break
		}
	}
	at = (at + delta + len(visible)*2) % len(visible)
	m.Capture.Hover = visible[at]
}

// captureVisibleWindows lists the indices a capture can aim at, in the order
// tab walks them.
func (m *OS) captureVisibleWindows() []int {
	var out []int
	for i, w := range m.Windows {
		if w == nil || w.Minimized || w.Workspace != m.CurrentWorkspace {
			continue
		}
		out = append(out, i)
	}
	return out
}

// rect is the selection rectangle in absolute cells, normalized so the drag
// reads the same in any direction. The far edge is exclusive, so a press and
// release on one cell is a 1x1 selection rather than a 0x0 one.
func (c captureState) rect() (x0, y0, x1, y1 int) {
	x0, x1 = min(c.AnchorX, c.CursorX), max(c.AnchorX, c.CursorX)
	y0, y1 = min(c.AnchorY, c.CursorY), max(c.AnchorY, c.CursorY)
	return x0, y0, x1 + 1, y1 + 1
}

// ScreenshotWindow captures one window and starts the render.
func (m *OS) ScreenshotWindow(index int) tea.Cmd {
	if index < 0 || index >= len(m.Windows) {
		index = m.FocusedWindow
	}
	if index < 0 || index >= len(m.Windows) || m.Windows[index] == nil {
		m.ShowNotification("There is no window to capture.", "warning", config.NotificationDuration)
		return nil
	}
	win := m.Windows[index]
	palette := m.shotPalette()
	grid := windowGrid(win, palette, m.screenshotSettings().Cursor)
	if grid == nil {
		m.ShowNotification("That window has no screen to capture.", "warning", config.NotificationDuration)
		return nil
	}
	return m.renderScreenshot(grid, windowRowTitle(win), false)
}

// ScreenshotScreen captures the whole composed frame: borders, dock, sidebar
// and every pane, exactly as drawn.
func (m *OS) ScreenshotScreen() tea.Cmd {
	return m.ScreenshotRegion(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
}

// ScreenshotRegion captures a rectangle of the composed frame. A region across
// two panes comes out as one flat picture that keeps the frame between them,
// because the frame is simply part of the composed cells.
func (m *OS) ScreenshotRegion(x0, y0, x1, y1 int) tea.Cmd {
	grid := m.composedGrid(x0, y0, x1, y1)
	if grid == nil {
		m.ShowNotification("That selection is too small to capture.", "warning", config.NotificationDuration)
		return nil
	}
	label := "screen"
	if x1-x0 != m.GetRenderWidth() || y1-y0 != m.GetRenderHeight() {
		label = "region"
	}
	return m.renderScreenshot(grid, label, true)
}

// windowGrid reads a live window's cells into a render grid, under the same
// read lock every other reader of that emulator takes.
func windowGrid(win *terminal.Window, palette *shot.Palette, cursor bool) *shot.Grid {
	if win == nil {
		return nil
	}
	win.RLockIO()
	defer win.RUnlockIO()
	return terminalGrid(win.Terminal, palette, cursor)
}

// terminalGrid walks an emulator's visible screen into a render grid.
func terminalGrid(t vt.Terminal, palette *shot.Palette, cursor bool) *shot.Grid {
	if t == nil || palette == nil {
		return nil
	}
	cols, rows := t.Width(), t.Height()
	if cols <= 0 || rows <= 0 {
		return nil
	}
	g := shot.NewGrid(cols, rows, palette.FG, palette.BG)
	for y := range rows {
		for x := range cols {
			cell := t.CellAt(x, y)
			if cell == nil {
				continue
			}
			g.Cells[y][x] = shot.MakeCell(cell.Content, cell.Width, cell.Style, cell.Link, palette)
		}
	}
	if cursor {
		pos := t.CursorPosition()
		g.ReverseCursor(pos.X, pos.Y)
	}
	return g
}

// composedGrid renders the current frame into a throwaway emulator and cuts a
// rectangle out of it. Only the client has a viewport, a layout and composed
// chrome, so this is the one place a region capture can happen; the emulator
// is one allocation per capture, on demand, and is dropped straight after.
func (m *OS) composedGrid(x0, y0, x1, y1 int) *shot.Grid {
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, w), min(y1, h)
	if x1-x0 < 1 || y1-y0 < 1 || w <= 0 || h <= 0 {
		return nil
	}
	g := frameToGrid(m.composeFrame(), w, h, m.shotPalette())
	if g == nil {
		return nil
	}
	return cropGrid(g, x0, y0, x1, y1)
}

// frameToGrid parses a composed frame string through a throwaway emulator and
// returns its cells.
//
// The newline substitution is the whole reason this is its own function.
// composeFrame separates rows with a bare newline, because that is what
// lipgloss joins with and what a host terminal's line discipline turns into a
// full return. An emulator in the default mode reads a bare line feed as "down
// one row, same column", so a frame whose rows are not padded to the full
// width cascades: every row lands one further right than the last and the
// capture comes out as a diagonal smear. Adding the carriage return is what
// the host would have done.
func frameToGrid(frame string, w, h int, palette *shot.Palette) *shot.Grid {
	if w <= 0 || h <= 0 {
		return nil
	}
	if palette == nil {
		palette = shot.XTermPalette()
	}
	em := vt.NewEmulator(w, h)
	if _, err := em.Write([]byte(strings.ReplaceAll(frame, "\n", "\r\n"))); err != nil {
		return nil
	}
	return terminalGrid(em, palette, false)
}

// cropGrid cuts a rectangle out of a grid.
func cropGrid(g *shot.Grid, x0, y0, x1, y1 int) *shot.Grid {
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, g.Cols), min(y1, g.Rows)
	if x1-x0 < 1 || y1-y0 < 1 {
		return nil
	}
	if x0 == 0 && y0 == 0 && x1 == g.Cols && y1 == g.Rows {
		return g
	}
	out := shot.NewGrid(x1-x0, y1-y0, g.FG, g.BG)
	for y := y0; y < y1; y++ {
		copy(out.Cells[y-y0], g.Cells[y][x0:x1])
	}
	return out
}

// shotPalette is this client's colours for the renderer. Unlike the daemon,
// the client has selected a tint of its own, so the process theme is the right
// answer here.
func (m *OS) shotPalette() *shot.Palette {
	p, _ := capture.Palette(theme.CurrentThemeID())
	return p
}

// screenshotSettings resolves the [screenshot] section this client holds.
func (m *OS) screenshotSettings() capture.Settings {
	cfg := config.ScreenshotConfig{}
	if m.UserConfig != nil {
		cfg = m.UserConfig.Screenshot
	}
	return capture.SettingsFrom(cfg, theme.CurrentThemeID(), theme.ActiveGlyphSetID())
}

// renderScreenshot renders, writes and copies off the Update goroutine.
//
// The render is milliseconds but it is not free, and the rule this codebase
// holds to is that work an event asked for runs in a command and comes back as
// a message. Nothing polls for it and nothing waits on it.
func (m *OS) renderScreenshot(grid *shot.Grid, label string, plain bool) tea.Cmd {
	settings := m.screenshotSettings()
	palette, paletteWarn := capture.Palette(settings.ThemeID)
	title := label
	if settings.Title != "" {
		title = config.FormatWindowTitle(label, m.FocusedWindow+1, "")
	}
	settings.Title = title
	frame, warnings := capture.Frame(settings, palette, plain)
	if paletteWarn != "" {
		// The no-theme notice rides with the rest of the warnings rather than
		// being raised here, so it arrives with the capture it describes
		// instead of ahead of it.
		warnings = append([]string{paletteWarn}, warnings...)
	}
	tryCopy := settings.Format != "" && m.screenshotCopyWanted()
	local := m.screenshotIsLocal()
	// The pixel tier needs a PNG whatever the saved format is, and only a host
	// that can show one pays for the second render.
	wantPixels := m.screenshotPreviewWanted() && m.screenshotGraphicsReady()

	return func() tea.Msg {
		data, err := shot.Render(settings.Format, grid, frame, nil)
		if err != nil {
			return screenshotResultMsg{err: err}
		}
		path, err := capture.ResolvePath("", settings.Directory, label, settings.Format, time.Now())
		if err != nil {
			return screenshotResultMsg{err: err}
		}
		if err := capture.Save(path, data); err != nil {
			return screenshotResultMsg{err: err}
		}
		msg := screenshotResultMsg{
			path: path, format: settings.Format, grid: grid,
			frame: frame, warnings: warnings,
		}
		if wantPixels {
			if settings.Format == shot.FormatPNG {
				msg.png = data
			} else if pixels, perr := shot.Render(shot.FormatPNG, grid, frame, nil); perr == nil {
				msg.png = pixels
			}
			if cfg, _, cerr := image.DecodeConfig(bytes.NewReader(msg.png)); cerr == nil {
				msg.pixelW, msg.pixelH = cfg.Width, cfg.Height
			}
		}
		if tryCopy && local {
			if tool, cerr := capture.CopyImage(path, data, settings.Format.MediaType()); cerr == nil {
				msg.copied = tool
			} else {
				msg.warnings = append(msg.warnings, cerr.Error())
			}
		}
		return msg
	}
}

// screenshotCopyWanted reads the config switch that decides whether a copy is
// attempted at all.
func (m *OS) screenshotCopyWanted() bool {
	if m.UserConfig == nil {
		return true
	}
	return m.UserConfig.Screenshot.CopyEnabled()
}

// screenshotPreviewWanted reads the config switch for the preview panel.
func (m *OS) screenshotPreviewWanted() bool {
	if m.UserConfig == nil {
		return true
	}
	return m.UserConfig.Screenshot.PreviewEnabled()
}

// screenshotIsLocal reports whether this process is on the user's own machine,
// which is the only condition under which it may run a clipboard helper.
//
// A tuios ssh client and a browser tab both run this code on the server. The
// helper there would write the operator's clipboard, not the user's, which is
// the trap the PR #133 review caught. So the answer is no unless this is a
// local attach.
func (m *OS) screenshotIsLocal() bool {
	return !m.IsRemoteClient() && capture.ImageRoute().Available
}

// HandleScreenshotResult takes a finished render: it notifies, and opens the
// preview panel when the config asks for one.
func (m *OS) HandleScreenshotResult(msg screenshotResultMsg) tea.Cmd {
	if msg.err != nil {
		m.ShowNotification("The screenshot failed. "+msg.err.Error(), "error", 0)
		return nil
	}
	m.ShotPreview = screenshotPreview{
		Open:   m.screenshotPreviewWanted(),
		Path:   msg.path,
		Format: msg.format,
		Grid:   msg.grid,
		Copied: msg.copied,
		PNG:    msg.png,
		PixelW: msg.pixelW,
		PixelH: msg.pixelH,
		Note:   m.screenshotPreviewNote(msg),
	}
	m.ShotPreview.CopyLabel, m.ShotPreview.Status = m.screenshotCopyOffer(msg)
	if info, err := os.Stat(msg.path); err == nil {
		m.ShotPreview.Bytes = int(info.Size())
	}
	if m.ShotPreview.Open {
		m.raiseOverlay(overlayKindShot)
	}

	line := "Saved to " + shortenPath(msg.path)
	if msg.copied != "" {
		line += ". Copied to the clipboard."
	}
	m.ShowNotification(line, "success", config.NotificationDuration)
	for _, w := range msg.warnings {
		m.ShowNotification(w, "warning", config.NotificationWarningDuration)
	}
	return nil
}

// screenshotPreviewNote is the one quiet line saying what the text tier cannot
// show. It is not an apology: the cells in the panel are the most faithful
// preview possible, because they come from the same renderer the content did.
// What they cannot carry is the pixel dressing, and saying so beats pretending.
func (m *OS) screenshotPreviewNote(msg screenshotResultMsg) string {
	if msg.frame == nil || msg.frame.Mode == shot.FrameNone {
		return ""
	}
	scale := msg.frame.Scale
	if scale < 1 {
		scale = 1
	}
	if msg.format != shot.FormatPNG {
		return "The frame shows in the saved file."
	}
	return fmt.Sprintf("Frame and wash at %dx. The frame shows in the saved file.", scale)
}

// screenshotCopyOffer decides what the panel's c key will do here, and returns
// the reason line when the answer is nothing.
//
// The whole point is that a key on the footer either works or is not drawn.
// A "copy" that silently does nothing on an SSH session is the inert control
// this project forbids.
func (m *OS) screenshotCopyOffer(msg screenshotResultMsg) (label, status string) {
	textFormat := msg.format == shot.FormatANSI || msg.format == shot.FormatText ||
		msg.format == shot.FormatSVG || msg.format == shot.FormatHTML
	if m.IsRemoteClient() {
		if textFormat {
			// OSC 52 reaches the user's real terminal wherever they are, so
			// the text formats copy honestly even over ssh.
			return "copy as text", "Files save on the server. Text still copies to you."
		}
		return "", "Files save on the server. This session cannot copy an image."
	}
	if textFormat {
		return "copy as text", ""
	}
	if route := capture.ImageRoute(); !route.Available {
		return "", route.Reason
	}
	return "copy image", ""
}

// CopyScreenshot runs the panel's c key. It only ever does what the label says.
func (m *OS) CopyScreenshot() tea.Cmd {
	p := &m.ShotPreview
	if !p.Open || p.CopyLabel == "" {
		return nil
	}
	data, err := os.ReadFile(p.Path) // #nosec G304 - this process just wrote it
	if err != nil {
		m.ShowNotification("The file could not be read to copy it.", "error", 0)
		return nil
	}
	if p.CopyLabel == "copy as text" {
		return m.CopyToClipboard(string(data))
	}
	tool, err := capture.CopyImage(p.Path, data, p.Format.MediaType())
	if err != nil {
		m.ShowNotification(err.Error(), "warning", config.NotificationWarningDuration)
		return nil
	}
	p.Copied = tool
	m.ShowNotification("Copied the image to the clipboard.", "success", config.NotificationDuration)
	return nil
}

// CloseScreenshotPreview closes the panel. discard deletes the file it wrote,
// so an accidental capture leaves nothing behind.
func (m *OS) CloseScreenshotPreview(discard bool) {
	if !m.ShotPreview.Open {
		m.ShotPreview = screenshotPreview{}
		return
	}
	path := m.ShotPreview.Path
	m.ShotPreview = screenshotPreview{}
	m.clearScreenshotGraphics()
	if discard && path != "" {
		if err := os.Remove(path); err == nil {
			m.ShowNotification("The screenshot was deleted.", "info", config.NotificationDuration)
		}
	}
}

// OpenScreenshotFile hands the file to the OS viewer. It is offered only on a
// local client, because a viewer opened on the server is a window nobody is
// sitting in front of.
func (m *OS) OpenScreenshotFile() tea.Cmd {
	if !m.ShotPreview.Open || m.IsRemoteClient() {
		return nil
	}
	path := m.ShotPreview.Path
	return func() tea.Msg {
		_ = openInOSViewer(path)
		return nil
	}
}

// RetakeScreenshot discards the file and returns to capture mode. It is only
// ever reached from the panel's r key, so the mode comes back with the
// keyboard hints rather than offering a drag nobody asked for; the first
// pointer motion switches it over.
func (m *OS) RetakeScreenshot() {
	m.CloseScreenshotPreview(true)
	m.BeginCapture(false)
}

// ScrollScreenshotPreview moves the preview viewport. It is a viewport, not a
// thumbnail: a capture larger than the panel scrolls rather than shrinking to
// something nobody can read.
func (m *OS) ScrollScreenshotPreview(dx, dy int) {
	p := &m.ShotPreview
	if !p.Open || p.Grid == nil {
		return
	}
	bodyW, bodyH := m.screenshotPreviewBody()
	p.Scroll = clampInt(p.Scroll+dy, 0, max(0, p.Grid.Rows-bodyH))
	p.ScrollX = clampInt(p.ScrollX+dx, 0, max(0, p.Grid.Cols-bodyW))
}

// shortenPath replaces the home prefix with ~ so a notification fits the slot
// it is drawn into.
func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + strings.TrimPrefix(p, home)
}

// IsRemoteClient reports whether this process is running beside the daemon
// with the user at the far end of a network, rather than on the user's own
// machine. It gates everything that would otherwise act on the wrong desktop.
func (m *OS) IsRemoteClient() bool { return m.RemoteClient }

// CaptureHover is the window index capture mode is aiming at, or -1.
func (m *OS) CaptureHover() int { return m.Capture.Hover }

// CaptureDragActive reports whether a region selection is in progress. The
// mouse-motion filter reads it, so a drag keeps getting motion events.
func (m *OS) CaptureDragActive() bool { return m.Capture.Active && m.Capture.Dragging }

// BeginCaptureDrag anchors a region selection at a cell.
func (m *OS) BeginCaptureDrag(x, y int) {
	if !m.Capture.Active {
		return
	}
	m.Capture.Dragging = true
	// A pointer is driving now, so the hint strip stops offering the keyboard
	// path and starts offering the drag the user is already doing.
	m.Capture.Keyboard = false
	m.Capture.AnchorX, m.Capture.AnchorY = x, y
	m.Capture.CursorX, m.Capture.CursorY = x, y
	m.Capture.Hover = m.CaptureWindowAt(x, y)
}

// UpdateCapturePointer moves the hover or the drag cursor.
func (m *OS) UpdateCapturePointer(x, y int, held bool) {
	if !m.Capture.Active {
		return
	}
	if m.Capture.Dragging && held {
		m.Capture.CursorX, m.Capture.CursorY = x, y
		return
	}
	if !held {
		// A bare hover with no button retires a drag whose release went
		// missing, the way every other gesture in this codebase does.
		m.Capture.Dragging = false
	}
	m.Capture.Hover = m.CaptureWindowAt(x, y)
	m.Capture.Keyboard = false
}

// captureClickSlop is how small a drag still counts as a click. A hand moves a
// cell or two between press and release, and a one-cell screenshot is never
// what anyone meant.
const captureClickSlop = 2

// FinishCaptureDrag ends the gesture and starts the capture it selected.
func (m *OS) FinishCaptureDrag() tea.Cmd {
	if !m.Capture.Active {
		return nil
	}
	if !m.Capture.Dragging {
		m.EndCapture()
		return nil
	}
	x0, y0, x1, y1 := m.Capture.rect()
	target := m.Capture.Hover
	m.EndCapture()
	if x1-x0 <= captureClickSlop && y1-y0 <= captureClickSlop {
		if target < 0 {
			// A click on empty desktop takes the whole screen, which is the
			// only thing there is to capture there.
			return m.ScreenshotScreen()
		}
		return m.ScreenshotWindow(target)
	}
	return m.ScreenshotRegion(x0, y0, x1, y1)
}

// ScrollScreenshotPreviewHome puts the preview viewport back at the top left.
func (m *OS) ScrollScreenshotPreviewHome() {
	m.ShotPreview.Scroll, m.ShotPreview.ScrollX = 0, 0
}

// ScreenshotFocusedWindow captures whatever window has the focus. It is the
// zero-decision path: the command palette entry, the context menu row and the
// screenshot_window action all land here.
func (m *OS) ScreenshotFocusedWindow() tea.Cmd { return m.ScreenshotWindow(m.FocusedWindow) }

// screenshotPlacementState is where the preview's picture was last drawn, so a
// frame that changed nothing emits no escape at all.
type screenshotPlacementState struct {
	x, y, cols, rows int
	path             string
}
