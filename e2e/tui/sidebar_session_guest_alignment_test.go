package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionGuestTopAlignment keeps all PTY content in the daemon but
// presents an extra empty leading row of an idle sidebar shell at the same
// level as the center shell. No byte is written to either PTY on attach.
func TestSidebarSessionGuestTopAlignment(t *testing.T) {
	const cfg = `[appearance.sidebar]
enabled = true
position = "left"
width = 36
[appearance.sidebar.left]
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
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "side") && strings.Contains(s.Text(), "sh-3.2$")
	}, bootTimeout); err != nil {
		t.Fatalf("sessions did not attach: %v\n%s", err, term.Snapshot())
	}
	// Drive the two real daemon PTYs to known screens after both sizes settle.
	// The side guest has one extra empty row, not one extra app chrome row.
	for _, target := range []struct{ name, command string }{
		{"center", `printf '\033[2J\033[H\nCMARK\n'`},
		{"side", `printf '\033[2J\033[H\n\nSMARK\n'`},
	} {
		list, err := daemonWindows(base, target.name)
		if err != nil || len(list.Windows) != 1 {
			t.Fatalf("%s windows: %+v %v", target.name, list, err)
		}
		id := list.Windows[0].ID
		if out, err := tuiosCLI(t, base, "send-keys", "-s", target.name, "-w", id, "--literal", target.command); err != nil {
			t.Fatalf("%s command: %v\n%s", target.name, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", target.name, "-w", id, "Enter"); err != nil {
			t.Fatalf("%s enter: %v\n%s", target.name, err, out)
		}
	}
	// The *underlying* side screen still contains its extra blank row.
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "capture-pane", "-s", "side")
		if err == nil {
			lines := strings.Split(out, "\n")
			if len(lines) > 2 && strings.TrimSpace(lines[0]) == "" && strings.TrimSpace(lines[1]) == "" && strings.Contains(lines[2], "SMARK") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("side guest did not retain its two leading blank rows: %v\n%s", err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		side, center, centerTop := -1, -1, -1
		for y := 0; y < rows; y++ {
			line := s.Line(y)
			if strings.Contains(line, "SMARK") {
				side = y
			}
			if strings.Contains(line, "CMARK") {
				center = y
			}
			if y > 0 && centerTop < 0 && strings.Contains(line, "╭") {
				centerTop = y
			}
		}
		return side >= 0 && center >= 0 && centerTop >= 0 && side == center-centerTop
	}, uiTimeout); err != nil {
		t.Fatalf("visible guest text did not begin at the same pane-relative row: %v\n%s", err, term.Snapshot())
	}
	// Focus the shifted shell. The host cursor must follow the *displayed*
	// prompt, not point one row below it at the original guest coordinate.
	mousePress(t, term, 5, 2, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 5, 2, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x, y, visible := s.Cursor()
		return visible && x > 2 && x < 35 && y == 3 && strings.Contains(s.Line(y), "sh-3.2$")
	}, uiTimeout); err != nil {
		t.Fatalf("cursor did not follow the normalized prompt: %v\n%s", err, term.Snapshot())
	}
	// Mouse tracking stays in the normal screen here. A click on the displayed
	// SMARK row (screen row 2) belongs to guest row 2, hence SGR row 3.
	for _, command := range []string{
		"stty -echo",
		`stty -icanon min 1; printf '\033[?1000h\033[?1006h'; printf '%s\n' MOUSE""READY; od -An -tx1 -N9; printf '\033[?1000l\033[?1006l'; printf '%s\n' MOUSE""DONE; stty sane`,
	} {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", command); err != nil {
			t.Fatalf("mouse setup: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
			t.Fatalf("run mouse setup: %v\n%s", err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "MOUSEREADY") }, uiTimeout); err != nil {
		t.Fatalf("mouse tracking not ready: %v\n%s", err, term.Snapshot())
	}
	mousePress(t, term, 5, 2, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "MOUSEDONE") }, uiTimeout); err != nil {
		t.Fatalf("shifted shell did not receive a tracked click: %v\n%s", err, term.Snapshot())
	}
	mouseRelease(t, term, 5, 2, tuitest.MouseLeft, 0)
	mouseOutput, err := tuiosCLI(t, base, "capture-pane", "-s", "side")
	if err != nil || !strings.Contains(strings.Join(strings.Fields(mouseOutput), " "), "1b 5b 3c 30 3b 35 3b 33 4d") {
		t.Fatalf("tracked click did not map screen row 2 to guest row 3: %v\n%s", err, mouseOutput)
	}

	// An alternate-screen program with the same leading blank rows must not
	// be shifted. Its screen has its own full-screen geometry and cursor.
	command := `printf '\033[?1049h\033[2J\033[H\n\nALTTEST'; sleep 3; printf '\033[?1049l'`
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", command); err != nil {
		t.Fatalf("start alternate screen: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
		t.Fatalf("run alternate screen: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Line(3), "ALTTEST") }, uiTimeout); err != nil {
		t.Fatalf("alternate-screen program was shifted: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Line(2), "SMARK") }, uiTimeout); err != nil {
		t.Fatalf("shell display did not return after alternate screen: %v\n%s", err, term.Snapshot())
	}
	t.Logf("side guest preserved; shell content, cursor, mouse and alternate screen verified:\n%s", term.Snapshot())
}
