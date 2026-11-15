package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitWarnsWhenAgentsShareAFolder starts two agents at work in one
// folder and a third in a folder of its own. The two rows say "shared
// folder", the third does not, and the new agent sheet, opened in that
// folder, says how many agents already work there.
//
// How this could pass wrongly, written down first:
//   - Every working row could say it. The third agent, alone in its folder,
//     must not.
//   - The mark could count agents at rest. An idle agent in the shared folder
//     is there too, and the sheet's count leaves it out.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main no row says it.
func TestCockpitWarnsWhenAgentsShareAFolder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	shared, alone := filepath.Join(base, "repo"), filepath.Join(base, "other")
	for _, d := range []string{shared, alone} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct{ name, dir string }{{"home", shared}, {"api", shared}, {"docs", shared}, {"misc", alone}} {
		if out, err := tuiosCLI(t, base, "new", s.name, "--detach", "--cwd", s.dir); err != nil {
			t.Fatalf("create %s: %v\n%s", s.name, err, out)
		}
	}
	for _, a := range []struct{ session, name, state string }{
		{"api", "auth-fix", "working"}, {"docs", "docs-pass", "working"},
		{"docs", "idle-one", "idle"}, {"misc", "lone-wolf", "working"},
	} {
		if out, err := tuiosCLI(t, base, "new-window", a.name, "-s", a.session, "--", "/bin/sh", "-c", "sleep 900"); err != nil {
			t.Fatalf("open %s: %v\n%s", a.name, err, out)
		}
		// The daemon learns a new pane's folder a moment after it starts.
		waitUntil(t, "the folder of "+a.name, uiTimeout, func() (bool, string) {
			out, _ := tuiosCLI(t, base, "list-windows", "-s", a.session, "--json")
			var res struct {
				Windows []struct {
					Name string `json:"display_name"`
					Cwd  string `json:"cwd"`
				} `json:"windows"`
			}
			_ = json.Unmarshal([]byte(out), &res)
			for _, w := range res.Windows {
				if w.Name == a.name && w.Cwd != "" {
					return true, w.Cwd
				}
			}
			return false, out
		})
		if out, err := tuiosCLI(t, base, "set-agent-state", a.state, "-s", a.session, "-w", a.name, "--harness", "claude-code", "-m", "Edit main.go"); err != nil {
			t.Fatalf("report %s: %v\n%s", a.name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 50, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	marked := func(s tuitest.Screen, name string) bool {
		r := fleetRowWith(s, name)
		if r < 0 || r+1 >= len(strings.Split(s.Text(), "\n")) {
			return false
		}
		return strings.Contains(s.Line(r), "shared folder") || strings.Contains(s.Line(r+1), "shared folder")
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "lone-wolf") >= 0 && marked(s, "auth-fix") && marked(s, "docs-pass")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the two agents in one folder are not marked: %v\n%s", err, term.Snapshot())
	}
	if s := term.Screen(); marked(s, "lone-wolf") || marked(s, "idle-one") {
		t.Fatalf("ASSERTION: an agent alone in its folder, or at rest, is marked\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-shared-folder")

	sendKeys(t, term, "n")
	waitText(t, term, "the sheet in the shared folder", "This folder", "2 agents work here. A worktree is safer.")
	saveCockpitFrames(t, term, "cockpit-sheet-shared-folder")
	alive(t, term, "after the shared folder warnings")
}
