package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionsPinAndSurviveDetach exercises workspace visibility and
// daemon ownership: hiding a rail or detaching the center client must not
// terminate the session whose active pane was displayed there.
func TestSidebarSessionsPinAndSurviveDetach(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 38
sections = "sessions"
[appearance.sidebar.right]
session = "follow-right"
[appearance.sidebar.left]
enabled = true
width = 38
session = "pin-left"
session_workspace = 2
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "pin-left", "follow-right"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	for _, item := range []struct{ session, command string }{
		{"pin-left", `printf '%s\n' LEFT-PIN-CONTENT`},
		{"follow-right", `printf '%s\n' RIGHT-FOLLOW-CONTENT`},
	} {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", item.session, "--literal", item.command); err != nil {
			t.Fatalf("send to %s: %v\n%s", item.session, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", item.session, "Enter"); err != nil {
			t.Fatalf("enter in %s: %v\n%s", item.session, err, out)
		}
	}
	first := startIn(t, base, startOpts{cols: 138, rows: 40, args: []string{"attach", "center"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "RIGHT-FOLLOW-CONTENT") && !strings.Contains(text, "LEFT-PIN-CONTENT")
	}, bootTimeout); err != nil {
		t.Fatalf("pin appeared outside workspace 2 or follow was missing: %v\n%s", err, first.Snapshot())
	}
	if err := first.SendKeys(tuitest.Alt("2")); err != nil {
		t.Fatal(err)
	}
	if err := first.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "LEFT-PIN-CONTENT") && strings.Contains(s.Text(), "RIGHT-FOLLOW-CONTENT")
	}, uiTimeout); err != nil {
		t.Fatalf("pinned session did not appear on workspace 2: %v\n%s", err, first.Snapshot())
	}
	if err := first.SendKeys(tuitest.Alt("1")); err != nil {
		t.Fatal(err)
	}
	if err := first.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "LEFT-PIN-CONTENT") && strings.Contains(s.Text(), "RIGHT-FOLLOW-CONTENT")
	}, uiTimeout); err != nil {
		t.Fatalf("pinned session stayed visible on workspace 1: %v\n%s", err, first.Snapshot())
	}
	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatal(err)
	}
	waitExit(t, first, "after detaching center session")
	for _, name := range []string{"center", "pin-left", "follow-right"} {
		if !sessionListed(t, base, name) {
			t.Fatalf("%s died when its client detached", name)
		}
	}
	second := startIn(t, base, startOpts{cols: 138, rows: 40, args: []string{"attach", "center"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && strings.Contains(s.Text(), "RIGHT-FOLLOW-CONTENT")
	}, bootTimeout); err != nil {
		t.Fatalf("center did not reattach before workspace switch: %v\n%s", err, second.Snapshot())
	}
	if err := second.SendKeys(tuitest.Alt("2")); err != nil {
		t.Fatal(err)
	}
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "LEFT-PIN-CONTENT") && strings.Contains(s.Text(), "RIGHT-FOLLOW-CONTENT")
	}, bootTimeout); err != nil {
		t.Fatalf("session views did not rehydrate after center reattach: %v\n%s", err, second.Snapshot())
	}
	t.Logf("pinned and following sessions survived detach:\n%s", second.Snapshot())
}
