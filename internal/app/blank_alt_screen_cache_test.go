package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests assert directly on renderTerminal's output and on the window's
// cache state. A black box assertion on a captured PTY screen was tried first
// and does not work for this class of bug: it passes against both the broken
// and the fixed build, because whether the bad frame is on screen at the moment
// the harness samples it is a matter of timing. The cache is the thing that
// goes wrong, so the cache is what these tests inspect.

// newTestWindow builds a daemon window with a live emulator and drains its data
// channel, mirroring the setup used by the state sync race test.
func newTestWindow(t testing.TB, id string, w, h int) *terminal.Window {
	t.Helper()
	ptyDataChan := make(chan struct{}, 1)
	drainDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyDataChan:
			case <-drainDone:
				return
			}
		}
	}()
	t.Cleanup(func() { close(drainDone) })

	win := terminal.NewDaemonWindow(id, "test", 0, 0, w, h, 0, "pty-"+id, ptyDataChan)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(func() { win.Close() })
	return win
}

func newTestOS(win *terminal.Window) *OS {
	return &OS{
		Windows:        []*terminal.Window{win},
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
	}
}

// TestBlankAltScreenFrameIsNotCached is the regression test for full-screen
// applications going blank when focus moves to another window.
//
// A full-screen application clears the alternate screen on entry and paints a
// moment later. A render landing in that gap is genuinely blank. Caching it
// also clears ContentDirty, so if focus moves away before the application
// paints, nothing re-reads the emulator and the pane serves the blank forever
// while the application sits idle.
//
// Without the fix the blank frame lands in CachedContent and ContentDirty is
// cleared, and the assertions below fail.
func TestBlankAltScreenFrameIsNotCached(t *testing.T) {
	win := newTestWindow(t, "blank-alt-0001", 60, 20)
	m := newTestOS(win)

	// Paint the main screen, as a shell would.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("SHELLPROMPT"))
	win.UnlockIO()
	win.MarkContentDirty()

	if out := m.renderTerminal(win, true, false); !strings.Contains(out, "SHELLPROMPT") {
		t.Fatalf("setup: main screen render lost its content: %q", out)
	}
	if win.CachedContent == "" {
		t.Fatal("setup: a non-blank frame should have been cached")
	}

	// The application enters the alternate screen, which clears it, and has not
	// painted yet. This is the gap the bug lives in.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[?1049h"))
	win.UnlockIO()
	win.MarkContentDirty()

	out := m.renderTerminal(win, true, false)
	if !isBlankRender(out) {
		t.Fatalf("setup: expected a blank frame just after alt screen entry, got %q", out)
	}

	// The blank frame must not have become the cached truth, and the window must
	// still be asking to repaint.
	if win.CachedContent != "" && isBlankRender(win.CachedContent) {
		t.Error("a blank frame was cached; an idle application can never repair it")
	}
	if !win.ContentDirty {
		t.Error("ContentDirty was cleared by a blank frame; nothing will re-read the emulator")
	}

	// Focus moves away before the application paints. The pane must not serve a
	// blank cache.
	if out := m.renderTerminal(win, false, false); isBlankRender(out) && win.CachedContent != "" {
		t.Error("unfocused render served a blank cached frame")
	}

	// The application finally paints. An unfocused render must pick it up.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[1;1HEDITORCONTENT"))
	win.UnlockIO()

	got := m.renderTerminal(win, false, false)
	if !strings.Contains(got, "EDITORCONTENT") {
		t.Errorf("unfocused render did not pick up the painted alt screen: %q", got)
	}
}

// TestUnfocusedRenderHonoursContentDirty covers the second half of the defect.
// WriteToPTY and the drag and resize release handler set ContentDirty without
// dropping CachedContent, and the unfocused branch used to return the cache
// regardless, so a repaint request was silently discarded and an unfocused
// window could serve stale bytes indefinitely.
func TestUnfocusedRenderHonoursContentDirty(t *testing.T) {
	win := newTestWindow(t, "stale-cache-0001", 60, 20)
	m := newTestOS(win)

	win.LockIO()
	_, _ = win.Terminal.Write([]byte("FIRSTCONTENT"))
	win.UnlockIO()
	win.MarkContentDirty()

	if out := m.renderTerminal(win, false, false); !strings.Contains(out, "FIRSTCONTENT") {
		t.Fatalf("setup: %q", out)
	}

	// New output arrives and the window is marked dirty without the cache being
	// dropped, exactly as WriteToPTY does.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[2;1HSECONDCONTENT"))
	win.UnlockIO()
	win.Dirty = true
	win.ContentDirty = true

	got := m.renderTerminal(win, false, false)
	if !strings.Contains(got, "SECONDCONTENT") {
		t.Errorf("unfocused render served a stale cache while ContentDirty was set: %q", got)
	}
}

// TestNonBlankFrameStillCached guards the fix from overcorrecting: ordinary
// frames must still populate the cache and clear the repaint request, otherwise
// every window would re-render every frame.
func TestNonBlankFrameStillCached(t *testing.T) {
	win := newTestWindow(t, "cache-kept-0001", 60, 20)
	m := newTestOS(win)

	win.LockIO()
	_, _ = win.Terminal.Write([]byte("VISIBLE"))
	win.UnlockIO()
	win.MarkContentDirty()

	out := m.renderTerminal(win, false, false)
	if !strings.Contains(out, "VISIBLE") {
		t.Fatalf("render lost content: %q", out)
	}
	if win.CachedContent == "" {
		t.Error("a non-blank frame was not cached")
	}
	if win.ContentDirty {
		t.Error("a non-blank frame did not clear ContentDirty")
	}
}

func TestIsBlankRender(t *testing.T) {
	blank := []string{
		"",
		"   ",
		"\n\n\n",
		"   \n   \n",
		"\x1b[0m   \x1b[m\n\x1b[38;5;12m \x1b[m",
		"\x1b[2J\x1b[H",
	}
	for _, s := range blank {
		if !isBlankRender(s) {
			t.Errorf("isBlankRender(%q) = false, want true", s)
		}
	}
	visible := []string{
		"x",
		"   x   ",
		"\x1b[0m   a\x1b[m",
		"\n\n\nz\n",
		"\x1b[38;5;12mtext\x1b[m",
	}
	for _, s := range visible {
		if isBlankRender(s) {
			t.Errorf("isBlankRender(%q) = true, want false", s)
		}
	}
}
