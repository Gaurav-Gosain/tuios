package app

import (
	"testing"

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
		Settings:       config.Global,
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

// TestTheCaptureHintStripSurvivesAPaneOnRowZero is the bug startup.tiled
// uncovered. Capture mode draws two things: the instruction strip along row 0,
// and a marquee around the pane it is aiming at. A tiled pane starts on row 0,
// so the marquee's top edge lands on the strip, and while the two shared a z
// step the marquee won and the whole instruction bar vanished behind a pane
// border. That is the first thing a new user sees, because the session ships
// tiled.
//
// The strip has to be the one that survives: it is the only thing on screen
// that says how to leave the mode.
func TestTheCaptureHintStripSurvivesAPaneOnRowZero(t *testing.T) {
	m := shotOS(t)
	// A tiled pane: the whole content box, starting at the top row.
	m.Windows = m.Windows[:1]
	m.Windows[0].X, m.Windows[0].Y = 0, 0
	m.Windows[0].Width, m.Windows[0].Height = 120, 38
	m.BeginCapture(false)

	var hintZ, marqueeTopZ int
	var sawHint, sawMarquee bool
	for _, l := range m.renderCaptureMode() {
		switch l.GetID() {
		case "capture-hints":
			hintZ, sawHint = l.GetZ(), true
		case "capture-marquee-top":
			marqueeTopZ, sawMarquee = l.GetZ(), true
		}
	}
	if !sawHint {
		t.Fatal("capture mode drew no instruction strip")
	}
	if !sawMarquee {
		t.Fatal("capture mode drew no marquee, so this proves nothing about the two overlapping")
	}
	if hintZ <= marqueeTopZ {
		t.Errorf("the marquee's top edge is at z %d and the strip at z %d, so a pane on row 0 hides the strip",
			marqueeTopZ, hintZ)
	}
}
