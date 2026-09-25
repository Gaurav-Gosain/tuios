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
