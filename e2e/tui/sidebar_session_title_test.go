package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestSidebarSessionUsesPaneTitles proves the first rail pane retains the
// ordinary window title, just like subsequent stacked panes, and a title
// click focuses that pane rather than cycling to a different one.
func TestSidebarSessionUsesPaneTitles(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 38
sections = "sessions"
[appearance.sidebar.right]
session = "side-right"
[appearance.sidebar.left]
enabled = true
width = 38
session = "side-left"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side-left", "side-right"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	for _, item := range []struct{ name, title string }{
		{"center", "Center Shell"}, {"side-left", "Left Shell"}, {"side-right", "Right Shell"},
	} {
		if out, err := tuiosCLI(t, base, "run-command", "-s", item.name, "RenameWindow", item.title); err != nil {
			t.Fatalf("rename %s: %v\n%s", item.name, err, out)
		}
	}
	original, err := daemonWindows(base, "side-left")
	if err != nil || len(original.Windows) != 1 {
		t.Fatalf("first left pane: %+v %v", original, err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "Editor Pane", "-s", "side-left"); err != nil {
		t.Fatalf("second left pane: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 138, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		var left, right, editor bool
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if len(line) < cols {
				continue
			}
			left = left || strings.Contains(string(line[:38]), "Left Shell")
			right = right || strings.Contains(string(line[cols-38:]), "Right Shell")
			editor = editor || strings.Contains(string(line[:38]), "Editor Pane")
		}
		return left && right && editor && !strings.Contains(s.Line(0), "side-left") && !strings.Contains(s.Line(0), "side-right")
	}, uiTimeout); err != nil {
		t.Fatalf("rail titles did not come from their panes: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "ordinary-rail-pane-titles")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "ordinary-rail-pane-titles")

	// An attached rail also follows the pane's ordinary live rename, rather
	// than holding an assignment label or a stale snapshot title.
	for _, title := range []string{"Live Renamed", "Left Shell"} {
		if out, err := tuiosCLI(t, base, "run-command", "-s", "side-left", "RenameWindow", original.Windows[0].ID, title); err != nil {
			t.Fatalf("live rename: %v\n%s", err, out)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), title) }, uiTimeout); err != nil {
			t.Fatalf("rail title did not follow live rename %q: %v\n%s", title, err, term.Snapshot())
		}
	}

	mousePress(t, term, 15, 0, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 15, 0, tuitest.MouseLeft, 0)
	deadline := time.Now().Add(uiTimeout)
	for {
		left, leftErr := daemonWindows(base, "side-left")
		center, centerErr := daemonWindows(base, "center")
		if leftErr == nil && centerErr == nil && left.FocusedWindowID == original.Windows[0].ID && center.Total == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clicking first rail title did not focus its pane: left=%+v (%v), center=%+v (%v)\n%s", left, leftErr, center, centerErr, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The three ordinary title-bar controls target the rail pane, not the
	// center window. Default dots are close (x=2), minimize (x=4), zoom (x=6).
	mousePress(t, term, 6, 0, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 6, 0, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Left Shell") && !strings.Contains(s.Text(), "Editor Pane")
	}, uiTimeout); err != nil {
		t.Fatalf("rail zoom did not expand the first pane: %v\n%s", err, term.Snapshot())
	}
	if left, err := daemonWindows(base, "side-left"); err != nil || left.Total != 2 {
		t.Fatalf("zoom changed daemon pane count: %+v %v", left, err)
	}
	mousePress(t, term, 6, 0, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 6, 0, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Editor Pane") }, uiTimeout); err != nil {
		t.Fatalf("rail unzoom did not restore the stack: %v\n%s", err, term.Snapshot())
	}
	mousePress(t, term, 4, 0, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 4, 0, tuitest.MouseLeft, 0)
	deadline = time.Now().Add(uiTimeout)
	for {
		left, err := daemonWindows(base, "side-left")
		if err == nil && len(left.Windows) == 2 && left.Windows[0].Minimized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("minimize button did not change the rail pane: %+v %v\n%s", left, err, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "run-command", "-s", "side-left", "RestoreWindow", original.Windows[0].ID); err != nil {
		t.Fatalf("restore first rail pane: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Left Shell") }, uiTimeout); err != nil {
		t.Fatalf("restored rail pane did not reappear: %v\n%s", err, term.Snapshot())
	}
	mousePress(t, term, 2, 0, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 2, 0, tuitest.MouseLeft, 0)
	deadline = time.Now().Add(uiTimeout)
	for {
		left, leftErr := daemonWindows(base, "side-left")
		center, centerErr := daemonWindows(base, "center")
		if leftErr == nil && centerErr == nil && left.Total == 1 && center.Total == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("close button targeted the wrong pane: left=%+v (%v), center=%+v (%v)\n%s", left, leftErr, center, centerErr, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
