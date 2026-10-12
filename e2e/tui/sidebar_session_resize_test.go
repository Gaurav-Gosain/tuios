package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionResizeDoesNotResizeSharedPTY checks a rail's viewport
// resize does not send SIGWINCH to the daemon's shared guest PTY.
func TestSidebarSessionResizeDoesNotResizeSharedPTY(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 36
[appearance.sidebar.right]
session = "side"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "╭") && strings.Count(s.Text(), "$") >= 2
	}, bootTimeout); err != nil {
		t.Fatalf("side rail did not attach: %v\n%s", err, term.Snapshot())
	}
	guestSize := func(marker string) (rows, cols int) {
		t.Helper()
		command := fmt.Sprintf(`printf '%s %%s\n' "$(stty size)"`, marker)
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", command); err != nil {
			t.Fatalf("send %s size query: %v\n%s", marker, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
			t.Fatalf("run %s size query: %v\n%s", marker, err, out)
		}
		pattern := regexp.MustCompile(regexp.QuoteMeta(marker) + ` (\d+) (\d+)`)
		deadline := time.Now().Add(uiTimeout)
		for {
			out, err := tuiosCLI(t, base, "capture-pane", "-s", "side")
			if err == nil {
				if match := pattern.FindStringSubmatch(out); len(match) == 3 {
					rows, _ = strconv.Atoi(match[1])
					cols, _ = strconv.Atoi(match[2])
					return rows, cols
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s size not captured: %v\n%s\n%s", marker, err, out, term.Snapshot())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	beforeRows, beforeCols := guestSize("BEFORESIZE")
	if err := term.Resize(100, 43); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { _, rows := s.Size(); return rows == 43 }, uiTimeout); err != nil {
		t.Fatalf("host resize not visible: %v", err)
	}
	// Give the resized rail time to settle before asking the shared guest.
	time.Sleep(300 * time.Millisecond)
	afterRows, afterCols := guestSize("AFTERSIZE")
	if beforeCols < 36 || beforeRows < 10 || afterCols != beforeCols || afterRows != beforeRows {
		t.Fatalf("rail viewport resized the shared PTY: %dx%d -> %dx%d\n%s", beforeCols, beforeRows, afterCols, afterRows, term.Snapshot())
	}
	t.Logf("rail viewport resized without changing guest PTY: %dx%d -> %dx%d\n%s", beforeCols, beforeRows, afterCols, afterRows, term.Snapshot())
}
