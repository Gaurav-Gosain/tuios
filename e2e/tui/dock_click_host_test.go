package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDockClickKnowsTheAttachedHost is #2295. A client attached to session far
// through host build has a dock cell whose on-click writes TUIOS_ATTACHED_HOST
// to a file and then fails with a line on stderr. The file must say build, and
// the failure must show as a notice with the exit status and that line.
//
// How this could pass wrongly, written down first:
//   - The file could hold a value from the test's own environment. The client
//     starts with no TUIOS_ATTACHED_HOST, so only tuios can set it.
//   - The notice could be the refresh failure of the cell. The cell's command
//     succeeds, so only the click can fail.
//
// Negative control: main before this change. The file says "host=" and no
// notice appears, so both waits time out.
func TestDockClickKnowsTheAttachedHost(t *testing.T) {
	remote := remoteMachine(t)
	home := farHomeWith(t, "true\n")
	record := filepath.Join(t.TempDir(), "click-host")
	dock := `
[dock]
right = ["custom/clicker", "session-controls"]

[dock.custom.clicker]
command = "echo CLICKME"
refresh = "once"
on-click = "echo host=$TUIOS_ATTACHED_HOST > '` + record + `'; echo NOPE-$((6*7)) >&2; exit 3"
`
	_, term := popupHostClient(t, remote, home, dock)
	waitUntil(t, "the dock draws the cell", uiTimeout, func() (bool, string) {
		return strings.Contains(term.Screen().Text(), "CLICKME"), term.Snapshot()
	})
	clickDockWord(t, term, "CLICKME")

	waitUntil(t, "the on-click command sees the attached host", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(record)
		return strings.TrimSpace(string(b)) == "host=build", string(b)
	})
	waitText(t, term, "the failed click shows a notice", "the click failed: exit 3: NOPE-42")
	saveFrame(t, term, "dock-click-failed")
}
