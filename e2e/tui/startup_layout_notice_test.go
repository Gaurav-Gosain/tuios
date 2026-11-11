package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// startupLayoutNotice is the fix the warning names.
const startupLayoutNotice = "Set tiled = true to use the layout."

// TestStartupLayoutWithTilingOffIsNamed: a config that sets [startup] layout
// and leaves tiled out reads tiled as false, as an existing install keeps its
// floating session. New sessions then float and the layout is not used, and
// nothing said so: a session made with the layout set to scrolling came up as
// a cascade of floating panes. The client must say that the layout is not
// used and how to use it, in the log viewer and in tuios config apply, and
// say nothing once tiled is on.
func TestStartupLayoutWithTilingOffIsNamed(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nlayout = \"scrolling\"\n")
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", "e2e-layout-notice"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("copy errors", uiTimeout); err != nil {
		t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("[startup] layout is set", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client does not say that [startup] layout is not used: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "startup-layout-notice-log")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the log viewer: %v", err)
	}

	out, err := tuiosCLI(t, base, "config", "apply")
	if err != nil {
		t.Fatalf("tuios config apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, startupLayoutNotice) {
		t.Fatalf("ASSERTION: tuios config apply does not say %q:\n%s", startupLayoutNotice, out)
	}

	writeConfig(t, base, "[startup]\ntiled = true\nlayout = \"scrolling\"\n")
	out, err = tuiosCLI(t, base, "config", "apply")
	if err != nil {
		t.Fatalf("tuios config apply with tiled on: %v\n%s", err, out)
	}
	if strings.Contains(out, startupLayoutNotice) {
		t.Fatalf("ASSERTION: with tiled = true, tuios config apply still warns:\n%s", out)
	}
	alive(t, term, "after config apply")
}
