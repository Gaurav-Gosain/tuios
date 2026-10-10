package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionDownwardSplit stacks two daemon panes inside the left
// rail, without splitting the center or changing the independent right rail.
func TestSidebarSessionDownwardSplit(t *testing.T) {
	const cfg = `[appearance.sidebar]
enabled = true
position = "right"
width = 36
[appearance.sidebar.left]
enabled = true
width = 36
session = "side-left"
[appearance.sidebar.right]
session = "side-right"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side-left", "side-right"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	first, err := daemonWindows(base, "side-left")
	if err != nil || len(first.Windows) != 1 {
		t.Fatalf("first rail pane: %+v %v", first, err)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", first.Windows[0].ID, "--literal", "printf '%s\\n' TOPPANE"); err != nil {
		t.Fatalf("top pane command: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", first.Windows[0].ID, "Enter"); err != nil {
		t.Fatalf("top pane enter: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "TOPPANE") }, bootTimeout); err != nil {
		t.Fatalf("top pane not visible: %v\n%s", err, term.Snapshot())
	}
	mousePress(t, term, 12, 5, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 12, 5, tuitest.MouseLeft, 0)
	if err := term.SendKeys(tuitest.Ctrl('b'), "-"); err != nil {
		t.Fatalf("prefix downward split: %v", err)
	}
	var second daemonWindowList
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		second, err = daemonWindows(base, "side-left")
		center, centerErr := daemonWindows(base, "center")
		if err == nil && centerErr == nil && second.Total == 2 && center.Total == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if second.Total != 2 {
		t.Fatalf("downward split did not create a pane in the left session: %+v (%v)\n%s", second, err, term.Snapshot())
	}
	var secondID string
	for _, w := range second.Windows {
		if w.ID != first.Windows[0].ID {
			secondID = w.ID
		}
	}
	if secondID == "" {
		t.Fatalf("cannot identify new pane: %+v", second)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", secondID, "--literal", "printf '%s\\n' BOTTOMPANE"); err != nil {
		t.Fatalf("bottom pane command: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", secondID, "Enter"); err != nil {
		t.Fatalf("bottom pane enter: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		if cols < 72 {
			return false
		}
		var top, bottom int = -1, -1
		for y := 0; y < rows; y++ {
			line := s.Line(y)
			if len(line) > 36 {
				line = line[:36]
			}
			if strings.Contains(line, "TOPPANE") {
				top = y
			}
			if strings.Contains(line, "BOTTOMPANE") {
				bottom = y
			}
		}
		return top >= 0 && bottom > top+2
	}, uiTimeout); err != nil {
		t.Fatalf("both rail panes did not remain visible, stacked top to bottom: %v\n%s", err, term.Snapshot())
	}
	// Both daemon PTYs must see their own half-height, not the old 80x24
	// startup size or the full height of the rail.
	for _, target := range []struct{ id, marker string }{
		{first.Windows[0].ID, "TOPSIZE"}, {secondID, "BOTSZ"},
	} {
		command := "printf '%s %s\\n' " + target.marker + " \"$(stty size)\""
		pattern := regexp.MustCompile(target.marker + ` (\d+) (\d+)`)
		deadline := time.Now().Add(uiTimeout)
		for {
			if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", target.id, "--literal", command); err != nil {
				t.Fatalf("size query: %v\n%s", err, out)
			}
			if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", target.id, "Enter"); err != nil {
				t.Fatalf("size query enter: %v\n%s", err, out)
			}
			out, err := tuiosCLI(t, base, "capture-pane", "-s", "side-left", "-w", target.id)
			if err == nil {
				matches := pattern.FindAllStringSubmatch(out, -1)
				if len(matches) > 0 {
					match := matches[len(matches)-1]
					rows, _ := strconv.Atoi(match[1])
					cols, _ := strconv.Atoi(match[2])
					if rows >= 4 && rows <= 20 && cols >= 10 && cols <= 36 {
						break
					}
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s guest PTY did not settle to a rail half: %v\n%s\n%s", target.marker, err, out, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// Clicking either half must send keyboard input to that pane alone.
	for _, target := range []struct {
		y                 int
		id, other, marker string
	}{
		{5, first.Windows[0].ID, secondID, "TOPFOCUS"},
		{26, secondID, first.Windows[0].ID, "BOTTOMFOCUS"},
	} {
		mousePress(t, term, 12, target.y, tuitest.MouseLeft, 0)
		mouseRelease(t, term, 12, target.y, tuitest.MouseLeft, 0)
		if err := term.SendKeys("printf '%s\\n' "+target.marker, tuitest.Enter); err != nil {
			t.Fatalf("type in stacked pane: %v", err)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			out, err := tuiosCLI(t, base, "capture-pane", "-s", "side-left", "-w", target.id)
			if err == nil && strings.Contains(out, target.marker) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s input did not reach its pane: %v\n%s\n%s", target.marker, err, out, term.Snapshot())
			}
			time.Sleep(50 * time.Millisecond)
		}
		other, err := tuiosCLI(t, base, "capture-pane", "-s", "side-left", "-w", target.other)
		if err != nil || strings.Contains(other, target.marker) {
			t.Fatalf("%s leaked to another stacked pane: %v\n%s", target.marker, err, other)
		}
	}
	t.Logf("two simultaneously visible, independently interactive rail panes:\n%s", term.Snapshot())
}
