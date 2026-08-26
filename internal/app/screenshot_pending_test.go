package app

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// The preview-first contract: the panel is up on the frame the gesture ended
// on, and the artifacts catch up under a serial that says which capture they
// belong to.

// TestThePanelIsUpBeforeTheRenderRuns is the whole of what the user reported.
// The cells are in hand at capture time, so the panel opens then, and nothing
// about the file, its size or its picture is claimed before it is true.
//
// Negative control: removing the openPendingPreview call from renderScreenshot
// left the panel closed until the command was run and this failed on the first
// assertion. Confirmed on the unfixed tree, where the panel only ever opened
// from HandleScreenshotResult.
func TestThePanelIsUpBeforeTheRenderRuns(t *testing.T) {
	m := shotOS(t)
	cmd := m.ScreenshotWindow(0)
	if cmd == nil {
		t.Fatal("no capture was started")
	}
	p := m.ShotPreview
	if !p.Open {
		t.Fatal("the panel is not open until the render finishes")
	}
	if !p.Pending {
		t.Error("the panel does not say it is still saving")
	}
	if p.Grid == nil {
		t.Fatal("the panel has no cells to draw")
	}
	if p.Grid.Cols == 0 || p.Grid.Rows == 0 {
		t.Errorf("the panel opened on an empty %dx%d grid", p.Grid.Cols, p.Grid.Rows)
	}
	if p.Path != "" || p.Bytes != 0 {
		t.Errorf("the pending panel claims a file: %q, %d bytes", p.Path, p.Bytes)
	}
	if p.Status != shotStatusSaving {
		t.Errorf("the pending status line says %q", p.Status)
	}
	// A pending panel offers no key that would act on a file that is not there.
	for _, h := range m.shotPreviewHints() {
		if h.Key == "c" || h.Key == "o" {
			t.Errorf("the pending panel drew an inert %q key", h.Key)
		}
	}

	// And the result fills it in.
	msg := cmd().(screenshotResultMsg)
	if msg.err != nil {
		t.Fatalf("the render failed: %v", msg.err)
	}
	m.HandleScreenshotResult(msg)
	if m.ShotPreview.Pending {
		t.Error("the panel is still pending after its result landed")
	}
	if m.ShotPreview.Path != msg.path {
		t.Errorf("the panel shows %q, want the file that was written, %q", m.ShotPreview.Path, msg.path)
	}
	if m.ShotPreview.Bytes == 0 {
		t.Error("the panel shows no file size")
	}
}

// TestEscapeBeforeTheFileLandsLeavesNothing is the pending half of discard. A
// panel closed with esc before its file exists cannot remove that file, so the
// serial is written down and the result removes its own.
//
// Negative control: deleting the `case pending:` arm of CloseScreenshotPreview
// left the file on disk and this failed.
func TestEscapeBeforeTheFileLandsLeavesNothing(t *testing.T) {
	m := shotOS(t)
	cmd := m.ScreenshotWindow(0)
	if cmd == nil {
		t.Fatal("no capture was started")
	}
	// esc, while the render is still running.
	m.CloseScreenshotPreview(true)
	if m.ShotPreview.Open {
		t.Fatal("esc left the panel open")
	}

	msg := cmd().(screenshotResultMsg)
	if msg.err != nil {
		t.Fatalf("the render failed: %v", msg.err)
	}
	if _, err := os.Stat(msg.path); err != nil {
		t.Fatalf("the render never wrote the file it was going to: %v", err)
	}
	m.HandleScreenshotResult(msg)
	if _, err := os.Stat(msg.path); err == nil {
		t.Errorf("a capture dismissed with esc left %s behind", msg.path)
	}
	if m.ShotPreview.Open {
		t.Error("a discarded result reopened the panel")
	}
}

// TestAStaleResultLeavesThePanelAlone is the ordering rule. A result that
// arrives after the user has taken another capture belongs to a panel that is
// no longer up, and writing it in is exactly the "it shows the previous
// screenshot" report.
//
// Negative control: dropping the serial comparison from HandleScreenshotResult
// and always filling the panel in put the first capture's path on the second
// capture's panel and this failed.
func TestAStaleResultLeavesThePanelAlone(t *testing.T) {
	m := shotOS(t)
	first := m.ScreenshotWindow(0)
	firstSerial := m.ShotPreview.Capture

	second := m.ScreenshotWindow(1)
	secondSerial := m.ShotPreview.Capture
	if secondSerial == firstSerial {
		t.Fatalf("two captures share serial %d", secondSerial)
	}

	// The second finishes first, then the first arrives late.
	secondMsg := second().(screenshotResultMsg)
	m.HandleScreenshotResult(secondMsg)
	firstMsg := first().(screenshotResultMsg)
	m.HandleScreenshotResult(firstMsg)

	if m.ShotPreview.Capture != secondSerial {
		t.Errorf("the panel moved to capture %d, want %d", m.ShotPreview.Capture, secondSerial)
	}
	if m.ShotPreview.Path != secondMsg.path {
		t.Errorf("the panel shows %q, want the capture it is open on, %q",
			m.ShotPreview.Path, secondMsg.path)
	}
	// The late one was still asked for, so its file stays.
	if _, err := os.Stat(firstMsg.path); err != nil {
		t.Errorf("the earlier capture's file went missing: %v", err)
	}
}

// TestOnlyTheNewestCaptureReachesTheClipboard checks a late result cannot put
// the wrong picture on the clipboard after the user has moved on. That is the
// same out-of-order symptom in a different costume.
//
// Negative control: removing the `msg.capture != m.shotCaptures` guard from
// screenshotCopyCmd returned a command for the stale capture and this failed.
func TestOnlyTheNewestCaptureReachesTheClipboard(t *testing.T) {
	m := shotOS(t)
	first := m.ScreenshotWindow(0)
	m.ScreenshotWindow(1)

	stale := first().(screenshotResultMsg)
	if cmd := m.screenshotCopyCmd(stale); cmd != nil {
		t.Error("a stale capture was allowed to take the clipboard")
	}
}

// TestTheRenderCommandDoesNotCopy pins the clipboard out of the render's way.
// The copy used to run before the result message was returned, and wl-copy
// forks a child that outlives it, so the preview waited on a clipboard server
// rather than on a render.
//
// Deliberately structural: it asserts the result type carries no copy at all,
// which is the shape that makes the old ordering impossible to write again.
func TestTheRenderCommandDoesNotCopy(t *testing.T) {
	m := shotOS(t)
	cmd := m.ScreenshotWindow(0)
	msg, ok := cmd().(screenshotResultMsg)
	if !ok {
		t.Fatalf("the render returned a %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("the render failed: %v", msg.err)
	}
	// The panel learns about a copy only from screenshotCopiedMsg, which is a
	// separate command with its own answer.
	m.HandleScreenshotResult(msg)
	if m.ShotPreview.Status == shotStatusCopied {
		t.Error("the render path reported a copy of its own")
	}
	m.HandleScreenshotCopied(screenshotCopiedMsg{capture: msg.capture})
	if m.ShotPreview.Status != shotStatusCopied {
		t.Errorf("the status line says %q after a copy", m.ShotPreview.Status)
	}
}

// TestThePreviewPayloadIsShrunkToThePanel checks the picture the panel places
// is the panel's size and not the file's.
//
// Negative control: putting the file's own bytes in the result message, which
// is what shipped, gave a payload several times the budget here and failed.
func TestThePreviewPayloadIsShrunkToThePanel(t *testing.T) {
	prev := clientCapabilities.Load()
	clientCapabilities.Store(&HostCapabilities{
		KittyGraphics: true, CellWidth: 10, CellHeight: 22, TerminalName: "kitty",
	})
	t.Cleanup(func() { clientCapabilities.Store(prev) })

	m := shotOS(t)
	m.PostRenderWriter = NewPostRenderWriter(nil)
	if !m.screenshotGraphicsReady() {
		t.Skip("the fixture has no pixel tier to size a payload for")
	}
	maxW, maxH := m.screenshotPreviewPixelBudget()
	if maxW <= 0 || maxH <= 0 {
		t.Fatalf("the pixel budget is %dx%d", maxW, maxH)
	}

	cmd := m.ScreenshotScreen()
	msg := cmd().(screenshotResultMsg)
	if msg.err != nil {
		t.Fatalf("the render failed: %v", msg.err)
	}
	if len(msg.png) == 0 {
		t.Fatal("a kitty host got no picture to place")
	}
	if msg.pixelW > maxW || msg.pixelH > maxH {
		t.Errorf("the preview picture is %dx%d, larger than the panel's %dx%d budget",
			msg.pixelW, msg.pixelH, maxW, maxH)
	}
	// The file itself is megabytes; the payload is a fraction of it, and the
	// bound is what stops it riding inside the frame's sync bracket.
	const payloadBudget = 512 << 10
	if len(msg.png) > payloadBudget {
		t.Errorf("the preview payload is %d KB, want under %d KB",
			len(msg.png)/1024, payloadBudget/1024)
	}
	if len(msg.transmit) == 0 {
		t.Error("the upload escapes were not built off the Update goroutine")
	}
}

// TestANonPNGCaptureRastersOnce checks the second full-scale render is gone. A
// text-format capture used to raster the whole grid again at the file's own
// scale purely to feed a preview a few hundred pixels wide.
//
// Negative control: restoring the old branch, which called Render(FormatPNG)
// with the file's frame, produced a picture at scale 2 and failed the size.
func TestANonPNGCaptureRastersOnce(t *testing.T) {
	g := shot.NewGrid(40, 10, shot.XTermFg, shot.XTermBg)
	f := shot.BuildFrame(
		shot.FrameSpec{Frame: "window", Controls: "auto", Padding: 10, Radius: 4, Scale: 4},
		shot.FrameInputs{Palette: shot.XTermPalette()},
	)
	_, raster, err := renderCaptureBytes(shot.FormatANSI, g, f, true)
	if err != nil {
		t.Fatal(err)
	}
	if raster == nil {
		t.Fatal("a preview was asked for and none was rastered")
	}
	scaled, err := shot.Raster(g, f)
	if err != nil {
		t.Fatal(err)
	}
	if raster.Bounds().Dx() >= scaled.Bounds().Dx() {
		t.Errorf("the preview raster is %d px wide against the file's %d; it should be scale 1",
			raster.Bounds().Dx(), scaled.Bounds().Dx())
	}
}
