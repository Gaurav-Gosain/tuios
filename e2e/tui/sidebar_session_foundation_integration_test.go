package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionFoundationHiddenAndToggle proves the session-backed rails
// use the foundation's explicit edges even when the legacy sidebar is hidden.
// Toggling a focused rail must detach only its viewer, not its daemon pane.
func TestSidebarSessionFoundationHiddenAndToggle(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "hidden"
enabled = true
width = 48
sections = "custom"
[appearance.sidebar.left]
enabled = true
width = 36
session = "side-left"
[appearance.sidebar.right]
enabled = true
width = 36
session = "side-right"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, item := range []struct{ name, mark string }{
		{"center", "CENTER-PROBE"}, {"side-left", "LEFT-PROBE"}, {"side-right", "RIGHT-PROBE"},
	} {
		if out, err := tuiosCLI(t, base, "new", item.name, "--detach"); err != nil {
			t.Fatalf("new %s: %v %s", item.name, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", item.name, "--literal", "printf '%s\\n' "+item.mark); err != nil {
			t.Fatalf("send %s: %v %s", item.name, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", item.name, "Enter"); err != nil {
			t.Fatalf("enter %s: %v %s", item.name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 138, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "LEFT-PROBE") && strings.Contains(s.Text(), "CENTER-PROBE") && strings.Contains(s.Text(), "RIGHT-PROBE")
	}, uiTimeout); err != nil {
		t.Fatalf("hidden legacy rails did not attach: %v\n%s", err, term.Snapshot())
	}
	mousePress(t, term, 120, 5, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 120, 5, tuitest.MouseLeft, 0)
	prefix(t, term, "H")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "LEFT-PROBE") && strings.Contains(s.Text(), "CENTER-PROBE") && !strings.Contains(s.Text(), "RIGHT-PROBE")
	}, uiTimeout); err != nil {
		t.Fatalf("opposite rail did not hide: %v\n%s", err, term.Snapshot())
	}
	if wins, err := daemonWindows(base, "side-right"); err != nil || wins.Total != 1 {
		t.Fatalf("right session lost its pane: %+v %v", wins, err)
	}
	prefix(t, term, "H")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "LEFT-PROBE") && strings.Contains(s.Text(), "CENTER-PROBE") && strings.Contains(s.Text(), "RIGHT-PROBE")
	}, uiTimeout); err != nil {
		t.Fatalf("opposite rail did not reattach: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hidden-session-rails")
}

// TestSidebarSessionDoesNotRunHiddenCustomCommand keeps the ordinary custom
// section from running a command behind the assigned daemon pane. The command
// can have side effects even though its section's output is not visible.
func TestSidebarSessionDoesNotRunHiddenCustomCommand(t *testing.T) {
	dir := t.TempDir()
	primaryMarker := filepath.Join(dir, "right-custom-ran")
	secondaryMarker := filepath.Join(dir, "left-custom-ran")
	cfg := fmt.Sprintf(`[appearance.sidebar]
position = "right"
enabled = true
width = 36
sections = "sessions,custom"
[appearance.sidebar.right]
session = "side-right"
[appearance.sidebar.right.custom]
command = %q
[appearance.sidebar.left]
enabled = true
width = 36
sections = "sessions,custom"
session = "side-left"
[appearance.sidebar.left.custom]
command = %q
`, "printf 'ran\\n' > '"+primaryMarker+"'", "printf 'ran\\n' > '"+secondaryMarker+"'")
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side-right", "side-left"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v %s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 110, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "side-right") && strings.Contains(s.Line(0), "side-left")
	}, uiTimeout); err != nil {
		t.Fatalf("assigned rail session did not appear: %v\n%s", err, term.Snapshot())
	}
	// A custom component on an open rail runs promptly. Give it a full
	// refresh interval before claiming that replacing its section suppressed it.
	time.Sleep(700 * time.Millisecond)
	for _, marker := range []string{primaryMarker, secondaryMarker} {
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("a custom command ran behind the assigned daemon session: %s", marker)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	saveArtifact(t, term, artifactDir(t), "session-rail-no-hidden-command")
}
