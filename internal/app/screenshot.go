package app

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/shot"
	"tuios.dev/vt"
)

const (
	shotStatusSaving     = "Saving the image…"
	shotStatusCopied     = "Copied."
	shotStatusCopyFailed = "The copy failed. The file is saved."
	shotStatusDeleted    = "The screenshot was deleted."
	shotCopyAsText       = "copy as text"
	shotCopyImage        = "copy image"
)

type captureState struct {
	Active      bool
	Hover       int
	Keyboard    bool
	Dragging    bool
	OverOverlay bool
	AnchorX     int
	AnchorY     int
	CursorX     int
	CursorY     int
	wasTerminal bool
}

type screenshotResultMsg struct {
	capture        int
	path           string
	format         shot.Format
	grid           *shot.Grid
	frame          *shot.Frame
	warnings       []string
	png            []byte
	transmit       []byte
	pixelW, pixelH int
	bytes          int
	err            error
}

type screenshotCopiedMsg struct {
	capture int
	err     error
}

type screenshotPreview struct {
	Open           bool
	Path           string
	Format         shot.Format
	Grid           *shot.Grid
	Scroll         int
	ScrollX        int
	Pending        bool
	Note           string
	Status         string
	CopyLabel      string
	Bytes          int
	PNG            []byte
	Transmit       []byte
	PixelW, PixelH int
	Capture        int
}

// CaptureActive reports whether capture mode has the input.
func (m *OS) CaptureActive() bool { return m.Capture.Active }

// ScreenshotPreviewOpen reports whether the preview panel is up.
func (m *OS) ScreenshotPreviewOpen() bool { return m.ShotPreview.Open }

// BeginCapture enters capture mode. mouse says whether a pointer drove the
// entry, which decides whether the hint strip offers the drag or the keyboard
// path; on a mouse-less entry every window is still reachable with tab.
func (m *OS) BeginCapture(mouse bool) {
	if m.Capture.Active || m.learnOff(learnNoteShot) {
		return
	}
	m.CloseScreenshotPreview(false)
	wasTerminal := m.Mode == TerminalMode
	over := m.OverlayOnScreen()
	m.BeginPointerGesture()
	m.Capture = captureState{
		Active: true, Hover: -1, Keyboard: !mouse, wasTerminal: wasTerminal, OverOverlay: over,
	}
	if !mouse && !over {
		m.Capture.Hover = m.FocusedWindow
	}
}

// CaptureOverOverlay reports whether the open capture was started over an
// overlay, where enter takes the whole screen rather than a pane.
func (m *OS) CaptureOverOverlay() bool { return m.Capture.Active && m.Capture.OverOverlay }

// OverlayOnScreen reports whether any overlay, panel or dialog is drawn over
// the panes: every one that owns the keyboard while it is up. Copy mode and
// the scrollback browser are views of a pane and are not counted.
func (m *OS) OverlayOnScreen() bool {
	return m.review.open || m.AnyOverlayOpen() || m.ContextMenuActive() || m.PathPickerOpen() ||
		m.Renaming() || m.ShowTapeReview || m.ShowTapeManager ||
		m.ShowLogs || m.ShowCacheStats
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

func (m *OS) captureVisibleWindows() []int {
	if m.Capture.OverOverlay {
		return nil
	}
	var out []int
	for i, w := range m.Windows {
		if w == nil || w.Minimized || w.Workspace != m.CurrentWorkspace {
			continue
		}
		out = append(out, i)
	}
	return out
}

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
		m.ShowNotification("There is no window to capture.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	win := m.Windows[index]
	palette := capture.WithPaneBackground(m.shotPalette(), m.Settings.PaneBackgroundResolved(), theme.CurrentThemeID())
	grid, pieces := m.windowShot(win, palette, m.screenshotSettings().Cursor)
	if grid == nil {
		m.ShowNotification("That window has no screen to capture.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	return m.renderScreenshot(grid, windowRowTitle(win), false, pieces)
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
	if m.learnOff(learnNoteShot) {
		return nil
	}
	m.settleImagesForCapture()
	grid, pieces := m.composedShot(x0, y0, x1, y1)
	if grid == nil {
		m.ShowNotification("That selection is too small to capture.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	label := "screen"
	if x1-x0 != m.GetRenderWidth() || y1-y0 != m.GetRenderHeight() {
		label = "region"
	}
	return m.renderScreenshot(grid, label, true, pieces)
}

func (m *OS) settleImagesForCapture() {
	kp := m.KittyPassthrough
	if kp == nil {
		return
	}
	if kp.replaceChromeOccluders(m.pipOccluders(m.captureOccluders())) {
		m.GetKittyGraphicsCmd()
	}
}

func (m *OS) windowShot(win *terminal.Window, palette *shot.Palette, cursor bool) (*shot.Grid, []shotPiece) {
	if win == nil {
		return nil, nil
	}
	win.RLockIO()
	defer win.RUnlockIO()
	g := terminalGrid(win.Terminal, palette, cursor)
	if g == nil {
		return nil, nil
	}
	return g, m.windowShotPieces(win.ID, win.IsAltScreen(), win.Terminal, g)
}

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

func (m *OS) composedGrid(x0, y0, x1, y1 int) *shot.Grid {
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, w), min(y1, h)
	if x1-x0 < 1 || y1-y0 < 1 || w <= 0 || h <= 0 {
		return nil
	}
	g := frameToGrid(m.composedFrame().Text(), w, h, m.shotPalette())
	if g == nil {
		return nil
	}
	return cropGrid(g, x0, y0, x1, y1)
}

func (m *OS) composedShot(x0, y0, x1, y1 int) (*shot.Grid, []shotPiece) {
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, w), min(y1, h)
	if x1-x0 < 1 || y1-y0 < 1 || w <= 0 || h <= 0 {
		return nil, nil
	}
	g, hits := frameToGridHits(m.composedFrame().Text(), w, h, m.shotPalette(), true)
	if g == nil {
		return nil, nil
	}
	pieces := m.screenShotPieces(hits)
	return cropGrid(g, x0, y0, x1, y1), cropShotPieces(pieces, x0, y0)
}

func frameToGrid(frame string, w, h int, palette *shot.Palette) *shot.Grid {
	g, _ := frameToGridHits(frame, w, h, palette, false)
	return g
}

func frameToGridHits(frame string, w, h int, palette *shot.Palette, images bool) (*shot.Grid, []shotCellHit) {
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	if palette == nil {
		palette = shot.XTermPalette()
	}
	em := vt.NewEmulator(w, h)
	if images {
		em.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	}
	if _, err := em.Write([]byte(strings.ReplaceAll(frame, "\n", "\r\n"))); err != nil {
		return nil, nil
	}
	g := terminalGrid(em, palette, false)
	if g == nil || !images {
		return g, nil
	}
	return g, scanImageCells(em, g)
}

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

func (m *OS) shotPalette() *shot.Palette {
	p, warn := capture.Palette(theme.CurrentThemeID())
	if warn == "" {
		return p
	}
	return m.liveHostPalette(hostPalette(m.hostCaps(), p))
}

func (m *OS) liveHostPalette(p *shot.Palette) *shot.Palette {
	h := &m.host
	if h.gen == 0 || p == nil {
		return p
	}
	out := *p
	for i, c := range h.ansi {
		if c != nil {
			out.ANSI[i] = shotColor(c)
		}
	}
	if h.fg != nil {
		out.FG = shotColor(h.fg)
	}
	if h.bg != nil {
		out.BG = shotColor(h.bg)
	}
	return &out
}

func shotColor(c color.Color) shot.Color {
	r, g, b, _ := c.RGBA()
	return shot.RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

func hostPalette(caps *HostCapabilities, fallback *shot.Palette) *shot.Palette {
	if caps == nil || fallback == nil {
		return fallback
	}
	if caps.ANSIMask == 0 && !caps.HasFg && !caps.HasBg {
		return fallback
	}
	out := *fallback
	for i, packed := range caps.ANSI {
		if caps.ANSIMask&(1<<uint(i)) != 0 {
			out.ANSI[i] = unpackShotColor(packed)
		}
	}
	if caps.HasFg {
		out.FG = unpackShotColor(caps.Fg)
	}
	if caps.HasBg {
		out.BG = unpackShotColor(caps.Bg)
	}
	return &out
}

func unpackShotColor(packed uint32) shot.Color {
	return shot.RGB(uint8(packed>>16), uint8(packed>>8), uint8(packed))
}

func (m *OS) screenshotSettings() capture.Settings {
	cfg := config.ScreenshotConfig{}
	if m.UserConfig != nil {
		cfg = m.UserConfig.Screenshot
	}
	s := capture.SettingsFrom(cfg, theme.CurrentThemeID(), theme.ActiveGlyphSetID())
	s.PaneBackground = m.Settings.PaneBackgroundResolved()
	if caps := m.hostCaps(); caps != nil {
		s.HostFontFamily, s.HostBoldFamily = caps.FontFamily, caps.BoldFontFamily
		s.HostCellW, s.HostCellH = caps.CellWidth, caps.CellHeight
	}
	return s
}

func (m *OS) renderScreenshot(grid *shot.Grid, label string, plain bool, pieces []shotPiece) tea.Cmd {
	settings := m.screenshotSettings()
	settings.Title = m.Settings.FormatScreenshotTitle(settings.Title, label, m.FocusedWindow+1, m.focusedCWD())
	m.shotCaptures++
	serial := m.shotCaptures
	wantPreview := m.screenshotPreviewWanted()
	if wantPreview {
		m.openPendingPreview(grid, settings.Format, serial)
	}
	maxW, maxH := 0, 0
	if wantPreview && m.screenshotGraphicsReady() {
		maxW, maxH = m.screenshotPreviewPixelBudget()
	}

	return func() tea.Msg {
		msg := screenshotResultMsg{capture: serial, format: settings.Format, grid: grid}
		palette, paletteWarn := capture.Palette(settings.ThemeID)
		if !plain {
			palette = capture.WithPaneBackground(palette, settings.PaneBackground, settings.ThemeID)
		}
		frame, warnings := capture.Frame(settings, palette, grid, plain)
		if paletteWarn != "" {
			warnings = append([]string{paletteWarn}, warnings...)
		}
		if len(frame.Font.Data) == 0 && shot.HasPrivateUse(grid) {
			warnings = append(warnings, capture.FontFallbackNotice)
		}
		msg.frame, msg.warnings = frame, warnings

		rendered := grid
		if images := decodeShotPieces(pieces); len(images) > 0 {
			withImages := *grid
			withImages.Images = images
			rendered = &withImages
		}
		data, raster, err := renderCaptureBytes(settings.Format, rendered, frame, maxW > 0)
		if err != nil {
			msg.err = err
			return msg
		}
		path, err := capture.ResolvePath("", settings.Directory, label, settings.Format, time.Now())
		if err != nil {
			msg.err = err
			return msg
		}
		if err := capture.Save(path, data); err != nil {
			msg.err = err
			return msg
		}
		msg.path, msg.bytes = path, len(data)
		if raster != nil && maxW > 0 {
			small := shot.Downscale(raster, maxW, maxH)
			if pixels, perr := shot.EncodePNG(small); perr == nil {
				b := small.Bounds()
				msg.png, msg.pixelW, msg.pixelH = pixels, b.Dx(), b.Dy()
				msg.transmit = buildScreenshotTransmit(pixels)
			}
		}
		return msg
	}
}

func renderCaptureBytes(format shot.Format, g *shot.Grid, f *shot.Frame, wantPixels bool) ([]byte, *image.RGBA, error) {
	if format == shot.FormatPNG {
		img, err := shot.Raster(g, f)
		if err != nil {
			return nil, nil, err
		}
		data, err := shot.EncodePNG(img)
		if err != nil {
			return nil, nil, err
		}
		return data, img, nil
	}
	data, err := shot.Render(format, g, f)
	if err != nil {
		return nil, nil, err
	}
	if !wantPixels {
		return data, nil, nil
	}
	small := *f
	small.Scale = 1
	img, rerr := shot.Raster(g, &small)
	if rerr != nil {
		return data, nil, nil
	}
	return data, img, nil
}

func (m *OS) openPendingPreview(grid *shot.Grid, format shot.Format, serial int) {
	m.ShotPreview = screenshotPreview{
		Open:    true,
		Pending: true,
		Format:  format,
		Grid:    grid,
		Status:  shotStatusSaving,
		Capture: serial,
	}
	m.raiseOverlay(overlayKindShot)
	m.MarkAllDirty()
}

func copyScreenshotCmd(serial int, path string, mediaType string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path) // #nosec G304 - this process just wrote it
		if err != nil {
			return screenshotCopiedMsg{capture: serial, err: err}
		}
		_, cerr := capture.CopyImage(path, data, mediaType)
		return screenshotCopiedMsg{capture: serial, err: cerr}
	}
}

func (m *OS) screenshotCopyWanted() bool {
	if m.UserConfig == nil {
		return true
	}
	return m.UserConfig.Screenshot.CopyEnabled()
}

func (m *OS) screenshotPreviewWanted() bool {
	if m.UserConfig == nil {
		return true
	}
	return m.UserConfig.Screenshot.PreviewEnabled()
}

func (m *OS) screenshotIsLocal() bool {
	return !m.IsRemoteClient() && capture.ImageRoute().Available
}

// HandleScreenshotResult takes a finished render and files it against the
// capture it belongs to.
//
// Three things can have happened while it was rendering, and the serial tells
// them apart. The panel can still be showing this capture, which is the usual
// case and the one that fills the panel in. The user can have pressed esc,
// which means the file they never saw goes away without a trace. Or they can
// have pressed enter, or taken another capture, in which case the file is kept
// and said once in a notification, and the panel is left alone.
func (m *OS) HandleScreenshotResult(msg screenshotResultMsg) tea.Cmd {
	discarded := m.takeDiscardedCapture(msg.capture)
	showing := m.ShotPreview.Open && m.ShotPreview.Capture == msg.capture

	if msg.err != nil {
		if showing {
			m.CloseScreenshotPreview(false)
		}
		if !discarded {
			m.ShowNotification("The screenshot failed. "+msg.err.Error(), "error", 0)
		}
		return nil
	}
	if discarded {
		_ = os.Remove(msg.path)
		return nil
	}

	for _, w := range msg.warnings {
		m.ShowNotification(w, "warning", m.Settings.NotificationWarningDuration)
	}
	if !showing {
		m.ShowNotification("Saved to "+shortenPath(msg.path), "success", m.Settings.NotificationDuration)
		return m.screenshotCopyCmd(msg)
	}

	p := &m.ShotPreview
	p.Pending = false
	p.Path = msg.path
	p.Format = msg.format
	p.Grid = msg.grid
	p.PNG = msg.png
	p.Transmit = msg.transmit
	p.PixelW, p.PixelH = msg.pixelW, msg.pixelH
	p.Bytes = msg.bytes
	p.Note = m.screenshotPreviewNote(msg)
	p.CopyLabel, p.Status = m.screenshotCopyOffer(msg)
	return m.screenshotCopyCmd(msg)
}

func (m *OS) screenshotCopyCmd(msg screenshotResultMsg) tea.Cmd {
	if msg.path == "" || msg.capture != m.shotCaptures {
		return nil
	}
	if !m.screenshotCopyWanted() || !m.screenshotIsLocal() || msg.format == "" {
		return nil
	}
	return copyScreenshotCmd(msg.capture, msg.path, msg.format.MediaType())
}

// HandleScreenshotCopied files the answer from a clipboard copy. It says so on
// the panel when the panel is still showing that capture, and out of the way
// when it is not.
func (m *OS) HandleScreenshotCopied(msg screenshotCopiedMsg) {
	showing := m.ShotPreview.Open && m.ShotPreview.Capture == msg.capture
	if msg.err != nil {
		m.ShowNotification(shotStatusCopyFailed, "warning", m.Settings.NotificationWarningDuration)
		return
	}
	if showing {
		m.ShotPreview.Status = shotStatusCopied
		return
	}
	m.ShowNotification(shotStatusCopied, "success", m.Settings.NotificationDuration)
}

func (m *OS) screenshotPreviewNote(msg screenshotResultMsg) string {
	if msg.frame == nil || msg.frame.Mode == shot.FrameNone {
		return ""
	}
	scale := max(msg.frame.Scale, 1)
	if msg.format != shot.FormatPNG {
		return "The frame shows in the saved file."
	}
	return fmt.Sprintf("Frame and wash at %dx. The frame shows in the saved file.", scale)
}

func (m *OS) screenshotCopyOffer(msg screenshotResultMsg) (label, status string) {
	textFormat := msg.format == shot.FormatANSI || msg.format == shot.FormatText ||
		msg.format == shot.FormatSVG || msg.format == shot.FormatHTML
	if m.IsRemoteClient() {
		if textFormat {
			return shotCopyAsText, "Files save on the server. Text still copies to you."
		}
		return "", "Files save on the server. This session cannot copy an image."
	}
	if textFormat {
		return shotCopyAsText, ""
	}
	if route := capture.ImageRoute(); !route.Available {
		return "", route.Reason
	}
	return shotCopyImage, ""
}

// CopyScreenshot runs the panel's c key. It only ever does what the label says,
// and it never does it on this goroutine.
func (m *OS) CopyScreenshot() tea.Cmd {
	p := &m.ShotPreview
	if !p.Open || p.Pending || p.CopyLabel == "" || p.Path == "" {
		return nil
	}
	if p.CopyLabel == shotCopyAsText {
		data, err := os.ReadFile(p.Path) // #nosec G304 - this process just wrote it
		if err != nil {
			m.ShowNotification("The file could not be read to copy it.", "error", 0)
			return nil
		}
		return m.CopyToClipboard(string(data))
	}
	return copyScreenshotCmd(p.Capture, p.Path, p.Format.MediaType())
}

// CloseScreenshotPreview closes the panel. discard gets rid of the file it
// wrote, so an accidental capture leaves nothing behind.
//
// A panel closed with esc before its file has landed cannot remove that file,
// because it does not exist yet. Its serial is written down instead, and the
// result that arrives later removes its own file and says nothing. Without that
// the one gesture that means "I did not want this" was the one gesture that
// reliably left a file on disk.
func (m *OS) CloseScreenshotPreview(discard bool) {
	if !m.ShotPreview.Open {
		m.ShotPreview = screenshotPreview{}
		return
	}
	path, pending, serial := m.ShotPreview.Path, m.ShotPreview.Pending, m.ShotPreview.Capture
	m.ShotPreview = screenshotPreview{}
	m.clearScreenshotGraphics()
	if !discard {
		return
	}
	switch {
	case path != "":
		if err := os.Remove(path); err == nil {
			m.ShowNotification(shotStatusDeleted, "info", m.Settings.NotificationDuration)
		}
	case pending:
		m.discardCapture(serial)
		m.ShowNotification(shotStatusDeleted, "info", m.Settings.NotificationDuration)
	}
}

const shotDiscardLimit = 32

func (m *OS) discardCapture(serial int) {
	if serial <= 0 {
		return
	}
	if len(m.shotDiscarded) >= shotDiscardLimit {
		m.shotDiscarded = m.shotDiscarded[1:]
	}
	m.shotDiscarded = append(m.shotDiscarded, serial)
}

func (m *OS) takeDiscardedCapture(serial int) bool {
	for i, s := range m.shotDiscarded {
		if s != serial {
			continue
		}
		m.shotDiscarded = append(m.shotDiscarded[:i], m.shotDiscarded[i+1:]...)
		return true
	}
	return false
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
		m.Capture.Dragging = false
	}
	m.Capture.Hover = m.CaptureWindowAt(x, y)
	m.Capture.Keyboard = false
}

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

type screenshotPlacementState struct {
	x, y, cols, rows int
	capture          int
	hostW, hostH     int
}

func (m *OS) focusedCWD() string {
	if w := m.GetFocusedWindow(); w != nil {
		return w.CWD()
	}
	return ""
}
