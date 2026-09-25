package app

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// The preview-first contract: the panel is up on the frame the gesture ended
// on, and the artifacts catch up under a serial that says which capture they
// belong to.

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
