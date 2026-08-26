package app

import (
	"fmt"
	"image"
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

// The panel's runtime strings. They are drawn into fixed slots in a panel that
// is at most a few dozen columns wide, so each one is a short sentence that
// says one thing.
const (
	shotStatusSaving     = "Saving the image…"
	shotStatusCopied     = "Copied."
	shotStatusCopyFailed = "The copy failed. The file is saved."
	shotStatusDeleted    = "The screenshot was deleted."
	shotCopyAsText       = "copy as text"
	shotCopyImage        = "copy image"
)

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
	// capture is the serial of the capture this result belongs to. The panel is
	// already open by the time this arrives, so the serial is what says whether
	// it is still open on this capture: a result for an older one updates
	// nothing, and a result for one the user already dismissed removes its own
	// file.
	capture  int
	path     string
	format   shot.Format
	grid     *shot.Grid
	frame    *shot.Frame
	warnings []string
	// png is the picture the preview's pixel tier places, already shrunk to the
	// panel's box. Empty on a host with no kitty graphics, which is every host
	// the text tier already serves.
	png []byte
	// transmit is png wrapped in the chunked upload escapes, built here rather
	// than in the render goroutine's flush: base64 of a megabyte inside the
	// frame's synchronized-update bracket is a frame nobody sees drawn.
	transmit []byte
	// pixelW and pixelH are png's own size, so the pixel tier can keep the
	// capture's shape instead of filling whatever cell box the panel has.
	pixelW, pixelH int
	// bytes is the size of the file that was written, so the panel does not
	// have to read it back off the Update goroutine.
	bytes int
	err   error
}

// screenshotCopiedMsg is the answer from a clipboard copy. The copy is its own
// command and gates nothing: the file is written and the preview is up by the
// time this is sent.
type screenshotCopiedMsg struct {
	capture int
	err     error
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
	// Pending says the artifacts have not landed yet. The cells are exact from
	// the first frame; what is missing is the file, its size and the picture.
	Pending bool
	// Note is the one quiet line about what the panel cannot show.
	Note string
	// Status carries a reason a control is missing, in plain words.
	Status string
	// CopyLabel is what c will actually do here, empty when nothing will.
	CopyLabel string
	// Bytes is the file size, for the header line.
	Bytes int
	// PNG is the picture the pixel tier places, empty on a host that cannot
	// show one. It is shrunk to the panel's own box: the preview is looked at
	// through a few hundred pixels, and uploading the file's own two or three
	// megabytes to see them scaled down cost the host seventy milliseconds of
	// decoding inside the frame's synchronized-update bracket.
	PNG []byte
	// Transmit is PNG already wrapped in the chunked upload escapes, built off
	// the Update goroutine so the render flush only concatenates.
	Transmit []byte
	// PixelW and PixelH are the picture's own size. The pixel tier draws into a
	// cell box, and a box chosen without them stretches the capture to fill it.
	PixelW, PixelH int
	// Capture is the serial number of the capture this panel is showing. It is
	// what tells the host's resident picture apart from this one; see
	// screenshotPlacementState.
	Capture int
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

// screenshotSettings resolves the [screenshot] section this client holds, and
// hands the renderer the font the host says it draws with.
//
// That last part is why a default capture on kitty comes out with the user's
// own icons in it. Nobody should have to find a .ttf path and put it in a
// config file to get a picture of their own terminal that looks like their own
// terminal; the terminal already knows, and it answers when asked.
func (m *OS) screenshotSettings() capture.Settings {
	cfg := config.ScreenshotConfig{}
	if m.UserConfig != nil {
		cfg = m.UserConfig.Screenshot
	}
	s := capture.SettingsFrom(cfg, theme.CurrentThemeID(), theme.ActiveGlyphSetID())
	if caps := GetHostCapabilities(); caps != nil {
		s.HostFontFamily, s.HostBoldFamily = caps.FontFamily, caps.BoldFontFamily
	}
	return s
}

// renderScreenshot opens the preview on this frame and renders on the next.
//
// The cells are already in hand the instant a capture happens: the grid was
// built synchronously by the caller, and it is the whole of what the panel's
// text tier draws. Only the file, the clipboard and the pixel picture are slow.
// So the panel opens here, seeded and marked pending, and the artifacts catch
// up under their own serial. What this used to do instead was render, write,
// copy and only then send one message, and the copy blocked; the panel arrived
// seconds later or not at all, which is the whole of "it does not show the
// preview after".
//
// Everything that touches a disk or a font file is inside the command. The
// settings resolution stays here because it only reads memory this goroutine
// owns, but capture.Frame reads a font file, so it moved.
func (m *OS) renderScreenshot(grid *shot.Grid, label string, plain bool) tea.Cmd {
	settings := m.screenshotSettings()
	if settings.Title != "" {
		settings.Title = config.FormatWindowTitle(label, m.FocusedWindow+1, "")
	}
	// The serial is bumped before the panel is filled in, so the panel carries
	// the number that says which capture it is showing.
	m.shotCaptures++
	serial := m.shotCaptures
	wantPreview := m.screenshotPreviewWanted()
	if wantPreview {
		m.openPendingPreview(grid, settings.Format, serial)
	}
	// The pixel tier needs a PNG whatever the saved format is, and only a host
	// that can show one pays for it. The budget is read after the panel is up,
	// because it is measured from the panel: the footer a pending panel draws
	// is what sets how many body rows there are to fill.
	maxW, maxH := 0, 0
	if wantPreview && m.screenshotGraphicsReady() {
		maxW, maxH = m.screenshotPreviewPixelBudget()
	}

	return func() tea.Msg {
		msg := screenshotResultMsg{capture: serial, format: settings.Format, grid: grid}
		palette, paletteWarn := capture.Palette(settings.ThemeID)
		frame, warnings := capture.Frame(settings, palette, plain)
		if paletteWarn != "" {
			// The no-theme notice rides with the rest of the warnings rather
			// than being raised on its own, so it arrives with the capture it
			// describes instead of ahead of it.
			warnings = append([]string{paletteWarn}, warnings...)
		}
		if len(frame.FontData) == 0 && shot.HasPrivateUse(grid) {
			warnings = append(warnings, capture.FontFallbackNotice)
		}
		msg.frame, msg.warnings = frame, warnings

		data, raster, err := renderCaptureBytes(settings.Format, grid, frame, maxW > 0)
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

// renderCaptureBytes produces the file's bytes, and the pixels the preview will
// be shrunk from when one is wanted.
//
// A PNG capture rasterizes once and the same pixels serve both. Any other
// format used to raster a second time at the file's own scale purely to feed
// the preview, which on a full-screen capture was a second whole second spent
// on a picture that was then thrown into a box a few hundred pixels wide. The
// preview raster is scale 1 now, always.
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
	data, err := shot.Render(format, g, f, nil)
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
		// The file is written either way. A preview that cannot be rastered is
		// a panel with no picture in it, which is the panel every terminal
		// without kitty graphics already gets.
		return data, nil, nil
	}
	return data, img, nil
}

// openPendingPreview puts the panel up on this frame with the captured cells in
// it and nothing claimed that is not yet true.
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
	// The whole point of opening here is that the panel is on screen on the
	// next frame. A capture that ended a gesture mode would probably have
	// redrawn anyway; a capture from the command palette or a keybinding would
	// not, and "probably" is what the reports were made of.
	m.MarkAllDirty()
}

// copyScreenshotCmd puts a file on the clipboard off the Update goroutine.
//
// This is a command and never an inline call. wl-copy and xclip fork a child
// that goes on serving the selection, so the call can take as long as the next
// thing to touch the clipboard takes to arrive; running it inline froze the
// whole TUI, and running it inside the render command held the preview hostage
// to it. Nothing waits on the answer now.
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
		// Silently. The panel already said the capture was deleted, at the
		// moment esc was pressed; saying it again half a second later would be
		// the app talking about its own bookkeeping.
		_ = os.Remove(msg.path)
		return nil
	}

	for _, w := range msg.warnings {
		m.ShowNotification(w, "warning", config.NotificationWarningDuration)
	}
	if !showing {
		// The panel moved on or was closed with enter. The file was still
		// asked for, so it stays, and this is the one line that says so.
		m.ShowNotification("Saved to "+shortenPath(msg.path), "success", config.NotificationDuration)
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
	// No success notification while the panel is up. The panel is the
	// notification, and two announcements of one event is one too many.
	return m.screenshotCopyCmd(msg)
}

// screenshotCopyCmd starts the automatic copy, when the config asks for one and
// this client is the machine whose clipboard it would be.
//
// Only the newest capture copies. An older render finishing late would
// otherwise put the wrong picture on the clipboard after the user had already
// taken another one, which is the same out-of-order symptom in a different
// costume.
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
		m.ShowNotification(shotStatusCopyFailed, "warning", config.NotificationWarningDuration)
		return
	}
	if showing {
		m.ShotPreview.Status = shotStatusCopied
		return
	}
	m.ShowNotification(shotStatusCopied, "success", config.NotificationDuration)
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
			m.ShowNotification(shotStatusDeleted, "info", config.NotificationDuration)
		}
	case pending:
		// The file does not exist yet, so the answer is given now and the
		// removal happens when there is something to remove.
		m.discardCapture(serial)
		m.ShowNotification(shotStatusDeleted, "info", config.NotificationDuration)
	}
}

// shotDiscardLimit bounds the discarded-serial list. Every capture returns
// exactly one result, so the list drains as fast as it fills; the cap is there
// so a command that somehow never answers cannot grow it without end.
const shotDiscardLimit = 32

// discardCapture writes down that a capture still being rendered is not wanted.
func (m *OS) discardCapture(serial int) {
	if serial <= 0 {
		return
	}
	if len(m.shotDiscarded) >= shotDiscardLimit {
		m.shotDiscarded = m.shotDiscarded[1:]
	}
	m.shotDiscarded = append(m.shotDiscarded, serial)
}

// takeDiscardedCapture reports whether a capture was dismissed while it was
// still rendering, and forgets it either way.
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
//
// capture is which capture the drawn picture belongs to, and it is a serial
// number rather than the file name. The name is stamped to the second, so two
// captures inside one second share it; keying the upload on the name meant the
// second capture was drawn from the first capture's pixels, which is the "it
// shows the previous screenshot" report. A content hash would answer the same
// question, but it answers a different one as well -- two captures of an
// unchanged screen are one picture to a hash and two captures to the user --
// and it costs a hash of a whole PNG on the Update goroutine. The serial is
// exact, free, and cannot collide.
type screenshotPlacementState struct {
	x, y, cols, rows int
	capture          int
}
