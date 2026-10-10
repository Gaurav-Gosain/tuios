package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionMouseTracking proves the rail pane receives a tracked
// click as a terminal mouse sequence. A plain focus-only click would leave
// the guest waiting in od; the screenshot records the received bytes.
func TestSidebarSessionMouseTracking(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 36
[appearance.sidebar.right]
session = "mouse-side"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "mouse-side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "mouse-side")
	}, bootTimeout); err != nil {
		t.Fatalf("rail session did not attach: %v\n%s", err, term.Snapshot())
	}
	// The guest explicitly requests SGR tracking and waits for three mouse
	// bytes. Splitting READY in the typed command avoids mistaking echo for
	// output. A left click starts ESC [ < in SGR mode.
	command := `stty -echo -icanon min 1; printf '\033[?1000h\033[?1006h'; printf '%s\n' MOUSE""READY; od -An -tx1 -N3; printf '\033[?1000l\033[?1006l'; printf '%s\n' MOUSE""DELIVERED; stty sane`
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "mouse-side", "--literal", command); err != nil {
		t.Fatalf("start mouse tracker: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "mouse-side", "Enter"); err != nil {
		t.Fatalf("run mouse tracker: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "MOUSEREADY")
	}, uiTimeout); err != nil {
		t.Fatalf("mouse tracker not ready: %v\n%s", err, term.Snapshot())
	}
	mousePress(t, term, 80, 10, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 80, 10, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(strings.Join(strings.Fields(text), " "), "1b 5b 3c") && strings.Contains(text, "MOUSEDELIVERED")
	}, uiTimeout); err != nil {
		t.Fatalf("tracked click did not reach the assigned session: %v\n%s", err, term.Snapshot())
	}
	t.Logf("daemon-backed sidebar pane received mouse tracking bytes:\n%s", term.Snapshot())
}
