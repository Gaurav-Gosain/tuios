package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// shotOS builds a client holding two live windows and a screenshot config that
// writes into the test's own directory.
func shotOS(t testing.TB) *OS {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Screenshot.Directory = t.TempDir()
	os := &OS{
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
		Width:          120,
		Height:         40,
		UserConfig:     cfg,
	}
	os.Windows = append(os.Windows,
		newTestWindow(t, "shot-a", 40, 10),
		newTestWindow(t, "shot-b", 40, 10),
	)
	os.Windows[0].X, os.Windows[0].Y = 0, 1
	os.Windows[1].X, os.Windows[1].Y = 50, 1
	for _, w := range os.Windows {
		w.Workspace = 1
	}
	os.CurrentWorkspace = 1
	return os
}

// TestCaptureModeCostsNoTickWork is the constraint this whole feature is held
// to: neither capture mode nor an open preview may put the idle loop back to
// work, because nothing about either of them changes on its own.
//
// Negative control: adding `if m.Capture.Active { return true }` to
// tickNeedsWork made the capture-mode case report true and this fail.
func TestCaptureModeCostsNoTickWork(t *testing.T) {
	m := shotOS(t)
	if m.tickNeedsWork() {
		t.Fatal("the fixture is not idle to begin with")
	}
	m.BeginCapture(true)
	if m.tickNeedsWork() {
		t.Error("capture mode put the idle loop back to work")
	}
	m.Capture.Dragging = true
	m.Capture.AnchorX, m.Capture.CursorX = 2, 20
	if m.tickNeedsWork() {
		t.Error("a region drag put the idle loop back to work")
	}
	m.EndCapture()
	m.ShotPreview = screenshotPreview{Open: true, Grid: shot.NewGrid(10, 4, shot.XTermFg, shot.XTermBg)}
	if m.tickNeedsWork() {
		t.Error("the open preview panel put the idle loop back to work")
	}
}

// TestCaptureHitRectsPointAtTheWindowsDrawn is the hit-rectangle invariant: the
// rectangle a click resolves against is the one the renderer recorded, not one
// a handler worked out for itself.
//
// Negative control: making CaptureWindowAt call WindowAt instead of reading
// m.captureHits still passed for these coordinates but failed the stale case
// below, where the recorded rectangle and the live window disagree.
func TestCaptureHitRectsPointAtTheWindowsDrawn(t *testing.T) {
	m := shotOS(t)
	m.BeginCapture(true)
	m.renderCaptureMode()

	if len(m.captureHits) != 2 {
		t.Fatalf("the renderer recorded %d rectangles for 2 windows", len(m.captureHits))
	}
	for _, h := range m.captureHits {
		w := m.Windows[h.Index]
		if h.X0 != w.X || h.Y0 != w.Y || h.X1 != w.X+w.Width || h.Y1 != w.Y+w.Height {
			t.Errorf("window %d recorded %v, drawn at %d,%d %dx%d",
				h.Index, h, w.X, w.Y, w.Width, w.Height)
		}
	}
	if got := m.CaptureWindowAt(m.Windows[1].X+1, m.Windows[1].Y+1); got != 1 {
		t.Errorf("a click inside window 1 resolved to %d", got)
	}
	if got := m.CaptureWindowAt(45, 30); got != -1 {
		t.Errorf("a click on empty desktop resolved to window %d", got)
	}

	// The stale case: a window that moved since the frame was drawn must still
	// hit-test where it was drawn, because that is where the user saw it.
	drawnX := m.Windows[1].X
	m.Windows[1].X = 5
	if got := m.CaptureWindowAt(drawnX+1, m.Windows[1].Y+1); got != 1 {
		t.Errorf("a click where window 1 was drawn resolved to %d after it moved", got)
	}
}

// TestCaptureModeReleasesTheGesture keeps a lost mouse release from stranding
// the mode with a marquee that follows a bare hover for ever.
//
// Negative control: removing the Capture.Dragging line from EndPointerGrabs
// left the drag active and failed the first case.
func TestCaptureModeReleasesTheGesture(t *testing.T) {
	m := shotOS(t)
	m.BeginCapture(true)
	m.BeginCaptureDrag(2, 2)
	if !m.CaptureDragActive() {
		t.Fatal("the drag did not start")
	}
	m.EndPointerGrabs()
	if m.CaptureDragActive() {
		t.Error("a lost release left the drag running")
	}
	// Leaving the mode has to leave nothing behind at all.
	m.EndCapture()
	if m.CaptureActive() || m.Capture != (captureState{}) {
		t.Errorf("capture mode left state behind: %+v", m.Capture)
	}
}

// TestCaptureEntryRestoresTheMode checks a capture taken from terminal mode
// puts the user back in terminal mode, so the gesture does not silently change
// where their typing goes.
//
// Negative control: dropping the EndPointerGesture call from EndCapture left
// the mode in window management and failed.
func TestCaptureEntryRestoresTheMode(t *testing.T) {
	m := shotOS(t)
	m.Mode = TerminalMode
	m.BeginCapture(false)
	if m.Mode != WindowManagementMode {
		t.Error("capture mode did not take the keyboard off the pane")
	}
	m.EndCapture()
	if m.Mode != TerminalMode {
		t.Errorf("mode came back as %v, want terminal mode", m.Mode)
	}
}

// TestCaptureKeyboardEntryReachesEveryWindow is the mouse-less path: tab must
// walk every visible window, because a capture that needs a pointer is a
// capture half the users cannot take.
//
// Negative control: making CaptureHoverNext a no-op left the hover on the
// focused window and failed.
func TestCaptureKeyboardEntryReachesEveryWindow(t *testing.T) {
	m := shotOS(t)
	m.BeginCapture(false)
	if !m.Capture.Keyboard {
		t.Error("a keyboard entry did not say so, so the hints offer a drag")
	}
	seen := map[int]bool{m.Capture.Hover: true}
	for range len(m.Windows) {
		m.CaptureHoverNext(1)
		seen[m.Capture.Hover] = true
	}
	if len(seen) != len(m.Windows) {
		t.Errorf("tab reached %d of %d windows", len(seen), len(m.Windows))
	}
}

// TestCaptureClickSlopTakesTheWindow checks a press and release on one spot is
// a click on a window rather than a one-cell region, because a hand moves a
// cell or two and nobody ever means a 1x1 screenshot.
//
// Negative control: setting captureClickSlop to 0 turned the two-cell drag into
// a region and failed.
func TestCaptureClickSlopTakesTheWindow(t *testing.T) {
	for name, drag := range map[string]struct{ dx, dy int }{
		"no movement": {0, 0},
		"a cell":      {1, 1},
	} {
		t.Run(name, func(t *testing.T) {
			m := shotOS(t)
			m.BeginCapture(true)
			m.renderCaptureMode()
			x, y := m.Windows[1].X+2, m.Windows[1].Y+2
			m.BeginCaptureDrag(x, y)
			m.UpdateCapturePointer(x+drag.dx, y+drag.dy, true)
			cmd := m.FinishCaptureDrag()
			if cmd == nil {
				t.Fatal("the gesture produced no capture")
			}
			msg, ok := cmd().(screenshotResultMsg)
			if !ok || msg.err != nil {
				t.Fatalf("capture failed: %+v", msg)
			}
			// A window capture is that pane's own cells, which is the
			// emulator inside the border and not the whole screen.
			win := m.Windows[1]
			win.RLockIO()
			cols, rows := win.Terminal.Width(), win.Terminal.Height()
			win.RUnlockIO()
			if msg.grid.Cols != cols || msg.grid.Rows != rows {
				t.Errorf("captured a %dx%d grid, want the pane's %dx%d",
					msg.grid.Cols, msg.grid.Rows, cols, rows)
			}
		})
	}
}

// TestCaptureRegionCutsTheComposedFrame checks a region really is a rectangle
// of the composed screen, chrome included, and not a pane read directly.
//
// Negative control: making composedGrid read the focused window's emulator
// instead of the composed frame returned a 40x10 grid and failed the size.
func TestCaptureRegionCutsTheComposedFrame(t *testing.T) {
	m := shotOS(t)
	m.BeginCapture(true)
	m.renderCaptureMode()
	m.BeginCaptureDrag(4, 3)
	m.UpdateCapturePointer(43, 12, true)
	cmd := m.FinishCaptureDrag()
	if cmd == nil {
		t.Fatal("the drag produced no capture")
	}
	msg, ok := cmd().(screenshotResultMsg)
	if !ok || msg.err != nil {
		t.Fatalf("capture failed: %+v", msg)
	}
	if msg.grid.Cols != 40 || msg.grid.Rows != 10 {
		t.Errorf("region is %dx%d cells, want the 40x10 rectangle dragged",
			msg.grid.Cols, msg.grid.Rows)
	}
	if _, err := os.Stat(msg.path); err != nil {
		t.Errorf("the region capture wrote no file: %v", err)
	}
}

// TestScreenshotScreenCoversTheViewport checks the full-screen grab is the
// whole composed frame and nothing less.
//
// Negative control: making ScreenshotScreen call ScreenshotWindow returned the
// 40x10 pane and failed.
func TestScreenshotScreenCoversTheViewport(t *testing.T) {
	m := shotOS(t)
	cmd := m.ScreenshotScreen()
	if cmd == nil {
		t.Fatal("the full-screen grab produced no capture")
	}
	msg := cmd().(screenshotResultMsg)
	if msg.err != nil {
		t.Fatalf("capture failed: %v", msg.err)
	}
	if msg.grid.Cols != m.GetRenderWidth() || msg.grid.Rows != m.GetRenderHeight() {
		t.Errorf("screen capture is %dx%d, want %dx%d",
			msg.grid.Cols, msg.grid.Rows, m.GetRenderWidth(), m.GetRenderHeight())
	}
}

// TestPreviewFooterOffersOnlyWorkingKeys is the "nothing inert" rule: a footer
// key is drawn when it works here and left out with a reason when it does not.
//
// Negative control: making shotPreviewHints always append the c and o hints
// made the remote case offer both and failed.
func TestPreviewFooterOffersOnlyWorkingKeys(t *testing.T) {
	m := shotOS(t)
	m.ShotPreview = screenshotPreview{Open: true, Format: shot.FormatPNG}

	// A remote client cannot open a viewer on the user's machine, so o is not
	// drawn at all.
	m.RemoteClient = true
	m.ShotPreview.CopyLabel, m.ShotPreview.Status = m.screenshotCopyOffer(
		screenshotResultMsg{format: shot.FormatPNG, path: "/srv/shot.png"})
	if m.ShotPreview.CopyLabel != "" {
		t.Errorf("a remote PNG offered %q, but no image copy exists there", m.ShotPreview.CopyLabel)
	}
	if !strings.Contains(m.ShotPreview.Status, "server") {
		t.Errorf("the reason line does not say where the file is: %q", m.ShotPreview.Status)
	}
	for _, h := range m.shotPreviewHints() {
		if h.Key == "o" || h.Key == "c" {
			t.Errorf("a remote client drew an inert %q key", h.Key)
		}
	}

	// A text format copies honestly over ssh: OSC 52 reaches the user's own
	// terminal wherever it is.
	m.ShotPreview.CopyLabel, _ = m.screenshotCopyOffer(
		screenshotResultMsg{format: shot.FormatANSI, path: "/srv/shot.ans"})
	if m.ShotPreview.CopyLabel != "copy as text" {
		t.Errorf("a remote ansi capture offered %q, want a text copy", m.ShotPreview.CopyLabel)
	}

	m.RemoteClient = false
	m.ShotPreview.CopyLabel, _ = m.screenshotCopyOffer(
		screenshotResultMsg{format: shot.FormatANSI, path: "/tmp/shot.ans"})
	keys := map[string]bool{}
	for _, h := range m.shotPreviewHints() {
		keys[h.Key] = true
	}
	for _, want := range []string{"enter", "c", "o", "r", "esc"} {
		if !keys[want] {
			t.Errorf("a local client did not offer %q", want)
		}
	}
}

// TestPreviewBodyDrawsTheCapturedCells checks the text tier really redraws the
// capture, in colour, on a host with no graphics at all.
//
// Negative control: making shotPreviewCells return nil emptied the panel and
// failed both assertions.
func TestPreviewBodyDrawsTheCapturedCells(t *testing.T) {
	m := shotOS(t)
	g := shot.NewGrid(20, 3, shot.RGB(0xff, 0xff, 0xff), shot.RGB(0, 0, 0))
	for i, r := range "hello" {
		g.Cells[1][i].Cluster = string(r)
		g.Cells[1][i].FG = shot.RGB(0, 0xff, 0)
	}
	m.ShotPreview = screenshotPreview{Open: true, Grid: g, Format: shot.FormatPNG}

	content, _, _ := m.renderScreenshotPreview()
	if !strings.Contains(content, "hello") {
		t.Error("the preview body does not carry the captured text")
	}
	if !strings.Contains(content, "\x1b[") {
		t.Error("the preview body carries no styling, so the capture is not in colour")
	}
	// The panel must fit the screen it is drawn on.
	if w := lipgloss.Width(content); w > m.Width {
		t.Errorf("the panel is %d cells wide on a %d cell screen", w, m.Width)
	}
}

// TestPreviewScrollStaysInsideTheCapture keeps the viewport from running past
// the grid in either direction.
//
// Negative control: removing the clamp from ScrollScreenshotPreview let the
// offset reach 40 on a 30-row grid and failed.
func TestPreviewScrollStaysInsideTheCapture(t *testing.T) {
	m := shotOS(t)
	g := shot.NewGrid(200, 30, shot.XTermFg, shot.XTermBg)
	m.ShotPreview = screenshotPreview{Open: true, Grid: g}
	cols, rows := m.screenshotPreviewBody()

	for range 40 {
		m.ScrollScreenshotPreview(20, 5)
	}
	if want := max(0, g.Rows-rows); m.ShotPreview.Scroll != want {
		t.Errorf("vertical scroll stopped at %d, want %d", m.ShotPreview.Scroll, want)
	}
	if want := max(0, g.Cols-cols); m.ShotPreview.ScrollX != want {
		t.Errorf("horizontal scroll stopped at %d, want %d", m.ShotPreview.ScrollX, want)
	}
	for range 40 {
		m.ScrollScreenshotPreview(-20, -5)
	}
	if m.ShotPreview.Scroll != 0 || m.ShotPreview.ScrollX != 0 {
		t.Errorf("scrolling back stopped at %d,%d, want 0,0",
			m.ShotPreview.ScrollX, m.ShotPreview.Scroll)
	}
}

// TestEscapeDiscardsTheFile checks the panel's esc really removes what it
// wrote, so an accidental capture leaves nothing behind, and that enter keeps
// it.
//
// Negative control: making CloseScreenshotPreview ignore its discard argument
// left the file on disk and failed the first case.
func TestEscapeDiscardsTheFile(t *testing.T) {
	for name, discard := range map[string]bool{"esc discards": true, "enter keeps": false} {
		t.Run(name, func(t *testing.T) {
			m := shotOS(t)
			path := filepath.Join(t.TempDir(), "shot.png")
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			m.ShotPreview = screenshotPreview{Open: true, Path: path}
			m.CloseScreenshotPreview(discard)
			_, err := os.Stat(path)
			if discard && err == nil {
				t.Error("esc left the file on disk")
			}
			if !discard && err != nil {
				t.Errorf("enter removed the file: %v", err)
			}
			if m.ShotPreview.Open {
				t.Error("the panel is still open")
			}
		})
	}
}

// TestCaptureWritesEveryFormat drives the whole client path per format and
// checks a real file lands with real bytes in it.
//
// Negative control: making capture.Save a no-op left every file missing.
func TestCaptureWritesEveryFormat(t *testing.T) {
	for _, format := range shot.Formats {
		t.Run(format, func(t *testing.T) {
			m := shotOS(t)
			m.UserConfig.Screenshot.Format = format
			cmd := m.ScreenshotWindow(0)
			if cmd == nil {
				t.Fatal("no capture was started")
			}
			msg := cmd().(screenshotResultMsg)
			if msg.err != nil {
				t.Fatalf("capture failed: %v", msg.err)
			}
			info, err := os.Stat(msg.path)
			if err != nil {
				t.Fatalf("no file at %s: %v", msg.path, err)
			}
			if info.Size() == 0 {
				t.Error("the file is empty")
			}
			if want := "." + shot.Format(format).Ext(); filepath.Ext(msg.path) != want {
				t.Errorf("wrote %s, want a %s file", msg.path, want)
			}
		})
	}
}

// TestPreviewOffCapturesWithoutAPanel checks screenshot.preview = false really
// suppresses the panel, so the option is not inert.
//
// Negative control: making HandleScreenshotResult always set Open kept the
// panel up and failed.
func TestPreviewOffCapturesWithoutAPanel(t *testing.T) {
	m := shotOS(t)
	off := false
	m.UserConfig.Screenshot.Preview = &off
	cmd := m.ScreenshotWindow(0)
	msg := cmd().(screenshotResultMsg)
	m.HandleScreenshotResult(msg)
	if m.ShotPreview.Open {
		t.Error("screenshot.preview = false still opened the panel")
	}
	if _, err := os.Stat(msg.path); err != nil {
		t.Errorf("the file was not written: %v", err)
	}
}

// TestScreenshotResultReachesUpdate checks the message the render command
// returns is actually handled, rather than falling through the switch and
// leaving the capture silent.
//
// Negative control: deleting the screenshotResultMsg arm of Update left
// ShotPreview closed and failed.
func TestScreenshotResultReachesUpdate(t *testing.T) {
	m := shotOS(t)
	cmd := m.ScreenshotWindow(0)
	msg := cmd()
	model, _ := m.Update(msg)
	out, ok := model.(*OS)
	if !ok {
		t.Fatalf("Update returned a %T", model)
	}
	if !out.ShotPreview.Open {
		t.Error("the finished capture did not open the preview")
	}
	if out.ShotPreview.Grid == nil {
		t.Error("the preview has no grid to draw")
	}
}

// TestCaptureBumpsTheCaptureSerial checks every capture gets a number of its
// own. The number is what tells the picture the host holds apart from the one
// this panel wants drawn, and the file name cannot: two captures inside one
// second share it.
//
// Negative control: removing the shotCaptures increment from
// HandleScreenshotResult leaves both captures on 0 and this fails.
func TestCaptureBumpsTheCaptureSerial(t *testing.T) {
	m := shotOS(t)

	first := m.ScreenshotWindow(0)()
	m.Update(first)
	one := m.ShotPreview.Capture
	if one == 0 {
		t.Fatal("the first capture has no serial number")
	}

	second := m.ScreenshotWindow(0)()
	m.Update(second)
	two := m.ShotPreview.Capture
	if two == one {
		t.Errorf("two captures share serial %d, so the second draws the first one's pixels", two)
	}
}

// TestClosingThePreviewForgetsTheUpload checks the picture bookkeeping is reset
// whether or not a placement was ever made.
//
// Guarding the whole reset on shotImagePlaced was the trap: a preview that
// uploaded a picture and never placed it left shotImageSent standing, and the
// next capture read that as "the host already holds my picture".
//
// Negative control: putting the `if !m.shotImagePlaced { return }` guard back at
// the top of clearScreenshotGraphics leaves shotImageSent set and this fails.
func TestClosingThePreviewForgetsTheUpload(t *testing.T) {
	m := shotOS(t)
	m.Update(m.ScreenshotWindow(0)())
	if !m.ShotPreview.Open {
		t.Fatal("the capture did not open the preview")
	}

	// Uploaded but never placed, which is every frame before the panel's hit
	// geometry has been recorded.
	m.shotImageSent = true
	m.shotImagePlaced = false

	m.CloseScreenshotPreview(false)
	if m.shotImageSent {
		t.Error("closing the panel left the upload remembered, so the next capture will not send its own")
	}
	if m.shotPlacement != (screenshotPlacementState{}) {
		t.Errorf("closing the panel left the placement at %+v", m.shotPlacement)
	}
}

// TestCaptureModeIsOnTheMotionPathAndOverlayStack pins the two wiring points a
// gesture mode silently dies without: it must be in overlayKindOrder so its
// panel gets a stack slot, and the preview must disqualify the fullscreen fast
// path so it is not drawn over.
//
// This control passes both ways on the current tree by design: it is a wiring
// assertion, and its value is that removing either line makes it fail.
func TestCaptureModeIsOnTheMotionPathAndOverlayStack(t *testing.T) {
	found := false
	for _, kind := range overlayKindOrder {
		if kind == overlayKindShot {
			found = true
		}
	}
	if !found {
		t.Error("the preview panel has no slot in overlayKindOrder, so clicks in it fall through")
	}
	m := shotOS(t)
	m.ShotPreview.Open = true
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Error("the fullscreen fast path would draw over the preview panel")
	}
	m.ShotPreview.Open = false
	m.BeginCapture(true)
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Error("the fullscreen fast path would draw over capture mode")
	}
}

// TestRemoteClientNeverRunsAClipboardHelper is the PR #133 trap: a client
// process beside the daemon must never write the machine's own clipboard,
// because that machine is not the user's.
//
// Negative control: making screenshotIsLocal return true unconditionally made
// the remote case report local and failed.
func TestRemoteClientNeverRunsAClipboardHelper(t *testing.T) {
	m := shotOS(t)
	m.RemoteClient = true
	if m.screenshotIsLocal() {
		t.Error("a remote client would run a clipboard helper on the server")
	}
	// CopyScreenshot on a remote client with no offer must do nothing at all.
	m.ShotPreview = screenshotPreview{Open: true, Format: shot.FormatPNG, CopyLabel: ""}
	if cmd := m.CopyScreenshot(); cmd != nil {
		t.Error("the copy key did something on a client that has no copy route")
	}
}

// TestFrameToGridDoesNotCascadeRows is the regression guard for a bug the
// visual pass caught and no unit test on the renderer could have: composeFrame
// separates rows with a bare newline, an emulator in its default mode reads
// that as "down one row, same column", and the whole full-screen capture came
// out as a diagonal smear. Rows shorter than the viewport are what expose it,
// because a full-width row wraps to column zero on its own.
//
// Negative control: replacing the ReplaceAll in frameToGrid with a plain
// composeFrame put "beta" at column 5 and "gamma" at column 9, and this failed
// on both.
func TestFrameToGridDoesNotCascadeRows(t *testing.T) {
	frame := "alpha\nbeta\ngamma"
	g := frameToGrid(frame, 40, 5, shot.XTermPalette())
	if g == nil {
		t.Fatal("the frame did not parse")
	}
	for y, want := range []string{"alpha", "beta", "gamma"} {
		got := ""
		for x := range len(want) {
			got += g.Cells[y][x].Cluster
		}
		if got != want {
			t.Errorf("row %d starts %q, want %q at column 0: the reparse is cascading", y, got, want)
		}
	}
}

// TestCropGridCutsTheRectangleAsked keeps a region from being off by a cell.
//
// Negative control: making cropGrid copy from g.Cells[y] instead of
// g.Cells[y][x0:x1] returned the left edge of the frame for every region and
// failed.
func TestCropGridCutsTheRectangleAsked(t *testing.T) {
	g := frameToGrid("....xy....\n....zw....", 20, 3, shot.XTermPalette())
	out := cropGrid(g, 4, 0, 6, 2)
	if out == nil {
		t.Fatal("the crop came back empty")
	}
	if out.Cols != 2 || out.Rows != 2 {
		t.Fatalf("crop is %dx%d, want 2x2", out.Cols, out.Rows)
	}
	got := out.Cells[0][0].Cluster + out.Cells[0][1].Cluster +
		out.Cells[1][0].Cluster + out.Cells[1][1].Cluster
	if got != "xyzw" {
		t.Errorf("crop holds %q, want %q", got, "xyzw")
	}
	// A crop outside the grid is nothing rather than a panic.
	if cropGrid(g, 100, 100, 110, 110) != nil {
		t.Error("a crop outside the grid returned something")
	}
}

// TestScreenshotSettingsRowsRender is the "a registered option appears in the
// UI for free" claim, checked on the drawn panel rather than on the registry.
//
// Negative control: removing the screenshot category from settingsCategories
// left the tab absent and this failed.
func TestScreenshotSettingsRowsRender(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	m.Width, m.Height = 140, 44
	cats := m.settingsCategories()
	idx := -1
	for i, c := range cats {
		if c.Name == "Screenshot" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("there is no Screenshot category in the settings panel")
	}
	m.ShowSettings = true
	m.SettingsCategory = idx
	content, _, _ := m.renderSettings()
	for _, want := range []string{"Screenshot", "format", "adow"} {
		if !strings.Contains(strings.ToLower(content), strings.ToLower(want)) {
			t.Errorf("the settings panel does not draw %q", want)
		}
	}
	if len(cats[idx].Items) != 13 {
		t.Errorf("the Screenshot tab has %d rows, want 13", len(cats[idx].Items))
	}
}

// TestKittyPlaceRefactorIsByteIdentical pins that generalising appendKittyPlace
// into appendKittyPlaceBox left the launcher's own escape unchanged. The
// refactor exists so the screenshot preview can name its own cell box; the
// launcher must still emit exactly what it emitted before.
//
// Negative control: swapping the c and r arguments in appendKittyPlace's call
// to appendKittyPlaceBox produced "c=1,r=2" against the expected "c=2,r=1" and
// failed.
func TestKittyPlaceRefactorIsByteIdentical(t *testing.T) {
	got := appendKittyPlace(nil, 7, 3, 10, 4)
	want := []byte(fmt.Sprintf("\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=%d,c=%d,r=%d,q=2,C=1;\x1b\\\x1b8",
		5, 11, 7, 3, launcherIconCols, launcherIconRows))
	if !bytes.Equal(got, want) {
		t.Errorf("place emits\n %q\nwant\n %q", got, want)
	}
}
