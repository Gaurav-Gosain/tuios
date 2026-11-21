package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestALayoutSavedThenLeftHoldsItsWorkspace saves a layout of three panes and
// switches to workspace 2 at once. In a daemon session the client asks the
// daemon to save, and the daemon saved the workspace the session showed when
// the request got there: workspace 2, which has no pane. The save failed
// while the dock had already said "Layout saved". The client now names the
// workspace in the request, and says the layout is saved when the daemon
// answers.
//
// The knob layout_template_hold_ms holds the request for a second, so the
// switch always lands first.
//
// How this could pass wrongly, written down first:
//   - The switch could come after the save. The knob holds the request,
//     and the test waits for workspace 2 before the hold ends.
//   - The layout could be saved empty or from another workspace. The test
//     reads the saved layout's pane count: 3.
//
// Negative control: without the workspace in the request, the dock says
// "Failed to save layout trio" and no layout is saved.
func TestALayoutSavedThenLeftHoldsItsWorkspace(t *testing.T) {
	term, base := start(t, startOpts{
		args: []string{"new", "lay"},
		env:  []string{"PS1=" + layoutPrompt + " ", knob("layout_template_hold_ms=1000")},
	})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return promptCount(s) >= 3 }, shellTimeout); err != nil {
		t.Fatalf("the three panes of workspace 1 never showed their prompts: %v\n%s", err, term.Snapshot())
	}

	// Save, and leave for workspace 2 before the daemon has the request.
	sendKeys(t, term, tuitest.Ctrl('b'), "L", "s")
	if err := term.WaitForText("Save layout", uiTimeout); err != nil {
		t.Fatalf("the layout name prompt never opened: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "t", "r", "i", "o", tuitest.Enter, tuitest.Alt("2"))
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "2:0") }, uiTimeout); err != nil {
		t.Fatalf("workspace 2 never showed: %v\n%s", err, term.Snapshot())
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Layout saved: trio") || strings.Contains(text, "Failed to save layout")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never said how the save went: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "saved-then-left")
	if strings.Contains(term.Screen().Text(), "Failed to save layout") {
		t.Fatalf("ASSERTION: the save of workspace 1 failed after the switch to workspace 2\n%s", term.Snapshot())
	}
	out, err := tuiosCLI(t, base, "layout", "list", "--json")
	if err != nil {
		t.Fatalf("layout list: %v\n%s", err, out)
	}
	var layouts []struct {
		Name    string `json:"name"`
		Windows int    `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &layouts); err != nil {
		t.Fatalf("layout list --json: %v\n%s", err, out)
	}
	if len(layouts) != 1 || layouts[0].Name != "trio" || layouts[0].Windows != 3 {
		t.Fatalf("ASSERTION: the saved layouts are %+v, want trio with three panes\n%s", layouts, out)
	}
}
