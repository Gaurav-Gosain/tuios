package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func paintCompleteFrame(t *testing.T, win *terminal.Window) {
	t.Helper()
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[H\x1b[2JOLDLINEONE\r\nOLDLINETWO"))
	win.UnlockIO()
	win.MarkContentDirty()
}

func applyHalfUpdate(t *testing.T, win *terminal.Window) {
	t.Helper()
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[?2026h\x1b[H\x1b[2JNEWLINEONE"))
	win.UnlockIO()
	win.MarkContentDirty()
	if !win.Terminal.IsSyncActive() {
		t.Fatal("setup: the guest's synchronized update is not open")
	}
}

func closeUpdate(t *testing.T, win *terminal.Window) {
	t.Helper()
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\r\nNEWLINETWO\x1b[?2026l"))
	win.UnlockIO()
	win.MarkContentDirty()
	if win.Terminal.IsSyncActive() {
		t.Fatal("setup: the synchronized update did not close")
	}
}

var invalidations = []struct {
	name string
	do   func(*terminal.Window)
}{
	{"MarkPositionDirty", func(w *terminal.Window) { w.MarkPositionDirty() }},
	{"InvalidateCache", func(w *terminal.Window) { w.InvalidateCache() }},
	{"resize", func(w *terminal.Window) { w.MarkPositionDirty(); w.MarkContentDirty() }},
}

// TestSyncHoldSurvivesCacheInvalidation is the defect. With a cached layer to
// hold, the renderer honours the guest's open update. The layer is not the
// guest's state though, it is tuios's, and any layout action drops it. The
// frame composed next reads the emulator mid-update and presents half of it.
func TestSyncHoldSurvivesCacheInvalidation(t *testing.T) {
	for _, inv := range invalidations {
		for _, focused := range []bool{true, false} {
			name := inv.name
			if !focused {
				name += "/unfocused"
			}
			t.Run(name, func(t *testing.T) {
				win := newTestWindow(t, "sync-hold-0001", 60, 20)
				m := newTestOS(win)

				paintCompleteFrame(t, win)
				if out := m.renderTerminal(win, focused, false); !strings.Contains(out, "OLDLINETWO") {
					t.Fatalf("setup: complete frame did not render: %q", out)
				}

				applyHalfUpdate(t, win)
				inv.do(win)

				got := m.renderTerminal(win, focused, false)
				if strings.Contains(got, "NEWLINEONE") {
					t.Errorf("presented the guest's half-drawn frame after %s:\n%q", inv.name, got)
				}
				if !strings.Contains(got, "OLDLINETWO") {
					t.Errorf("dropped the last complete frame after %s:\n%q", inv.name, got)
				}

				closeUpdate(t, win)
				got = m.renderTerminal(win, focused, false)
				if !strings.Contains(got, "NEWLINEONE") || !strings.Contains(got, "NEWLINETWO") {
					t.Errorf("the closed update never reached the screen:\n%q", got)
				}
				if strings.Contains(got, "OLDLINETWO") {
					t.Errorf("still serving the held frame after the update closed:\n%q", got)
				}
			})
		}
	}
}

// TestComposedFrameHoldsThroughRetile is the same defect one level up, on the
// bytes the host is actually handed: a retile lands between the guest opening
// its update and the frame being composed.
func TestComposedFrameHoldsThroughRetile(t *testing.T) {
	win, m := composedPane(t, "sync-hold-0003")

	paintCompleteFrame(t, win)
	if frame := composedFrame(m); !strings.Contains(frame, "OLDLINETWO") {
		t.Fatalf("setup: complete frame did not compose:\n%s", frame)
	}

	applyHalfUpdate(t, win)
	win.MarkPositionDirty()
	win.MarkContentDirty()

	frame := composedFrame(m)
	if strings.Contains(frame, "NEWLINEONE") {
		t.Errorf("composed a frame from the middle of the guest's update:\n%s", frame)
	}
	if !strings.Contains(frame, "OLDLINETWO") {
		t.Errorf("composed frame lost the last complete one:\n%s", frame)
	}

	closeUpdate(t, win)
	frame = composedFrame(m)
	if !strings.Contains(frame, "NEWLINETWO") {
		t.Errorf("the closed update never composed:\n%s", frame)
	}
}

// TestFullscreenFastPathHoldsMidUpdate covers the other way a frame reaches the
// host. A lone pane filling the screen skips the compositor entirely, so it used
// to be disqualified from the fast path for the whole length of every
// synchronized update just to inherit the compositor's hold. The hold now lives
// where the emulator is read, so the fast path keeps it and keeps the pane.
func TestFullscreenFastPathHoldsMidUpdate(t *testing.T) {
	win, m := composedPane(t, "sync-hold-0004")
	win.Width, win.Height = m.GetRenderWidth(), m.GetUsableHeight()
	win.Resize(win.Width, win.Height)
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Skip("layout defaults do not put this pane on the fast path")
	}

	paintCompleteFrame(t, win)
	if frame := ansi.Strip(string(m.composeView())); !strings.Contains(frame, "OLDLINETWO") {
		t.Fatalf("setup:\n%s", frame)
	}

	applyHalfUpdate(t, win)
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Fatal("an open synchronized update still disqualifies the fast path")
	}

	frame := ansi.Strip(string(m.composeView()))
	if strings.Contains(frame, "NEWLINEONE") {
		t.Errorf("the fast path composed a frame from the middle of the update:\n%s", frame)
	}
	if !strings.Contains(frame, "OLDLINETWO") {
		t.Errorf("the fast path lost the last complete frame:\n%s", frame)
	}

	closeUpdate(t, win)
	if frame := ansi.Strip(string(m.composeView())); !strings.Contains(frame, "NEWLINETWO") {
		t.Errorf("the closed update never reached the fast path's frame:\n%s", frame)
	}
}

func composedPane(t *testing.T, id string) (*terminal.Window, *OS) {
	t.Helper()
	win := newTestWindow(t, id, 60, 20)
	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 0
	win.Workspace = 0
	win.X, win.Y = 0, m.GetTopMargin()
	m.Windows = []*terminal.Window{win}
	m.FocusedWindow = 0
	return win, m
}

func composedFrame(m *OS) string {
	return ansi.Strip(lipgloss.Sprint(m.GetCanvas(true).Render()))
}
